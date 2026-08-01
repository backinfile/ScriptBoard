// Package pirpc implements ScriptBoard's narrow, headless integration with the
// Pi coding agent RPC protocol. It deliberately owns framing, correlation and
// process-launch details so those concerns cannot leak into HTTP handlers.
package pirpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

const defaultMaxRecordBytes = 4 << 20

var (
	ErrRecordTooLarge  = errors.New("pi RPC record is too large")
	ErrMalformedRecord = errors.New("pi RPC record is malformed")
	ErrUnknownResponse = errors.New("pi RPC response has an unknown request ID")
	ErrEventBacklog    = errors.New("pi RPC event backlog exceeded its bound")
	ErrClientClosed    = errors.New("pi RPC client is closed")
)

type AssistantMessageEvent struct {
	Type   string `json:"type"`
	Delta  string `json:"delta,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type AgentMessage struct {
	Role       string `json:"role,omitempty"`
	StopReason string `json:"stopReason,omitempty"`
}

// Envelope contains the common fields needed to correlate responses and map
// browser-safe events. Unknown Pi fields remain intentionally ignored.
type Envelope struct {
	ID                    string                `json:"id,omitempty"`
	Type                  string                `json:"type"`
	Command               string                `json:"command,omitempty"`
	Success               *bool                 `json:"success,omitempty"`
	Error                 json.RawMessage       `json:"error,omitempty"`
	Data                  json.RawMessage       `json:"data,omitempty"`
	Message               AgentMessage          `json:"-"`
	MessageRaw            json.RawMessage       `json:"message,omitempty"`
	Messages              []AgentMessage        `json:"messages,omitempty"`
	WillRetry             bool                  `json:"willRetry,omitempty"`
	AssistantMessageEvent AssistantMessageEvent `json:"assistantMessageEvent,omitempty"`
	ToolCallID            string                `json:"toolCallId,omitempty"`
	ToolName              string                `json:"toolName,omitempty"`
	Args                  json.RawMessage       `json:"args,omitempty"`
	IsError               bool                  `json:"isError,omitempty"`
	Method                string                `json:"method,omitempty"`
	Title                 string                `json:"title,omitempty"`
	MessageText           string                `json:"-"`
	Timeout               int64                 `json:"timeout,omitempty"`
	Attempt               int                   `json:"attempt,omitempty"`
	MaxAttempts           int                   `json:"maxAttempts,omitempty"`
	DelayMilliseconds     int64                 `json:"delayMs,omitempty"`
	Reason                string                `json:"reason,omitempty"`
	Aborted               bool                  `json:"aborted,omitempty"`
}

func (event Envelope) Progress() (kind, status string, attempt int, delayMilliseconds int64, ok bool) {
	switch event.Type {
	case "auto_retry_start", "summarization_retry_scheduled", "summarization_retry_attempt_start":
		return "retrying", "running", event.Attempt, event.DelayMilliseconds, true
	case "auto_retry_end", "summarization_retry_finished":
		status = "complete"
		if event.Success != nil && !*event.Success {
			status = "error"
		}
		return "retrying", status, event.Attempt, 0, true
	case "compaction_start":
		return "compacting", "running", 0, 0, true
	case "compaction_end":
		status = "complete"
		if event.Aborted {
			status = "cancelled"
		}
		return "compacting", status, 0, 0, true
	default:
		return "", "", 0, 0, false
	}
}

type ExtensionConfirmation struct {
	ID, Title, Message string
	Timeout            int64
}

func (event Envelope) ExtensionConfirmation() (ExtensionConfirmation, bool) {
	if event.Type != "extension_ui_request" || event.Method != "confirm" || strings.TrimSpace(event.ID) == "" {
		return ExtensionConfirmation{}, false
	}
	return ExtensionConfirmation{ID: event.ID, Title: event.Title, Message: event.MessageText, Timeout: event.Timeout}, true
}

func (event Envelope) TextDelta() (string, bool) {
	if event.Type != "message_update" || event.AssistantMessageEvent.Type != "text_delta" {
		return "", false
	}
	return event.AssistantMessageEvent.Delta, true
}

func (event Envelope) Settled() bool {
	return event.Type == "agent_settled"
}

// AssistantOutcome reports the latest terminal assistant outcome carried by
// an event. Pi accepts prompts before provider work finishes, so callers must
// inspect the asynchronous message stream instead of treating agent_settled
// as proof of success. Retrying agent_end events are deliberately ignored; a
// later attempt will provide the final outcome before agent_settled.
func (event Envelope) AssistantOutcome() (known bool, failed bool) {
	switch event.Type {
	case "message_update":
		switch event.AssistantMessageEvent.Type {
		case "error":
			return true, true
		case "done":
			return true, false
		}
	case "message_end", "turn_end":
		return agentMessageOutcome(event.Message)
	case "agent_end":
		if event.WillRetry {
			return false, false
		}
		for index := len(event.Messages) - 1; index >= 0; index-- {
			if known, failed := agentMessageOutcome(event.Messages[index]); known {
				return true, failed
			}
		}
	case "auto_retry_end":
		if event.Success != nil {
			return true, !*event.Success
		}
	}
	return false, false
}

func agentMessageOutcome(message AgentMessage) (known bool, failed bool) {
	if message.Role != "assistant" {
		return false, false
	}
	switch message.StopReason {
	case "error", "aborted":
		return true, true
	case "stop", "length", "toolUse":
		return true, false
	default:
		return false, false
	}
}

type Decoder struct {
	reader         *bufio.Reader
	maxRecordBytes int
}

func NewDecoder(reader io.Reader, maxRecordBytes int) *Decoder {
	if maxRecordBytes <= 0 {
		maxRecordBytes = defaultMaxRecordBytes
	}
	return &Decoder{reader: bufio.NewReaderSize(reader, 64*1024), maxRecordBytes: maxRecordBytes}
}

// Decode splits only on LF. In particular, U+2028 and U+2029 remain ordinary
// JSON string content, as required by Pi's strict JSONL contract.
func (decoder *Decoder) Decode() (Envelope, error) {
	record, err := decoder.readRecord()
	if err != nil {
		return Envelope{}, err
	}
	var envelope Envelope
	if len(record) == 0 || json.Unmarshal(record, &envelope) != nil || envelope.Type == "" {
		return Envelope{}, ErrMalformedRecord
	}
	if len(envelope.MessageRaw) > 0 {
		if envelope.MessageRaw[0] == '"' {
			if json.Unmarshal(envelope.MessageRaw, &envelope.MessageText) != nil {
				return Envelope{}, ErrMalformedRecord
			}
		} else if json.Unmarshal(envelope.MessageRaw, &envelope.Message) != nil {
			return Envelope{}, ErrMalformedRecord
		}
	}
	return envelope, nil
}

func (decoder *Decoder) readRecord() ([]byte, error) {
	record := make([]byte, 0, 1024)
	for {
		fragment, err := decoder.reader.ReadSlice('\n')
		if len(record)+len(fragment) > decoder.maxRecordBytes+1 {
			if err != nil && !errors.Is(err, bufio.ErrBufferFull) && !errors.Is(err, io.EOF) {
				return nil, err
			}
			if len(fragment) == 0 || fragment[len(fragment)-1] != '\n' {
				for {
					_, discardErr := decoder.reader.ReadSlice('\n')
					if discardErr == nil || errors.Is(discardErr, io.EOF) {
						break
					}
					if !errors.Is(discardErr, bufio.ErrBufferFull) {
						return nil, discardErr
					}
				}
			}
			return nil, ErrRecordTooLarge
		}
		record = append(record, fragment...)
		switch {
		case err == nil:
			record = record[:len(record)-1]
			if len(record) > 0 && record[len(record)-1] == '\r' {
				record = record[:len(record)-1]
			}
			return record, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(record) == 0 {
				return nil, io.EOF
			}
			return nil, io.ErrUnexpectedEOF
		default:
			return nil, err
		}
	}
}

type Command struct {
	ID                string `json:"id,omitempty"`
	Type              string `json:"type"`
	Message           string `json:"message,omitempty"`
	Provider          string `json:"provider,omitempty"`
	ModelID           string `json:"modelId,omitempty"`
	StreamingBehavior string `json:"streamingBehavior,omitempty"`
	Confirmed         *bool  `json:"confirmed,omitempty"`
	Cancelled         bool   `json:"cancelled,omitempty"`
}

type Encoder struct {
	writer io.Writer
	mu     sync.Mutex
}

func NewEncoder(writer io.Writer) *Encoder {
	return &Encoder{writer: writer}
}

func (encoder *Encoder) Write(command Command) error {
	if command.Type == "" {
		return fmt.Errorf("pi RPC command type is required")
	}
	payload, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("encode pi RPC command: %w", err)
	}
	payload = append(payload, '\n')
	encoder.mu.Lock()
	defer encoder.mu.Unlock()
	if _, err := encoder.writer.Write(payload); err != nil {
		return fmt.Errorf("write pi RPC command: %w", err)
	}
	return nil
}

type ClientOptions struct {
	MaxRecordBytes int
	EventBuffer    int
}

type responseResult struct {
	envelope Envelope
	err      error
}

// Client drains stdout continuously. Browser consumers cannot block the Pi
// pipe indefinitely: exceeding the bounded event buffer fails the session and
// lets the owning supervisor terminate only that managed process.
type Client struct {
	decoder *Decoder
	encoder *Encoder
	reader  io.Reader
	writer  io.Writer

	mu        sync.Mutex
	pending   map[string]chan responseResult
	abandoned map[string]struct{}
	closed    bool

	events chan Envelope
	errors chan error
	done   chan struct{}
	once   sync.Once
}

func NewClient(reader io.Reader, writer io.Writer, options ClientOptions) *Client {
	buffer := options.EventBuffer
	if buffer <= 0 {
		buffer = 256
	}
	client := &Client{
		decoder: NewDecoder(reader, options.MaxRecordBytes), encoder: NewEncoder(writer), reader: reader, writer: writer,
		pending: make(map[string]chan responseResult), abandoned: make(map[string]struct{}),
		events: make(chan Envelope, buffer), errors: make(chan error, 1), done: make(chan struct{}),
	}
	go client.readLoop()
	return client
}

func (client *Client) Events() <-chan Envelope { return client.events }
func (client *Client) Errors() <-chan error    { return client.errors }

func (client *Client) Prompt(ctx context.Context, requestID, message string) (Envelope, error) {
	if message == "" {
		return Envelope{}, fmt.Errorf("pi RPC prompt message is required")
	}
	return client.request(ctx, Command{ID: requestID, Type: "prompt", Message: message})
}

func (client *Client) Abort(ctx context.Context, requestID string) (Envelope, error) {
	return client.request(ctx, Command{ID: requestID, Type: "abort"})
}

func (client *Client) GetState(ctx context.Context, requestID string) (Envelope, error) {
	return client.request(ctx, Command{ID: requestID, Type: "get_state"})
}

func (client *Client) SetManagedModel(ctx context.Context, requestID, modelID string) (Envelope, error) {
	if strings.TrimSpace(modelID) == "" {
		return Envelope{}, fmt.Errorf("Pi RPC model ID is required")
	}
	return client.request(ctx, Command{ID: requestID, Type: "set_model", Provider: privateProviderName, ModelID: modelID})
}

func (client *Client) RespondExtensionConfirmation(requestID string, confirmed, cancelled bool) error {
	if strings.TrimSpace(requestID) == "" {
		return fmt.Errorf("Pi extension UI request ID is required")
	}
	command := Command{ID: requestID, Type: "extension_ui_response", Cancelled: cancelled}
	if !cancelled {
		command.Confirmed = &confirmed
	}
	return client.encoder.Write(command)
}

func (client *Client) request(ctx context.Context, command Command) (Envelope, error) {
	if command.ID == "" {
		return Envelope{}, fmt.Errorf("pi RPC request ID is required")
	}
	result := make(chan responseResult, 1)
	client.mu.Lock()
	if client.closed {
		client.mu.Unlock()
		return Envelope{}, ErrClientClosed
	}
	if _, exists := client.pending[command.ID]; exists {
		client.mu.Unlock()
		return Envelope{}, fmt.Errorf("pi RPC request ID %q is already pending", command.ID)
	}
	client.pending[command.ID] = result
	client.mu.Unlock()

	if err := client.encoder.Write(command); err != nil {
		client.removePending(command.ID)
		client.finish(err)
		return Envelope{}, err
	}
	select {
	case response := <-result:
		return response.envelope, response.err
	case <-ctx.Done():
		client.mu.Lock()
		if _, exists := client.pending[command.ID]; exists {
			delete(client.pending, command.ID)
			client.abandoned[command.ID] = struct{}{}
		}
		client.mu.Unlock()
		return Envelope{}, ctx.Err()
	case <-client.done:
		return Envelope{}, ErrClientClosed
	}
}

func (client *Client) readLoop() {
	for {
		envelope, err := client.decoder.Decode()
		if err != nil {
			client.mu.Lock()
			closed := client.closed
			client.mu.Unlock()
			if closed && errors.Is(err, io.EOF) {
				client.finish(nil)
			} else {
				client.finish(err)
			}
			return
		}
		if envelope.Type == "response" {
			if !client.deliverResponse(envelope) {
				client.finish(fmt.Errorf("%w: %q", ErrUnknownResponse, envelope.ID))
				return
			}
			continue
		}
		if !client.deliverEvent(envelope) {
			client.finish(ErrEventBacklog)
			return
		}
	}
}

func (client *Client) deliverEvent(envelope Envelope) bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		return true
	}
	select {
	case client.events <- envelope:
		return true
	default:
		return false
	}
}

func (client *Client) deliverResponse(envelope Envelope) bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	if _, ignored := client.abandoned[envelope.ID]; ignored {
		delete(client.abandoned, envelope.ID)
		return true
	}
	pending, exists := client.pending[envelope.ID]
	if !exists || envelope.ID == "" {
		return false
	}
	delete(client.pending, envelope.ID)
	var responseErr error
	if envelope.Success == nil {
		responseErr = ErrMalformedRecord
	} else if !*envelope.Success {
		responseErr = fmt.Errorf("pi RPC %s command failed", envelope.Command)
	}
	pending <- responseResult{envelope: envelope, err: responseErr}
	return true
}

func (client *Client) removePending(id string) {
	client.mu.Lock()
	delete(client.pending, id)
	client.mu.Unlock()
}

func (client *Client) finish(terminalErr error) {
	client.once.Do(func() {
		client.mu.Lock()
		client.closed = true
		pending := client.pending
		client.pending = make(map[string]chan responseResult)
		close(client.events)
		client.mu.Unlock()
		for _, waiter := range pending {
			waiter <- responseResult{err: terminalErr}
		}
		if terminalErr != nil {
			client.errors <- terminalErr
		}
		close(client.errors)
		close(client.done)
	})
}

func (client *Client) Close() error {
	client.mu.Lock()
	alreadyClosed := client.closed
	client.closed = true
	client.mu.Unlock()
	if alreadyClosed {
		return nil
	}
	var closeErr error
	if closer, ok := client.writer.(io.Closer); ok {
		closeErr = closer.Close()
	}
	if closer, ok := client.reader.(io.Closer); ok {
		if err := closer.Close(); closeErr == nil {
			closeErr = err
		}
	}
	client.finish(nil)
	return closeErr
}
