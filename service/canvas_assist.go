package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"easytv-api/model"
)

// ===== 画布助手（API.md 16）=====
//
// 流程：前端把选中节点及其上下游的摘要发过来 → 服务端补上这些节点上次生成的真实记录（模型、失败原因）
// 拼成提示词，按普通文本任务提交（计费、并发、团队额度、失败退分都走 StartTask）→ 任务完成后
// 前端取方案：服务端解析模型回复，丢掉引用不存在节点的修改，给「重新生成」补上模型、参数与预计积分。
// 方案只是预览，应用与生成都由前端在画布上执行（可撤销），生成仍走 POST /user/task。

const (
	AssistActionUpdatePrompt = "update_prompt"
	AssistActionRegenerate   = "regenerate"
	AssistActionAddNode      = "add_node"
	AssistActionConnect      = "connect"

	assistMaxNodes    = 40
	assistMaxChanges  = 8
	assistMaxHistory  = 8
	assistMaxQuestion = 1000
	assistMaxPrompt   = 2000
)

// AssistNode 前端发来的节点摘要。
type AssistNode struct {
	Id         string   `json:"id"`
	Kind       string   `json:"kind"`
	Label      string   `json:"label"`
	Prompt     string   `json:"prompt"`
	Content    string   `json:"content"`
	HasMedia   bool     `json:"has_media"`
	TaskId     int      `json:"task_id"`
	Selected   bool     `json:"selected"`
	Upstream   []string `json:"upstream"`
	Downstream []string `json:"downstream"`
}

// AssistTurn 之前的对话。
type AssistTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AssistRequest POST /user/canvas/:id/assist 请求体。
type AssistRequest struct {
	Question string       `json:"question"`
	Model    string       `json:"model"`
	Nodes    []AssistNode `json:"nodes"`
	History  []AssistTurn `json:"history"`
}

// assistLastTask 节点上次生成的记录（服务端查库补全，不信前端）。
type assistLastTask struct {
	Model  string `json:"model"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Params string `json:"params,omitempty"`
}

// assistNodeRef 存进任务 params，取方案时据此校验节点并给出修改前的提示词。
type assistNodeRef struct {
	Id       string          `json:"id"`
	Kind     string          `json:"kind"`
	Label    string          `json:"label"`
	Prompt   string          `json:"prompt"`
	LastTask *assistLastTask `json:"last_task,omitempty"`
}

type assistParams struct {
	Assist struct {
		Question string          `json:"question"`
		Nodes    []assistNodeRef `json:"nodes"`
	} `json:"assist"`
}

// AssistChange 校验后的一条修改。
type AssistChange struct {
	Action    string         `json:"action"`
	NodeId    string         `json:"node_id,omitempty"`
	NodeLabel string         `json:"node_label,omitempty"`
	Kind      string         `json:"kind,omitempty"`
	Label     string         `json:"label,omitempty"`
	Prompt    string         `json:"prompt,omitempty"`
	Before    string         `json:"before,omitempty"`
	From      string         `json:"from,omitempty"`
	Source    string         `json:"source,omitempty"`
	Target    string         `json:"target,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Type      string         `json:"type,omitempty"`
	Model     string         `json:"model,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
	Points    int            `json:"points,omitempty"`
	// Content / Graph 是给对话式工作流生成（API.md 32）新加的两个字段：
	// add_node 的文本正文与 apply_graph 落下的整张图；老 assist 永远不会填它们。
	Content string               `json:"content,omitempty"`
	Graph   *CanvasWorkflowGraph `json:"graph,omitempty"`
}

// AssistPlan GET /user/canvas/:id/assist/:task_id 的返回。
type AssistPlan struct {
	TaskId          int            `json:"task_id"`
	Status          string         `json:"status"`
	ErrorMsg        string         `json:"error_msg,omitempty"`
	Question        string         `json:"question"`
	Reply           string         `json:"reply"`
	Changes         []AssistChange `json:"changes"`
	Dropped         int            `json:"dropped"`
	EstimatedPoints int            `json:"estimated_points"`
	CostPoints      int            `json:"cost_points"`
}

// assistPricer 给「重新生成」挑模型并估价；测试里替换掉，不查库。
type assistPricer interface {
	// Resolve 返回实际使用的模型与预计积分；wanted 为模型建议的模型，last 为节点上次生成用的模型。
	Resolve(taskType, wanted, last, paramsJSON string) (modelName string, points int, err error)
}

type dbAssistPricer struct{ isMember bool }

func (p dbAssistPricer) Resolve(taskType, wanted, last, paramsJSON string) (string, int, error) {
	for _, name := range []string{wanted, last, PickDefaultModel(taskType)} {
		name = strings.TrimSpace(name)
		if name == "" || FindProvider(taskType, name) == nil {
			continue
		}
		points, _, err := EstimateTaskPoints(taskType, name, paramsJSON, p.isMember)
		if err != nil {
			continue
		}
		return name, points, nil
	}
	return "", 0, fmt.Errorf("暂无可用的%s模型", taskTypeLabel(taskType))
}

// AssistModel 助手用的文本模型：前端选的文本模型优先，其次策划案模型。
func AssistModel(requested string) string {
	requested = strings.TrimSpace(requested)
	if requested != "" && FindProvider(model.TaskTypeText, requested) != nil {
		if p, err := model.GetModelPrice(requested); err == nil && p.Type == model.TaskTypeText {
			return requested
		}
	}
	return planModel()
}

func clipRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// NewAssistTask 校验请求、补全上次生成记录，组装文本任务（未入库）。
func NewAssistTask(userId int, cv *model.Canvas, req AssistRequest) (*model.GenerationTask, GenerationProvider, error) {
	question := strings.TrimSpace(req.Question)
	if question == "" {
		return nil, nil, errors.New("请输入问题")
	}
	if len([]rune(question)) > assistMaxQuestion {
		return nil, nil, fmt.Errorf("问题太长了，最多 %d 字", assistMaxQuestion)
	}
	modelName := AssistModel(req.Model)
	if modelName == "" {
		return nil, nil, errors.New("暂无可用的文本模型")
	}
	provider := FindProvider(model.TaskTypeText, modelName)
	if provider == nil {
		return nil, nil, fmt.Errorf("模型 %s 暂未接入", modelName)
	}

	nodes := sanitizeAssistNodes(req.Nodes)
	last := map[string]*assistLastTask{}
	for _, n := range nodes {
		if n.TaskId <= 0 {
			continue
		}
		t, err := model.GetTaskById(n.TaskId)
		// 只认这张画布上、这个节点的任务，别让人借助手读到别处的任务
		if err != nil || t.CanvasId != cv.Id || (t.NodeId != "" && t.NodeId != n.Id) {
			continue
		}
		last[n.Id] = &assistLastTask{Model: t.Model, Status: t.Status, Error: clipRunes(t.ErrorMsg, 300), Params: t.Params}
	}

	prompt := BuildAssistPrompt(nodes, last, trimAssistHistory(req.History), question, assistModelHints())

	var params assistParams
	params.Assist.Question = question
	for _, n := range nodes {
		params.Assist.Nodes = append(params.Assist.Nodes, assistNodeRef{Id: n.Id, Kind: n.Kind, Label: n.Label, Prompt: n.Prompt, LastTask: last[n.Id]})
	}
	raw, _ := json.Marshal(params)
	task := &model.GenerationTask{
		UserId:    userId,
		ProjectId: cv.ProjectId,
		CanvasId:  cv.Id,
		Type:      model.TaskTypeText,
		Provider:  provider.Name(),
		Model:     modelName,
		Prompt:    prompt,
		Params:    string(raw),
		Inputs:    "[]",
	}
	return task, provider, nil
}

func sanitizeAssistNodes(in []AssistNode) []AssistNode {
	out := make([]AssistNode, 0, len(in))
	seen := map[string]bool{}
	for _, n := range in {
		n.Id = clipRunes(n.Id, 64)
		if n.Id == "" || seen[n.Id] {
			continue
		}
		seen[n.Id] = true
		n.Kind = clipRunes(n.Kind, 20)
		n.Label = clipRunes(n.Label, 40)
		n.Prompt = clipRunes(n.Prompt, 1500)
		n.Content = clipRunes(n.Content, 600)
		if len(n.Upstream) > 12 {
			n.Upstream = n.Upstream[:12]
		}
		if len(n.Downstream) > 12 {
			n.Downstream = n.Downstream[:12]
		}
		out = append(out, n)
		if len(out) >= assistMaxNodes {
			break
		}
	}
	return out
}

func trimAssistHistory(in []AssistTurn) []AssistTurn {
	var out []AssistTurn
	for _, t := range in {
		if t.Role != "user" && t.Role != "assistant" {
			continue
		}
		if c := clipRunes(t.Content, 500); c != "" {
			out = append(out, AssistTurn{Role: t.Role, Content: c})
		}
	}
	if len(out) > assistMaxHistory {
		out = out[len(out)-assistMaxHistory:]
	}
	return out
}

// assistModelHints 可用生成模型（每类最多 8 个），供模型在 regenerate 里挑。
func assistModelHints() map[string][]string {
	hints := map[string][]string{}
	prices, _ := model.GetModelPrices(true)
	for _, p := range prices {
		if p.Type == model.TaskTypeText || len(hints[p.Type]) >= 8 || FindProvider(p.Type, p.Model) == nil {
			continue
		}
		hints[p.Type] = append(hints[p.Type], p.Model)
	}
	return hints
}

const assistSystemPrompt = `你是 EasyTV 画布里的创作助手。用户在一张无限画布上用节点做短视频：文本节点写脚本和提示词；图片、视频、音频节点根据自己的提示词和连到它的上游节点（参考图、文本）生成内容。
你的任务：结合下面的画布上下文回答用户的问题；需要改动时给出具体、可以直接执行的修改。

只输出一个 JSON 对象，不要 Markdown 代码块，不要任何额外文字：
{"reply":"给用户看的说明：问题出在哪、打算怎么改，简洁，用用户的语言","changes":[]}

changes 里每条是下面四种之一（最多 8 条）：
1. {"action":"update_prompt","node_id":"节点 id","prompt":"改好后的完整提示词","reason":"为什么这样改"}
2. {"action":"regenerate","node_id":"节点 id","model":"模型标识，可留空","reason":"为什么要重新生成"}
3. {"action":"add_node","kind":"text|image|video|audio","label":"节点名","prompt":"提示词","from":"上游节点 id，可留空","reason":"..."}
4. {"action":"connect","source":"上游节点 id","target":"下游节点 id","reason":"..."}

规则：
- node_id、from、source、target 只能用上下文里出现的 id，不要编造。
- update_prompt 给完整的新提示词，保留原提示词里仍然有用的部分。
- regenerate 只用于图片、视频、音频节点，会消耗积分：只有用户想看新效果或你改了它的提示词时才给。
- 用户只是提问、不需要改画布时，changes 给 []。
- 节点上次生成失败时，先根据失败原因解释；能通过改提示词、换模型或补连线解决的，给出对应修改。`

// BuildAssistPrompt 拼提示词：说明 → 可用模型 → 画布上下文 → 之前的对话 → 这次的问题。
func BuildAssistPrompt(nodes []AssistNode, last map[string]*assistLastTask, history []AssistTurn, question string, hints map[string][]string) string {
	parts := []string{assistSystemPrompt}

	var models []string
	for _, t := range []string{model.TaskTypeImage, model.TaskTypeVideo, model.TaskTypeAudio} {
		if len(hints[t]) > 0 {
			models = append(models, taskTypeLabel(t)+"："+strings.Join(hints[t], "、"))
		}
	}
	if len(models) > 0 {
		parts = append(parts, "可用的生成模型：\n"+strings.Join(models, "\n"))
	}

	labels := map[string]string{}
	for _, n := range nodes {
		labels[n.Id] = n.Label
	}
	ref := func(ids []string) string {
		var out []string
		for _, id := range ids {
			if l, ok := labels[id]; ok && l != "" {
				out = append(out, fmt.Sprintf("%s（%s）", id, l))
			} else {
				out = append(out, id)
			}
		}
		return strings.Join(out, "、")
	}
	var b strings.Builder
	if len(nodes) == 0 {
		b.WriteString("画布上下文：画布上还没有节点。")
	} else {
		b.WriteString("画布上下文（★ 为用户选中的节点）：")
		for _, n := range nodes {
			b.WriteString("\n\n")
			if n.Selected {
				b.WriteString("★ ")
			}
			fmt.Fprintf(&b, "[%s] %s「%s」", n.Id, n.Kind, n.Label)
			if n.Prompt != "" {
				b.WriteString("\n  提示词：" + n.Prompt)
			}
			if n.Content != "" {
				b.WriteString("\n  文本内容：" + n.Content)
			}
			if n.HasMedia {
				b.WriteString("\n  已有生成结果")
			}
			if len(n.Upstream) > 0 {
				b.WriteString("\n  上游：" + ref(n.Upstream))
			}
			if len(n.Downstream) > 0 {
				b.WriteString("\n  下游：" + ref(n.Downstream))
			}
			if lt := last[n.Id]; lt != nil {
				fmt.Fprintf(&b, "\n  上次生成：模型 %s，状态 %s", lt.Model, lt.Status)
				if isEditParams(lt.Params) {
					b.WriteString("，方式：在当前图片上编辑（重新生成会在现有图片上按提示词再改一次，提示词应写成修改指令）")
				}
				if lt.Error != "" {
					b.WriteString("，失败原因：" + lt.Error)
				}
			}
		}
	}
	parts = append(parts, b.String())

	if len(history) > 0 {
		lines := []string{"之前的对话："}
		for _, t := range history {
			who := "用户"
			if t.Role == "assistant" {
				who = "助手"
			}
			lines = append(lines, who+"："+t.Content)
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	parts = append(parts, "用户这次的问题："+question, "现在只输出 JSON 对象。")
	return strings.Join(parts, "\n\n")
}

// GetAssistPlan 读取助手任务并生成方案。
func GetAssistPlan(userId int, cv *model.Canvas, taskId int) (*AssistPlan, error) {
	task, err := model.GetTaskById(taskId)
	if err != nil || task.UserId != userId || task.CanvasId != cv.Id || task.Type != model.TaskTypeText {
		return nil, model.ErrNotFound
	}
	var params assistParams
	if json.Unmarshal([]byte(task.Params), &params) != nil || params.Assist.Question == "" {
		return nil, model.ErrNotFound
	}
	plan := &AssistPlan{TaskId: task.Id, Status: task.Status, ErrorMsg: task.ErrorMsg, Question: params.Assist.Question, Changes: []AssistChange{}, CostPoints: task.CostPoints}
	if task.Status != model.TaskStatusSucceeded {
		return plan, nil
	}
	reply, changes, dropped := ParseAssistReply(task.ResultText, params.Assist.Nodes, dbAssistPricer{isMember: IsMemberForPricing(userId, task.TeamId)})
	plan.Reply, plan.Changes, plan.Dropped = reply, changes, dropped
	for _, c := range changes {
		plan.EstimatedPoints += c.Points
	}
	return plan, nil
}

type rawAssistChange struct {
	Action string `json:"action"`
	NodeId string `json:"node_id"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Prompt string `json:"prompt"`
	From   string `json:"from"`
	Source string `json:"source"`
	Target string `json:"target"`
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

// ParseAssistReply 解析模型回复（容忍代码块和前后废话）；解析不出 JSON 时整段当作回答、没有修改。
// 返回说明、校验通过的修改，以及被丢掉的条数。
func ParseAssistReply(raw string, nodes []assistNodeRef, pricer assistPricer) (string, []AssistChange, int) {
	text := strings.TrimSpace(raw)
	var parsed struct {
		Reply   string            `json:"reply"`
		Changes []json.RawMessage `json:"changes"`
	}
	body := text
	if i := strings.Index(body, "{"); i >= 0 {
		if j := strings.LastIndex(body, "}"); j > i {
			body = body[i : j+1]
		}
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil || (parsed.Reply == "" && parsed.Changes == nil) {
		return text, []AssistChange{}, 0
	}

	byId := map[string]assistNodeRef{}
	for _, n := range nodes {
		byId[n.Id] = n
	}
	out := []AssistChange{}
	dropped := 0
	regenerated := map[string]bool{}
	for _, item := range parsed.Changes {
		var rc rawAssistChange
		if json.Unmarshal(item, &rc) != nil {
			dropped++
			continue
		}
		if len(out) >= assistMaxChanges {
			dropped++
			continue
		}
		c, ok := validateAssistChange(rc, byId, pricer)
		if !ok || (c.Action == AssistActionRegenerate && regenerated[c.NodeId]) {
			dropped++
			continue
		}
		if c.Action == AssistActionRegenerate {
			regenerated[c.NodeId] = true
		}
		out = append(out, c)
	}
	return strings.TrimSpace(parsed.Reply), out, dropped
}

func validateAssistChange(rc rawAssistChange, nodes map[string]assistNodeRef, pricer assistPricer) (AssistChange, bool) {
	c := AssistChange{Action: strings.TrimSpace(rc.Action), Reason: clipRunes(rc.Reason, 200)}
	switch c.Action {
	case AssistActionUpdatePrompt:
		n, ok := nodes[rc.NodeId]
		prompt := strings.TrimSpace(rc.Prompt)
		if !ok || prompt == "" || len([]rune(prompt)) > assistMaxPrompt || prompt == strings.TrimSpace(n.Prompt) {
			return c, false
		}
		c.NodeId, c.NodeLabel, c.Kind, c.Prompt, c.Before = n.Id, n.Label, n.Kind, prompt, n.Prompt
	case AssistActionRegenerate:
		n, ok := nodes[rc.NodeId]
		if !ok {
			return c, false
		}
		taskType := nodeTaskType(n.Kind)
		if taskType == "" || taskType == model.TaskTypeText {
			return c, false
		}
		lastModel, params := "", map[string]any{}
		if n.LastTask != nil {
			lastModel = n.LastTask.Model
			params = regenerateParams(n.LastTask.Params)
		}
		paramsJSON, _ := json.Marshal(params)
		modelName, points, err := pricer.Resolve(taskType, strings.TrimSpace(rc.Model), lastModel, string(paramsJSON))
		if err != nil {
			return c, false
		}
		// 换了模型，上一个模型的参数（时长、分辨率）不一定适用，交给节点面板默认值
		if lastModel != "" && modelName != lastModel {
			kept := map[string]any{}
			// 编辑方式跟着节点走，换模型也要保留，否则会变成文生图
			for _, k := range []string{"edit", "mode"} {
				if v, ok := params[k]; ok && v != nil {
					kept[k] = v
				}
			}
			params = kept
			paramsJSON, _ = json.Marshal(params)
			if _, p, err := pricer.Resolve(taskType, modelName, "", string(paramsJSON)); err == nil {
				points = p
			}
		}
		c.NodeId, c.NodeLabel, c.Kind, c.Type, c.Model, c.Params, c.Points = n.Id, n.Label, n.Kind, taskType, modelName, params, points
	case AssistActionAddNode:
		kind := strings.TrimSpace(rc.Kind)
		prompt := strings.TrimSpace(rc.Prompt)
		if (kind != "text" && kind != "image" && kind != "video" && kind != "audio") || prompt == "" || len([]rune(prompt)) > assistMaxPrompt {
			return c, false
		}
		if rc.From != "" {
			if _, ok := nodes[rc.From]; !ok {
				return c, false
			}
		}
		c.Kind, c.Label, c.Prompt, c.From = kind, clipRunes(rc.Label, 40), prompt, rc.From
	case AssistActionConnect:
		s, ok1 := nodes[rc.Source]
		t, ok2 := nodes[rc.Target]
		if !ok1 || !ok2 || s.Id == t.Id {
			return c, false
		}
		c.Source, c.Target, c.NodeLabel = s.Id, t.Id, s.Label+" → "+t.Label
	default:
		return c, false
	}
	return c, true
}

func isEditParams(raw string) bool {
	var p struct {
		Edit string `json:"edit"`
	}
	return json.Unmarshal([]byte(raw), &p) == nil && p.Edit != ""
}

// regenerateParams 沿用上次生成的参数；摄影配置、特效、张数由节点当前状态决定，前端生成时重新带上。
func regenerateParams(raw string) map[string]any {
	params := map[string]any{}
	if json.Unmarshal([]byte(raw), &params) != nil {
		return map[string]any{}
	}
	for _, k := range []string{"camera", "camera_setup", "fx", "count", "assist"} {
		delete(params, k)
	}
	return params
}
