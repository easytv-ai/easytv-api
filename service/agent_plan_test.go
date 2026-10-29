package service

import (
	"encoding/json"
	"strings"
	"testing"
)

// ===== 解析容错与计划装配（agent-contract 2.4 / 4.4 解析级 / 10.5）=====

func agentParseNodes() map[string]assistNodeRef {
	return map[string]assistNodeRef{
		"n1": {Id: "n1", Kind: "text", Label: "脚本", Prompt: ""},
		"n2": {Id: "n2", Kind: "image", Label: "首帧", Prompt: "一只橘猫",
			LastTask: &assistLastTask{Model: "gpt-image-1", Status: "succeeded", Params: `{"ratio":"9:16","count":1,"camera_setup":{"body":"x"}}`}},
		"n3": {Id: "n3", Kind: "video", Label: "镜头", LastTask: &assistLastTask{Model: "kling-v1", Status: "failed", Error: "内容审核未通过", Params: `{"duration":5}`}},
	}
}

func agentParse(t *testing.T, raw string) *AgentParseResult {
	t.Helper()
	res := ParseAgentReply(raw, agentParseNodes(), AgentParseOptions{})
	if res == nil {
		t.Fatal("ParseAgentReply 返回 nil")
	}
	return res
}

// TestParseAgentReplyToleratesWrapping 代码块包裹 + 前后废话都要能剥出来。
func TestParseAgentReplyToleratesWrapping(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"代码块包裹", "好的，我来改：\n```json\n{\"reply\":\"已经改好提示词\",\"changes\":[{\"action\":\"update_prompt\",\"node_id\":\"n2\",\"prompt\":\"一只戴帽子的橘猫\"}]}\n```\n需要我继续吗？"},
		{"前后废话", "让我先看看画布。\n{\"reply\":\"已经改好提示词\",\"changes\":[{\"action\":\"update_prompt\",\"node_id\":\"n2\",\"prompt\":\"一只戴帽子的橘猫\"}]}\n以上是我的建议。"},
		{"无代码块纯 JSON", `{"reply":"已经改好提示词","changes":[{"action":"update_prompt","node_id":"n2","prompt":"一只戴帽子的橘猫"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := agentParse(t, tc.raw)
			if res.Kind != "plan" {
				t.Fatalf("Kind = %s", res.Kind)
			}
			if res.Reply != "已经改好提示词" {
				t.Fatalf("Reply = %q", res.Reply)
			}
			if len(res.Changes) != 1 || res.Changes[0].Action != AssistActionUpdatePrompt || res.Changes[0].Before != "一只橘猫" {
				t.Fatalf("Changes = %+v", res.Changes)
			}
			if res.Dropped != 0 || len(res.Issues) != 0 {
				t.Fatalf("Dropped=%d Issues=%v", res.Dropped, agentCodes(res.Issues))
			}
		})
	}
}

// TestParseAgentReplyEmpty 空回复：当成文本，带 BAD_JSON 问题。
func TestParseAgentReplyEmpty(t *testing.T) {
	for _, raw := range []string{"", "   \n  ", "我不知道该怎么回答"} {
		res := agentParse(t, raw)
		if res.Kind != "text" {
			t.Fatalf("%q → Kind = %s", raw, res.Kind)
		}
		if len(res.Changes) != 0 {
			t.Fatalf("%q → Changes = %+v", raw, res.Changes)
		}
		if !agentHasIssue(res.Issues, AgentCodeBadJson) {
			t.Fatalf("%q → Issues = %v", raw, agentCodes(res.Issues))
		}
	}
}

// TestParseAgentReplyMissingFields 字段缺失：只有 reply 没有 changes（用户只是提问）。
func TestParseAgentReplyMissingFields(t *testing.T) {
	res := agentParse(t, `{"reply":"这条片子建议用 9:16，主镜头 5 秒。","changes":[]}`)
	if res.Kind != "plan" || res.Reply == "" {
		t.Fatalf("Kind=%s Reply=%q", res.Kind, res.Reply)
	}
	if len(res.Changes) != 0 || res.Dropped != 0 {
		t.Fatalf("Changes=%+v Dropped=%d", res.Changes, res.Dropped)
	}
	if !agentHasIssue(res.Issues, AgentCodeNoChanges) {
		t.Fatalf("Issues = %v", agentCodes(res.Issues))
	}
	if AgentIssueSeverity(AgentCodeNoChanges) != "warning" {
		t.Fatal("NO_CHANGES 必须是 warning")
	}
}

// TestParseAgentReplyUnknownAction 未知动作（set_params 等明确不做）→ UNKNOWN_ACTION + dropped。
func TestParseAgentReplyUnknownAction(t *testing.T) {
	for _, action := range []string{"set_params", "disconnect", "remove_node"} {
		res := agentParse(t, `{"reply":"改一下参数","changes":[{"action":"`+action+`","node_id":"n2"}]}`)
		if res.Kind != "plan" {
			t.Fatalf("Kind = %s", res.Kind)
		}
		if !agentHasIssue(res.Issues, AgentCodeUnknownAction) {
			t.Fatalf("%s → Issues = %v", action, agentCodes(res.Issues))
		}
		if res.Dropped != 1 || len(res.Changes) != 0 {
			t.Fatalf("%s → Dropped=%d Changes=%+v", action, res.Dropped, res.Changes)
		}
		if AgentIssueSeverity(AgentCodeUnknownAction) != "warning" {
			t.Fatal("UNKNOWN_ACTION 必须是 warning")
		}
	}
}

// TestParseAgentReplyToolCall 工具调用形态。
func TestParseAgentReplyToolCall(t *testing.T) {
	res := agentParse(t, `{"thought":"先看看有哪些视频模型","tool":"search_models","args":{"type":"video"}}`)
	if res.Kind != "tool" || res.Tool != "search_models" {
		t.Fatalf("Kind=%s Tool=%s", res.Kind, res.Tool)
	}
	var args map[string]any
	if json.Unmarshal(res.Args, &args) != nil || args["type"] != "video" {
		t.Fatalf("Args = %s", res.Args)
	}
	if len(res.Changes) != 0 {
		t.Fatalf("Changes = %+v", res.Changes)
	}
}

// TestParseAgentReplyApplyGraph apply_graph：Graph 是模型原始图（未编译），Graphs 与之一一对应。
func TestParseAgentReplyApplyGraph(t *testing.T) {
	raw := `{"reply":"搭一条竖屏骨架","changes":[{"action":"apply_graph","reason":"从零搭","graph":` +
		`{"version":1,"nodes":[{"id":"n1","kind":"text","x":0,"y":0,"data":{"label":"脚本","content":"猫"}},` +
		`{"id":"n2","kind":"image","x":400,"y":0,"data":{"label":"首帧","prompt":"橘猫","model":"gpt-image-1"}}],` +
		`"edges":[{"source":"n1","target":"n2"}]}}]}`
	res := agentParse(t, raw)
	if len(res.Changes) != 1 || res.Changes[0].Action != AssistActionApplyGraph {
		t.Fatalf("Changes = %+v", res.Changes)
	}
	if len(res.Graphs) != 1 || res.Graphs[0] == nil || len(res.Graphs[0].Nodes) != 2 {
		t.Fatalf("Graphs = %+v", res.Graphs)
	}
	// 解析层不改图：x 还是模型给的 400（编译发生在 BuildAgentPlan）
	if res.Graphs[0].Nodes[1].X != 400 {
		t.Fatalf("解析层不该动坐标：%d", res.Graphs[0].Nodes[1].X)
	}
	// graph 不是合法 JSON → dropped + BAD_JSON
	bad := agentParse(t, `{"reply":"搭图","changes":[{"action":"apply_graph","graph":"不是对象"}]}`)
	if bad.Dropped != 1 || len(bad.Changes) != 0 || !agentHasIssue(bad.Issues, AgentCodeBadJson) {
		t.Fatalf("坏 graph：Dropped=%d Changes=%+v Issues=%v", bad.Dropped, bad.Changes, agentCodes(bad.Issues))
	}
}

// TestParseAgentReplyAddNodeCarriesNewFields add_node 的 model / params / content 三个新字段要透传，
// params 只留白名单键。
func TestParseAgentReplyAddNodeCarriesNewFields(t *testing.T) {
	raw := `{"reply":"补两个节点","changes":[
		{"action":"add_node","kind":"image","label":"尾帧","prompt":"橘猫回头","model":"gpt-image-1","params":{"ratio":"9:16","junk":1},"from":"n1"},
		{"action":"add_node","kind":"text","label":"旁白","prompt":"欢快女声","content":"十五秒讲完这只猫"}]}`
	res := agentParse(t, raw)
	if len(res.Changes) != 2 {
		t.Fatalf("Changes = %+v", res.Changes)
	}
	img := res.Changes[0]
	if img.Model != "gpt-image-1" || img.From != "n1" {
		t.Fatalf("add_node = %+v", img)
	}
	if len(img.Params) != 1 || img.Params["ratio"] != "9:16" || img.Params["junk"] != nil {
		t.Fatalf("params 白名单没生效：%+v", img.Params)
	}
	txt := res.Changes[1]
	if txt.Kind != "text" || txt.Content != "十五秒讲完这只猫" {
		t.Fatalf("text 节点 content = %+v", txt)
	}
}

// TestParseAgentReplyDropsBadSmallChanges 引用不存在节点 / 空提示词等小改直接丢。
func TestParseAgentReplyDropsBadSmallChanges(t *testing.T) {
	raw := `{"reply":"改","changes":[
		{"action":"update_prompt","node_id":"n9","prompt":"x"},
		{"action":"update_prompt","node_id":"n2","prompt":""},
		{"action":"connect","source":"n2","target":"n2"},
		{"action":"regenerate","node_id":"n1"},
		{"action":"add_node","kind":"group","prompt":"x"}]}`
	res := agentParse(t, raw)
	if len(res.Changes) != 0 || res.Dropped != 5 {
		t.Fatalf("Changes=%+v Dropped=%d", res.Changes, res.Dropped)
	}
}

// TestBuildAgentPlanPricesRegenerate regenerate 要带上节点上次的模型与参数定价，并清掉暂存字段。
func TestBuildAgentPlanPricesRegenerate(t *testing.T) {
	pricer := &fakeAssistPricer{available: map[string]int{"gpt-image-1": 20, "kling": 80}}
	raw := `{"reply":"重新生成一版","changes":[
		{"action":"regenerate","node_id":"n2"},
		{"action":"regenerate","node_id":"n3","model":"kling"}]}`
	res := agentParse(t, raw)
	plan := BuildAgentPlan(res, AgentValidateOptions{Catalog: agentTestCatalog()}, pricer, 1, 6, "patch")
	if !plan.Validate.OK || len(plan.Changes) != 2 {
		t.Fatalf("plan = %+v errors=%v", plan.Changes, agentCodes(plan.Validate.Errors))
	}
	// 沿用上次模型：gpt-image-1 20 积分，参数沿用但去掉 count / camera_setup
	first := plan.Changes[0]
	if first.Model != "gpt-image-1" || first.Points != 20 || first.Type != "image" {
		t.Fatalf("regenerate #1 = %+v", first)
	}
	if first.Params["ratio"] != "9:16" || first.Params["count"] != nil || first.Params["camera_setup"] != nil {
		t.Fatalf("regenerate 参数 = %+v", first.Params)
	}
	if first.Before != "" {
		t.Fatalf("Before 是暂存字段，定价后必须清空：%q", first.Before)
	}
	// 换模型：参数不沿用，按新模型重新估价
	second := plan.Changes[1]
	if second.Model != "kling" || second.Points != 80 || len(second.Params) != 0 {
		t.Fatalf("regenerate #2 = %+v", second)
	}
	// pricer 收到的 last 必须是节点上次的模型
	joined := strings.Join(pricer.calls, ";")
	if !strings.Contains(joined, "gpt-image-1") || !strings.Contains(joined, "kling-v1") {
		t.Fatalf("pricer 调用 = %v", pricer.calls)
	}
}

// TestBuildAgentPlanRefTokenLeft {{ref:}} 残留必须在 validate.errors 里报 REF_TOKEN_LEFT。
func TestBuildAgentPlanRefTokenLeft(t *testing.T) {
	raw := `{"reply":"搭图","changes":[{"action":"apply_graph","graph":{"version":1,"nodes":[
		{"id":"n1","kind":"image","x":0,"y":0,"data":{"label":"首帧","prompt":"用 {{ref:产品图}} 做首帧","model":"gpt-image-1"}}],"edges":[]}}]}`
	res := agentParse(t, raw)
	plan := BuildAgentPlan(res, AgentValidateOptions{Catalog: agentTestCatalog()}, &fakeAssistPricer{}, 1, 6, "generate")
	if !agentHasCode(plan.Validate.Errors, AgentCodeRefTokenLeft) {
		t.Fatalf("errors = %v", agentCodes(plan.Validate.Errors))
	}
	if plan.Validate.OK || len(plan.Changes) != 0 {
		t.Fatalf("带占位符的图必须整条丢弃：ok=%v changes=%d", plan.Validate.OK, len(plan.Changes))
	}
}

// TestBuildAgentPlanAllowNoChange 纯提问用例：NO_CHANGES + reply 非空 → ok=true（4.3 的例外）。
func TestBuildAgentPlanAllowNoChange(t *testing.T) {
	res := agentParse(t, `{"reply":"这条片子建议用 9:16，主镜头 5 秒就够了。","changes":[]}`)
	plan := BuildAgentPlan(res, AgentValidateOptions{Catalog: agentTestCatalog()}, &fakeAssistPricer{}, 1, 6, "patch")
	if !plan.Validate.OK {
		t.Fatalf("纯提问必须 ok=true：errors=%v", agentCodes(plan.Validate.Errors))
	}
	if len(plan.Changes) != 0 || plan.Reply == "" {
		t.Fatalf("changes=%d reply=%q", len(plan.Changes), plan.Reply)
	}
}

// TestBuildAgentPlanCompilesApplyGraph 计划里的 apply_graph 必须是编译后的图，estimated_points 由两处相加。
func TestBuildAgentPlanCompilesApplyGraph(t *testing.T) {
	pricer := &fakeAssistPricer{available: map[string]int{"gpt-image-1": 20}}
	raw := `{"reply":"搭图","changes":[{"action":"apply_graph","graph":{"version":1,"nodes":[
		{"id":"n1","kind":"image","x":800,"y":100,"data":{"label":"首帧","prompt":"橘猫","model":"gpt-image-1"}}],"edges":[]}},
		{"action":"regenerate","node_id":"n2"}]}`
	res := agentParse(t, raw)
	plan := BuildAgentPlan(res, AgentValidateOptions{Catalog: agentTestCatalog()}, pricer, 2, 6, "generate")
	if !plan.Validate.OK {
		t.Fatalf("errors=%v", agentCodes(plan.Validate.Errors))
	}
	if plan.Validate.Normalized == nil || len(plan.Validate.Normalized.Nodes) != 1 {
		t.Fatalf("normalized = %+v", plan.Validate.Normalized)
	}
	if plan.Validate.Normalized.Nodes[0].X != 0 || plan.Validate.Normalized.Nodes[0].Y != 0 {
		t.Fatalf("apply_graph 的坐标应吸附到原点：%+v", plan.Validate.Normalized.Nodes[0])
	}
	if plan.Validate.Estimate.Points != 8 {
		t.Fatalf("图估价 = %d", plan.Validate.Estimate.Points)
	}
	if plan.Estimate.Points != plan.Validate.Estimate.Points {
		t.Fatalf("estimate 与 validate.estimate 必须是同一个值：%d vs %d", plan.Estimate.Points, plan.Validate.Estimate.Points)
	}
	if plan.EstimatedPoints != 8+20 {
		t.Fatalf("estimated_points = %d，应为图估价 + 小改积分", plan.EstimatedPoints)
	}
	if plan.Round != 2 || plan.MaxRounds != 6 || plan.Mode != "generate" {
		t.Fatalf("计划元信息 = %+v", plan)
	}
}

// TestParseAgentReplyMaxChangesCap 一条计划最多 8 条动作（超出按丢弃计）。
func TestParseAgentReplyMaxChangesCap(t *testing.T) {
	var items []string
	for i := 0; i < 10; i++ {
		items = append(items, `{"action":"update_prompt","node_id":"n2","prompt":"第 `+string(rune('a'+i))+` 版橘猫"}`)
	}
	res := agentParse(t, `{"reply":"改十处","changes":[`+strings.Join(items, ",")+`]}`)
	if len(res.Changes) != 8 || res.Dropped != 2 {
		t.Fatalf("Changes=%d Dropped=%d", len(res.Changes), res.Dropped)
	}
}

// TestAgentSystemPromptFrozen 系统提示词的冻结要素（10.5 的断言口径）。
func TestAgentSystemPromptFrozen(t *testing.T) {
	for _, want := range []string{
		"apply_graph", "update_prompt", "regenerate", "add_node", "connect",
		"{{ref:", "search_models", "get_kind_schema", "list_templates", "get_canvas", "validate_graph", "estimate_points",
		"一个节点只有一个输入口",
	} {
		if !strings.Contains(AgentSystemPrompt, want) {
			t.Fatalf("AgentSystemPrompt 缺少 %q", want)
		}
	}
	for _, kind := range AgentCreatableKinds() {
		if !strings.Contains(AgentSystemPrompt, kind) {
			t.Fatalf("AgentSystemPrompt 缺少可新建 kind %q", kind)
		}
	}
}

// TestBuildAgentPromptCarriesContext 回灌：上一轮问题、工具结果、画布上下文、对话历史都要进提示词。
func TestBuildAgentPromptCarriesContext(t *testing.T) {
	nodes := []AssistNode{{Id: "n2", Kind: "image", Label: "首帧", Prompt: "橘猫", Selected: true, Upstream: []string{"n1"}}}
	prompt := BuildAgentPrompt(AgentPromptInput{
		Mode: "patch", Kinds: AgentKindSchemas(), Models: agentTestCatalog().All(),
		Canvas: AgentCanvasText(nodes, map[string]*AgentNodeRecord{"n2": {Model: "gpt-image-1", Status: "failed", Error: "审核没过"}}),
		History: []AssistTurn{{Role: "user", Content: "上一句话"}},
		Question: "把首帧换成戴帽子的猫",
		Issues:   []AgentIssue{{Code: AgentCodeModelNotFound, Severity: "error", NodeId: "n2", Message: "模型没接入", Hint: "换一个"}},
		ToolFeed: []string{"[工具 search_models] type=video 可用：kling-v1"},
		Round:    2, MaxRounds: 6,
	})
	for _, want := range []string{"把首帧换成戴帽子的猫", "上一句话", "MODEL_NOT_FOUND", "换一个", "[工具 search_models]", "n2", "kling-v1", "2/6"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("提示词缺少 %q\n%s", want, prompt)
		}
	}
}

// TestBuildAgentPlanKeepsModeOnModelSwitch 换模型时保留的参数要逐键各判各的：
// 以前条件对 mode 也看 params["edit"]，没有 edit 键的 i2v 节点换完模型 mode 就没了，
// 图生视频会退回默认的 reference（文生视频），积分照扣。
func TestBuildAgentPlanKeepsModeOnModelSwitch(t *testing.T) {
	nodes := map[string]assistNodeRef{
		"n9": {Id: "n9", Kind: "video", Label: "主镜头",
			LastTask: &assistLastTask{Model: "kling-v1", Status: "succeeded", Params: `{"mode":"i2v","duration":5,"resolution":"720P"}`}},
	}
	res := ParseAgentReply(`{"reply":"换个模型再来","changes":[{"action":"regenerate","node_id":"n9","model":"seedance"}]}`,
		nodes, AgentParseOptions{})
	pricer := &fakeAssistPricer{available: map[string]int{"kling-v1": 60, "seedance": 80}}
	plan := BuildAgentPlan(res, AgentValidateOptions{Catalog: agentTestCatalog()}, pricer, 1, 6, "patch")

	if len(plan.Changes) != 1 || plan.Changes[0].Model != "seedance" {
		t.Fatalf("changes = %+v", plan.Changes)
	}
	// 时长 / 分辨率不跟着换模型走，mode 必须留下
	params := plan.Changes[0].Params
	if params["mode"] != "i2v" {
		t.Fatalf("换模型后 mode 丢了：%+v", params)
	}
	if params["duration"] != nil || params["resolution"] != nil {
		t.Fatalf("时长 / 分辨率不该沿用到新模型：%+v", params)
	}
}
