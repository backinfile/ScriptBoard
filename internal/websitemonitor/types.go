package websitemonitor

import (
	"context"
	"fmt"
	"time"
)

const UserAgent = "ScriptBoard-Website-Monitor/1.0"

type Scope string

const (
	ScopeLocal    Scope = "local"
	ScopeExternal Scope = "external"
)

type Kind string

const (
	KindHTTP      Kind = "http"
	KindWebSocket Kind = "websocket"
)

type State string

const (
	StatePending   State = "pending"
	StateUp        State = "up"
	StateVerifying State = "verifying"
	StateDown      State = "down"
	StatePaused    State = "paused"
)

type HTTPSuccessMode string

const (
	HTTPSuccessRange       HTTPSuccessMode = "range"
	HTTPSuccessExact       HTTPSuccessMode = "exact"
	HTTPSuccessAnyResponse HTTPSuccessMode = "response"
)

type HTTPStatusRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type WebSocketSuccess string

const (
	WebSocketHandshake       WebSocketSuccess = "handshake"
	WebSocketAnyMessage      WebSocketSuccess = "any-message"
	WebSocketMatchingMessage WebSocketSuccess = "matching-message"
	WebSocketPingPong        WebSocketSuccess = "ping-pong"
)

type MessageType string

const (
	MessageNone   MessageType = "none"
	MessageText   MessageType = "text"
	MessageBinary MessageType = "binary"
)

type PayloadFormat string

const (
	PayloadNone   PayloadFormat = "none"
	PayloadText   PayloadFormat = "text"
	PayloadHex    PayloadFormat = "hex"
	PayloadBase64 PayloadFormat = "base64"
)

type Config struct {
	Name                 string
	Scope                Scope
	Kind                 Kind
	URL                  string
	Frequency            time.Duration
	Timeout              time.Duration
	HTTPMethod           string
	HTTPContentType      string
	HTTPBody             string
	HTTPSuccessMode      HTTPSuccessMode
	ExpectedStatuses     []int
	ExpectedStatusRanges []HTTPStatusRange
	ResponseKeyword      string
	DisableRedirects     bool
	SkipTLSVerification  bool
	WebSocketSuccess     WebSocketSuccess
	SendType             MessageType
	SendPayload          string
	ReceiveType          MessageType
	ExpectedMessage      string
	PingPayloadFormat    PayloadFormat
	PingPayload          string
	Source               string
}

type Certificate struct {
	Subject       string    `json:"subject,omitempty"`
	Issuer        string    `json:"issuer,omitempty"`
	NotBefore     time.Time `json:"notBefore,omitempty"`
	NotAfter      time.Time `json:"notAfter,omitempty"`
	DaysRemaining int       `json:"daysRemaining,omitempty"`
	TLSVersion    string    `json:"tlsVersion,omitempty"`
	Verified      bool      `json:"verified"`
}

type Evidence struct {
	Success        bool
	StatusCode     int
	Latency        time.Duration
	CheckedAt      time.Time
	ErrorCategory  string
	Summary        string
	TechnicalError string
	Certificate    Certificate
}

type Monitor struct {
	ID           string
	Config       Config
	State        State
	FailureCount int
	SortOrder    int
	NextCheckAt  time.Time
	Latest       Evidence
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
	generation   int64
}

type Incident struct {
	ID          string
	MonitorID   string
	StartedAt   time.Time
	EndedAt     time.Time
	Category    string
	Summary     string
	CloseReason string
}

type Availability string

const (
	AvailabilityGap  Availability = "gap"
	AvailabilityUp   Availability = "up"
	AvailabilityDown Availability = "down"
)

// AvailabilityBucket describes one time segment in an availability history.
// A failed check wins the bucket state so a short outage remains visible even
// when a later check recovered inside the same segment. Provisional marks the
// current empty segment as a fresh continuation of the latest known state; it
// never increases the check counters.
type AvailabilityBucket struct {
	StartedAt        time.Time
	State            Availability
	Provisional      bool
	TotalChecks      int
	SuccessfulChecks int
	FailedChecks     int
}

// IncidentSnapshot adds live scheduling context to the currently open
// incident. FailureCount is the monitor's current consecutive failure count.
type IncidentSnapshot struct {
	Incident
	FailureCount int
	Duration     time.Duration
	NextCheckAt  time.Time
}

// DetailSnapshot is the complete read model used by the website detail
// surface. Statistics and recent checks cover the preceding 24 hours.
type DetailSnapshot struct {
	Monitor             Monitor
	Availability        []AvailabilityBucket
	AvailabilityPercent float64
	AverageLatency      time.Duration
	P95Latency          time.Duration
	TotalChecks         int
	SuccessfulChecks    int
	FailedChecks        int
	IncidentCount       int
	RecentChecks        []Evidence
	CurrentIncident     *IncidentSnapshot
	Incidents           []Incident
}

type ErrorCode string

const (
	ErrorSelectionRequired ErrorCode = "selection_required"
	ErrorDuplicate         ErrorCode = "duplicate"
	ErrorStaleScan         ErrorCode = "stale_scan"
	ErrorInvalidInput      ErrorCode = "invalid_input"
	ErrorConflict          ErrorCode = "conflict"
	ErrorNotFound          ErrorCode = "not_found"
)

// OperationError gives browser clients a stable error code while preserving a
// useful human-readable message for the no-JavaScript HTML flow.
type OperationError struct {
	Code    ErrorCode
	Message string
	Field   string
	Err     error
}

func (e *OperationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Code)
}

func (e *OperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func operationError(code ErrorCode, message, field string, err error) error {
	if message == "" && err == nil {
		message = fmt.Sprintf("website monitor operation failed: %s", code)
	}
	return &OperationError{Code: code, Message: message, Field: field, Err: err}
}

type Filter struct {
	State          State
	Scope          Scope
	IncludeDeleted bool
}

type Probe interface {
	Check(context.Context, Config) Evidence
}

type Options struct {
	Probe          Probe
	Now            func() time.Time
	Tick           time.Duration
	RetryDelay     time.Duration
	MaxConcurrency int
	NginxProcesses NginxProcessSource
}
