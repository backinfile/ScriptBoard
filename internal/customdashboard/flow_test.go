package customdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// flowTestManager 注入内存版快捷执行项表，供 flow 解析与校验测试使用。
func flowTestManager(t *testing.T, quickRuns map[string]string) *Manager {
	t.Helper()
	manager := testManager(t)
	manager.quickRunByName = func(_ context.Context, name string) (string, bool, error) {
		for id, quickRunName := range quickRuns {
			if quickRunName == name {
				return id, true, nil
			}
		}
		return "", false, nil
	}
	manager.quickRunExists = func(_ context.Context, id string) (bool, error) {
		_, ok := quickRuns[id]
		return ok, nil
	}
	return manager
}

const flowTestYAML = `version: 1
flow:
  nodes:
    - id: build
      name: 构建
      run: build-app
    - id: test
      name: 测试
      needs: [build]
      run: run-tests
    - id: deploy
      name: 部署
      needs: [test]
      run: deploy-prod
      confirm: true
      timeout: 300
  post:
    - name: 清理临时产物
      run: cleanup
      when: always
`

func TestFlowYAMLNormalizesAndIsIdempotent(t *testing.T) {
	manager := flowTestManager(t, map[string]string{"qr-build": "build-app", "qr-test": "run-tests", "qr-deploy": "deploy-prod", "qr-clean": "cleanup"})
	config, err := manager.NormalizeFlowYAML(context.Background(), flowTestYAML)
	if err != nil {
		t.Fatal(err)
	}
	var envelope flowConfigEnvelope
	if err := json.Unmarshal(config, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Flow.Nodes) != 3 || len(envelope.Flow.Post) != 1 {
		t.Fatalf("definition=%+v", envelope.Flow)
	}
	if envelope.Flow.Nodes[0].RunID != "qr-build" || envelope.Flow.Nodes[2].RunID != "qr-deploy" || envelope.Flow.Post[0].RunID != "qr-clean" {
		t.Fatalf("run ids not resolved: %+v", envelope.Flow)
	}
	if !envelope.Flow.Nodes[2].Confirm || envelope.Flow.Nodes[2].Timeout != 300 {
		t.Fatalf("node options lost: %+v", envelope.Flow.Nodes[2])
	}
	// 幂等：规范化 JSON 再次经过落库校验后逐字节一致。
	again, err := manager.validateFlowConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(config) {
		t.Fatalf("normalization not idempotent:\n%s\n%s", config, again)
	}
	// YAML 往返：还原后的 YAML 重新规范化结果一致。
	card := Card{ID: "card-1", Type: CardFlow, Config: config}
	yamlText, err := FlowYAML(card)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := manager.NormalizeFlowYAML(context.Background(), yamlText)
	if err != nil {
		t.Fatalf("round-trip YAML invalid: %v\n%s", err, yamlText)
	}
	if string(reparsed) != string(config) {
		t.Fatalf("YAML round-trip changed config:\n%s\n%s", config, reparsed)
	}
}

func TestFlowYAMLValidationProblems(t *testing.T) {
	manager := flowTestManager(t, map[string]string{"qr-a": "run-a", "qr-b": "run-b"})
	cases := []struct {
		name    string
		yaml    string
		problem string
	}{
		{"空配置", "", "流程配置不能为空"},
		{"非法YAML", "version: [", "不是有效的 YAML"},
		{"未知字段", "version: 1\nflow:\n  nodes: []\n  nope: 1\n", "不是有效的 YAML"},
		{"版本不支持", "version: 2\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a}\n", "version 仅支持 1"},
		{"无节点", "version: 1\nflow:\n  nodes: []\n", "至少需要一个节点"},
		{"ID非法", "version: 1\nflow:\n  nodes:\n    - {id: 'Build!', name: A, run: run-a}\n", "节点 ID \"Build!\" 无效"},
		{"ID重复", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a}\n    - {id: a, name: B, run: run-b}\n", "重复"},
		{"needs悬空", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a, needs: [ghost]}\n", "不存在的节点 \"ghost\""},
		{"needs自身", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a, needs: [a]}\n", "不能引用自身"},
		{"依赖环", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a, needs: [b]}\n    - {id: b, name: B, run: run-b, needs: [a]}\n", "循环"},
		{"run缺失", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A}\n", "必须指定 run、script、uses 之一"},
		{"run未知", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: nope}\n", "快捷执行项 \"nope\" 不存在"},
		{"when非法", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a}\n  post:\n    - {name: 清理, run: run-b, when: sometimes}\n", "when 仅支持"},
		{"post超限", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a}\n  post:\n    - {name: p1, run: run-b}\n    - {name: p2, run: run-b}\n    - {name: p3, run: run-b}\n    - {name: p4, run: run-b}\n    - {name: p5, run: run-b}\n", "post 步骤最多 4 个"},
		{"timeout超限", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a, timeout: 99999}\n", "timeout 需在 0-86400"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := manager.NormalizeFlowYAML(context.Background(), testCase.yaml)
			var configError *FlowConfigError
			if !errors.As(err, &configError) {
				t.Fatalf("err=%v, want FlowConfigError", err)
			}
			if !strings.Contains(err.Error(), testCase.problem) {
				t.Fatalf("err=%q, want substring %q", err.Error(), testCase.problem)
			}
		})
	}
	// 节点超限单独构造。
	var nodes strings.Builder
	nodes.WriteString("version: 1\nflow:\n  nodes:\n")
	for index := 0; index < 21; index++ {
		fmt.Fprintf(&nodes, "    - {id: n%d, name: N%d, run: run-a}\n", index, index)
	}
	if _, err := manager.NormalizeFlowYAML(context.Background(), nodes.String()); !strings.Contains(err.Error(), "最多 20 个") {
		t.Fatalf("node limit err=%v", err)
	}
}

func TestFlowCardPersistsNormalizedConfig(t *testing.T) {
	manager := flowTestManager(t, map[string]string{"qr-a": "run-a"})
	dashboard, err := manager.CreateDashboard(context.Background(), DashboardInput{Name: "流程", Slug: "flow-board"})
	if err != nil {
		t.Fatal(err)
	}
	config, err := manager.NormalizeFlowYAML(context.Background(), "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a}\n")
	if err != nil {
		t.Fatal(err)
	}
	card, err := manager.CreateCard(context.Background(), dashboard.ID, CardInput{Name: "发布流程", Type: CardFlow, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	definition, ok := FlowDefinitionOf(card)
	if !ok || len(definition.Nodes) != 1 || definition.Nodes[0].RunID != "qr-a" {
		t.Fatalf("definition=%+v ok=%v", definition, ok)
	}
	// 引用不存在快捷执行项的配置被拒绝。
	tampered := strings.Replace(string(config), "qr-a", "qr-ghost", 1)
	if _, err := manager.CreateCard(context.Background(), dashboard.ID, CardInput{Name: "坏流程", Type: CardFlow, Config: json.RawMessage(tampered)}); err == nil || !strings.Contains(err.Error(), "快捷执行项不存在") {
		t.Fatalf("tampered config err=%v", err)
	}
	// 空配置被拒绝。
	if _, err := manager.CreateCard(context.Background(), dashboard.ID, CardInput{Name: "空流程", Type: CardFlow, Config: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("empty flow config accepted")
	}
}

type flowFakeStep struct {
	statuses []string
}

// flowFakeRuntime 是可编程的 Start/Status/Stop 假实现。
type flowFakeRuntime struct {
	runs    map[string]*flowFakeStep
	started []string
	stopped []string
	next    int
}

func (f *flowFakeRuntime) start(_ context.Context, _ string, node FlowNode, _ FlowActor) (string, error) {
	f.next++
	runID := fmt.Sprintf("run-%d", f.next)
	f.started = append(f.started, node.RunID)
	// 测试可预先注入 run-N 的状态脚本，这里不覆盖。
	if _, present := f.runs[runID]; !present {
		f.runs[runID] = &flowFakeStep{}
	}
	return runID, nil
}

func (f *flowFakeRuntime) status(_ context.Context, runID string) (string, error) {
	step := f.runs[runID]
	if len(step.statuses) == 0 {
		return "succeeded", nil
	}
	status := step.statuses[0]
	step.statuses = step.statuses[1:]
	return status, nil
}

func (f *flowFakeRuntime) stop(_ context.Context, runID string) error {
	f.stopped = append(f.stopped, runID)
	return nil
}

func flowTestCard(t *testing.T, manager *Manager, yamlText string) Card {
	t.Helper()
	config, err := manager.NormalizeFlowYAML(context.Background(), yamlText)
	if err != nil {
		t.Fatal(err)
	}
	return Card{ID: "card-flow", Type: CardFlow, Config: config}
}

func waitFlowFinished(t *testing.T, runner *FlowRunner, cardID string) FlowRunView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		view, ok := runner.Snapshot(cardID)
		if ok && view.Finished {
			return view
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("flow run did not finish")
	return FlowRunView{}
}

func TestFlowRunnerExecutesInTopologicalOrder(t *testing.T) {
	manager := flowTestManager(t, map[string]string{"qr-build": "build-app", "qr-test": "run-tests", "qr-deploy": "deploy-prod", "qr-clean": "cleanup"})
	card := flowTestCard(t, manager, flowTestYAML)
	runtime := &flowFakeRuntime{runs: map[string]*flowFakeStep{}}
	runner := NewFlowRunner(FlowRunnerOptions{Start: runtime.start, Status: runtime.status, Stop: runtime.stop, Poll: time.Millisecond})
	defer runner.Close()
	view, err := runner.Start(card, FlowActor{Username: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != FlowRunRunning || len(view.Nodes) != 3 {
		t.Fatalf("initial view=%+v", view)
	}
	final := waitFlowFinished(t, runner, card.ID)
	if final.Status != FlowRunSucceeded {
		t.Fatalf("final=%+v", final)
	}
	for _, node := range final.Nodes {
		if node.Status != FlowNodeSucceeded || node.RunID == "" {
			t.Fatalf("node=%+v", node)
		}
		// 节点应带回开始时间与耗时，供面板展示。
		if node.StartedAtMs <= 0 {
			t.Fatalf("node missing startedAtMs: %+v", node)
		}
	}
	if final.StartedAtMs <= 0 || final.FinishedAtMs < final.StartedAtMs {
		t.Fatalf("run timestamps invalid: %+v", final)
	}
	if len(final.Posts) != 1 || final.Posts[0].Status != FlowNodeSucceeded {
		t.Fatalf("posts=%+v", final.Posts)
	}
	// 拓扑串行：build → test → deploy → post cleanup。
	want := []string{"qr-build", "qr-test", "qr-deploy", "qr-clean"}
	if strings.Join(runtime.started, ",") != strings.Join(want, ",") {
		t.Fatalf("started=%v, want %v", runtime.started, want)
	}
}

func TestFlowRunnerSkipsDownstreamAndRunsFailurePost(t *testing.T) {
	yamlText := `version: 1
flow:
  nodes:
    - id: build
      name: 构建
      run: build-app
    - id: deploy
      name: 部署
      needs: [build]
      run: deploy-prod
  post:
    - name: 失败清理
      run: cleanup
      when: on_failure
    - name: 成功通知
      run: notify
      when: on_success
`
	manager := flowTestManager(t, map[string]string{"qr-build": "build-app", "qr-deploy": "deploy-prod", "qr-clean": "cleanup", "qr-notify": "notify"})
	card := flowTestCard(t, manager, yamlText)
	runtime := &flowFakeRuntime{runs: map[string]*flowFakeStep{}}
	runner := NewFlowRunner(FlowRunnerOptions{Start: runtime.start, Status: runtime.status, Stop: runtime.stop, Poll: time.Millisecond})
	defer runner.Close()
	// 第一个节点（build）直接失败（start 不会覆盖预注入的状态脚本）。
	runtime.runs["run-1"] = &flowFakeStep{statuses: []string{"failed"}}
	if _, err := runner.Start(card, FlowActor{}); err != nil {
		t.Fatal(err)
	}
	final := waitFlowFinished(t, runner, card.ID)
	if final.Status != FlowRunFailed {
		t.Fatalf("final=%+v", final)
	}
	if final.Nodes[0].Status != FlowNodeFailed || final.Nodes[1].Status != FlowNodeSkipped {
		t.Fatalf("nodes=%+v", final.Nodes)
	}
	if len(final.Posts) != 1 || final.Posts[0].Name != "失败清理" || final.Posts[0].Status != FlowNodeSucceeded {
		t.Fatalf("posts=%+v", final.Posts)
	}
	if strings.Contains(strings.Join(runtime.started, ","), "qr-deploy") || strings.Contains(strings.Join(runtime.started, ","), "qr-notify") {
		t.Fatalf("unexpected starts=%v", runtime.started)
	}
}

func TestFlowRunnerConflictWhileRunning(t *testing.T) {
	manager := flowTestManager(t, map[string]string{"qr-a": "run-a"})
	card := flowTestCard(t, manager, "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a}\n")
	block := make(chan struct{})
	runtime := &flowFakeRuntime{runs: map[string]*flowFakeStep{}}
	runner := NewFlowRunner(FlowRunnerOptions{
		Start: runtime.start,
		Status: func(ctx context.Context, runID string) (string, error) {
			select {
			case <-block:
				return "succeeded", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
		Stop: runtime.stop, Poll: time.Millisecond,
	})
	defer close(block)
	defer runner.Close()
	if _, err := runner.Start(card, FlowActor{}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Start(card, FlowActor{}); !errors.Is(err, ErrFlowRunConflict) {
		t.Fatalf("second start err=%v, want conflict", err)
	}
}

func TestFlowRunnerRecordsHistory(t *testing.T) {
	manager := flowTestManager(t, map[string]string{"qr-a": "run-a"})
	card := flowTestCard(t, manager, "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a}\n")
	historyPath := filepath.Join(t.TempDir(), "flow-history.json")
	newRunner := func() *FlowRunner {
		runtime := &flowFakeRuntime{runs: map[string]*flowFakeStep{}}
		return NewFlowRunner(FlowRunnerOptions{Start: runtime.start, Status: runtime.status, Stop: runtime.stop, Poll: time.Millisecond, HistoryPath: historyPath})
	}
	runner := newRunner()
	defer runner.Close()
	if _, err := runner.Start(card, FlowActor{Username: "admin"}); err != nil {
		t.Fatal(err)
	}
	waitFlowFinished(t, runner, card.ID)
	if _, err := runner.Start(card, FlowActor{Username: "admin"}); err != nil {
		t.Fatal(err)
	}
	waitFlowFinished(t, runner, card.ID)
	entries := runner.History(card.ID)
	if len(entries) != 2 {
		t.Fatalf("entries=%v", entries)
	}
	// 新条目在前，字段完整。
	if !strings.HasSuffix(entries[0].ID, "-2") || !strings.HasSuffix(entries[1].ID, "-1") {
		t.Fatalf("entries not newest-first: %+v", entries)
	}
	latest := entries[0]
	if latest.Status != FlowRunSucceeded || latest.StartedAtMs <= 0 || latest.FinishedAtMs < latest.StartedAtMs || latest.DurationMs != latest.FinishedAtMs-latest.StartedAtMs || latest.TriggeredBy != "admin" {
		t.Fatalf("latest=%+v", latest)
	}
	// 历史持久化：重建 runner 后仍在。
	reloaded := newRunner()
	defer reloaded.Close()
	if got := reloaded.History(card.ID); len(got) != 2 || got[0].ID != latest.ID {
		t.Fatalf("reloaded history=%v", got)
	}
}

func TestFlowRunnerNodeTimeoutStopsRun(t *testing.T) {
	yamlText := "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a, timeout: 1}\n"
	manager := flowTestManager(t, map[string]string{"qr-a": "run-a"})
	card := flowTestCard(t, manager, yamlText)
	runtime := &flowFakeRuntime{runs: map[string]*flowFakeStep{}}
	// 状态永不结束，依赖 1 秒超时触发 Stop。
	runner := NewFlowRunner(FlowRunnerOptions{
		Start:  runtime.start,
		Status: func(_ context.Context, _ string) (string, error) { return "running", nil },
		Stop:   runtime.stop,
		Poll:   time.Millisecond,
	})
	defer runner.Close()
	if _, err := runner.Start(card, FlowActor{}); err != nil {
		t.Fatal(err)
	}
	final := waitFlowFinished(t, runner, card.ID)
	if final.Status != FlowRunFailed || final.Nodes[0].Status != FlowNodeFailed {
		t.Fatalf("final=%+v", final)
	}
	if len(runtime.stopped) != 1 {
		t.Fatalf("stopped=%v, want timeout stop", runtime.stopped)
	}
}

func TestFlowConcurrentHistoryAndSnapshot(t *testing.T) {
	for iteration := 0; iteration < 10; iteration++ {
		path := filepath.Join(t.TempDir(), "history.json")
		gate := make(chan struct{})
		r := NewFlowRunner(FlowRunnerOptions{HistoryPath: path, RunBuiltin: func(context.Context, string, string, map[string]string) (string, error) { <-gate; return "ok", nil }})
		snapshots := []FlowRunView{}
		for i := 0; i < 16; i++ {
			view, err := r.Start(Card{ID: fmt.Sprint(i), Type: CardFlow, Config: json.RawMessage(`{"flow":{"nodes":[{"id":"one","name":"one","uses":"sleep"}]}}`)}, FlowActor{})
			if err != nil {
				t.Fatal(err)
			}
			snapshots = append(snapshots, view)
		}
		close(gate)
		r.Close()
		if n := len(loadFlowHistory(path)); n != 16 {
			t.Fatalf("history cards=%d", n)
		}
		for _, view := range snapshots {
			if view.Nodes[0].Status != FlowNodeQueued {
				t.Fatal("Start snapshot mutated")
			}
		}
	}
}
func TestFlowNodePostsResolveExecuteAndRequirePermission(t *testing.T) {
	m := flowTestManager(t, map[string]string{"clean-id": "cleanup"})
	config, err := m.NormalizeFlowYAML(context.Background(), `flow:
  nodes:
    - id: task
      name: task
      uses: sleep
      with: {seconds: "0"}
      post:
        - name: cleanup
          run: cleanup
          when: on_success
        - name: failure-only
          uses: sleep
          with: {seconds: "0"}
          when: on_failure
`)
	if err != nil {
		t.Fatal(err)
	}
	var env flowConfigEnvelope
	json.Unmarshal(config, &env)
	if env.Flow.Nodes[0].Post[0].RunID != "clean-id" {
		t.Fatal("node post run unresolved")
	}
	seen := make(chan string, 1)
	r := NewFlowRunner(FlowRunnerOptions{RunBuiltin: func(context.Context, string, string, map[string]string) (string, error) { return "ok", nil }, Start: func(_ context.Context, _ string, n FlowNode, _ FlowActor) (string, error) {
		seen <- n.RunID
		return "run", nil
	}, Status: func(context.Context, string) (string, error) { return "succeeded", nil }})
	_, err = r.Start(Card{ID: "card", Type: CardFlow, Config: config}, FlowActor{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-seen:
		if id != "clean-id" {
			t.Fatal(id)
		}
	case <-time.After(time.Second):
		t.Fatal("post not executed")
	}
	r.Close()
	view, _ := r.Snapshot("card")
	if len(view.Posts) != 1 || view.Posts[0].NodeID != "task" {
		t.Fatalf("posts=%+v", view.Posts)
	}
	if !FlowNeedsManageExecution(json.RawMessage(`{"flow":{"nodes":[{"run":"x","post":[{"uses":"sleep"}]}]}}`)) {
		t.Fatal("node posts bypass management permission")
	}
}
