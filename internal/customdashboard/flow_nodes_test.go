package customdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"scriptboard/internal/quickrun"
)

// flowTestLanguage 返回当前平台可用的一种语言，避免测试绑定具体平台。
func flowTestLanguage() string {
	return quickrun.PlatformLanguages(runtime.GOOS)[0].ID
}

func TestFlowYAMLExecutorValidation(t *testing.T) {
	manager := flowTestManager(t, map[string]string{"qr-a": "run-a"})
	language := flowTestLanguage()
	cases := []struct {
		name    string
		yaml    string
		problem string
	}{
		{"三选一冲突", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a, uses: sleep}\n", "只能选择一个"},
		{"language缺失", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, script: \"echo hi\"}\n", "必须指定 language"},
		{"language不支持", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, script: \"echo hi\", language: cobol}\n", "不是本平台支持的语言"},
		{"uses未注册", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, uses: nope}\n", "未注册"},
		{"with键非法", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, uses: sleep, with: {\"1bad\": \"1\"}}\n", "参数名 \"1bad\" 无效"},
		{"必填输入缺失", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, uses: sleep}\n", "缺少必填输入 \"seconds\""},
		{"uses未声明键", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, uses: sleep, with: {seconds: \"0\", foo: bar}}\n", "不是内置节点声明的输入"},
		{"script空白", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, script: \"  \", language: " + language + "}\n", "必须指定 run、script、uses 之一"},
		{"run的with键非法", "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a, with: {\"a b\": \"1\"}}\n", "参数名 \"a b\" 无效"},
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
	// script 源码超限单独构造。
	oversized := "version: 1\nflow:\n  nodes:\n    - id: a\n      name: A\n      language: " + language + "\n      script: \"" + strings.Repeat("x", quickrun.MaxSourceBytes+1) + "\"\n"
	if _, err := manager.NormalizeFlowYAML(context.Background(), oversized); err == nil || !strings.Contains(err.Error(), "script 无效") {
		t.Fatalf("oversized script err=%v", err)
	}
	// with 值超限。
	longValue := "version: 1\nflow:\n  nodes:\n    - id: a\n      name: A\n      uses: write-file\n      with:\n        path: out.txt\n        content: \"" + strings.Repeat("y", 2001) + "\"\n"
	if _, err := manager.NormalizeFlowYAML(context.Background(), longValue); err == nil || !strings.Contains(err.Error(), "最长 2000") {
		t.Fatalf("long with value err=%v", err)
	}
	// 合法样例：script、http-request 的 header_ 动态键、post 的 uses。
	valid := "version: 1\nflow:\n  nodes:\n    - id: a\n      name: A\n      uses: http-request\n      with: {url: \"https://example.com/health\", header_Authorization: \"Bearer x\"}\n  post:\n    - name: 收尾\n      uses: sleep\n      with: {seconds: \"0\"}\n"
	if _, err := manager.NormalizeFlowYAML(context.Background(), valid); err != nil {
		t.Fatalf("valid yaml err=%v", err)
	}
}

func TestFlowYAMLScriptAndUsesRoundTrip(t *testing.T) {
	manager := flowTestManager(t, map[string]string{"qr-a": "run-a"})
	language := flowTestLanguage()
	yamlText := "version: 1\nflow:\n  nodes:\n    - id: build\n      name: 构建\n      run: run-a\n      with: {target: release}\n    - id: note\n      name: 记录\n      needs: [build]\n      uses: write-file\n      with: {path: notes/out.txt, content: \"done\"}\n      timeout: 30\n    - id: hook\n      name: 钩子\n      needs: [note]\n      language: " + language + "\n      script: |\n        echo line1\n        echo line2\n  post:\n    - name: 收尾\n      uses: sleep\n      with: {seconds: \"0\"}\n      when: on_success\n"
	config, err := manager.NormalizeFlowYAML(context.Background(), yamlText)
	if err != nil {
		t.Fatal(err)
	}
	var envelope flowConfigEnvelope
	if err := json.Unmarshal(config, &envelope); err != nil {
		t.Fatal(err)
	}
	note := envelope.Flow.Nodes[1]
	if note.Uses != "write-file" || note.RunID != "" || note.With["path"] != "notes/out.txt" || note.Timeout != 30 {
		t.Fatalf("uses node not preserved: %+v", note)
	}
	hook := envelope.Flow.Nodes[2]
	if hook.Script != "echo line1\necho line2\n" || hook.Language != language || hook.RunID != "" {
		t.Fatalf("script node not preserved: %+v", hook)
	}
	if envelope.Flow.Nodes[0].With["target"] != "release" || envelope.Flow.Nodes[0].RunID != "qr-a" {
		t.Fatalf("run node with lost: %+v", envelope.Flow.Nodes[0])
	}
	if envelope.Flow.Post[0].Uses != "sleep" || envelope.Flow.Post[0].When != FlowPostOnSuccess {
		t.Fatalf("post not preserved: %+v", envelope.Flow.Post[0])
	}
	// FlowYAML 还原后再规范化结果一致（新字段随 YAML 往返）。
	card := Card{ID: "card-1", Type: CardFlow, Config: config}
	yamlBack, err := FlowYAML(card)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := manager.NormalizeFlowYAML(context.Background(), yamlBack)
	if err != nil {
		t.Fatalf("round-trip YAML invalid: %v\n%s", err, yamlBack)
	}
	if string(reparsed) != string(config) {
		t.Fatalf("YAML round-trip changed config:\n%s\n%s", config, reparsed)
	}
	// 导入路径（validateFlowConfig）接受 script/uses 节点（无 runId 校验）。
	if _, err := manager.validateFlowConfig(context.Background(), config); err != nil {
		t.Fatalf("validateFlowConfig err=%v", err)
	}
	if !FlowNeedsManageExecution(config) {
		t.Fatal("FlowNeedsManageExecution should report script/uses flow")
	}
	// 篡改 run 节点的 runId 置空应被拒绝。
	tampered := strings.Replace(string(config), `"runId":"qr-a"`, `"runId":""`, 1)
	if _, err := manager.validateFlowConfig(context.Background(), json.RawMessage(tampered)); err == nil || !strings.Contains(err.Error(), "未解析") {
		t.Fatalf("unresolved runId err=%v", err)
	}
	pureRun, err := manager.NormalizeFlowYAML(context.Background(), "version: 1\nflow:\n  nodes:\n    - {id: a, name: A, run: run-a}\n")
	if err != nil {
		t.Fatal(err)
	}
	if FlowNeedsManageExecution(pureRun) {
		t.Fatal("pure run flow must not require execution management permission")
	}
}

// flowNodeSpy 记录三种 executor 的调度入参。
type flowNodeSpy struct {
	started  []FlowNode
	builtins []string
	builtin  func(uses string) (string, error)
}

func (s *flowNodeSpy) start(_ context.Context, _ string, node FlowNode, _ FlowActor) (string, error) {
	s.started = append(s.started, node)
	return "run-x", nil
}

func (s *flowNodeSpy) runBuiltin(_ context.Context, _ string, uses string, with map[string]string) (string, error) {
	s.builtins = append(s.builtins, uses+":"+with["seconds"])
	if s.builtin != nil {
		return s.builtin(uses)
	}
	return "ok", nil
}

func flowSpyRunner(spy *flowNodeSpy) *FlowRunner {
	return NewFlowRunner(FlowRunnerOptions{
		Start:      spy.start,
		RunBuiltin: spy.runBuiltin,
		Status:     func(_ context.Context, _ string) (string, error) { return "succeeded", nil },
		Stop:       func(_ context.Context, _ string) error { return nil },
		Poll:       time.Millisecond,
	})
}

func TestFlowRunnerDispatchesExecutors(t *testing.T) {
	manager := flowTestManager(t, map[string]string{"qr-a": "run-a"})
	language := flowTestLanguage()
	yamlText := "version: 1\nflow:\n  nodes:\n    - id: a\n      name: 快捷\n      run: run-a\n      with: {target: release}\n    - id: b\n      name: 脚本\n      needs: [a]\n      language: " + language + "\n      script: echo hi\n      with: {KEY: value}\n    - id: c\n      name: 内置\n      needs: [b]\n      uses: sleep\n      with: {seconds: \"0\"}\n  post:\n    - name: 内置收尾\n      uses: sleep\n      with: {seconds: \"0\"}\n    - name: 脚本收尾\n      language: " + language + "\n      script: echo bye\n"
	card := flowTestCard(t, manager, yamlText)
	spy := &flowNodeSpy{}
	runner := flowSpyRunner(spy)
	defer runner.Close()
	if _, err := runner.Start(card, FlowActor{}); err != nil {
		t.Fatal(err)
	}
	final := waitFlowFinished(t, runner, card.ID)
	if final.Status != FlowRunSucceeded {
		t.Fatalf("final=%+v", final)
	}
	// run 与 script 节点都走 Start（script 节点保留源码与 with 供 web 层 dispatch）。
	if len(spy.started) != 3 || spy.started[0].RunID != "qr-a" || spy.started[0].With["target"] != "release" {
		t.Fatalf("started=%+v", spy.started)
	}
	if spy.started[1].Script != "echo hi" || spy.started[1].With["KEY"] != "value" {
		t.Fatalf("script dispatch=%+v", spy.started[1])
	}
	if spy.started[2].Script != "echo bye" {
		t.Fatalf("post script dispatch=%+v", spy.started[2])
	}
	if len(spy.builtins) != 2 {
		t.Fatalf("builtins=%v", spy.builtins)
	}
	for _, node := range final.Nodes {
		if node.Status != FlowNodeSucceeded {
			t.Fatalf("node=%+v", node)
		}
	}
	if final.Nodes[2].Message != "ok" {
		t.Fatalf("builtin message missing: %+v", final.Nodes[2])
	}
	if len(final.Posts) != 2 || final.Posts[0].Message != "ok" {
		t.Fatalf("posts=%+v", final.Posts)
	}
}

func TestFlowRunnerBuiltinFailureMarksMessage(t *testing.T) {
	manager := flowTestManager(t, map[string]string{"qr-a": "run-a"})
	yamlText := "version: 1\nflow:\n  nodes:\n    - id: a\n      name: 内置\n      uses: sleep\n      with: {seconds: \"0\"}\n    - id: b\n      name: 下游\n      needs: [a]\n      run: run-a\n"
	card := flowTestCard(t, manager, yamlText)
	spy := &flowNodeSpy{builtin: func(string) (string, error) { return "", errors.New("磁盘已满") }}
	runner := flowSpyRunner(spy)
	defer runner.Close()
	if _, err := runner.Start(card, FlowActor{}); err != nil {
		t.Fatal(err)
	}
	final := waitFlowFinished(t, runner, card.ID)
	if final.Status != FlowRunFailed || final.Nodes[0].Status != FlowNodeFailed || final.Nodes[0].Message != "磁盘已满" {
		t.Fatalf("final=%+v", final)
	}
	if final.Nodes[1].Status != FlowNodeSkipped {
		t.Fatalf("downstream=%+v", final.Nodes[1])
	}
}

func TestFlowRunnerBuiltinTimeout(t *testing.T) {
	manager := flowTestManager(t, map[string]string{"qr-a": "run-a"})
	yamlText := "version: 1\nflow:\n  nodes:\n    - id: a\n      name: 内置\n      uses: sleep\n      timeout: 1\n      with: {seconds: \"0\"}\n"
	card := flowTestCard(t, manager, yamlText)
	runner := NewFlowRunner(FlowRunnerOptions{
		RunBuiltin: func(ctx context.Context, _ string, _ string, _ map[string]string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
		Poll: time.Millisecond,
	})
	defer runner.Close()
	if _, err := runner.Start(card, FlowActor{}); err != nil {
		t.Fatal(err)
	}
	final := waitFlowFinished(t, runner, card.ID)
	if final.Nodes[0].Status != FlowNodeFailed || !strings.Contains(final.Nodes[0].Message, "deadline") {
		t.Fatalf("final=%+v", final)
	}
}
