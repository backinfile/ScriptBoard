package pirpc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDecoderUsesStrictLFFraming(t *testing.T) {
	content := strings.Repeat("a", 70*1024) + "\u2028middle\u2029end"
	input := "{\"type\":\"message_update\",\"assistantMessageEvent\":{\"type\":\"text_delta\",\"delta\":\"" + content + "\"}}\r\n" +
		"{\"type\":\"agent_settled\"}\n"
	decoder := NewDecoder(strings.NewReader(input), 128*1024)

	first, err := decoder.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if delta, ok := first.TextDelta(); !ok || delta != content {
		t.Fatalf("text delta = %q, %v", delta, ok)
	}
	second, err := decoder.Decode()
	if err != nil {
		t.Fatal(err)
	}
	if !second.Settled() {
		t.Fatalf("event = %#v", second)
	}
}

func TestDecoderRejectsOversizedMalformedAndTruncatedRecords(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  error
	}{
		{name: "oversized", input: "{\"type\":\"" + strings.Repeat("x", 80) + "\"}\n", want: ErrRecordTooLarge},
		{name: "malformed", input: "{not-json}\n", want: ErrMalformedRecord},
		{name: "truncated", input: "{\"type\":\"agent_settled\"}", want: io.ErrUnexpectedEOF},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewDecoder(strings.NewReader(test.input), 32).Decode()
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestEnvelopeTracksFinalAssistantOutcome(t *testing.T) {
	failed := false
	cases := []struct {
		name       string
		event      Envelope
		wantKnown  bool
		wantFailed bool
	}{
		{name: "stream error", event: Envelope{Type: "message_update", AssistantMessageEvent: AssistantMessageEvent{Type: "error", Reason: "error"}}, wantKnown: true, wantFailed: true},
		{name: "successful message", event: Envelope{Type: "message_end", Message: AgentMessage{Role: "assistant", StopReason: "stop"}}, wantKnown: true},
		{name: "retrying run is not final", event: Envelope{Type: "agent_end", WillRetry: true, Messages: []AgentMessage{{Role: "assistant", StopReason: "error"}}}},
		{name: "final failed run", event: Envelope{Type: "agent_end", Messages: []AgentMessage{{Role: "assistant", StopReason: "error"}}}, wantKnown: true, wantFailed: true},
		{name: "failed retry loop", event: Envelope{Type: "auto_retry_end", Success: &failed}, wantKnown: true, wantFailed: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			known, failed := test.event.AssistantOutcome()
			if known != test.wantKnown || failed != test.wantFailed {
				t.Fatalf("outcome = (%v, %v), want (%v, %v)", known, failed, test.wantKnown, test.wantFailed)
			}
		})
	}
}

func TestClientCorrelatesPromptAndStreamsEvents(t *testing.T) {
	stdoutReader, stdoutWriter := io.Pipe()
	stdinReader, stdinWriter := io.Pipe()
	client := NewClient(stdoutReader, stdinWriter, ClientOptions{MaxRecordBytes: 128 * 1024, EventBuffer: 8})
	t.Cleanup(func() { _ = client.Close() })

	serverDone := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdinReader).ReadString('\n')
		if err != nil {
			serverDone <- err
			return
		}
		if line != "{\"id\":\"request-1\",\"type\":\"prompt\",\"message\":\"hello\"}\n" {
			serverDone <- errors.New("unexpected command: " + line)
			return
		}
		_, err = io.WriteString(stdoutWriter,
			"{\"type\":\"message_update\",\"assistantMessageEvent\":{\"type\":\"text_delta\",\"delta\":\"hi\"}}\n"+
				"{\"id\":\"request-1\",\"type\":\"response\",\"command\":\"prompt\",\"success\":true}\n"+
				"{\"type\":\"agent_settled\"}\n")
		serverDone <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := client.Prompt(ctx, "request-1", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if response.Command != "prompt" || response.Success == nil || !*response.Success {
		t.Fatalf("response = %#v", response)
	}
	first := <-client.Events()
	if delta, ok := first.TextDelta(); !ok || delta != "hi" {
		t.Fatalf("first event = %#v", first)
	}
	if second := <-client.Events(); !second.Settled() {
		t.Fatalf("second event = %#v", second)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestClientRejectsUnknownResponseIDWithoutBlockingReader(t *testing.T) {
	stdoutReader, stdoutWriter := io.Pipe()
	client := NewClient(stdoutReader, io.Discard, ClientOptions{EventBuffer: 1})
	t.Cleanup(func() { _ = client.Close() })

	if _, err := io.WriteString(stdoutWriter, "{\"id\":\"unknown\",\"type\":\"response\",\"command\":\"prompt\",\"success\":true}\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-client.Errors():
		if !errors.Is(err, ErrUnknownResponse) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("client reader remained blocked")
	}
}

func TestClientCanCloseWhileEventsAreArriving(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		stdoutReader, stdoutWriter := io.Pipe()
		client := NewClient(stdoutReader, io.Discard, ClientOptions{EventBuffer: 256})
		writerDone := make(chan struct{})
		go func() {
			defer close(writerDone)
			for index := 0; index < 200; index++ {
				if _, err := io.WriteString(stdoutWriter, "{\"type\":\"agent_start\"}\n"); err != nil {
					return
				}
			}
		}()
		if err := client.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("close client: %v", err)
		}
		_ = stdoutWriter.Close()
		<-writerDone
	}
}

func TestEncoderSerializesWrites(t *testing.T) {
	var output bytes.Buffer
	encoder := NewEncoder(&output)
	if err := encoder.Write(Command{ID: "abort-1", Type: "abort"}); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "{\"id\":\"abort-1\",\"type\":\"abort\"}\n" {
		t.Fatalf("output = %q", got)
	}
	if err := encoder.Write(Command{ID: "model-1", Type: "set_model", Provider: privateProviderName, ModelID: "fixture-model"}); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.HasSuffix(got, "{\"id\":\"model-1\",\"type\":\"set_model\",\"provider\":\"scriptboard-provider\",\"modelId\":\"fixture-model\"}\n") {
		t.Fatalf("model command output = %q", got)
	}
}

func TestClientMapsAndAnswersExtensionConfirmation(t *testing.T) {
	var output bytes.Buffer
	client := NewClient(strings.NewReader(""), &output, ClientOptions{})
	request := Envelope{Type: "extension_ui_request", ID: "ui-1", Method: "confirm", Title: "Approve action", MessageText: "Bound parameters", Timeout: 120000}
	confirmation, ok := request.ExtensionConfirmation()
	if !ok || confirmation.ID != "ui-1" || confirmation.Timeout != 120000 {
		t.Fatalf("confirmation = %#v, %v", confirmation, ok)
	}
	if err := client.RespondExtensionConfirmation("ui-1", true, false); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "{\"id\":\"ui-1\",\"type\":\"extension_ui_response\",\"confirmed\":true}\n" {
		t.Fatalf("extension response = %q", got)
	}
}

func TestEnvelopeMapsRetryAndCompactionWithoutExposingProviderErrors(t *testing.T) {
	success := false
	tests := []struct {
		event        Envelope
		kind, status string
	}{
		{Envelope{Type: "auto_retry_start", Attempt: 2, DelayMilliseconds: 1500}, "retrying", "running"},
		{Envelope{Type: "auto_retry_end", Success: &success, Attempt: 3}, "retrying", "error"},
		{Envelope{Type: "compaction_start", Reason: "overflow"}, "compacting", "running"},
		{Envelope{Type: "compaction_end", Aborted: true}, "compacting", "cancelled"},
	}
	for _, test := range tests {
		kind, status, _, _, ok := test.event.Progress()
		if !ok || kind != test.kind || status != test.status {
			t.Fatalf("Progress(%q) = %q %q %t", test.event.Type, kind, status, ok)
		}
	}
}
