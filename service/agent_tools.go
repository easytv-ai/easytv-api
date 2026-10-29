package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"
)

// ===== 对话式工作流生成的工具集（API.md 32，agent-contract 2.7 / 5.3）=====
//
// 工具永不返回 error：模型写错参数也只回一句人话，由调用方按 [工具 x] / [工具 x] 失败：… 的格式回灌。

// AgentToolNames 工具白名单（提示词与解析器共用；顺序固定，测试断言用）。
func AgentToolNames() []string {
	return []string{"search_models", "get_kind_schema", "list_templates", "get_canvas", "validate_graph", "estimate_points"}
}

// AgentToolKnown 该工具名在白名单里。
func AgentToolKnown(name string) bool {
	for _, n := range AgentToolNames() {
		if n == name {
			return true
		}
	}
	return false
}

// runAgentTool 执行一个工具，返回回灌给模型的纯文本（**永不返回 error**，失败也返回一句人话）。
func runAgentTool(name string, args json.RawMessage, in AgentLoopInput) (summary string, ok bool) {
	switch strings.TrimSpace(name) {
	case "search_models":
		var a struct {
			Type string `json:"type"`
		}
		if len(args) > 0 && json.Unmarshal(args, &a) != nil {
			return "args 不是合法 JSON，type 可留空", false
		}
		taskType := strings.TrimSpace(a.Type)
		if taskType != "" && taskType != model.TaskTypeText && taskType != model.TaskTypeImage &&
			taskType != model.TaskTypeVideo && taskType != model.TaskTypeAudio {
			return fmt.Sprintf("没有 type=%s 这种类型，只能是 image/video/audio/text", taskType), false
		}
		if in.Catalog == nil {
			return "（模型目录不可用）", false
		}
		return agentModelListText(in.Catalog.All(), taskType), true

	case "get_kind_schema":
		var a struct {
			Kind string `json:"kind"`
		}
		if len(args) > 0 && json.Unmarshal(args, &a) != nil {
			return "args 不是合法 JSON，kind 可留空", false
		}
		kind := strings.TrimSpace(a.Kind)
		if kind != "" {
			s := AgentKindSchema(kind)
			if s == nil {
				return fmt.Sprintf("没有 %s 这个节点类型，可选：%s", kind, strings.Join(AgentAllKinds(), "/")), false
			}
			return agentKindSchemaLine(*s), true
		}
		lines := make([]string, 0, len(agentKindSchemas))
		for _, s := range AgentKindSchemas() {
			lines = append(lines, agentKindSchemaLine(s))
		}
		return strings.Join(lines, "\n"), true

	case "list_templates":
		var a struct {
			Keyword string `json:"keyword"`
			Limit   int    `json:"limit"`
		}
		if len(args) > 0 && json.Unmarshal(args, &a) != nil {
			return "args 不是合法 JSON，keyword/limit 可留空", false
		}
		limit := a.Limit
		if limit <= 0 || limit > 8 {
			limit = 3
		}
		if in.Templates == nil {
			return "（没有可复用模板）", true
		}
		list := in.Templates(strings.TrimSpace(a.Keyword), limit)
		if len(list) == 0 {
			return "（没有可复用模板）", true
		}
		lines := make([]string, 0, len(list))
		for _, t := range list {
			line := fmt.Sprintf("[%s] %s：%s", t.Source, t.Name, t.Summary)
			// graph 只在模型主动调这个工具时给：提示词里的模板块保持一行摘要，
			// 不然三条模板的图就能把每轮提示词撑大几千字。
			if g := strings.TrimSpace(t.Graph); g != "" {
				line += "\n  可抄的结构：" + g
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n"), true

	case "get_canvas":
		if in.Canvas == nil {
			return "画布不可读", true
		}
		return AgentCanvasText(in.Nodes, in.LastTasks), true

	case "validate_graph", "estimate_points":
		var a struct {
			Graph *CanvasWorkflowGraph `json:"graph"`
		}
		if len(args) > 0 && json.Unmarshal(args, &a) != nil {
			return "args 不是合法 JSON，graph 要放在 args.graph 里", false
		}
		if a.Graph == nil {
			return "没有拿到 graph，请把工作流放在 args.graph 里", false
		}
		opts := AgentValidateOptions{
			Catalog: in.Catalog, IsMember: in.IsMember, MaxNodes: in.MaxNodes, MaxPoints: in.MaxPoints,
			UserId: in.UserId, TeamId: in.TeamId,
		}
		_, report := CompileAgentGraph(a.Graph, opts)
		if strings.TrimSpace(name) == "estimate_points" {
			if len(report.Estimate.Items) == 0 {
				return fmt.Sprintf("预计 %d 积分：没有可计价的节点", report.Estimate.Points), true
			}
			parts := make([]string, 0, len(report.Estimate.Items))
			for _, it := range report.Estimate.Items {
				parts = append(parts, fmt.Sprintf("%s %d", it.NodeId, it.Points))
			}
			return fmt.Sprintf("预计 %d 积分：%s", report.Estimate.Points, strings.Join(parts, "、")), true
		}
		if report.OK {
			text := fmt.Sprintf("通过，预计 %d 积分", report.Estimate.Points)
			if len(report.Warnings) > 0 {
				text += "；注意：" + agentIssueListText(report.Warnings)
			}
			return text, true
		}
		return "不通过：" + agentIssueListText(report.Errors) + agentIssueTailText(report.Warnings), true
	}
	return fmt.Sprintf("没有 %s 这个工具，可用：%s", name, strings.Join(AgentToolNames(), "/")), false
}

// agentIssueListText 把问题列表渲染成 MODEL_NOT_PRICED(image-2, "foo-1")；ORPHAN_NODE(text-3) 这样的文本。
func agentIssueListText(issues []AgentIssue) string {
	parts := make([]string, 0, len(issues))
	for _, i := range issues {
		switch {
		case i.NodeId != "" && i.Value != "":
			parts = append(parts, fmt.Sprintf("%s(%s, %q)", i.Code, i.NodeId, i.Value))
		case i.NodeId != "":
			parts = append(parts, fmt.Sprintf("%s(%s)", i.Code, i.NodeId))
		default:
			parts = append(parts, i.Code)
		}
	}
	return strings.Join(parts, "；")
}

func agentIssueTailText(warnings []AgentIssue) string {
	if len(warnings) == 0 {
		return ""
	}
	return "；另外注意：" + agentIssueListText(warnings)
}

// RetrieveAgentTemplates 生产检索：canvas_workflow（uses desc）+ skill.graph + setting.AgentExamples，各取 Top-N 后合并去重。
// 只读，不写库、不计数。
func RetrieveAgentTemplates(userId, teamId int, keyword string, limit int) []AgentTemplate {
	if limit <= 0 {
		limit = 3
	}
	keyword = strings.TrimSpace(keyword)
	out := []AgentTemplate{}
	seen := map[string]bool{}
	add := func(t AgentTemplate) {
		if len(out) >= limit {
			return
		}
		key := t.Source + "|" + t.Name
		if t.Name == "" || seen[key] {
			return
		}
		seen[key] = true
		t.Summary = clipRunes(t.Summary, 200)
		t.Graph = compactAgentGraph(t.Graph, agentTemplateGraphMax)
		out = append(out, t)
	}

	// 1. 用户 / 团队自己的工作流模板（按热度）
	if list, _, err := model.GetCanvasWorkflows(model.CanvasWorkflowFilter{UserId: userId, TeamId: teamId, Keyword: keyword, WithGraph: true},
		&common.PageInfo{PageSize: limit}); err == nil {
		for _, w := range list {
			add(AgentTemplate{Source: "workflow", Name: w.Name,
				Summary: fmt.Sprintf("%d 个节点 / %d 条连线", w.NodeCount, w.EdgeCount), Graph: w.Graph})
		}
	}
	// 2. 官方 / 已上架的技能里带画布模板的
	if list, _, err := model.GetSkills(model.SkillFilter{PublicVisible: true, Keyword: keyword},
		&common.PageInfo{PageSize: limit}); err == nil {
		for _, s := range list {
			if strings.TrimSpace(s.Graph) == "" {
				continue
			}
			summary := s.Description
			if summary == "" {
				summary = s.Scene
			}
			add(AgentTemplate{Source: "skill", Name: s.Title, Summary: summary, Graph: s.Graph})
		}
	}
	// 3. 运营经验条目（setting.AgentExamples）
	for _, e := range setting.GetAgentExamples() {
		if keyword != "" && !strings.Contains(e.Name, keyword) && !strings.Contains(e.Description, keyword) &&
			!strings.Contains(e.Content, keyword) {
			continue
		}
		add(AgentTemplate{Source: "example", Name: e.Name, Summary: e.Description, Graph: e.Content})
	}
	return out
}
