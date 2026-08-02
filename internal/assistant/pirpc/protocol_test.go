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

func TestClientSendsBoundedImagePrompt(t *testing.T) {
	stdoutReader, stdoutWriter := io.Pipe()
	stdinReader, stdinWriter := io.Pipe()
	client := NewClient(stdoutReader, stdinWriter, ClientOptions{EventBuffer: 2})
	t.Cleanup(func() { _ = client.Close() })
	serverDone := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdinReader).ReadString('\n')
		if err == nil && line != "{\"id\":\"image-1\",\"type\":\"prompt\",\"message\":\"inspect\",\"images\":[{\"type\":\"image\",\"data\":\"YWJj\",\"mimeType\":\"image/jpeg\"}]}\n" {
			err = errors.New("unexpected image prompt: " + line)
		}
		if err == nil {
			_, err = io.WriteString(stdoutWriter, `{"id":"image-1","type":"response","command":"prompt","success":true}`+"\n")
		}
		serverDone <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.PromptWithImages(ctx, "image-1", "inspect", []PromptImage{{Type: "image", Data: "YWJj", MIMEType: "image/jpeg"}}); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestClientReadsBoundedSessionStats(t *testing.T) {
	stdoutReader, stdoutWriter := io.Pipe()
	stdinReader, stdinWriter := io.Pipe()
	client := NewClient(stdoutReader, stdinWriter, ClientOptions{EventBuffer: 8})
	t.Cleanup(func() { _ = client.Close() })

	serverDone := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(stdinReader).ReadString('\n')
		if err != nil {
			serverDone <- err
			return
		}
		if line != "{\"id\":\"stats-1\",\"type\":\"get_session_stats\"}\n" {
			serverDone <- errors.New("unexpected command: " + line)
			return
		}
		_, err = io.WriteString(stdoutWriter, `{"id":"stats-1","type":"response","command":"get_session_stats","success":true,"data":{"userMessages":3,"assistantMessages":2,"toolCalls":4,"toolResults":4,"totalMessages":13,"tokens":{"input":1200,"output":300,"cacheRead":800,"cacheWrite":20,"total":2320},"cost":0.125,"contextUsage":{"tokens":750,"contextWindow":4096,"percent":18.31}}}`+"\n")
		serverDone <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stats, err := client.GetSessionStats(ctx, "stats-1")
	if err != nil {
		t.Fatal(err)
	}
	if stats.UserMessages != 3 || stats.AssistantMessages != 2 || stats.ToolCalls != 4 || stats.TotalMessages != 13 {
		t.Fatalf("message stats = %#v", stats)
	}
	if stats.Tokens.Input != 1200 || stats.Tokens.Output != 300 || stats.Tokens.Total != 2320 || stats.Cost != 0.125 {
		t.Fatalf("usage stats = %#v", stats)
	}
	if stats.ContextUsage == nil || stats.ContextUsage.Tokens == nil || *stats.ContextUsage.Tokens != 750 || stats.ContextUsage.ContextWindow != 4096 || stats.ContextUsage.Percent == nil || *stats.ContextUsage.Percent != 18.31 {
		t.Fatalf("context stats = %#v", stats.ContextUsage)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestClientReadsModelInputCapabilities(t *testing.T) {
	stdoutReader, stdoutWriter := io.Pipe()
	stdinReader, stdinWriter := io.Pipe()
	client := NewClient(stdoutReader, stdinWriter, ClientOptions{EventBuffer: 2})
	t.Cleanup(func() { _ = client.Close() })
	go func() {
		_, _ = bufio.NewReader(stdinReader).ReadString('\n')
		_, _ = io.WriteString(stdoutWriter, `{"id":"state-1","type":"response","command":"get_state","success":true,"data":{"model":{"id":"vision","input":["text","image"]},"thinkingLevel":"medium","isStreaming":false}}`+"\n")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	state, err := client.GetSessionState(ctx, "state-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.Model == nil || !state.Model.SupportsImages() || state.ThinkingLevel != "medium" {
		t.Fatalf("state = %#v", state)
	}
}

func TestClientDiscoversAndSetsSupportedThinkingLevel(t *testing.T) {
	stdoutReader, stdoutWriter := io.Pipe()
	stdinReader, stdinWriter := io.Pipe()
	client := NewClient(stdoutReader, stdinWriter, ClientOptions{EventBuffer: 8})
	t.Cleanup(func() { _ = client.Close() })

	serverDone := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(stdinReader)
		line, err := reader.ReadString('\n')
		if err != nil {
			serverDone <- err
			return
		}
		if line != "{\"id\":\"levels-1\",\"type\":\"get_available_thinking_levels\"}\n" {
			serverDone <- errors.New("unexpected levels command: " + line)
			return
		}
		if _, err = io.WriteString(stdoutWriter, `{"id":"levels-1","type":"response","command":"get_available_thinking_levels","success":true,"data":{"levels":["off","low","medium","high"]}}`+"\n"); err != nil {
			serverDone <- err
			return
		}
		line, err = reader.ReadString('\n')
		if err != nil {
			serverDone <- err
			return
		}
		if line != "{\"id\":\"thinking-1\",\"type\":\"set_thinking_level\",\"level\":\"high\"}\n" {
			serverDone <- errors.New("unexpected thinking command: " + line)
			return
		}
		_, err = io.WriteString(stdoutWriter, `{"id":"thinking-1","type":"response","command":"set_thinking_level","success":true}`+"\n")
		serverDone <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	levels, err := client.GetAvailableThinkingLevels(ctx, "levels-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(levels, ",") != "off,low,medium,high" {
		t.Fatalf("levels = %#v", levels)
	}
	if _, err := client.SetThinkingLevel(ctx, "thinking-1", "high"); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestClientCompactsAndControlsRecoveryPolicies(t *testing.T) {
	stdoutReader, stdoutWriter := io.Pipe()
	stdinReader, stdinWriter := io.Pipe()
	client := NewClient(stdoutReader, stdinWriter, ClientOptions{EventBuffer: 8})
	t.Cleanup(func() { _ = client.Close() })

	serverDone := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(stdinReader)
		commands := []string{
			"{\"id\":\"compact-1\",\"type\":\"compact\"}\n",
			"{\"id\":\"auto-compact-1\",\"type\":\"set_auto_compaction\",\"enabled\":true}\n",
			"{\"id\":\"auto-retry-1\",\"type\":\"set_auto_retry\",\"enabled\":true}\n",
		}
		responses := []string{
			`{"id":"compact-1","type":"response","command":"compact","success":true,"data":{"tokensBefore":3900,"estimatedTokensAfter":950,"firstKeptEntryId":"entry-7"}}` + "\n",
			`{"id":"auto-compact-1","type":"response","command":"set_auto_compaction","success":true}` + "\n",
			`{"id":"auto-retry-1","type":"response","command":"set_auto_retry","success":true}` + "\n",
		}
		for index := range commands {
			line, err := reader.ReadString('\n')
			if err != nil {
				serverDone <- err
				return
			}
			if line != commands[index] {
				serverDone <- errors.New("unexpected recovery command: " + line)
				return
			}
			if _, err = io.WriteString(stdoutWriter, responses[index]); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := client.Compact(ctx, "compact-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.TokensBefore != 3900 || result.EstimatedTokensAfter != 950 || result.FirstKeptEntryID != "entry-7" {
		t.Fatalf("compaction result = %#v", result)
	}
	if _, err := client.SetAutoCompaction(ctx, "auto-compact-1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetAutoRetry(ctx, "auto-retry-1", true); err != nil {
		t.Fatal(err)
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
