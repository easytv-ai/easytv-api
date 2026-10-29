package service

import (
	"strings"
	"testing"
)

// ===== 图级 / 计划级错误码 fixture（agent-contract 4.1 / 4.2 / 4.4）=====
//
// 断言口径：error 级必须阻断（report.OK == false），warning 级单独出现时不得阻断（report.OK == true）。

// fakeAgentCatalog 内存模型目录（测试与评测台注入用，不查库）。
type fakeAgentCatalog struct {
	hints    map[string]*AgentModelHint // 键 = taskType|model
	points   map[string]int             // 键 = taskType|model
	unpriced map[string]bool            // 定价表里没有、但 provider 有 → ErrAgentModelNotPriced
}

func agentTestCatalog() *fakeAgentCatalog {
	return &fakeAgentCatalog{
		hints: map[string]*AgentModelHint{
			"image|gpt-image-1": {Model: "gpt-image-1", Type: "image", Label: "GPT Image", Unit: "image", Points: 8},
			"video|kling-v1":    {Model: "kling-v1", Type: "video", Label: "Kling", Unit: "second", Points: 20, Durations: []int{5, 10}, Resolutions: []string{"720P", "1080P"}},
			"audio|tts-1":       {Model: "tts-1", Type: "audio", Label: "TTS", Unit: "count", Points: 3},
			"text|gpt-4o-mini":  {Model: "gpt-4o-mini", Type: "text", Label: "GPT-4o mini", Unit: "count", Points: 1},
		},
		points:   map[string]int{"image|gpt-image-1": 8, "video|kling-v1": 20, "audio|tts-1": 3, "text|gpt-4o-mini": 1},
		unpriced: map[string]bool{"foo-priced": true},
	}
}

func (c *fakeAgentCatalog) Resolve(taskType, modelName string) (*AgentModelHint, error) {
	if h, ok := c.hints[taskType+"|"+modelName]; ok {
		return h, nil
	}
	// 模型存在但类型不同 → 返回它的真实类型，由调用方判 MODEL_TYPE_MISMATCH
	for key, h := range c.hints {
		if strings.HasSuffix(key, "|"+modelName) {
			return h, nil
		}
	}
	if c.unpriced[modelName] {
		return nil, ErrAgentModelNotPriced
	}
	return nil, ErrAgentModelMissing
}

func (c *fakeAgentCatalog) Estimate(taskType, modelName, paramsJSON string, isMember bool) (int, error) {
	if _, err := c.Resolve(taskType, modelName); err != nil {
		return 0, err
	}
	return c.points[taskType+"|"+modelName], nil
}

func (c *fakeAgentCatalog) All() map[string][]AgentModelHint {
	out := map[string][]AgentModelHint{}
	for _, h := range c.hints {
		out[h.Type] = append(out[h.Type], *h)
	}
	return out
}

// agentTextNode / agentImageNode / agentVideoNode 常用节点。
func agentTextNode(id, content string) WorkflowNode {
	return WorkflowNode{Id: id, Kind: "text", X: 0, Y: 0, Data: map[string]any{"label": "脚本", "content": content}}
}

func agentImageNode(id string, x, y int) WorkflowNode {
	return WorkflowNode{Id: id, Kind: "image", X: x, Y: y, Data: map[string]any{"label": "图 " + id, "prompt": "一只橘猫在窗台上", "model": "gpt-image-1"}}
}

func agentVideoNode(id string, x, y int) WorkflowNode {
	return WorkflowNode{Id: id, Kind: "video", X: x, Y: y, Data: map[string]any{"label": "镜头 " + id, "prompt": "镜头缓慢推进", "model": "kling-v1"}}
}

// agentBaseGraph 一张通过校验的最小图：文本节点 → 图片节点。
func agentBaseGraph() *CanvasWorkflowGraph {
	return &CanvasWorkflowGraph{
		Version: 1,
		Nodes:   []WorkflowNode{agentTextNode("n1", "一只橘猫的 15 秒广告"), agentImageNode("n2", 400, 0)},
		Edges:   []WorkflowEdge{{Source: "n1", Target: "n2"}},
	}
}

func agentHasCode(issues []AgentIssue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

func agentCodes(issues []AgentIssue) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Code)
	}
	return out
}

func agentNodeOf(t *testing.T, g *CanvasWorkflowGraph, id string) *WorkflowNode {
	t.Helper()
	if g == nil {
		t.Fatalf("normalized 为空，取不到节点 %s", id)
	}
	for i := range g.Nodes {
		if g.Nodes[i].Id == id {
			return &g.Nodes[i]
		}
	}
	t.Fatalf("normalized 里没有节点 %s", id)
	return nil
}

// TestAgentValidateGraphErrorCodes 18 个 error 级 code 各一例：必须出现在 errors 里且阻断。
func TestAgentValidateGraphErrorCodes(t *testing.T) {
	cases := []struct {
		name  string
		code  string
		build func() *CanvasWorkflowGraph
		opts  func(*AgentValidateOptions)
	}{
		{
			name: "BAD_GRAPH", code: AgentCodeBadGraph,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				g.Version = 2
				return g
			},
		},
		{
			name: "DUPLICATE_ID", code: AgentCodeDuplicateId,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				g.Nodes = append(g.Nodes, agentImageNode("n2", 800, 0))
				return g
			},
		},
		{
			name: "UNKNOWN_KIND", code: AgentCodeUnknownKind,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				g.Nodes[1].Kind = "group"
				return g
			},
		},
		{
			name: "MISSING_REQUIRED", code: AgentCodeMissingRequired,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				delete(g.Nodes[1].Data, "prompt")
				return g
			},
		},
		{
			name: "MODEL_NOT_FOUND", code: AgentCodeModelNotFound,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				g.Nodes[1].Data["model"] = "not-a-model"
				return g
			},
		},
		{
			name: "MODEL_TYPE_MISMATCH", code: AgentCodeModelTypeMismatch,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				g.Nodes[1].Data["model"] = "gpt-4o-mini" // 文本模型给了图片节点
				return g
			},
		},
		{
			name: "MODEL_NOT_PRICED", code: AgentCodeModelNotPriced,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				g.Nodes[1].Data["model"] = "foo-priced"
				return g
			},
		},
		{
			name: "PARAM_OUT_OF_RANGE", code: AgentCodeParamOutOfRange,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				g.Nodes = append(g.Nodes, agentVideoNode("n3", 800, 0))
				g.Nodes[2].Data["params"] = map[string]any{"duration": 7, "ratio": "9:16"} // kling-v1 只有 5/10 秒
				return g
			},
		},
		{
			name: "EDGE_DANGLING", code: AgentCodeEdgeDangling,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				g.Edges = append(g.Edges, WorkflowEdge{Source: "n2", Target: "n9"})
				return g
			},
		},
		{
			name: "SELF_LOOP", code: AgentCodeSelfLoop,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				g.Edges = append(g.Edges, WorkflowEdge{Source: "n2", Target: "n2"})
				return g
			},
		},
		{
			name: "DUPLICATE_EDGE", code: AgentCodeDuplicateEdge,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				g.Edges = append(g.Edges, WorkflowEdge{Source: "n1", Target: "n2"})
				return g
			},
		},
		{
			name: "CYCLE", code: AgentCodeCycle,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				g.Nodes = append(g.Nodes, agentImageNode("n3", 800, 0))
				g.Edges = append(g.Edges, WorkflowEdge{Source: "n2", Target: "n3"}, WorkflowEdge{Source: "n3", Target: "n2"})
				return g
			},
		},
		{
			name: "VIDEO_NEEDS_FRAME", code: AgentCodeVideoNeedsFrame,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				v := agentVideoNode("n3", 800, 0)
				v.Data["videoMode"] = "i2v"
				g.Nodes = append(g.Nodes, v)
				return g
			},
		},
		{
			name: "ROLE_KIND_MISMATCH", code: AgentCodeRoleKindMismatch,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				g.Nodes = append(g.Nodes, WorkflowNode{Id: "n3", Kind: "audio", X: 800, Y: 0,
					Data: map[string]any{"label": "旁白", "prompt": "欢快女声", "model": "tts-1"}})
				g.Edges = append(g.Edges, WorkflowEdge{Source: "n2", Target: "n3"}) // audio 只收 text/audio
				return g
			},
		},
		{
			name: "REF_TOKEN_LEFT", code: AgentCodeRefTokenLeft,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				g.Nodes[1].Data["prompt"] = "用 {{ref:产品图}} 做首帧"
				return g
			},
		},
		{
			name: "ASSET_FORBIDDEN", code: AgentCodeAssetForbidden,
			build: func() *CanvasWorkflowGraph {
				g := agentBaseGraph()
				g.Nodes[1].Data["assetId"] = 12
				return g
			},
		},
		{
			name: "TOO_LARGE", code: AgentCodeTooLarge,
			build: func() *CanvasWorkflowGraph { return agentBaseGraph() },
			opts:  func(o *AgentValidateOptions) { o.MaxNodes = 1 },
		},
		{
			name: "OVER_BUDGET", code: AgentCodeOverBudget,
			build: func() *CanvasWorkflowGraph { return agentBaseGraph() },
			opts:  func(o *AgentValidateOptions) { o.MaxPoints = 1 }, // 图片节点 8 积分
		},
	}

	seen := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := AgentValidateOptions{Catalog: agentTestCatalog()}
			if tc.opts != nil {
				tc.opts(&opts)
			}
			report := ValidateAgentGraph(tc.build(), opts)
			if !agentHasCode(report.Errors, tc.code) {
				t.Fatalf("%s 未命中，errors=%v warnings=%v", tc.code, agentCodes(report.Errors), agentCodes(report.Warnings))
			}
			if len(report.Errors) != 1 {
				t.Fatalf("fixture 应只触发 %s，实际 errors=%v", tc.code, agentCodes(report.Errors))
			}
			if report.OK {
				t.Fatalf("%s 是 error 级，必须阻断（OK=false）", tc.code)
			}
			if got := AgentIssueSeverity(tc.code); got != "error" {
				t.Fatalf("AgentIssueSeverity(%s) = %s, want error", tc.code, got)
			}
			if report.Errors[0].Severity != "error" {
				t.Fatalf("%s 的 severity 字段 = %s", tc.code, report.Errors[0].Severity)
			}
			seen[tc.code] = true
		})
	}

	// 表里必须覆盖 4.1 的全部 error 级 code
	for _, code := range []string{
		AgentCodeBadGraph, AgentCodeDuplicateId, AgentCodeUnknownKind, AgentCodeMissingRequired,
		AgentCodeModelNotFound, AgentCodeModelTypeMismatch, AgentCodeModelNotPriced, AgentCodeParamOutOfRange,
		AgentCodeEdgeDangling, AgentCodeSelfLoop, AgentCodeDuplicateEdge, AgentCodeCycle,
		AgentCodeVideoNeedsFrame, AgentCodeRoleKindMismatch, AgentCodeRefTokenLeft, AgentCodeAssetForbidden,
		AgentCodeTooLarge, AgentCodeOverBudget,
	} {
		if !seen[code] {
			t.Fatalf("缺少 %s 的 fixture", code)
		}
	}
}

// TestAgentValidateGraphNilGraph 空图不 panic，返回 BAD_GRAPH。
func TestAgentValidateGraphNilGraph(t *testing.T) {
	report := ValidateAgentGraph(nil, AgentValidateOptions{Catalog: agentTestCatalog()})
	if report == nil || !agentHasCode(report.Errors, AgentCodeBadGraph) || report.OK {
		t.Fatalf("nil 图应返回 BAD_GRAPH 且不阻断失败：%+v", report)
	}
}

// TestAgentValidateGraphWarningCodes 3 个 warning 级 code 各一例：不阻断（OK 仍为 true）。
func TestAgentValidateGraphWarningCodes(t *testing.T) {
	t.Run("USELESS_EDGE", func(t *testing.T) {
		g := agentBaseGraph()
		g.Nodes = append(g.Nodes, agentTextNode("n3", "旁白文案"))
		g.Edges = append(g.Edges, WorkflowEdge{Source: "n2", Target: "n3"}) // 图片 → 文本，前端不会把它当提示词
		report := ValidateAgentGraph(g, AgentValidateOptions{Catalog: agentTestCatalog()})
		if !agentHasCode(report.Warnings, AgentCodeUselessEdge) {
			t.Fatalf("warnings=%v", agentCodes(report.Warnings))
		}
		if len(report.Errors) != 0 || !report.OK {
			t.Fatalf("warning 不该阻断：errors=%v ok=%v", agentCodes(report.Errors), report.OK)
		}
		if AgentIssueSeverity(AgentCodeUselessEdge) != "warning" {
			t.Fatal("USELESS_EDGE 必须是 warning")
		}
		if report.Normalized != nil {
			for _, e := range report.Normalized.Edges {
				if e.Source == "n2" && e.Target == "n3" {
					t.Fatal("无用的连线必须从 normalized 里丢掉")
				}
			}
		}
	})

	t.Run("ORPHAN_NODE", func(t *testing.T) {
		g := agentBaseGraph()
		g.Nodes = append(g.Nodes, agentImageNode("n3", 800, 0)) // 谁都没连
		report := ValidateAgentGraph(g, AgentValidateOptions{Catalog: agentTestCatalog()})
		if !agentHasCode(report.Warnings, AgentCodeOrphanNode) {
			t.Fatalf("warnings=%v", agentCodes(report.Warnings))
		}
		if len(report.Errors) != 0 || !report.OK {
			t.Fatalf("warning 不该阻断：errors=%v ok=%v", agentCodes(report.Errors), report.OK)
		}
	})

	t.Run("FRAME_ORDER", func(t *testing.T) {
		report := ValidateAgentGraph(agentFrameCase("first", 800, 0, "last", 0, 0, 0), AgentValidateOptions{Catalog: agentTestCatalog()})
		if !agentHasCode(report.Warnings, AgentCodeFrameOrder) {
			t.Fatalf("warnings=%v", agentCodes(report.Warnings))
		}
		if len(report.Errors) != 0 || !report.OK {
			t.Fatalf("warning 不该阻断：errors=%v ok=%v", agentCodes(report.Errors), report.OK)
		}
		if AgentIssueSeverity(AgentCodeFrameOrder) != "warning" {
			t.Fatal("FRAME_ORDER 必须是 warning")
		}
	})

	t.Run("ORPHAN_NODE_TEXT_ALLOWED", func(t *testing.T) {
		// text 节点允许孤立：单独一个文本节点不该报 ORPHAN_NODE
		g := &CanvasWorkflowGraph{Version: 1, Nodes: []WorkflowNode{agentTextNode("n1", "只是写点字")}}
		report := ValidateAgentGraph(g, AgentValidateOptions{Catalog: agentTestCatalog()})
		if agentHasCode(report.Warnings, AgentCodeOrphanNode) {
			t.Fatalf("text 节点不该报孤立：%v", agentCodes(report.Warnings))
		}
		if !report.OK {
			t.Fatalf("ok 应为 true：%v", agentCodes(report.Errors))
		}
	})
}

// agentFrameCase 构造「一个 video 节点 + 两个上游 image（首帧 / 尾帧）」的图，坐标按反例给。
// vy 是 video 节点的 y：它决定 minY，从而决定吸附后的行号（三个反例的差别就在这里）。
func agentFrameCase(firstRole string, fx, fy int, lastRole string, lx, ly, vy int) *CanvasWorkflowGraph {
	f := agentImageNode("n1", fx, fy)
	f.Data["frameRole"] = firstRole
	l := agentImageNode("n2", lx, ly)
	l.Data["frameRole"] = lastRole
	v := agentVideoNode("n3", 1600, vy)
	return &CanvasWorkflowGraph{
		Version: 1,
		Nodes:   []WorkflowNode{f, l, v},
		Edges: []WorkflowEdge{
			{Source: "n1", Target: "n3"},
			{Source: "n2", Target: "n3"},
		},
	}
}

// TestAgentFrameOrderCounterExamples 2.5 注意二的 (a)(b)(c) 三个反例：
// 修正后 (rowF, colF) 必须严格早于 (rowL, colL)，且 warnings 里必须有 FRAME_ORDER（(c) 原本漏判）。
func TestAgentFrameOrderCounterExamples(t *testing.T) {
	cases := []struct {
		name  string
		graph *CanvasWorkflowGraph
	}{
		// (a) 触发不到：F 与 L 原始 y 都是 0（minY=0），靠列决胜
		{name: "a_同一行靠列决胜", graph: agentFrameCase("first", 800, 0, "last", 0, 0, 0)},
		// (b) 修了也没用：F(y=200) / L(y=150)，minY=150，吸附后都在第 0 行
		{name: "b_吸附后仍在同一行", graph: agentFrameCase("first", 800, 200, "last", 0, 150, 200)},
		// (c) 漏判：F(y=100) / L(y=150)，minY=100，吸附后都在第 0 行，原始 y 上判不出来
		{name: "c_原始_y_上判不出", graph: agentFrameCase("first", 800, 100, "last", 0, 150, 100)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := ValidateAgentGraph(tc.graph, AgentValidateOptions{Catalog: agentTestCatalog()})
			if len(report.Errors) != 0 {
				t.Fatalf("fixture 不该有 error：%v", agentCodes(report.Errors))
			}
			if !agentHasCode(report.Warnings, AgentCodeFrameOrder) {
				t.Fatalf("缺 FRAME_ORDER warning：%v", agentCodes(report.Warnings))
			}
			f := agentNodeOf(t, report.Normalized, "n1")
			l := agentNodeOf(t, report.Normalized, "n2")
			fRow, fCol := f.Y/AgentRowGap, f.X/AgentColGap
			lRow, lCol := l.Y/AgentRowGap, l.X/AgentColGap
			if !(fRow < lRow || (fRow == lRow && fCol < lCol)) {
				t.Fatalf("首帧没有严格排在尾帧前面：F=(row %d, col %d) L=(row %d, col %d)", fRow, fCol, lRow, lCol)
			}
			// value 里带的是吸附后的 y（修正前后）
			var value string
			for _, w := range report.Warnings {
				if w.Code == AgentCodeFrameOrder {
					value = w.Value
				}
			}
			if !strings.Contains(value, "n1:") || !strings.Contains(value, "n2:") || !strings.Contains(value, "→") {
				t.Fatalf("FRAME_ORDER value 格式不对：%q", value)
			}
			// frameRole 是编译期提示，输出前必须删掉
			for _, n := range report.Normalized.Nodes {
				if _, ok := n.Data["frameRole"]; ok {
					t.Fatalf("normalized 里不该留 frameRole：%+v", n.Data)
				}
			}
		})
	}
}

// TestAgentFrameOrderNeedsBothRoles 只有首帧、没有尾帧时不判也不修。
func TestAgentFrameOrderNeedsBothRoles(t *testing.T) {
	g := agentBaseGraph()
	v := agentVideoNode("n3", 1600, 0)
	g.Nodes = append(g.Nodes, v)
	g.Nodes[1].Data["frameRole"] = "first" // n2 是首帧，但没有尾帧
	g.Edges = append(g.Edges, WorkflowEdge{Source: "n2", Target: "n3"})
	report := ValidateAgentGraph(g, AgentValidateOptions{Catalog: agentTestCatalog()})
	if agentHasCode(report.Warnings, AgentCodeFrameOrder) {
		t.Fatal("只有一侧 frameRole 时不该判 FRAME_ORDER")
	}
	if !report.OK {
		t.Fatalf("ok 应为 true：%v", agentCodes(report.Errors))
	}
}

// TestCompileAgentGraphNormalizes 编译产物：坐标吸附成网格、补 kind/mode/handle、丢白名单外的键。
func TestCompileAgentGraphNormalizes(t *testing.T) {
	g := &CanvasWorkflowGraph{
		Version: 1,
		Nodes: []WorkflowNode{
			{Id: "n1", Kind: "text", X: 37, Y: 12, Data: map[string]any{"label": "脚本", "content": "文案"}},
			{Id: "n2", Kind: "image", X: 402, Y: 8, Data: map[string]any{
				"label": "首帧", "prompt": "橘猫", "model": "gpt-image-1",
				"params": map[string]any{"ratio": "9:16", "clarity": "2K", "unknown_key": 1},
				"junk":   "应该被丢掉",
			}},
			{Id: "n3", Kind: "image", X: 405, Y: 10, Data: map[string]any{"label": "第二张", "prompt": "另一只猫", "model": "gpt-image-1"}},
		},
		Edges: []WorkflowEdge{{Source: "n1", Target: "n2"}, {Source: "n1", Target: "n3"}},
	}
	compiled, report := CompileAgentGraph(g, AgentValidateOptions{Catalog: agentTestCatalog()})
	if len(report.Errors) != 0 {
		t.Fatalf("不该有 error：%v", agentCodes(report.Errors))
	}
	if compiled == nil || compiled.Version != 1 {
		t.Fatal("编译结果为空")
	}
	n1 := agentNodeOf(t, compiled, "n1")
	if n1.X != 0 || n1.Y != 0 {
		t.Fatalf("n1 应吸附到 (0,0)，得到 (%d,%d)", n1.X, n1.Y)
	}
	n2 := agentNodeOf(t, compiled, "n2")
	if n2.X != AgentColGap || n2.Y != 0 {
		t.Fatalf("n2 应吸附到 (400,0)，得到 (%d,%d)", n2.X, n2.Y)
	}
	// n3 与 n2 同格：按输入顺序下移一行
	n3 := agentNodeOf(t, compiled, "n3")
	if n3.X != AgentColGap || n3.Y != AgentRowGap {
		t.Fatalf("n3 与 n2 同格应下移一行，得到 (%d,%d)", n3.X, n3.Y)
	}
	if n2.Data["kind"] != "image" || n2.Data["mode"] != "idle" {
		t.Fatalf("必须补 kind/mode：%+v", n2.Data)
	}
	if _, ok := n2.Data["junk"]; ok {
		t.Fatal("白名单外的键必须丢掉")
	}
	params, _ := n2.Data["params"].(map[string]any)
	if len(params) != 2 || params["unknown_key"] != nil {
		t.Fatalf("params 白名单没生效：%+v", params)
	}
	if n2.Width != AgentNodeWMedia || n2.Height != AgentNodeH {
		t.Fatalf("宽高缺省不对：%dx%d", n2.Width, n2.Height)
	}
	for _, e := range compiled.Edges {
		if e.SourceHandle != "out" || e.TargetHandle != "in" {
			t.Fatalf("handle 应补成 out/in：%+v", e)
		}
	}
}

// TestBuildAgentPlanTooManyChanges 计划级：changes > 8 或 2 个 apply_graph → TOO_MANY_CHANGES（error），Changes 为空。
func TestBuildAgentPlanTooManyChanges(t *testing.T) {
	opts := AgentValidateOptions{Catalog: agentTestCatalog()}
	pricer := &fakeAssistPricer{available: map[string]int{"gpt-image-1": 8}}

	t.Run("nine_changes", func(t *testing.T) {
		changes := make([]AssistChange, 9)
		for i := range changes {
			changes[i] = AssistChange{Action: AssistActionConnect, Source: "n1", Target: "n2"}
		}
		plan := BuildAgentPlan(&AgentParseResult{Kind: "plan", Reply: "改 9 处", Changes: changes}, opts, pricer, 1, 6, "patch")
		if !agentHasCode(plan.Validate.Errors, AgentCodeTooManyChanges) || plan.Validate.OK || len(plan.Changes) != 0 {
			t.Fatalf("9 条应触发 TOO_MANY_CHANGES 且不返回 changes：errors=%v ok=%v changes=%d",
				agentCodes(plan.Validate.Errors), plan.Validate.OK, len(plan.Changes))
		}
		if AgentIssueSeverity(AgentCodeTooManyChanges) != "error" {
			t.Fatal("TOO_MANY_CHANGES 必须是 error")
		}
	})

	t.Run("two_apply_graph", func(t *testing.T) {
		g := agentBaseGraph()
		plan := BuildAgentPlan(&AgentParseResult{
			Kind: "plan", Reply: "落两张图",
			Changes: []AssistChange{{Action: AssistActionApplyGraph, Graph: g}, {Action: AssistActionApplyGraph, Graph: g}},
			Graphs:  []*CanvasWorkflowGraph{g, g},
		}, opts, pricer, 1, 6, "generate")
		if !agentHasCode(plan.Validate.Errors, AgentCodeTooManyChanges) || plan.Validate.OK || len(plan.Changes) != 0 {
			t.Fatalf("2 个 apply_graph 应触发 TOO_MANY_CHANGES：errors=%v ok=%v changes=%d",
				agentCodes(plan.Validate.Errors), plan.Validate.OK, len(plan.Changes))
		}
	})
}

// TestAgentKindSchemaTable kind 注册表的关键内容（冻结表逐字照抄，缺一项规则就失效）。
func TestAgentKindSchemaTable(t *testing.T) {
	if got := AgentCreatableKinds(); strings.Join(got, ",") != "text,image,panorama,video,director,audio" {
		t.Fatalf("可新建白名单 = %v", got)
	}
	if got := len(AgentAllKinds()); got != 9 {
		t.Fatalf("全部 kind = %d 个", got)
	}
	img := AgentKindSchema("image")
	if img == nil || !agentContainsString(img.Optional, "frameRole") {
		t.Fatal("frameRole 必须在 image 的 optional 里，否则 FRAME_ORDER 永远不触发")
	}
	for _, kind := range []string{"video", "director"} {
		s := AgentKindSchema(kind)
		if s == nil || !agentContainsString(s.Optional, "videoMode") {
			t.Fatalf("videoMode 必须在 %s 的 optional 里，否则 VIDEO_NEEDS_FRAME 永远不触发", kind)
		}
	}
	if AgentKindSchema("character") == nil || AgentKindSchema("character").Creatable {
		t.Fatal("character 只能出现在已有画布上")
	}
	if AgentKindSchema("group") != nil {
		t.Fatal("group 不在注册表里")
	}
	if !AgentKindAcceptable("video", "image") || AgentKindAcceptable("audio", "image") || !AgentKindAcceptable("text", "text") {
		t.Fatal("语义连线表不对")
	}
	if AgentIssueSeverity("NOT_A_CODE") != "error" {
		t.Fatal("未知 code 必须按 error 处理")
	}
}

// TestAgentToolNamesAndRun runAgentTool 的白名单与基本回话（不查库）。
func TestAgentToolNamesAndRun(t *testing.T) {
	want := "search_models,get_kind_schema,list_templates,get_canvas,validate_graph,estimate_points"
	if got := strings.Join(AgentToolNames(), ","); got != want {
		t.Fatalf("工具白名单 = %s", got)
	}
	in := AgentLoopInput{Catalog: agentTestCatalog()}
	if text, ok := runAgentTool("search_models", []byte(`{"type":"video"}`), in); !ok || !strings.Contains(text, "kling-v1") {
		t.Fatalf("search_models = %q ok=%v", text, ok)
	}
	if text, ok := runAgentTool("get_kind_schema", []byte(`{"kind":"video"}`), in); !ok || !strings.Contains(text, "可接 image/video/audio/text") {
		t.Fatalf("get_kind_schema = %q ok=%v", text, ok)
	}
	if text, ok := runAgentTool("list_templates", []byte(`{}`), in); !ok || text != "（没有可复用模板）" {
		t.Fatalf("list_templates = %q ok=%v", text, ok)
	}
	if text, ok := runAgentTool("get_canvas", []byte(`{}`), in); !ok || text != "画布不可读" {
		t.Fatalf("get_canvas = %q ok=%v", text, ok)
	}
	// validate_graph：不通过时列出 code(node, value)
	bad := &CanvasWorkflowGraph{Version: 1, Nodes: []WorkflowNode{
		{Id: "n1", Kind: "image", X: 0, Y: 0, Data: map[string]any{"label": "图", "prompt": "猫", "model": "not-a-model"}},
	}}
	if text, ok := runAgentTool("validate_graph", []byte(`{"graph":`+agentJSON(bad)+`}`), in); !ok || !strings.Contains(text, "不通过") || !strings.Contains(text, "MODEL_NOT_FOUND(n1") {
		t.Fatalf("validate_graph = %q ok=%v", text, ok)
	}
	// estimate_points：逐节点报价
	good := agentBaseGraph()
	text, ok := runAgentTool("estimate_points", []byte(`{"graph":`+agentJSON(good)+`}`), in)
	if !ok || !strings.Contains(text, "预计 8 积分") || !strings.Contains(text, "n2 8") {
		t.Fatalf("estimate_points = %q ok=%v", text, ok)
	}
	// 写错参数也只回一句人话（永不 panic、永不 error）
	if text, ok := runAgentTool("validate_graph", []byte(`{bad json`), in); ok || !strings.Contains(text, "args") {
		t.Fatalf("坏 args 应回人话：%q ok=%v", text, ok)
	}
	if text, ok := runAgentTool("nope", nil, in); ok || !strings.Contains(text, "没有 nope 这个工具") {
		t.Fatalf("未知工具应回人话：%q ok=%v", text, ok)
	}
}

// TestAgentKindAndModelWhitespaceNormalized 对抗回归：kind / model / videoMode / params 值 / handle
// 带空白时，编译期必须规范化——否则 " video " 会绕过 UNKNOWN_KIND 判定、躲开 n.Kind == "video"
// 的语义规则（VIDEO_NEEDS_FRAME），还会把未注册的 kind 原样交给前端。
func TestAgentKindAndModelWhitespaceNormalized(t *testing.T) {
	g := &CanvasWorkflowGraph{
		Version: 1,
		Nodes: []WorkflowNode{
			agentImageNode("n1", 0, 0),
			{Id: "n2", Kind: " video ", X: 400, Y: 0, Data: map[string]any{
				"label": "镜头", "prompt": "镜头缓慢推进", "model": " kling-v1 ", "videoMode": " i2v ",
				"params": map[string]any{"ratio": " 9:16 ", "duration": 5}}},
		},
		Edges: []WorkflowEdge{{Source: "n1", Target: "n2", SourceHandle: " out ", TargetHandle: "foo"}},
	}
	compiled, report := CompileAgentGraph(g, AgentValidateOptions{Catalog: agentTestCatalog()})
	if len(report.Errors) != 0 {
		t.Fatalf("规范化后应通过：%v", agentCodes(report.Errors))
	}
	n2 := agentNodeOf(t, compiled, "n2")
	if n2.Kind != "video" || !agentContainsString(AgentCreatableKinds(), n2.Kind) {
		t.Fatalf("kind 必须规范化成注册表里的值：%q", n2.Kind)
	}
	if n2.Data["kind"] != "video" {
		t.Fatalf("data.kind = %v", n2.Data["kind"])
	}
	if n2.Data["model"] != "kling-v1" || n2.Data["videoMode"] != "i2v" {
		t.Fatalf("model/videoMode 必须去空白：%+v", n2.Data)
	}
	params, _ := n2.Data["params"].(map[string]any)
	if params["ratio"] != "9:16" {
		t.Fatalf("params 值必须去空白：%+v", params)
	}
	for _, e := range compiled.Edges {
		if e.SourceHandle != "out" || e.TargetHandle != "in" {
			t.Fatalf("handle 必须统一成 out/in：%+v", e)
		}
	}

	// 语义规则不能被空白绕过：带空白的 kind + videoMode=i2v 且没有上游 image → 必须报 VIDEO_NEEDS_FRAME
	lonely := &CanvasWorkflowGraph{Version: 1, Nodes: []WorkflowNode{
		{Id: "n1", Kind: " video ", X: 0, Y: 0, Data: map[string]any{
			"label": "镜头", "prompt": "镜头缓慢推进", "model": "kling-v1", "videoMode": "i2v"}},
	}}
	report2 := ValidateAgentGraph(lonely, AgentValidateOptions{Catalog: agentTestCatalog()})
	if !agentHasCode(report2.Errors, AgentCodeVideoNeedsFrame) || report2.OK {
		t.Fatalf("VIDEO_NEEDS_FRAME 被空白绕过：errors=%v ok=%v", agentCodes(report2.Errors), report2.OK)
	}

	// 真正没注册的 kind（带空白）仍然按 UNKNOWN_KIND 拒绝
	fake := &CanvasWorkflowGraph{Version: 1, Nodes: []WorkflowNode{
		{Id: "n1", Kind: " hologram ", X: 0, Y: 0, Data: map[string]any{"label": "幻觉", "prompt": "x", "model": "gpt-image-1"}},
	}}
	report3 := ValidateAgentGraph(fake, AgentValidateOptions{Catalog: agentTestCatalog()})
	if !agentHasCode(report3.Errors, AgentCodeUnknownKind) || report3.Normalized != nil {
		t.Fatalf("未知 kind 必须阻断：errors=%v normalized=%v", agentCodes(report3.Errors), report3.Normalized)
	}
}

// TestAgentFilterParamsNormalizesResolution resolution 的判定走 ToUpper，输出也必须归一，
// 否则模型写的 "1080p" 原样落到节点上，前端在档位表里找不到会重置成默认档。
func TestAgentFilterParamsNormalizesResolution(t *testing.T) {
	got := agentFilterParams("video", map[string]any{"resolution": " 1080p ", "ratio": "9:16", "seed": 7})
	if got["resolution"] != "1080P" {
		t.Fatalf("resolution 没归一：%+v", got)
	}
	if got["ratio"] != "9:16" {
		t.Fatalf("其余枚举按原样精确比对，不该被改：%+v", got)
	}
	if _, ok := got["seed"]; ok {
		t.Fatalf("白名单外的键要丢掉：%+v", got)
	}

	// 走一遍编译器：小写也能过校验，且 normalized 里是大写
	graph := &CanvasWorkflowGraph{Version: 1, Nodes: []WorkflowNode{
		{Id: "n1", Kind: "video", Data: map[string]any{
			"label": "主镜头", "prompt": "推近", "model": "kling-v1",
			"params": map[string]any{"resolution": "720p"},
		}},
	}}
	out, report := CompileAgentGraph(graph, AgentValidateOptions{Catalog: agentTestCatalog()})
	if !report.OK {
		t.Fatalf("小写分辨率应当通过校验：%v", agentCodes(report.Errors))
	}
	if params, _ := out.Nodes[0].Data["params"].(map[string]any); params["resolution"] != "720P" {
		t.Fatalf("normalized 里的分辨率 = %+v", out.Nodes[0].Data["params"])
	}
}

// TestListTemplatesFeedsGraph 检索到的模板必须把可抄的结构给模型，
// 只回一句「几个节点几条连线」的话，模型调了工具、耗了一轮却什么也抄不到。
func TestListTemplatesFeedsGraph(t *testing.T) {
	in := AgentLoopInput{
		Catalog: agentTestCatalog(),
		Templates: func(keyword string, limit int) []AgentTemplate {
			return []AgentTemplate{
				{Source: "workflow", Name: "竖屏广告骨架", Summary: "4 个节点 / 3 条连线", Graph: `{"version":1,"nodes":[{"id":"n1","kind":"text"}]}`},
				{Source: "example", Name: "只有经验没有图", Summary: "先写脚本再出图"},
			}
		},
	}
	text, ok := runAgentTool("list_templates", []byte(`{"keyword":"广告"}`), in)
	if !ok {
		t.Fatalf("list_templates 失败：%s", text)
	}
	if !strings.Contains(text, `"kind":"text"`) {
		t.Fatalf("模板的 graph 没喂给模型：\n%s", text)
	}
	if !strings.Contains(text, "只有经验没有图") || strings.Contains(text, "可抄的结构：\n") {
		t.Fatalf("没有 graph 的条目不该多一行空结构：\n%s", text)
	}
}
