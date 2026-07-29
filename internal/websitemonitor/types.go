package websitemonitor

import (
	"context"
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
	HTTPSuccessRange HTTPSuccessMode = "range"
	HTTPSuccessExact HTTPSuccessMode = "exact"
)

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
	Name                string
	Scope               Scope
	Kind                Kind
	URL                 string
	DialHost            string
	Frequency           time.Duration
	Timeout             time.Duration
	HTTPMethod          string
	HTTPContentType     string
	HTTPBody            string
	HTTPSuccessMode     HTTPSuccessMode
	ExpectedStatuses    []int
	ResponseKeyword     string
	DisableRedirects    bool
	SkipTLSVerification bool
	WebSocketSuccess    WebSocketSuccess
	SendType            MessageType
	SendPayload         string
	ReceiveType         MessageType
	ExpectedMessage     string
	PingPayloadFormat   PayloadFormat
	PingPayload         string
	Source              string
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
