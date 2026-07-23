package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

const SubmitBatchToolName = "submit_action_batch"

type CallContext struct {
	Conversation Conversation
	Profile      ModelProfile
	Turn         Turn
}

type QueryHandler func(context.Context, json.RawMessage, CallContext) (json.RawMessage, error)
type ActionPreparer func(context.Context, json.RawMessage, CallContext) (Action, error)

type registeredTool struct {
	definition ToolDefinition
	risk       Risk
	query      QueryHandler
	prepare    ActionPreparer
}

type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]registeredTool
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]registeredTool)}
}

func (r *ToolRegistry) RegisterQuery(definition ToolDefinition, handler QueryHandler) error {
	return r.register(registeredTool{definition: definition, risk: RiskQuery, query: handler})
}

func (r *ToolRegistry) RegisterAction(definition ToolDefinition, risk Risk, prepare ActionPreparer) error {
	if risk != RiskExecute && risk != RiskModify {
		return errors.New("action Tool must be Execute or Modify")
	}
	return r.register(registeredTool{definition: definition, risk: risk, prepare: prepare})
}

func (r *ToolRegistry) register(tool registeredTool) error {
	if tool.definition.Name == "" || len(tool.definition.InputSchema) == 0 {
		return errors.New("Tool name and schema are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[tool.definition.Name]; exists {
		return fmt.Errorf("Tool %q is already registered", tool.definition.Name)
	}
	r.tools[tool.definition.Name] = tool
	return nil
}

func (r *ToolRegistry) definitions(permission Permission) []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var names []string
	for name, tool := range r.tools {
		if riskAllowed(permission, tool.risk) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	result := make([]ToolDefinition, 0, len(names)+1)
	for _, name := range names {
		result = append(result, r.tools[name].definition)
	}
	if permission.Execute || permission.Modify {
		result = append(result, ToolDefinition{
			Name: SubmitBatchToolName, Description: "Freeze and submit all prepared side-effect actions as one ordered batch.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"action_ids":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":20}},"required":["action_ids"],"additionalProperties":false}`),
		})
	}
	return result
}

func (r *ToolRegistry) get(name string) (registeredTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.tools[name]
	return value, ok
}

func riskAllowed(permission Permission, risk Risk) bool {
	permission = permission.normalized()
	switch risk {
	case RiskQuery:
		return permission.Query
	case RiskExecute:
		return permission.Execute
	case RiskModify:
		return permission.Modify
	default:
		return false
	}
}

type ActionExecutor interface {
	Validate(context.Context, Action) error
	Execute(context.Context, Action) (json.RawMessage, error)
}
