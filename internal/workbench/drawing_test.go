package workbench

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDrawingTextEraserValidationAndRoundTrip(t *testing.T) {
	valid := []Stroke{{Color: "#3b5bfd", Points: []Point{{1, 2}, {3, 4}}}, {Kind: "text", Color: "#9333ea", Text: "想法\nNext step", FontSize: 24, Points: []Point{{-30, 40}}}, {Kind: "erase", Color: "#343b49", Points: []Point{{0, 0}, {100, 100}}}}
	state := State{Boards: []Board{{ID: "board", Name: "Canvas", Items: []Item{{ID: "drawing", Type: "draw", Size: "medium", Strokes: valid}}}}}
	if err := Validate(state); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var decoded State
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err = Validate(decoded); err != nil {
		t.Fatal(err)
	}
	got := decoded.Boards[0].Items[0].Strokes
	if got[1].Text != valid[1].Text || got[1].FontSize != 24 || got[2].Kind != "erase" {
		t.Fatal("drawing objects did not round-trip")
	}
	for _, color := range []string{"#3b5bfd", "#343b49", "#dc2626", "#ea580c", "#ca8a04", "#16a34a", "#0d9488", "#0284c7", "#9333ea", "#db2777"} {
		state.Boards[0].Items[0].Strokes = []Stroke{{Color: color, Points: []Point{{1, 2}}}}
		if err := Validate(state); err != nil {
			t.Fatalf("palette %s: %v", color, err)
		}
	}
	for _, bad := range []Stroke{
		{Kind: "text", Color: "#3b5bfd", Text: " ", FontSize: 24, Points: []Point{{0, 0}}},
		{Kind: "text", Color: "#3b5bfd", Text: strings.Repeat("字", 2001), FontSize: 24, Points: []Point{{0, 0}}},
		{Kind: "text", Color: "#3b5bfd", Text: "x", FontSize: 100, Points: []Point{{0, 0}}},
		{Kind: "text", Color: "#3b5bfd", Text: "x", FontSize: 24},
		{Kind: "erase", Color: "#3b5bfd", Text: "x", Points: []Point{{0, 0}}},
		{Kind: "unknown", Color: "#3b5bfd", Points: []Point{{0, 0}}},
		{Color: "javascript:bad", Points: []Point{{0, 0}}},
	} {
		state.Boards[0].Items[0].Strokes = []Stroke{bad}
		if Validate(state) == nil {
			t.Fatalf("invalid stroke accepted: %+v", bad)
		}
	}
}
