// Package mcpserver adapts ScriptBoard's application capabilities to the MCP
// protocol. It contains no session, OAuth, persistence, or Broker logic.
package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"scriptboard/internal/mcpaccess"
)

type Backend interface {
	HostStatus(context.Context) (any, error)
	ListQuickRuns(context.Context, string, int) (QuickRunPage, error)
	GetRun(context.Context, string) (any, error)
	GetRunLogs(context.Context, string, string, int) (RunLogPage, error)
	StartQuickRun(context.Context, mcpaccess.Principal, StartQuickRunInput) (any, error)
	StopRun(context.Context, mcpaccess.Principal, StopRunInput) (any, error)
}

type QuickRun struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Group          string `json:"group,omitempty"`
	Version        int64  `json:"version"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Running        bool   `json:"running"`
}
type QuickRunPage struct {
	Items      []QuickRun `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}
type RunLogEvent struct {
	Cursor string    `json:"cursor"`
	Time   time.Time `json:"time"`
	Source string    `json:"source"`
	Text   string    `json:"text"`
}
type RunLogPage struct {
	Events     []RunLogEvent `json:"events"`
	NextCursor string        `json:"next_cursor,omitempty"`
	Truncated  bool          `json:"truncated"`
}
type PageInput struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type RunInput struct {
	RunID string `json:"run_id" jsonschema:"Run identifier"`
}
type RunLogsInput struct {
	RunID  string `json:"run_id"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type StartQuickRunInput struct {
	QuickRunID     string `json:"quick_run_id"`
	RequestID      string `json:"request_id"`
	ConfirmOverlap bool   `json:"confirm_overlap,omitempty"`
}
type StopRunInput struct {
	RunID     string `json:"run_id"`
	RequestID string `json:"request_id"`
}
type EmptyInput struct{}

type Handler struct {
	access   *mcpaccess.Store
	backend  Backend
	resource string
	protocol http.Handler
	limiter  *mcpaccess.Limiter
}
type principalKey struct{}

func New(access *mcpaccess.Store, backend Backend, resource string) *Handler {
	h := &Handler{access: access, backend: backend, resource: strings.TrimRight(resource, "/"), limiter: mcpaccess.NewLimiter(120, 8)}
	h.protocol = mcp.NewStreamableHTTPHandler(h.serverForRequest, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20, PropagateRequestCancellation: true, DisableLocalhostProtection: true})
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	release, ok := h.limiter.Acquire(mcpaccess.SourceKey(r.RemoteAddr))
	if !ok {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	defer release()
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(auth, "Bearer ") {
		h.unauthorized(w)
		return
	}
	p, err := h.access.Authenticate(r.Context(), strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")), h.resource)
	if err != nil {
		h.unauthorized(w)
		return
	}
	principalRelease, ok := h.limiter.Acquire(p.UserID + "\x00" + p.ClientID)
	if !ok {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	defer principalRelease()
	ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), principalKey{}, p), 30*time.Second)
	defer cancel()
	h.protocol.ServeHTTP(w, r.WithContext(ctx))
}

func (h *Handler) unauthorized(w http.ResponseWriter) {
	base := strings.TrimSuffix(h.resource, "/mcp")
	w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+base+`/.well-known/oauth-protected-resource", scope="scriptboard.observe"`)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
}

func boolPointer(value bool) *bool { return &value }
func (h *Handler) serverForRequest(r *http.Request) *mcp.Server {
	p, ok := r.Context().Value(principalKey{}).(mcpaccess.Principal)
	if !ok {
		return nil
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "scriptboard", Version: "1"}, &mcp.ServerOptions{Instructions: "Use published Quick Runs only. Never infer or request secret values."})
	readonly := mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: boolPointer(false), OpenWorldHint: boolPointer(false)}
	mcp.AddTool(s, &mcp.Tool{Name: "scriptboard.get_host_status", Description: "Return a bounded snapshot of ScriptBoard host status.", Annotations: &readonly}, func(ctx context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, any, error) {
		out, err := h.backend.HostStatus(ctx)
		return nil, out, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "scriptboard.list_quick_runs", Description: "List currently valid published Quick Runs without scripts or variables.", Annotations: &readonly}, func(ctx context.Context, _ *mcp.CallToolRequest, in PageInput) (*mcp.CallToolResult, QuickRunPage, error) {
		out, err := h.backend.ListQuickRuns(ctx, in.Cursor, boundedLimit(in.Limit, 100))
		return nil, out, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "scriptboard.get_run", Description: "Return sanitized Run metadata and result.", Annotations: &readonly}, func(ctx context.Context, _ *mcp.CallToolRequest, in RunInput) (*mcp.CallToolResult, any, error) {
		out, err := h.backend.GetRun(ctx, in.RunID)
		return nil, out, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "scriptboard.get_run_logs", Description: "Return a bounded, redacted page of Run log events.", Annotations: &readonly}, func(ctx context.Context, _ *mcp.CallToolRequest, in RunLogsInput) (*mcp.CallToolResult, RunLogPage, error) {
		out, err := h.backend.GetRunLogs(ctx, in.RunID, in.Cursor, boundedLimit(in.Limit, 200))
		return nil, out, err
	})
	if p.Scopes[mcpaccess.ScopeExecute] {
		write := mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPointer(false), IdempotentHint: true, OpenWorldHint: boolPointer(false)}
		mcp.AddTool(s, &mcp.Tool{Name: "scriptboard.start_quick_run", Description: "Start an existing published Quick Run. request_id is idempotent for 24 hours.", Annotations: &write}, func(ctx context.Context, _ *mcp.CallToolRequest, in StartQuickRunInput) (*mcp.CallToolResult, any, error) {
			if err := validateRequestID(in.RequestID); err != nil {
				return nil, nil, err
			}
			out, err := h.backend.StartQuickRun(ctx, p, in)
			return nil, out, err
		})
		destructive := write
		destructive.DestructiveHint = boolPointer(true)
		mcp.AddTool(s, &mcp.Tool{Name: "scriptboard.stop_run", Description: "Stop an active Run subject to current role and ownership.", Annotations: &destructive}, func(ctx context.Context, _ *mcp.CallToolRequest, in StopRunInput) (*mcp.CallToolResult, any, error) {
			if err := validateRequestID(in.RequestID); err != nil {
				return nil, nil, err
			}
			out, err := h.backend.StopRun(ctx, p, in)
			return nil, out, err
		})
	}
	return s
}
func boundedLimit(value, maximum int) int {
	if value <= 0 {
		return min(50, maximum)
	}
	if value > maximum {
		return maximum
	}
	return value
}
func validateRequestID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return errors.New("request_id must contain 1 to 128 characters")
	}
	return nil
}
