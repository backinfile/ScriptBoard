package customdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"unicode/utf8"

	"scriptboard/internal/quickrun"

	"go.yaml.in/yaml/v3"
)

// flow 卡片配置：用户在卡片抽屉中提交 YAML（draft），服务端解析、校验并把
// 规范化 JSON（run 名称已解析为 runId）存入 config_json；渲染与执行只读
// 规范化 JSON，不重复解析 YAML。

const (
	maxFlowNodes        = 20
	maxFlowPostSteps    = 4
	maxFlowNameRunes    = 60
	maxFlowTimeoutSecs  = 86400
	flowDocumentVersion = 1
)

// flowNodeIDPattern 限制节点 ID 为安全字符，便于作为 DOM/SSE 标识直接使用。
var flowNodeIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

type FlowNode struct {
	Post     []FlowPost        `json:"post,omitempty" yaml:"post,omitempty"`
	ID       string            `json:"id" yaml:"id"`
	Name     string            `json:"name" yaml:"name"`
	Needs    []string          `json:"needs,omitempty" yaml:"needs"`
	Run      string            `json:"run" yaml:"run,omitempty"`
	RunID    string            `json:"runId" yaml:"-"`
	Language string            `json:"language,omitempty" yaml:"language,omitempty"`
	Script   string            `json:"script,omitempty" yaml:"script,omitempty"`
	Uses     string            `json:"uses,omitempty" yaml:"uses,omitempty"`
	With     map[string]string `json:"with,omitempty" yaml:"with,omitempty"`
	Confirm  bool              `json:"confirm,omitempty" yaml:"confirm"`
	Timeout  int               `json:"timeout,omitempty" yaml:"timeout"`
}

const (
	FlowPostOnSuccess = "on_success"
	FlowPostOnFailure = "on_failure"
	FlowPostAlways    = "always"
)

type FlowPost struct {
	Name     string            `json:"name" yaml:"name"`
	Run      string            `json:"run" yaml:"run,omitempty"`
	RunID    string            `json:"runId" yaml:"-"`
	Language string            `json:"language,omitempty" yaml:"language,omitempty"`
	Script   string            `json:"script,omitempty" yaml:"script,omitempty"`
	Uses     string            `json:"uses,omitempty" yaml:"uses,omitempty"`
	With     map[string]string `json:"with,omitempty" yaml:"with,omitempty"`
	When     string            `json:"when" yaml:"when"`
}

type FlowDefinition struct {
	Nodes []FlowNode `json:"nodes" yaml:"nodes"`
	Post  []FlowPost `json:"post,omitempty" yaml:"post"`
}

// FlowConfigError 汇总流程配置的全部问题，供管理页逐条展示。
type FlowConfigError struct {
	Problems []string
}

func (e *FlowConfigError) Error() string { return strings.Join(e.Problems, "；") }

// flowDocument 是用户提交的 YAML 草案结构。
type flowDocument struct {
	Version int            `yaml:"version"`
	Flow    FlowDefinition `yaml:"flow"`
}

// flowConfigEnvelope 是 config_json 中的规范化结构。
type flowConfigEnvelope struct {
	Flow FlowDefinition `json:"flow"`
}

// NormalizeFlowYAML 解析用户提交的流程 YAML，校验结构并按名称解析快捷执行项，
// 返回可写入 config_json 的规范化配置。
func (m *Manager) NormalizeFlowYAML(ctx context.Context, yamlText string) (json.RawMessage, error) {
	if strings.TrimSpace(yamlText) == "" {
		return nil, &FlowConfigError{Problems: []string{"流程配置不能为空"}}
	}
	var document flowDocument
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(yamlText)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return nil, &FlowConfigError{Problems: []string{"流程配置不是有效的 YAML：" + err.Error()}}
	}
	definition := document.Flow
	problems := validateFlowStructure(&definition)
	if document.Version != 0 && document.Version != flowDocumentVersion {
		problems = append(problems, fmt.Sprintf("version 仅支持 %d", flowDocumentVersion))
	}
	if len(problems) == 0 {
		problems = append(problems, m.resolveFlowRuns(ctx, &definition)...)
	}
	if len(problems) > 0 {
		return nil, &FlowConfigError{Problems: problems}
	}
	encoded, err := json.Marshal(flowConfigEnvelope{Flow: definition})
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// resolveFlowRuns 把节点与 post 步骤的 run 名称解析为快捷执行项 ID 落库。
func (m *Manager) resolveFlowRuns(ctx context.Context, definition *FlowDefinition) []string {
	if m.quickRunByName == nil {
		return []string{"快捷执行项名称解析未配置"}
	}
	var problems []string
	resolve := func(owner, name string) (string, bool) {
		id, ok, err := m.quickRunByName(ctx, name)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s：解析快捷执行项失败：%v", owner, err))
			return "", false
		}
		if !ok {
			problems = append(problems, fmt.Sprintf("%s：快捷执行项 %q 不存在", owner, name))
			return "", false
		}
		return id, true
	}
	for index := range definition.Nodes {
		node := &definition.Nodes[index]
		if node.Run == "" {
			continue
		}
		if id, ok := resolve("节点 "+node.ID, node.Run); ok {
			node.RunID = id
		}
	}
	for _, posts := range flowPostGroups(definition) {
		for index := range posts {
			post := &posts[index]
			if post.Run == "" {
				continue
			}
			if id, ok := resolve("post 步骤 "+post.Name, post.Run); ok {
				post.RunID = id
			}
		}
	}
	return problems
}

// validateFlowStructure 校验 DAG 结构：节点/post 数量、ID 唯一且安全、
// needs 引用存在、无环、when 枚举合法。返回全部问题而非首个错误。
func validateFlowStructure(definition *FlowDefinition) []string {
	var problems []string
	if len(definition.Nodes) == 0 {
		problems = append(problems, "流程至少需要一个节点")
	}
	if len(definition.Nodes) > maxFlowNodes {
		problems = append(problems, fmt.Sprintf("流程节点最多 %d 个", maxFlowNodes))
	}
	if len(definition.Post) > maxFlowPostSteps {
		problems = append(problems, fmt.Sprintf("post 步骤最多 %d 个", maxFlowPostSteps))
	}
	ids := map[string]bool{}
	for index := range definition.Nodes {
		node := &definition.Nodes[index]
		node.ID = strings.TrimSpace(node.ID)
		node.Name = strings.TrimSpace(node.Name)
		node.Run = strings.TrimSpace(node.Run)
		node.Language = strings.TrimSpace(node.Language)
		node.Uses = strings.TrimSpace(node.Uses)
		if !flowNodeIDPattern.MatchString(node.ID) {
			problems = append(problems, fmt.Sprintf("节点 ID %q 无效：需以小写字母或数字开头，仅含小写字母、数字、-、_，最长 40 字符", node.ID))
		} else if ids[node.ID] {
			problems = append(problems, fmt.Sprintf("节点 ID %q 重复", node.ID))
		}
		ids[node.ID] = true
		if count := utf8.RuneCountInString(node.Name); count < 1 || count > maxFlowNameRunes {
			problems = append(problems, fmt.Sprintf("节点 %s：名称需为 1-%d 个字符", node.ID, maxFlowNameRunes))
		}
		problems = append(problems, validateFlowExecutor("节点 "+node.ID, node.Run, node.Script, node.Language, node.Uses, node.With)...)
		if node.Timeout < 0 || node.Timeout > maxFlowTimeoutSecs {
			problems = append(problems, fmt.Sprintf("节点 %s：timeout 需在 0-%d 秒之间", node.ID, maxFlowTimeoutSecs))
		}
	}
	for index := range definition.Nodes {
		node := &definition.Nodes[index]
		seenNeeds := map[string]bool{}
		needs := node.Needs[:0]
		for _, need := range node.Needs {
			need = strings.TrimSpace(need)
			if need == "" || seenNeeds[need] {
				continue
			}
			seenNeeds[need] = true
			if need == node.ID {
				problems = append(problems, fmt.Sprintf("节点 %s：needs 不能引用自身", node.ID))
				continue
			}
			if !ids[need] {
				problems = append(problems, fmt.Sprintf("节点 %s：needs 引用了不存在的节点 %q", node.ID, need))
				continue
			}
			needs = append(needs, need)
		}
		node.Needs = needs
	}
	if len(problems) == 0 {
		if _, acyclic := FlowTopologicalOrder(definition); !acyclic {
			problems = append(problems, "流程节点依赖存在循环")
		}
	}
	for _, posts := range flowPostGroups(definition) {
		if len(posts) > maxFlowPostSteps {
			problems = append(problems, "每组 post 步骤最多 4 个")
		}
		for index := range posts {
			post := &posts[index]
			post.Name = strings.TrimSpace(post.Name)
			post.Run = strings.TrimSpace(post.Run)
			post.Language = strings.TrimSpace(post.Language)
			post.Uses = strings.TrimSpace(post.Uses)
			if post.When == "" {
				post.When = FlowPostAlways
			}
			if count := utf8.RuneCountInString(post.Name); count < 1 || count > maxFlowNameRunes {
				problems = append(problems, fmt.Sprintf("post 步骤：名称需为 1-%d 个字符", maxFlowNameRunes))
			}
			problems = append(problems, validateFlowExecutor("post 步骤 "+post.Name, post.Run, post.Script, post.Language, post.Uses, post.With)...)
			switch post.When {
			case FlowPostOnSuccess, FlowPostOnFailure, FlowPostAlways:
			default:
				problems = append(problems, fmt.Sprintf("post 步骤 %s：when 仅支持 on_success、on_failure、always", post.Name))
			}
		}
	}
	return problems
}

// flowWithKeyPattern 限制 with 参数键，与安全的环境变量名一致。
var flowWithKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)

const (
	maxFlowWithKeys       = 20
	maxFlowWithValueRunes = 2000
)

// validateFlowExecutor 校验节点/post 步骤的执行器：run、script、uses 恰好一个，
// 并分别校验语言、源码与内置节点输入。错误汇总风格与结构校验一致。
func validateFlowExecutor(owner, run, script, language, uses string, with map[string]string) []string {
	var problems []string
	kinds := 0
	if run != "" {
		kinds++
	}
	if strings.TrimSpace(script) != "" {
		kinds++
	}
	if uses != "" {
		kinds++
	}
	switch {
	case kinds == 0:
		problems = append(problems, owner+"：必须指定 run、script、uses 之一")
	case kinds > 1:
		problems = append(problems, owner+"：run、script、uses 只能选择一个")
	}
	if strings.TrimSpace(script) != "" {
		if language == "" {
			problems = append(problems, owner+"：script 节点必须指定 language")
		} else if _, err := quickrun.PlatformLanguage(runtime.GOOS, language); err != nil {
			problems = append(problems, fmt.Sprintf("%s：language %q 不是本平台支持的语言", owner, language))
		}
		if err := quickrun.ValidateSource(script); err != nil {
			problems = append(problems, fmt.Sprintf("%s：script 无效：%v", owner, err))
		}
	}
	var declared map[string]bool
	var required []string
	allowHeaderKeys := false
	if uses != "" {
		spec, ok := LookupBuiltinNode(uses)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s：内置节点 %q 未注册", owner, uses))
		} else {
			declared = map[string]bool{}
			for _, input := range spec.Inputs {
				declared[input.Name] = true
				if input.Required {
					required = append(required, input.Name)
				}
			}
			allowHeaderKeys = spec.AllowHeaderKeys
		}
	}
	return append(problems, validateFlowWith(owner, with, declared, required, allowHeaderKeys)...)
}

// validateFlowWith 校验 with 参数形态；uses 节点额外校验键已声明且必填输入齐全。
// run 节点的取值合法性由 runcontrol 在运行时按参数定义强校验，这里只做形态校验。
func validateFlowWith(owner string, with map[string]string, declared map[string]bool, required []string, allowHeaderKeys bool) []string {
	var problems []string
	if len(with) > maxFlowWithKeys {
		problems = append(problems, fmt.Sprintf("%s：with 最多 %d 个参数", owner, maxFlowWithKeys))
	}
	for key, value := range with {
		if !flowWithKeyPattern.MatchString(key) {
			problems = append(problems, fmt.Sprintf("%s：with 参数名 %q 无效：需以字母开头，仅含字母、数字、_，最长 32 字符", owner, key))
			continue
		}
		if utf8.RuneCountInString(value) > maxFlowWithValueRunes {
			problems = append(problems, fmt.Sprintf("%s：with 参数 %s 的值最长 %d 字符", owner, key, maxFlowWithValueRunes))
		}
		if declared == nil {
			continue
		}
		if declared[key] {
			continue
		}
		if allowHeaderKeys && strings.HasPrefix(key, "header_") && len(key) > len("header_") {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s：with 参数 %q 不是内置节点声明的输入", owner, key))
	}
	for _, name := range required {
		if strings.TrimSpace(with[name]) == "" {
			problems = append(problems, fmt.Sprintf("%s：缺少必填输入 %q", owner, name))
		}
	}
	return problems
}

// FlowTopologicalOrder 以定义顺序为 tie-break 返回拓扑序；有环时第二个返回值为 false。
func FlowTopologicalOrder(definition *FlowDefinition) ([]string, bool) {
	remaining := make(map[string]FlowNode, len(definition.Nodes))
	for _, node := range definition.Nodes {
		remaining[node.ID] = node
	}
	order := make([]string, 0, len(definition.Nodes))
	done := map[string]bool{}
	for len(remaining) > 0 {
		progressed := false
		for _, node := range definition.Nodes {
			if done[node.ID] {
				continue
			}
			ready := true
			for _, need := range node.Needs {
				if !done[need] {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			done[node.ID] = true
			delete(remaining, node.ID)
			order = append(order, node.ID)
			progressed = true
		}
		if !progressed {
			return order, false
		}
	}
	return order, true
}

// FlowDefinitionOf 从卡片 config_json 读出规范化流程定义。
func FlowDefinitionOf(card Card) (FlowDefinition, bool) {
	if card.Type != CardFlow || len(card.Config) == 0 {
		return FlowDefinition{}, false
	}
	var envelope flowConfigEnvelope
	if err := json.Unmarshal(card.Config, &envelope); err != nil || len(envelope.Flow.Nodes) == 0 {
		return FlowDefinition{}, false
	}
	return envelope.Flow, true
}

// FlowNeedsManageExecution 报告规范化流程配置是否含 script/uses 节点，
// 这类配置的保存与导入需要执行管理权限。
func FlowNeedsManageExecution(config json.RawMessage) bool {
	var envelope flowConfigEnvelope
	if err := json.Unmarshal(config, &envelope); err != nil {
		return false
	}
	for _, node := range envelope.Flow.Nodes {
		if strings.TrimSpace(node.Script) != "" || node.Uses != "" {
			return true
		}
	}
	for _, post := range allFlowPosts(&envelope.Flow) {
		if strings.TrimSpace(post.Script) != "" || post.Uses != "" {
			return true
		}
	}
	return false
}

// validateFlowConfig 校验待落库的规范化流程配置：结构与每个 runId 的存在性，
// 并返回修剪后的规范化配置（needs 去重、when 默认值）。保存路径（含导入）
// 都会经过这里，保证库内配置始终可执行。
func (m *Manager) validateFlowConfig(ctx context.Context, config json.RawMessage) (json.RawMessage, error) {
	var envelope flowConfigEnvelope
	if err := json.Unmarshal(config, &envelope); err != nil {
		return nil, errors.New("流程卡片配置无效")
	}
	definition := envelope.Flow
	if problems := validateFlowStructure(&definition); len(problems) > 0 {
		return nil, &FlowConfigError{Problems: problems}
	}
	if m.quickRunExists == nil {
		return nil, errors.New("快捷执行项存在性校验未配置")
	}
	var problems []string
	check := func(owner, id string) {
		exists, err := m.quickRunExists(ctx, id)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s：校验快捷执行项失败：%v", owner, err))
			return
		}
		if !exists {
			problems = append(problems, fmt.Sprintf("%s：快捷执行项不存在", owner))
		}
	}
	// run 节点必须有已解析的 runId；script/uses 节点无 runId，跳过存在性校验。
	unresolved := func(owner, run, id string) bool {
		if run != "" && id == "" {
			problems = append(problems, owner+"：快捷执行项未解析")
			return true
		}
		return id == ""
	}
	for _, node := range definition.Nodes {
		if unresolved("节点 "+node.ID, node.Run, node.RunID) {
			continue
		}
		check("节点 "+node.ID, node.RunID)
	}
	for _, post := range allFlowPosts(&definition) {
		if unresolved("post 步骤 "+post.Name, post.Run, post.RunID) {
			continue
		}
		check("post 步骤 "+post.Name, post.RunID)
	}
	if len(problems) > 0 {
		return nil, &FlowConfigError{Problems: problems}
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(config, &object); err != nil {
		return nil, errors.New("流程卡片配置无效")
	}
	object["flow"] = encoded
	return json.Marshal(object)
}

// FlowYAML 把卡片的规范化流程定义还原为可编辑的 YAML 草案。
func FlowYAML(card Card) (string, error) {
	definition, ok := FlowDefinitionOf(card)
	if !ok {
		return "", errors.New("流程卡片配置无效")
	}
	document := flowDocument{Version: flowDocumentVersion, Flow: definition}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return buffer.String(), nil
}

func flowPostGroups(definition *FlowDefinition) [][]FlowPost {
	groups := [][]FlowPost{definition.Post}
	for i := range definition.Nodes {
		groups = append(groups, definition.Nodes[i].Post)
	}
	return groups
}
func allFlowPosts(definition *FlowDefinition) []FlowPost {
	var posts []FlowPost
	for _, group := range flowPostGroups(definition) {
		posts = append(posts, group...)
	}
	return posts
}
