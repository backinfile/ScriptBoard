package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestTransferCustomRoundTripAndAtomicity(t *testing.T) {
	ctx := context.Background()
	source := testRepository(t)
	target := testRepository(t)
	c, err := source.SaveCustom(ctx, CustomDefinition{Name: "portable", Language: "python", Code: "def main(inputs): return {}", Inputs: []Field{}, Outputs: []Field{}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	d, err := source.Save(ctx, Definition{Name: "portable flow", Graph: Graph{Nodes: []Node{{ID: "s", Kind: "start"}, {ID: "n", Kind: "custom", Name: "custom", CustomID: c.ID, CustomRevision: 1, Inputs: []Field{}, Outputs: []Field{}}, {ID: "e", Kind: "end"}}, Links: []DataLink{}, Dependencies: []Dependency{{From: "s", To: "n"}, {From: "n", To: "e"}}}}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	c.Code = "def main(inputs): return {\"new\": 2}"
	if _, err = source.SaveCustom(ctx, c, "admin"); err != nil {
		t.Fatal(err)
	}
	bundle, err := source.Export(ctx, d)
	if err != nil || len(bundle.CustomNodes) != 1 || bundle.CustomNodes[0].Revision != 1 {
		t.Fatal(bundle, err)
	}
	bytes, _ := json.Marshal(bundle)
	first, err := target.Import(ctx, bundle, "admin")
	if err != nil {
		t.Fatal(err)
	}
	second, err := target.Import(ctx, bundle, "admin")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(bundle)
	if string(bytes) != string(after) {
		t.Fatal("import mutated bundle")
	}
	if first.ID == second.ID || first.ID == d.ID || first.Graph.Nodes[1].CustomID == c.ID || second.Graph.Nodes[1].CustomID == first.Graph.Nodes[1].CustomID {
		t.Fatal("IDs not isolated")
	}
	var published Definition
	if err = target.Version(ctx, "published", first.ID, 1, &published); err != nil {
		t.Fatal(err)
	}
	if published.Graph.Nodes[1].Script.Code != "def main(inputs): return {}" {
		t.Fatal("wrong script version")
	}
	count := func() int {
		var n int
		target.DB.QueryRow("SELECT COUNT(*) FROM workflow_documents").Scan(&n)
		return n
	}
	before := count()
	invalid := bundle
	invalid.Workflow.Graph.Dependencies = append([]Dependency{}, bundle.Workflow.Graph.Dependencies...)
	invalid.Workflow.Graph.Dependencies = append(invalid.Workflow.Graph.Dependencies, Dependency{From: "e", To: "s"})
	if _, err = target.Import(ctx, invalid, "admin"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if count() != before {
		t.Fatal("partial custom/flow persisted")
	}
	invalid = bundle
	invalid.CustomNodes = nil
	if _, err = target.Import(ctx, invalid, "admin"); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing definition", err)
	}
	invalid = bundle
	invalid.Version = 9
	if _, err = target.Import(ctx, invalid, "admin"); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown version", err)
	}
}
