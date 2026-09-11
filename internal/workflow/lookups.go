package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// captureLookups reads only explicitly referenced, non-secret variables in the enqueue transaction.
func captureLookups(ctx context.Context, tx *sql.Tx, snapshot *Snapshot) error {
	snapshot.Variables = map[string]json.RawMessage{}
	for _, n := range snapshot.Workflow.Graph.Nodes {
		if n.Kind != "lookup" {
			continue
		}
		var path string
		if err := json.Unmarshal(n.Config["path"], &path); err != nil {
			return fmt.Errorf("%w: lookup path required", ErrInvalid)
		}
		if !strings.HasPrefix(path, "variables.") {
			continue
		}
		name := strings.TrimPrefix(path, "variables.")
		var value, kind string
		if err := tx.QueryRowContext(ctx, "SELECT value,value_type FROM variables WHERE name=? AND is_password=0", name).Scan(&value, &kind); err != nil {
			return fmt.Errorf("%w: variable %s unavailable", ErrInvalid, name)
		}
		if kind == "integer" || kind == "float" || kind == "bool" {
			if !json.Valid([]byte(value)) {
				return fmt.Errorf("%w: invalid variable %s", ErrInvalid, name)
			}
			snapshot.Variables[name] = json.RawMessage(value)
		} else {
			snapshot.Variables[name], _ = json.Marshal(value)
		}
	}
	return nil
}
func lookupValue(snapshot Snapshot, path string) (json.RawMessage, error) {
	if strings.HasPrefix(path, "variables.") {
		if raw, ok := snapshot.Variables[strings.TrimPrefix(path, "variables.")]; ok {
			return raw, nil
		}
		return nil, fmt.Errorf("variable %s missing from snapshot", path)
	}
	path = strings.TrimPrefix(path, "entry.")
	parts := strings.Split(path, ".")
	raw, ok := snapshot.Entry.Values[parts[0]]
	if !ok {
		return nil, fmt.Errorf("Entry path %s missing", path)
	}
	for _, part := range parts[1:] {
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil {
			return nil, fmt.Errorf("Entry path %s is not an object", path)
		}
		raw, ok = object[part]
		if !ok {
			return nil, fmt.Errorf("Entry path %s missing", path)
		}
	}
	return raw, nil
}
func validateNodeKinds(d Definition) error {
	for _, n := range d.Graph.Nodes {
		switch n.Kind {
		case "start":
			if len(n.Inputs) > 0 || len(n.Outputs) != len(d.Parameters) {
				return fmt.Errorf("%w: start ports must match Entry parameters", ErrInvalid)
			}
			for i, f := range n.Outputs {
				p := d.Parameters[i]
				if f.Name != p.Name || f.Type != p.Type || f.Required != p.Required {
					return fmt.Errorf("%w: start ports must match Entry parameters", ErrInvalid)
				}
			}
		case "end":
			if len(n.Outputs) > 0 {
				return fmt.Errorf("%w: end exposes its input fields as the run result", ErrInvalid)
			}
		case "literal", "lookup":
			if len(n.Inputs) > 0 || len(n.Outputs) != 1 || n.Outputs[0].Name != "value" {
				return fmt.Errorf("%w: value nodes require one output named value", ErrInvalid)
			}
			if n.Kind == "literal" {
				if err := ValidateValue(n.Outputs[0].Type, n.Config["value"]); err != nil {
					return fmt.Errorf("%w: literal: %v", ErrInvalid, err)
				}
			}
		case "switch", "merge", "wait_for", "wait", "terminate", "arithmetic", "compare", "logic", "script", "condition", "custom", "git", "maven", "go", "image_build", "image_push", "image_pull", "k3d", "k3s":
		default:
			return fmt.Errorf("%w: unsupported node kind %s", ErrInvalid, n.Kind)
		}
	}
	return nil
}
