package workflow

import (
	"encoding/json"
	"reflect"
	"testing"
)

func fixture() Graph {
	return Graph{Nodes: []Node{
		{ID: "source", Kind: "literal", Outputs: []Field{{Name: "value", Type: String}}},
		{ID: "build", Kind: "script", Inputs: []Field{{Name: "version", Type: String, Required: true, Default: json.RawMessage(`"dev"`)}}},
	}, Links: []DataLink{{Port{"source", "value"}, Port{"build", "version"}}}}
}
func TestCompileAndResolve(t *testing.T) {
	g := fixture()
	g.Dependencies = []Dependency{{From: "source", To: "build"}}
	order, err := Compile(g)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"source", "build"}) {
		t.Fatal(order)
	}
	result := map[string]map[string]json.RawMessage{"source": {"value": json.RawMessage(`"v2"`)}}
	values, err := ResolveInputs(g, "build", result)
	if err != nil {
		t.Fatal(err)
	}
	if string(values["version"]) != `"v2"` {
		t.Fatal(values)
	}
	values["version"][1] = 'x'
	if string(result["source"]["value"]) != `"v2"` {
		t.Fatal("result buffer was aliased")
	}
}
func TestConnectedInputNeverFallsBack(t *testing.T) {
	for _, required := range []bool{true, false} {
		t.Run(map[bool]string{true: "required", false: "optional"}[required], func(t *testing.T) {
			g := fixture()
			g.Nodes[1].Inputs[0].Required = required
			g.Nodes[1].Values = map[string]json.RawMessage{"version": json.RawMessage(`"local"`)}
			for _, result := range []map[string]map[string]json.RawMessage{nil, {"source": {"value": json.RawMessage("false")}}} {
				if _, err := ResolveInputs(g, "build", result); err == nil {
					t.Fatal("connected input fell back")
				}
			}
		})
	}
}
func TestRequiredLocalAndLinkOnly(t *testing.T) {
	g := fixture()
	g.Links = nil
	v, err := ResolveInputs(g, "build", nil)
	if err != nil || string(v["version"]) != `"dev"` {
		t.Fatal(v, err)
	}
	g.Nodes[1].Inputs[0].Source = LinkOnly
	g.Nodes[1].Inputs[0].Default = nil
	if _, err := Compile(g); err == nil {
		t.Fatal("required link accepted missing source")
	}
	g.Nodes[1].Inputs[0].Required = false
	g.Nodes[1].Values = map[string]json.RawMessage{"version": json.RawMessage(`"local"`)}
	v, err = ResolveInputs(g, "build", nil)
	if err != nil || len(v) != 0 {
		t.Fatal("optional link-only used local value", v, err)
	}
}
func TestInvalidGraphs(t *testing.T) {
	cases := map[string]func(*Graph){
		"duplicate input":         func(g *Graph) { g.Nodes[1].Inputs = append(g.Nodes[1].Inputs, g.Nodes[1].Inputs[0]) },
		"duplicate node":          func(g *Graph) { g.Nodes = append(g.Nodes, g.Nodes[0]) },
		"duplicate connection":    func(g *Graph) { g.Links = append(g.Links, g.Links[0]) },
		"cycle across edge kinds": func(g *Graph) { g.Dependencies = []Dependency{{From: "build", To: "source"}} },
		"missing port":            func(g *Graph) { g.Links[0].From.Field = "missing" },
		"missing dependency":      func(g *Graph) { g.Dependencies = []Dependency{{From: "missing", To: "build"}} },
		"type mismatch":           func(g *Graph) { g.Nodes[0].Outputs[0].Type = Number },
		"bad default":             func(g *Graph) { g.Nodes[1].Inputs[0].Default = json.RawMessage("false") },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			g := fixture()
			change(&g)
			if _, err := Compile(g); err == nil {
				t.Fatal("invalid graph accepted")
			}
		})
	}
}
func TestValueTypes(t *testing.T) {
	for _, tc := range []struct {
		kind  ValueType
		raw   string
		valid bool
	}{
		{String, `""`, true}, {String, "null", false}, {Number, "0", true}, {Number, `"1"`, false},
		{Boolean, "false", true}, {Boolean, "null", false}, {JSON, "null", true}, {JSON, `{"x":[1]}`, true}, {JSON, "broken", false},
	} {
		err := ValidateValue(tc.kind, json.RawMessage(tc.raw))
		if (err == nil) != tc.valid {
			t.Errorf("%s %s: %v", tc.kind, tc.raw, err)
		}
	}
}
