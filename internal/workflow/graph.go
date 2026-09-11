// Package workflow defines typed workflow graphs independently of HTTP and process execution.
package workflow

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
)

type ValueType string

const (
	String  ValueType = "string"
	Number  ValueType = "number"
	Integer ValueType = "integer"
	Boolean ValueType = "boolean"
	JSON    ValueType = "json"
)

type SourcePolicy string

const (
	LocalOrLink SourcePolicy = "local_or_link"
	LinkOnly    SourcePolicy = "link_only"
)

type Field struct {
	Enum     []string        `json:"enum,omitempty"`
	Name     string          `json:"name"`
	Type     ValueType       `json:"type"`
	Required bool            `json:"required"`
	Source   SourcePolicy    `json:"source,omitempty"`
	Default  json.RawMessage `json:"default,omitempty"`
}
type Node struct {
	Name           string                     `json:"name"`
	X              float64                    `json:"x"`
	Y              float64                    `json:"y"`
	Config         map[string]json.RawMessage `json:"config,omitempty"`
	CustomID       string                     `json:"customId,omitempty"`
	CustomRevision int64                      `json:"customRevision,omitempty"`
	Script         *CustomDefinition          `json:"script,omitempty"`

	ID      string                     `json:"id"`
	Kind    string                     `json:"kind"`
	Inputs  []Field                    `json:"inputs"`
	Outputs []Field                    `json:"outputs"`
	Values  map[string]json.RawMessage `json:"values,omitempty"`
}
type Port struct {
	Node  string `json:"node"`
	Field string `json:"field"`
}
type DataLink struct {
	From Port `json:"from"`
	To   Port `json:"to"`
}
type Dependency struct {
	Outlet string `json:"outlet,omitempty"`
	From   string `json:"from"`
	To     string `json:"to"`
}
type Graph struct {
	Nodes        []Node       `json:"nodes"`
	Links        []DataLink   `json:"links"`
	Dependencies []Dependency `json:"dependencies"`
}

var fieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateValue validates a JSON value without coercing strings or accepting null
// as a typed scalar. RawMessage preserves JSON precision until the executor boundary.
func ValidateValue(t ValueType, raw json.RawMessage) error {
	if !json.Valid(raw) {
		return fmt.Errorf("invalid JSON value")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	valid := false
	switch t {
	case String:
		_, valid = v.(string)
	case Integer:
		f, ok := v.(float64)
		valid = ok && math.Trunc(f) == f && math.Abs(f) <= 9007199254740991
	case Number:
		_, valid = v.(float64)
	case Boolean:
		_, valid = v.(bool)
	case JSON:
		valid = true
	default:
		return fmt.Errorf("unsupported type %q", t)
	}
	if !valid {
		return fmt.Errorf("expected %s", t)
	}
	return nil
}
func fields(list []Field, input bool) (map[string]Field, error) {
	out := make(map[string]Field, len(list))
	for _, f := range list {
		if !fieldName.MatchString(f.Name) {
			return nil, fmt.Errorf("invalid field %q", f.Name)
		}
		if _, ok := out[f.Name]; ok {
			return nil, fmt.Errorf("duplicate field %q", f.Name)
		}
		switch f.Type {
		case String, Number, Integer, Boolean, JSON:
		default:
			return nil, fmt.Errorf("unsupported type %q", f.Type)
		}
		if len(f.Enum) > 0 {
			if f.Type != String {
				return nil, fmt.Errorf("enum requires string field %s", f.Name)
			}
			seen := map[string]bool{}
			for _, value := range f.Enum {
				if seen[value] {
					return nil, fmt.Errorf("duplicate enum choice %s", f.Name)
				}
				seen[value] = true
			}
		}
		if input && f.Source != "" && f.Source != LocalOrLink && f.Source != LinkOnly {
			return nil, fmt.Errorf("invalid source policy for %s", f.Name)
		}
		if len(f.Default) > 0 {
			if f.Source == LinkOnly {
				return nil, fmt.Errorf("link-only field %s cannot have a default", f.Name)
			}
			if err := ValidateFieldValue(f, f.Default); err != nil {
				return nil, fmt.Errorf("%s default: %w", f.Name, err)
			}
		}
		out[f.Name] = f
	}
	return out, nil
}

// Compile validates ports and combines data and execution dependencies into a
// deterministic order. It performs no I/O and never starts an executor.
func Compile(g Graph) ([]string, error) {
	nodes := map[string]Node{}
	ins := map[string]map[string]Field{}
	outs := map[string]map[string]Field{}
	for _, n := range g.Nodes {
		if n.ID == "" || n.Kind == "" {
			return nil, fmt.Errorf("node ID and kind are required")
		}
		if _, ok := nodes[n.ID]; ok {
			return nil, fmt.Errorf("duplicate node %s", n.ID)
		}
		var err error
		if ins[n.ID], err = fields(n.Inputs, true); err != nil {
			return nil, fmt.Errorf("%s inputs: %w", n.ID, err)
		}
		if outs[n.ID], err = fields(n.Outputs, false); err != nil {
			return nil, fmt.Errorf("%s outputs: %w", n.ID, err)
		}
		for key, value := range n.Values {
			f, ok := ins[n.ID][key]
			if !ok {
				return nil, fmt.Errorf("%s: unknown local input %s", n.ID, key)
			}
			if err := ValidateValue(f.Type, value); err != nil {
				return nil, fmt.Errorf("%s.%s: %w", n.ID, key, err)
			}
		}
		if err := validateControl(n); err != nil {
			return nil, fmt.Errorf("%s: %w", n.ID, err)
		}
		if _, exists := outs[n.ID]["_error"]; exists {
			return nil, fmt.Errorf("%s: _error is a reserved output", n.ID)
		}
		outs[n.ID]["_error"] = Field{Name: "_error", Type: JSON}
		nodes[n.ID] = n
	}
	linked := map[Port]bool{}
	edges := map[Dependency]bool{}
	for _, e := range g.Links {
		a, ok := outs[e.From.Node][e.From.Field]
		if !ok {
			return nil, fmt.Errorf("unknown output %s.%s", e.From.Node, e.From.Field)
		}
		b, ok := ins[e.To.Node][e.To.Field]
		if !ok {
			return nil, fmt.Errorf("unknown input %s.%s", e.To.Node, e.To.Field)
		}
		if linked[e.To] {
			return nil, fmt.Errorf("multiple sources for %s.%s", e.To.Node, e.To.Field)
		}
		if a.Type != b.Type && b.Type != JSON && !(a.Type == Integer && b.Type == Number) {
			return nil, fmt.Errorf("incompatible types %s -> %s", a.Type, b.Type)
		}
		linked[e.To] = true
		edges[Dependency{From: e.From.Node, To: e.To.Node}] = true
	}
	for _, e := range g.Dependencies {
		if e.Outlet != "" && !controlOutlets(nodes[e.From])[e.Outlet] {
			return nil, fmt.Errorf("unknown control outlet %s.%s", e.From, e.Outlet)
		}
		edges[Dependency{From: e.From, To: e.To}] = true
	}
	for _, n := range g.Nodes {
		if n.Kind == "wait_for" {
			found := false
			for _, e := range g.Dependencies {
				if e.To == n.ID {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("%s: select at least one node to wait for", n.ID)
			}
		}
		for _, f := range n.Inputs {
			if linked[Port{n.ID, f.Name}] {
				continue
			}
			if f.Source == LinkOnly {
				if f.Required {
					return nil, fmt.Errorf("%s.%s requires a link", n.ID, f.Name)
				}
				continue
			}
			if _, ok := n.Values[f.Name]; !ok && len(f.Default) == 0 && f.Required {
				return nil, fmt.Errorf("%s.%s requires a value", n.ID, f.Name)
			}
		}
	}
	counts := map[string]int{}
	children := map[string][]string{}
	for id := range nodes {
		counts[id] = 0
	}
	for e := range edges {
		if _, ok := nodes[e.From]; !ok {
			return nil, fmt.Errorf("unknown dependency source %s", e.From)
		}
		if _, ok := nodes[e.To]; !ok {
			return nil, fmt.Errorf("unknown dependency target %s", e.To)
		}
		counts[e.To]++
		children[e.From] = append(children[e.From], e.To)
	}
	ready := []string{}
	for id, c := range counts {
		if c == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	order := make([]string, 0, len(nodes))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, child := range children[id] {
			counts[child]--
			if counts[child] == 0 {
				ready = append(ready, child)
			}
		}
		sort.Strings(ready)
	}
	if len(order) != len(nodes) {
		return nil, fmt.Errorf("workflow contains a dependency cycle")
	}
	return order, nil
}

// ResolveInputs uses linked results exclusively. A missing upstream output is
// an error even when the input is optional or has a local/default value.
func ResolveInputs(g Graph, id string, results map[string]map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if _, err := Compile(g); err != nil {
		return nil, err
	}
	var node *Node
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			node = &g.Nodes[i]
			break
		}
	}
	if node == nil {
		return nil, fmt.Errorf("unknown node %s", id)
	}
	links := map[string]Port{}
	for _, l := range g.Links {
		if l.To.Node == id {
			links[l.To.Field] = l.From
		}
	}
	values := map[string]json.RawMessage{}
	for _, f := range node.Inputs {
		var raw json.RawMessage
		if from, ok := links[f.Name]; ok {
			var exists bool
			raw, exists = results[from.Node][from.Field]
			if !exists {
				return nil, fmt.Errorf("missing upstream output %s.%s", from.Node, from.Field)
			}
			for _, n := range g.Nodes {
				if n.ID == from.Node {
					for _, out := range n.Outputs {
						if out.Name == from.Field {
							if err := ValidateFieldValue(out, raw); err != nil {
								return nil, fmt.Errorf("upstream %s.%s: %w", from.Node, from.Field, err)
							}
						}
					}
				}
			}
		} else if f.Source != LinkOnly {
			raw = node.Values[f.Name]
			if len(raw) == 0 {
				raw = f.Default
			}
		}
		if len(raw) == 0 {
			if f.Required {
				return nil, fmt.Errorf("missing required input %s", f.Name)
			}
			continue
		}
		if err := ValidateFieldValue(f, raw); err != nil {
			return nil, fmt.Errorf("%s.%s: %w", id, f.Name, err)
		}
		values[f.Name] = append(json.RawMessage(nil), raw...)
	}
	return values, nil
}

func ValidateFieldValue(f Field, raw json.RawMessage) error {
	if err := ValidateValue(f.Type, raw); err != nil {
		return err
	}
	if len(f.Enum) > 0 {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		for _, choice := range f.Enum {
			if choice == value {
				return nil
			}
		}
		return fmt.Errorf("value outside enum for %s", f.Name)
	}
	return nil
}
