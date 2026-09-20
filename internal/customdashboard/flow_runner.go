package customdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// FlowRunner 按拓扑序串行编排 flow 卡片的快捷执行项：逐节点启动并等待结束，
// 节点失败则其下游标记为 skipped；节点全部结束后按 when 条件执行 post 步骤。
// 运行中的状态保存在内存，页面刷新后通过 Snapshot 重建最近一次运行；
// 已结束的运行追加到历史记录（每卡保留最近 flowHistoryKeep 条），
// HistoryPath 非空时持久化为 JSON 文件，供卡片展示上次执行时间与执行历史。
// 外部依赖（启动/查询/停止 Run）全部以函数注入，
// 保持 customdashboard 不反向依赖 runcontrol/runmanager。

var ErrFlowRunConflict = errors.New("该卡片已有正在运行的流程")

const (
	FlowNodeQueued    = "queued"
	FlowNodeRunning   = "running"
	FlowNodeSucceeded = "succeeded"
	FlowNodeFailed    = "failed"
	FlowNodeSkipped   = "skipped"

	FlowRunRunning   = "running"
	FlowRunSucceeded = "succeeded"
	FlowRunFailed    = "failed"
)

// FlowActor 是触发流程的登录用户身份（由 web 层从会话映射）。
type FlowActor struct {
	UserID, Username, Role string
}

type FlowRunnerOptions struct {
	// Start 启动 run/script 节点（uses 节点不走这里）；cardID 用于定位卡片工作区。
	Start func(ctx context.Context, cardID string, node FlowNode, actor FlowActor) (runID string, err error)
	// RunBuiltin 同步执行 uses 内置节点，返回简短结果说明。
	RunBuiltin func(ctx context.Context, cardID string, uses string, with map[string]string) (message string, err error)
	Status     func(ctx context.Context, runID string) (status string, err error)
	Stop       func(ctx context.Context, runID string) error
	Now        func() time.Time
	Poll       time.Duration
	// HistoryPath 可选：流程执行历史的 JSON 持久化文件路径。
	HistoryPath string
}

// FlowHistoryEntry 记录一次已结束的流程运行，用于卡片的上次执行信息与历史晴雨表。
type FlowHistoryEntry struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	StartedAtMs  int64  `json:"startedAtMs,omitempty"`
	FinishedAtMs int64  `json:"finishedAtMs,omitempty"`
	DurationMs   int64  `json:"durationMs,omitempty"`
	TriggeredBy  string `json:"triggeredBy,omitempty"`
}

// flowHistoryKeep 是每张卡片保留的历史条数上限。
const flowHistoryKeep = 12

type FlowNodeState struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	RunID       string `json:"runId,omitempty"`
	StartedAtMs int64  `json:"startedAtMs,omitempty"`
	DurationMs  int64  `json:"durationMs,omitempty"`
	Message     string `json:"message,omitempty"`
}

type FlowPostState struct {
	NodeID     string `json:"nodeId,omitempty"`
	Name       string `json:"name"`
	When       string `json:"when"`
	Status     string `json:"status"`
	RunID      string `json:"runId,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
	Message    string `json:"message,omitempty"`
}

// FlowRunView 是 SSE 推送与页面重建使用的快照。
type FlowRunView struct {
	ID           string          `json:"id"`
	CardID       string          `json:"cardId"`
	Status       string          `json:"status"`
	Version      int64           `json:"version"`
	StartedAtMs  int64           `json:"startedAtMs,omitempty"`
	FinishedAtMs int64           `json:"finishedAtMs,omitempty"`
	Nodes        []FlowNodeState `json:"nodes"`
	Posts        []FlowPostState `json:"posts,omitempty"`
	Finished     bool            `json:"finished"`
}

type flowRun struct {
	historyRecorded bool
	view            FlowRunView
}

type FlowRunner struct {
	options   FlowRunnerOptions
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mu        sync.Mutex
	historyMu sync.Mutex
	closed    bool
	runs      map[string]*flowRun
	history   map[string][]FlowHistoryEntry
	nextID    int64
	version   int64
}

func NewFlowRunner(options FlowRunnerOptions) *FlowRunner {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Poll <= 0 {
		options.Poll = 300 * time.Millisecond
	}
	ctx, cancel := context.WithCancel(context.Background())
	runner := &FlowRunner{options: options, ctx: ctx, cancel: cancel, runs: map[string]*flowRun{}, history: map[string][]FlowHistoryEntry{}}
	if options.HistoryPath != "" {
		runner.history = loadFlowHistory(options.HistoryPath)
	}
	return runner
}

func (r *FlowRunner) Close() {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	r.mu.Unlock()
	r.wg.Wait()
}

// Start 为 flow 卡片启动一次流程运行；同一卡片同时只允许一个运行。
func (r *FlowRunner) Start(card Card, actor FlowActor) (FlowRunView, error) {
	definition, ok := FlowDefinitionOf(card)
	if !ok {
		return FlowRunView{}, errors.New("流程卡片配置无效")
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return FlowRunView{}, errors.New("流程执行器已关闭")
	}
	if existing, present := r.runs[card.ID]; present && (!existing.view.Finished || !existing.historyRecorded) {
		r.mu.Unlock()
		return FlowRunView{}, ErrFlowRunConflict
	}
	r.nextID++
	nodes := make([]FlowNodeState, 0, len(definition.Nodes))
	for _, node := range definition.Nodes {
		nodes = append(nodes, FlowNodeState{ID: node.ID, Name: node.Name, Status: FlowNodeQueued})
	}
	run := &flowRun{view: FlowRunView{
		ID:          "flow-" + card.ID + "-" + strconv.FormatInt(r.nextID, 10),
		CardID:      card.ID,
		Status:      FlowRunRunning,
		StartedAtMs: r.options.Now().UnixMilli(),
		Nodes:       nodes,
	}}
	run.view.Version = r.bumpLocked()
	r.runs[card.ID] = run
	// Snapshot and register the worker while locked to avoid Start/Close races.
	initial := cloneFlowRunView(run.view)
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		r.execute(run, definition, actor)
	}()
	return initial, nil
}

// Snapshot 返回卡片最近一次流程运行的快照。
func (r *FlowRunner) Snapshot(cardID string) (FlowRunView, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, present := r.runs[cardID]
	if !present {
		return FlowRunView{}, false
	}
	return cloneFlowRunView(run.view), true
}

func (r *FlowRunner) bumpLocked() int64 {
	r.version++
	return r.version
}

func cloneFlowRunView(view FlowRunView) FlowRunView {
	cloned := view
	cloned.Nodes = append([]FlowNodeState(nil), view.Nodes...)
	cloned.Posts = append([]FlowPostState(nil), view.Posts...)
	return cloned
}

func (r *FlowRunner) mutate(cardID string, apply func(view *FlowRunView)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	run, present := r.runs[cardID]
	if !present {
		return
	}
	apply(&run.view)
	run.view.Version = r.bumpLocked()
}

func (r *FlowRunner) execute(run *flowRun, definition FlowDefinition, actor FlowActor) {
	cardID := run.view.CardID
	order, _ := FlowTopologicalOrder(&definition)
	byID := map[string]FlowNode{}
	for _, node := range definition.Nodes {
		byID[node.ID] = node
	}
	results := map[string]string{}
	failed := false
	for _, nodeID := range order {
		node := byID[nodeID]
		skipped := false
		for _, need := range node.Needs {
			if results[need] != FlowNodeSucceeded {
				skipped = true
				break
			}
		}
		if skipped {
			results[nodeID] = FlowNodeSkipped
			r.setNodeState(cardID, nodeID, FlowNodeState{Status: FlowNodeSkipped})
			continue
		}
		r.setNodeState(cardID, nodeID, FlowNodeState{Status: FlowNodeRunning, StartedAtMs: r.options.Now().UnixMilli()})
		status, runID, message, duration := r.executeStep(r.ctx, cardID, node, actor, func(startedRunID string) {
			// 节点一启动就回填 Run ID，运行中即可点击节点跳转运行记录。
			r.setNodeState(cardID, nodeID, FlowNodeState{RunID: startedRunID})
		})
		results[nodeID] = status
		if status != FlowNodeSucceeded {
			failed = true
		}
		r.setNodeState(cardID, nodeID, FlowNodeState{Status: status, RunID: runID, DurationMs: duration, Message: message})
		r.executePosts(cardID, nodeID, node.Post, status != FlowNodeSucceeded, actor)
	}
	overall := FlowRunSucceeded
	if failed {
		overall = FlowRunFailed
	}
	// 流程整体成败只由节点决定；post 步骤失败不改变整体结果。
	r.executePosts(cardID, "", definition.Post, failed, actor)
	r.mutate(cardID, func(view *FlowRunView) {
		view.Status = overall
		view.Finished = true
		view.FinishedAtMs = r.options.Now().UnixMilli()
	})
	r.recordHistory(cardID, actor.Username)
}

// recordHistory 在运行结束后把结果追加到卡片历史（新条目在前），并按需持久化。
func (r *FlowRunner) recordHistory(cardID, triggeredBy string) {
	// Serialize snapshots and writes together so an older snapshot cannot replace a newer one.
	r.historyMu.Lock()
	defer r.historyMu.Unlock()
	r.mu.Lock()
	run, present := r.runs[cardID]
	if !present {
		r.mu.Unlock()
		return
	}
	entry := FlowHistoryEntry{
		ID:           run.view.ID,
		Status:       run.view.Status,
		StartedAtMs:  run.view.StartedAtMs,
		FinishedAtMs: run.view.FinishedAtMs,
		TriggeredBy:  triggeredBy,
	}
	if entry.StartedAtMs > 0 && entry.FinishedAtMs >= entry.StartedAtMs {
		entry.DurationMs = entry.FinishedAtMs - entry.StartedAtMs
	}
	entries := append([]FlowHistoryEntry{entry}, r.history[cardID]...)
	if len(entries) > flowHistoryKeep {
		entries = entries[:flowHistoryKeep]
	}
	r.history[cardID] = entries
	run.historyRecorded = true
	// 写盘前深拷贝，避免持锁做文件 IO。
	snapshot := make(map[string][]FlowHistoryEntry, len(r.history))
	for id, list := range r.history {
		snapshot[id] = append([]FlowHistoryEntry(nil), list...)
	}
	path := r.options.HistoryPath
	r.mu.Unlock()
	if path != "" {
		if err := writeFlowHistory(path, snapshot); err != nil {
			log.Printf("persist dashboard flow history: %v", err)
		}
	}
}

// History 返回卡片最近的运行历史，新条目在前。
func (r *FlowRunner) History(cardID string) []FlowHistoryEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]FlowHistoryEntry(nil), r.history[cardID]...)
}

// loadFlowHistory 读取持久化的历史文件；文件缺失或损坏时返回空表。
func loadFlowHistory(path string) map[string][]FlowHistoryEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string][]FlowHistoryEntry{}
	}
	parsed := map[string][]FlowHistoryEntry{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return map[string][]FlowHistoryEntry{}
	}
	return parsed
}

// writeFlowHistory 原子写入历史文件（先写临时文件再改名）；失败仅放弃本次持久化。
func writeFlowHistory(path string, history map[string][]FlowHistoryEntry) error {
	data, err := json.Marshal(history)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".flow-history-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}

func flowPostMatches(when string, failed bool) bool {
	switch when {
	case FlowPostAlways:
		return true
	case FlowPostOnFailure:
		return failed
	case FlowPostOnSuccess:
		return !failed
	}
	return false
}

func (r *FlowRunner) setNodeState(cardID, nodeID string, patch FlowNodeState) {
	r.mutate(cardID, func(view *FlowRunView) {
		for index := range view.Nodes {
			if view.Nodes[index].ID == nodeID {
				if patch.Status != "" {
					view.Nodes[index].Status = patch.Status
				}
				if patch.StartedAtMs > 0 {
					view.Nodes[index].StartedAtMs = patch.StartedAtMs
				}
				view.Nodes[index].RunID = patch.RunID
				view.Nodes[index].DurationMs = patch.DurationMs
				view.Nodes[index].Message = patch.Message
			}
		}
	})
}

// asNode 把 post 步骤转换为节点形态，与节点共用同一套 executor 调度。
func (post FlowPost) asNode() FlowNode {
	return FlowNode{ID: "post", Name: post.Name, Run: post.Run, RunID: post.RunID, Script: post.Script, Language: post.Language, Uses: post.Uses, With: post.With}
}

// executeStep 执行一个节点/post 步骤并等待结束，返回状态、Run ID、简短消息与耗时毫秒。
// uses 节点进程内同步执行；run/script 节点经 Start 启动后轮询状态，
// onStarted 回调在拿到 Run ID 时触发（用于运行中即可跳转运行记录）。
func (r *FlowRunner) executeStep(ctx context.Context, cardID string, node FlowNode, actor FlowActor, onStarted func(runID string)) (status, runID, message string, durationMs int64) {
	started := r.options.Now()
	elapsed := func() int64 { return r.options.Now().Sub(started).Milliseconds() }
	if node.Uses != "" && r.options.RunBuiltin != nil {
		if r.options.RunBuiltin == nil {
			return FlowNodeFailed, "", "内置节点执行器未配置", 0
		}
		stepCtx := ctx
		cancel := func() {}
		if node.Timeout > 0 {
			stepCtx, cancel = context.WithTimeout(ctx, time.Duration(node.Timeout)*time.Second)
		}
		defer cancel()
		builtinMessage, err := r.options.RunBuiltin(stepCtx, cardID, node.Uses, node.With)
		if err != nil {
			return FlowNodeFailed, "", truncateFlowMessage(err.Error()), elapsed()
		}
		return FlowNodeSucceeded, "", truncateFlowMessage(builtinMessage), elapsed()
	}
	runID, err := r.options.Start(ctx, cardID, node, actor)
	if err != nil {
		return FlowNodeFailed, "", truncateFlowMessage(err.Error()), 0
	}
	if onStarted != nil {
		onStarted(runID)
	}
	deadline := time.Time{}
	if node.Timeout > 0 {
		deadline = started.Add(time.Duration(node.Timeout) * time.Second)
	}
	for {
		status, err := r.options.Status(ctx, runID)
		if err == nil && flowRunTerminal(status) {
			if status == FlowNodeSucceeded {
				return FlowNodeSucceeded, runID, "", elapsed()
			}
			return FlowNodeFailed, runID, "运行结束状态：" + status, elapsed()
		}
		if !deadline.IsZero() && !r.options.Now().Before(deadline) {
			_ = r.options.Stop(ctx, runID)
			return FlowNodeFailed, runID, "执行超时，已停止", elapsed()
		}
		select {
		case <-ctx.Done():
			if r.options.Stop != nil {
				_ = r.options.Stop(context.Background(), runID)
			}
			return FlowNodeFailed, runID, "已取消", elapsed()
		case <-time.After(r.options.Poll):
		}
	}
}

// truncateFlowMessage 把节点状态消息截到 200 字符。
func truncateFlowMessage(message string) string {
	runes := []rune(message)
	if len(runes) > 200 {
		runes = runes[:200]
	}
	return string(runes)
}

func flowRunTerminal(status string) bool {
	switch status {
	case "succeeded", "failed", "stopped", "timed_out", "disconnected":
		return true
	}
	return false
}

// Node posts complete before dependent nodes; global posts run after the graph.
func (r *FlowRunner) executePosts(cardID, nodeID string, posts []FlowPost, failed bool, actor FlowActor) {
	for index, post := range posts {
		if !flowPostMatches(post.When, failed) {
			continue
		}
		position := 0
		r.mutate(cardID, func(view *FlowRunView) {
			position = len(view.Posts)
			view.Posts = append(view.Posts, FlowPostState{NodeID: nodeID, Name: post.Name, When: post.When, Status: FlowNodeRunning})
		})
		node := post.asNode()
		node.ID = nodeID + "-post-" + strconv.Itoa(index)
		status, runID, message, duration := r.executeStep(r.ctx, cardID, node, actor, nil)
		r.mutate(cardID, func(view *FlowRunView) {
			view.Posts[position] = FlowPostState{NodeID: nodeID, Name: post.Name, When: post.When, Status: status, RunID: runID, Message: message, DurationMs: duration}
		})
	}
}
