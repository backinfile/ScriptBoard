package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func controlConfig(v map[string]any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range v {
		out[k], _ = json.Marshal(v)
	}
	return out
}
func TestCalculations(t *testing.T) {
	for _, tc := range []struct{ kind, op, a, b, want string }{
		{"arithmetic", "add", "2", "3", "5"}, {"arithmetic", "subtract", "2", "3", "-1"}, {"arithmetic", "multiply", "2", "3", "6"}, {"arithmetic", "divide", "7", "2", "3.5"}, {"arithmetic", "modulo", "7", "2", "1"}, {"arithmetic", "power", "2", "3", "8"}, {"arithmetic", "min", "2", "3", "2"}, {"arithmetic", "max", "2", "3", "3"}, {"arithmetic", "abs", "-2", "", "2"}, {"arithmetic", "floor", "2.8", "", "2"}, {"arithmetic", "ceil", "2.1", "", "3"}, {"arithmetic", "round", "-2.5", "", "-3"}, {"logic", "not", "true", "", "false"}, {"logic", "and", "true", "false", "false"}, {"logic", "or", "true", "false", "true"}, {"logic", "xor", "true", "true", "false"}, {"compare", "eq", "2", "\"2\"", "false"}, {"compare", "gt", "3", "2", "true"},
	} {
		t.Run(tc.kind+tc.op, func(t *testing.T) {
			out, err := runCalculation(Node{Kind: tc.kind, Config: controlConfig(map[string]any{"operation": tc.op})}, map[string]json.RawMessage{"a": json.RawMessage(tc.a), "b": json.RawMessage(tc.b)})
			if err != nil || string(out["result"]) != tc.want {
				t.Fatal(out, err)
			}
		})
	}
	for _, tc := range []struct{ kind, op, a, b string }{{"arithmetic", "divide", "1", "0"}, {"arithmetic", "modulo", "1", "0"}, {"arithmetic", "power", "1e300", "2"}, {"logic", "and", "null", "true"}, {"arithmetic", "add", "\"1\"", "2"}} {
		if _, err := runCalculation(Node{Kind: tc.kind, Config: controlConfig(map[string]any{"operation": tc.op})}, map[string]json.RawMessage{"a": json.RawMessage(tc.a), "b": json.RawMessage(tc.b)}); err == nil {
			t.Fatal("accepted", tc)
		}
	}
	if ValidateValue(Integer, json.RawMessage("2.5")) == nil || ValidateValue(Integer, json.RawMessage("9007199254740992")) == nil {
		t.Fatal("invalid integer")
	}
}
func TestSwitchTypesAndModes(t *testing.T) {
	for _, tc := range []struct{ typ, path, op, input, value, upper, want string }{
		{"boolean", "", "eq", "true", "true", "", "hit"}, {"integer", "", "between", "3", "2", "4", "hit"}, {"string", "", "contains", "\"release/v2\"", "\"v2\"", "", "hit"}, {"integer", "result.code", "eq", "{\"result\":{\"code\":200}}", "200", "", "hit"}, {"json", "absent", "missing", "{}", "", "", "hit"}, {"json", "", "is_null", " null ", "", "", "hit"}, {"integer", "", "eq", "2", "3", "", "default"},
	} {
		t.Run(tc.typ+tc.op+tc.path, func(t *testing.T) {
			n := Node{Kind: "switch", Config: controlConfig(map[string]any{"valueType": tc.typ, "path": tc.path, "rules": []BranchRule{{ID: "hit", Operator: tc.op, Value: json.RawMessage(tc.value), Upper: json.RawMessage(tc.upper)}}})}
			if err := validateControl(n); err != nil {
				t.Fatal(err)
			}
			_, routes, err := runSwitch(n, map[string]json.RawMessage{"value": json.RawMessage(tc.input)})
			if err != nil || len(routes) != 1 || routes[0] != tc.want {
				t.Fatal(routes, err)
			}
		})
	}
	n := Node{Kind: "switch", Config: controlConfig(map[string]any{"valueType": "integer", "rules": []BranchRule{{ID: "one", Operator: "gte", Value: json.RawMessage("0")}, {ID: "two", Operator: "lte", Value: json.RawMessage("10")}}})}
	for _, mode := range []string{"first", "all"} {
		n.Config["matchMode"], _ = json.Marshal(mode)
		_, routes, err := runSwitch(n, map[string]json.RawMessage{"value": json.RawMessage("3")})
		want := 1
		if mode == "all" {
			want = 2
		}
		if err != nil || len(routes) != want {
			t.Fatal(routes, err)
		}
	}
	if _, _, err := runSwitch(n, map[string]json.RawMessage{"value": json.RawMessage("\"3\"")}); err == nil {
		t.Fatal("coerced string")
	}
}
func executeControlGraph(t *testing.T, g Graph, ex Executor) Run {
	t.Helper()
	r := testRepository(t)
	ctx := context.Background()
	d, err := r.SaveDraft(ctx, Definition{Name: "control", Graph: g}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	d, err = r.Publish(ctx, d.ID, d.Revision, "admin")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := r.SaveEntry(ctx, Entry{Name: "control", WorkflowID: d.ID, WorkflowRevision: d.Revision}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	run, err := r.Enqueue(ctx, entry.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := r.Claim(ctx, run.ID); err != nil || !ok {
		t.Fatal(err)
	}
	e := Engine{Repository: r, Executor: ex}
	e.execute(ctx, run)
	got, err := r.Run(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func stepByID(t *testing.T, r Run, id string) Step {
	t.Helper()
	for _, s := range r.Steps {
		if s.NodeID == id {
			return s
		}
	}
	t.Fatal(id)
	return Step{}
}
func TestFailureRoutesRecoveryAndJoin(t *testing.T) {
	for _, recover := range []bool{false, true} {
		g := Graph{Nodes: []Node{{ID: "s", Kind: "start"}, {ID: "bad", Kind: "arithmetic", Inputs: []Field{{Name: "a", Type: Number, Default: json.RawMessage("1")}, {Name: "b", Type: Number, Default: json.RawMessage("0")}}, Config: controlConfig(map[string]any{"operation": "divide"})}, {ID: "ok", Kind: "merge"}, {ID: "catch", Kind: "merge", Inputs: []Field{{Name: "error", Type: JSON, Required: true}}}, {ID: "join", Kind: "merge", Config: controlConfig(map[string]any{"joinMode": "any"})}, {ID: "e", Kind: "end"}}, Dependencies: []Dependency{{From: "s", To: "bad"}, {From: "bad", To: "ok", Outlet: "success"}, {From: "bad", To: "catch", Outlet: "failure"}, {From: "ok", To: "join"}, {From: "catch", To: "join"}, {From: "join", To: "e"}}, Links: []DataLink{{From: Port{"bad", "_error"}, To: Port{"catch", "error"}}}}
		if recover {
			g.Nodes = append(g.Nodes, Node{ID: "recover", Kind: "terminate", Config: controlConfig(map[string]any{"status": "succeeded"})})
			g.Dependencies = append(g.Dependencies, Dependency{From: "join", To: "recover"}, Dependency{From: "recover", To: "e"})
		}
		r := executeControlGraph(t, g, nil)
		want := "failed"
		if recover {
			want = "succeeded"
		}
		if r.Status != want || stepByID(t, r, "ok").Status != "skipped" || stepByID(t, r, "catch").Status != "succeeded" || stepByID(t, r, "join").Status != "succeeded" {
			t.Fatal(r)
		}
		if string(stepByID(t, r, "catch").Result["error"]) == "" {
			t.Fatal("missing structured error")
		}
	}
	for _, mode := range []string{"any", "all"} {
		g := Graph{Nodes: []Node{{ID: "s", Kind: "start"}, {ID: "c", Kind: "condition", Inputs: []Field{{Name: "condition", Type: Boolean, Default: json.RawMessage("false")}}}, {ID: "a", Kind: "merge"}, {ID: "b", Kind: "merge"}, {ID: "j", Kind: "merge", Config: controlConfig(map[string]any{"joinMode": mode})}, {ID: "e", Kind: "end"}}, Dependencies: []Dependency{{From: "s", To: "c"}, {From: "c", To: "a", Outlet: "true"}, {From: "c", To: "b", Outlet: "false"}, {From: "a", To: "j"}, {From: "b", To: "j"}, {From: "j", To: "e"}}}
		r := executeControlGraph(t, g, nil)
		want := "succeeded"
		if mode == "all" {
			want = "skipped"
		}
		if stepByID(t, r, "j").Status != want {
			t.Fatal(r)
		}
	}
}

type retryExecutor struct {
	calls     int
	uncertain bool
}

func (f *retryExecutor) Execute(ctx context.Context, r Run, n Node, in map[string]json.RawMessage, started func(string) error) (map[string]json.RawMessage, error) {
	f.calls++
	if err := started(fmt.Sprintf("process-%d", f.calls)); err != nil {
		return nil, err
	}
	if f.uncertain {
		return nil, ErrUncertain
	}
	if f.calls == 1 {
		return nil, fmt.Errorf("first attempt")
	}
	return map[string]json.RawMessage{}, nil
}
func TestRetryRecordsAndUncertainty(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		f := &retryExecutor{uncertain: uncertain}
		r := executeControlGraph(t, Graph{Nodes: []Node{{ID: "s", Kind: "start"}, {ID: "n", Kind: "script", Config: controlConfig(map[string]any{"retryAttempts": 3, "retryDelayMs": 0})}, {ID: "e", Kind: "end"}}, Dependencies: []Dependency{{From: "s", To: "n"}, {From: "n", To: "e"}}}, f)
		want := 2
		status := "succeeded"
		if uncertain {
			want = 1
			status = "needs_attention"
		}
		step := stepByID(t, r, "n")
		if r.Status != status || f.calls != want || len(step.Attempts) != want || step.Attempts[0].ProcessID != "process-1" {
			t.Fatal(r, f.calls)
		}
		if step.Attempts[want-1].Status != status {
			t.Fatal(step.Attempts)
		}
	}
}
func TestControlValidationAndCancellation(t *testing.T) {
	g := Graph{Nodes: []Node{{ID: "a", Kind: "start"}, {ID: "b", Kind: "end"}}, Dependencies: []Dependency{{From: "a", To: "b", Outlet: "invalid"}}}
	if _, err := Compile(g); err == nil {
		t.Fatal("invalid outlet")
	}
	g.Dependencies[0].Outlet = "success"
	g.Dependencies = append(g.Dependencies, Dependency{From: "a", To: "b", Outlet: "always"})
	if _, err := Compile(g); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := Engine{}
	start := time.Now()
	if e.waitControl(ctx, "", time.Hour) == nil || time.Since(start) > time.Second {
		t.Fatal("wait cancellation")
	}
}

func TestWaitForCompletionAndCycle(t *testing.T) {
	g := Graph{Nodes: []Node{{ID: "s", Kind: "start"}, {ID: "timer", Kind: "wait", Inputs: []Field{{Name: "seconds", Type: Number, Default: json.RawMessage("0.02")}}}, {ID: "done", Kind: "wait_for"}, {ID: "e", Kind: "end"}}, Dependencies: []Dependency{{From: "s", To: "timer"}, {From: "timer", To: "done", Outlet: "always"}, {From: "done", To: "e"}}}
	r := executeControlGraph(t, g, nil)
	if r.Status != "succeeded" {
		t.Fatal(r)
	}
	timer := stepByID(t, r, "timer")
	done := stepByID(t, r, "done")
	if done.Attempts[0].StartedAt < timer.Attempts[0].FinishedAt {
		t.Fatal("waiter ran too early")
	}
	g.Dependencies = append(g.Dependencies, Dependency{From: "done", To: "timer"})
	if _, err := Compile(g); err == nil {
		t.Fatal("cyclic wait accepted")
	}
	g.Dependencies = nil
	if _, err := Compile(g); err == nil {
		t.Fatal("wait without target accepted")
	}
	for _, mode := range []string{"all", "any"} {
		n := Node{ID: "wait", Kind: "wait_for", Config: controlConfig(map[string]any{"joinMode": mode})}
		g := Graph{Dependencies: []Dependency{{From: "a", To: "wait", Outlet: "always"}, {From: "b", To: "wait", Outlet: "always"}}}
		active := activation(g, n, map[string]string{"a": "failed", "b": "skipped"}, map[string]map[string]bool{"a": {"always": true}})
		if active != (mode == "any") {
			t.Fatal(mode, active)
		}
	}
}
