package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easytv-api/service"
	"easytv-api/setting"
	"easytv-api/tests/agent_eval"
)

// ===== 评测台自测：全部用确定性 mock，不烧 token、不连库、不提交任务 =====

// expectedMockStructures 是 mock 生成器跑出来的**手算**结构分（自测断言精确值）。
// 每条都对着 tests/agent_eval/mock.go 里那张固定图与 cases.go 里的期望算出来：
//
//	c01 3/3 kind + 3∈[3,5] + 2/2 连线 + 首帧            = 0.40+0.20+0.25+0.15 = 1.00
//	c02 4/4 + 4∈[4,6] + 2/2 + 首帧                       = 1.00
//	c03 4/4 + 5>Max4(4/5=0.8) + 2/2 + 首帧               = 0.40+0.16+0.25+0.15 = 0.96
//	c04 3/3 + 8∈[4,9] + 1/1 + 首帧                       = 1.00
//	c05 3/4(0.75) + 7∈[4,9] + 2/2 + 首帧                 = 0.30+0.20+0.25+0.15 = 0.90
//	c06 2/2 + 3∈[2,4] + 1/1 + 首帧                       = 1.00
//	c07 3/3 + 3∈[3,4] + 1/2(0.5) + 无首帧要求            = 0.40+0.20+0.125+0.15 = 0.875
//	c08 2/2 + 2∈[2,3] + 1/1 + 无首帧要求                 = 1.00
//	c09 2/2 + 2∈[2,3] + 1/1 + 要求首帧但没有上游图(0)     = 0.85
//	c10 4/4 + 4∈[4,5] + 3/3 + 首帧                       = 1.00
//	c11 AllowNoChange：changes 空且 reply 非空            = 1.00
//	c12 1.00 × 0.5（估价 128 > MaxPoints 100）            = 0.50
var expectedMockStructures = map[string]float64{
	"c01": 1.00,
	"c02": 1.00,
	"c03": 0.96,
	"c04": 1.00,
	"c05": 0.90,
	"c06": 1.00,
	"c07": 0.875,
	"c08": 1.00,
	"c09": 0.85,
	"c10": 1.00,
	"c11": 1.00,
	"c12": 0.50,
}

// mockStats 是上面那张表推出来的冻结 stats 字符串：
// 12 条全通过、结构均分 (1+1+0.96+1+0.90+1+0.875+1+0.85+1+1+0.5)/12 = 0.92375 → 0.92、0 条修复轮、平均 1.0 轮。
const mockStats = "12 条用例：校验通过 12/12（100.0%），结构均分 0.92，修复轮命中 0 条，平均 1.0 轮；live=false"

// TestAgentEvalCasesWellFormed 用例集本身要自洽，且每条都能被 mock 关键词命中。
func TestAgentEvalCasesWellFormed(t *testing.T) {
	cases := agent_eval.Cases()
	if len(cases) < 12 {
		t.Fatalf("至少 12 条用例，got %d", len(cases))
	}
	wantIDs := []string{"c01", "c02", "c03", "c04", "c05", "c06", "c07", "c08", "c09", "c10", "c11", "c12"}
	if got := agent_eval.CaseIDs(); strings.Join(got, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("用例 id 必须是 c01..c12 且顺序固定，got %v", got)
	}
	// 契约 10.1 要求的覆盖面，逐个关键词确认（可机器判定，不靠人读）。
	// 关键词必须互斥：同一段 brief 不能命中两条规则（MockRuleID 的断言会兜住）。
	coverage := map[string]string{
		"竖屏短视频": "c01", "人物口播": "c02", "产品广告": "c03", "音乐卡点": "c04",
		"短剧": "c05", "首尾帧": "c06", "旁白配音": "c07", "全景": "c08",
		"导演台": "c09", "复刻": "c10", "别改": "c11", "预算": "c12",
	}
	seen := map[string]bool{}
	for keyword, id := range coverage {
		c, ok := agent_eval.CaseByID(id)
		if !ok {
			t.Fatalf("用例 %s 不存在", id)
		}
		if !strings.Contains(c.Brief, keyword) {
			t.Errorf("用例 %s 的 brief 里没有覆盖关键词 %q：%s", id, keyword, c.Brief)
		}
		if got := agent_eval.MockRuleID(c.Brief); got != id {
			t.Errorf("关键词 %q 把 %s 的 brief 映射到了 %s", keyword, id, got)
		}
		seen[id] = true
	}
	for _, c := range cases {
		if !seen[c.Id] {
			t.Errorf("用例 %s 没有登记覆盖面关键词", c.Id)
		}
	}
	for _, c := range cases {
		if problems := c.Problems(); len(problems) > 0 {
			t.Errorf("用例 %s 不自洽：%s", c.Id, strings.Join(problems, "；"))
		}
		if _, ok := agent_eval.MockReplyForCase(c); !ok {
			t.Errorf("用例 %s 没有 mock 回复", c.Id)
		}
	}
	// 纯提问用例必须显式标 AllowNoChange，否则永远拿不到 schema 通过
	c11, ok := agent_eval.CaseByID("c11")
	if !ok || !c11.Expect.AllowNoChange {
		t.Fatalf("c11 必须是 AllowNoChange 的纯提问用例")
	}
	// 超预算用例必须有 MaxPoints
	c12, ok := agent_eval.CaseByID("c12")
	if !ok || c12.Expect.MaxPoints <= 0 {
		t.Fatalf("c12 必须带 MaxPoints（超预算打对折）")
	}
}

// TestAgentEvalMockSelfCheck 用确定性 mock 跑完整的「循环 → 校验 → 打分」，断言精确分数与 stats。
func TestAgentEvalMockSelfCheck(t *testing.T) {
	catalog := agent_eval.NewMockCatalog()
	opts := agent_eval.ScoreOptions{Catalog: catalog}
	cases := agent_eval.Cases()
	results := make([]agent_eval.CaseResult, 0, len(cases))

	for _, c := range cases {
		in := agent_eval.NewLoopInput(c, catalog, "gpt-4o-mini", 0)
		outcome := agent_eval.RunCase(context.Background(), c, agent_eval.NewMockChatterForCase(c), in, opts)
		if outcome.Err != nil {
			t.Fatalf("用例 %s 循环报错：%v", c.Id, outcome.Err)
		}
		if outcome.Plan == nil {
			t.Fatalf("用例 %s 没有产出计划", c.Id)
		}
		want, ok := expectedMockStructures[c.Id]
		if !ok {
			t.Fatalf("用例 %s 没有手算期望值", c.Id)
		}
		if !outcome.Result.SchemaOk {
			t.Errorf("用例 %s：SchemaOk 应为 true，errors=%s", c.Id, dumpIssues(outcome.Plan.Validate.Errors))
		}
		if diff := outcome.Result.Structure - want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("用例 %s：Structure=%.6f，手算值 %.3f（errors=%s warnings=%s）",
				c.Id, outcome.Result.Structure, want,
				dumpIssues(outcome.Plan.Validate.Errors), dumpIssues(outcome.Plan.Validate.Warnings))
		}
		if outcome.Result.Execution != agent_eval.ExecSkipped {
			t.Errorf("用例 %s：默认可执行轴必须是 skipped，got %q", c.Id, outcome.Result.Execution)
		}
		if outcome.Result.Rounds != 1 || outcome.Result.Repaired {
			t.Errorf("用例 %s：mock 一次就过，期望 1 轮且未修复，got rounds=%d repaired=%v",
				c.Id, outcome.Result.Rounds, outcome.Result.Repaired)
		}
		results = append(results, outcome.Result)
	}

	summary := agent_eval.NewSummary(false, "mock 自测（不烧 token）", results)
	if summary.Stats != mockStats {
		t.Errorf("stats 字符串与手算不符：\n got %s\nwant %s", summary.Stats, mockStats)
	}
}

// TestAgentEvalScoreCriticalZero 关键错误直接归零（契约 10.2）。
func TestAgentEvalScoreCriticalZero(t *testing.T) {
	c, _ := agent_eval.CaseByID("c01")
	graph := testGraph(
		testNode("n1", "text", map[string]any{"content": "文案"}),
		testNode("n2", "image", map[string]any{"prompt": "图", "model": "gpt-image-1"}),
	)
	plan := testPlan(graph, 8)
	plan.Validate.OK = false
	plan.Validate.Errors = []service.AgentIssue{{Code: "UNKNOWN_KIND", Severity: "error", Message: "kind 不在白名单"}}
	if agent_eval.SchemaOk(c, plan) {
		t.Error("关键错误时 SchemaOk 必须为 false")
	}
	if got := agent_eval.Structure(c, plan); got != 0 {
		t.Errorf("关键错误时 Structure 必须硬零，got %.4f", got)
	}
	for _, code := range agent_eval.CriticalCodes() {
		if !agent_eval.IsCriticalCode(code) {
			t.Errorf("%s 应该在关键错误清单里", code)
		}
	}
	if agent_eval.IsCriticalCode("ORPHAN_NODE") || agent_eval.IsCriticalCode("FRAME_ORDER") {
		t.Error("warning 级错误码不该算关键错误")
	}
}

// TestAgentEvalScoreEmptyGraphZero 空图（normalized 为空）硬零。
func TestAgentEvalScoreEmptyGraphZero(t *testing.T) {
	c, _ := agent_eval.CaseByID("c01")
	plan := testPlan(nil, 0)
	plan.Validate.OK = false
	plan.Validate.Errors = []service.AgentIssue{{Code: "BAD_GRAPH", Severity: "error", Message: "空图"}}
	if got := agent_eval.Structure(c, plan); got != 0 {
		t.Errorf("空图必须硬零，got %.4f", got)
	}
	// 即使校验器说 ok，只要 normalized 为空也不给结构分（反作弊）
	plan.Validate.OK = true
	plan.Validate.Errors = nil
	if got := agent_eval.Structure(c, plan); got != 0 {
		t.Errorf("normalized 为空必须硬零，got %.4f", got)
	}
	if agent_eval.Structure(c, nil) != 0 {
		t.Error("plan 为 nil 时必须 0 分")
	}
}

// TestAgentEvalScoreAllowNoChange 纯提问用例走契约 4.3 的例外：
// changes 为空且 reply 非空 → SchemaOk=true 且 Structure=1；给了 changes → 0 分。
func TestAgentEvalScoreAllowNoChange(t *testing.T) {
	c, _ := agent_eval.CaseByID("c11")
	plan := testNoChangePlan("主镜头缺上游图片，补一张图连上即可。按你说的没有改图。")
	if !agent_eval.SchemaOk(c, plan) {
		t.Error("AllowNoChange 用例：changes 空 + reply 非空 → SchemaOk 必须为 true")
	}
	if got := agent_eval.Structure(c, plan); got != 1 {
		t.Errorf("AllowNoChange 用例：Structure 必须为 1，got %.4f", got)
	}
	// 模型不该改图却改了 → 0 分
	plan.Changes = []service.AssistChange{{Action: "update_prompt", NodeId: "video-3"}}
	if agent_eval.Structure(c, plan) != 0 {
		t.Error("AllowNoChange 用例给了 changes → Structure 必须为 0")
	}
	// 空回复不算通过
	empty := testNoChangePlan("   ")
	if agent_eval.SchemaOk(c, empty) {
		t.Error("AllowNoChange 用例：reply 为空时不该算通过")
	}
}

// TestAgentEvalScoreBudgetHalfNotZero 超预算打对折，不是硬零（契约 10.2 冻结的修法）。
func TestAgentEvalScoreBudgetHalfNotZero(t *testing.T) {
	c, _ := agent_eval.CaseByID("c12")
	if c.Expect.MaxPoints <= 0 {
		t.Fatal("c12 必须带 MaxPoints")
	}
	graph := testGraph(
		testNode("n1", "image", map[string]any{"prompt": "产品图", "model": "gpt-image-1"}),
		testNode("n2", "video", map[string]any{"prompt": "视频", "model": "kling-v1",
			"videoMode": "i2v", "params": map[string]any{"duration": 10}}),
	)
	edge := []service.WorkflowEdge{{Source: "n1", Target: "n2"}}
	graph.Edges = edge

	// 不超预算 → 满分
	cheap := testPlan(graph, c.Expect.MaxPoints)
	if got := agent_eval.Structure(c, cheap); got != 1 {
		t.Fatalf("不超预算时应满分，got %.4f", got)
	}
	// 超预算 → 恰好一半，且不为 0
	pricy := testPlan(graph, c.Expect.MaxPoints+1)
	got := agent_eval.Structure(c, pricy)
	if got == 0 {
		t.Fatal("超预算必须是打对折而不是硬零")
	}
	if diff := got - 0.5; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("超预算应得 0.5×，got %.6f", got)
	}
}

// TestAgentEvalMissingModelZeroesCase 模型清单少一类模型时，相关用例必须被判关键错误 → 0 分。
// （live 跑法最容易踩的坑：AGENT_EVAL_MODELS 里漏了 audio，带配音的用例全变 0 分。）
func TestAgentEvalMissingModelZeroesCase(t *testing.T) {
	hints := agent_eval.NewMockCatalog().All()
	delete(hints, "audio")
	partial := agent_eval.NewCatalogFromHints(hints)
	if partial == nil {
		t.Fatal("构造清单失败")
	}
	c, _ := agent_eval.CaseByID("c02") // 人物口播：带 audio 节点
	in := agent_eval.NewLoopInput(c, partial, "gpt-4o-mini", 0)
	outcome := agent_eval.RunCase(context.Background(), c, agent_eval.NewMockChatterForCase(c), in,
		agent_eval.ScoreOptions{Catalog: partial})
	if outcome.Plan == nil {
		t.Fatal("降级路径也要留下最后一轮的计划")
	}
	if !hasIssue(outcome.Plan.Validate.Errors, "MODEL_NOT_FOUND") {
		t.Errorf("清单里没有 audio 模型时应报 MODEL_NOT_FOUND，got %s", dumpIssues(outcome.Plan.Validate.Errors))
	}
	if outcome.Result.SchemaOk || outcome.Result.Structure != 0 {
		t.Errorf("关键错误必须归零：schemaOk=%v structure=%.4f", outcome.Result.SchemaOk, outcome.Result.Structure)
	}
}

// TestAgentEvalScoreKindHitRate 只命中一半 kind → 手算值。
func TestAgentEvalScoreKindHitRate(t *testing.T) {
	c := agent_eval.Case{
		Id:    "x-half",
		Brief: "手算用例",
		Mode:  "generate",
		Expect: agent_eval.Expect{
			Kinds:     []string{"text", "image", "video", "audio"},
			MinNodes:  2,
			MaxNodes:  4,
			MustEdges: [][2]string{{"text", "image"}},
		},
	}
	// 期望 4 个 kind，实际只有 text/image → 0.5；节点数 2∈[2,4]；连线 1/1；无首帧要求
	graph := testGraph(
		testNode("n1", "text", map[string]any{"content": "文案"}),
		testNode("n2", "image", map[string]any{"prompt": "图", "model": "gpt-image-1"}),
	)
	graph.Edges = []service.WorkflowEdge{{Source: "n1", Target: "n2"}}
	// 0.40*0.5 + 0.20 + 0.25 + 0.15 = 0.80
	if got := agent_eval.Structure(c, testPlan(graph, 0)); got != 0.80 {
		t.Errorf("kind 命中一半应得 0.80，got %.6f", got)
	}
}

// TestAgentEvalScoreNodeRange 节点数在区间外按到区间的距离衰减。
func TestAgentEvalScoreNodeRange(t *testing.T) {
	base := agent_eval.Expect{Kinds: []string{"text"}, MinNodes: 4, MaxNodes: 4}
	c := agent_eval.Case{Id: "x-range", Brief: "手算用例", Mode: "generate", Expect: base}
	// 3 个节点 < Min4 → n/Min = 0.75；kinds 1/1；无连线期望；无首帧要求
	// 0.40 + 0.20*0.75 + 0.25 + 0.15 = 0.95
	graph := testGraph(
		testNode("n1", "text", map[string]any{"content": "a"}),
		testNode("n2", "text", map[string]any{"content": "b"}),
		testNode("n3", "text", map[string]any{"content": "c"}),
	)
	if got := agent_eval.Structure(c, testPlan(graph, 0)); got != 0.95 {
		t.Errorf("节点数不足应得 0.95，got %.6f", got)
	}
	// 5 个节点 > Max4 → Max/n = 0.8 → 0.40 + 0.16 + 0.25 + 0.15 = 0.96
	graph2 := testGraph(
		testNode("n1", "text", map[string]any{"content": "a"}),
		testNode("n2", "text", map[string]any{"content": "b"}),
		testNode("n3", "text", map[string]any{"content": "c"}),
		testNode("n4", "text", map[string]any{"content": "d"}),
		testNode("n5", "text", map[string]any{"content": "e"}),
	)
	if got := agent_eval.Structure(c, testPlan(graph2, 0)); got != 0.96 {
		t.Errorf("节点数超上限应得 0.96，got %.6f", got)
	}
}

// TestAgentEvalWeights 结构轴四项权重和恒为 1.0（冻结口径）。
func TestAgentEvalWeights(t *testing.T) {
	if sum := agent_eval.WeightSum(); sum < 1-1e-9 || sum > 1+1e-9 {
		t.Fatalf("四项权重之和必须为 1.0，got %.6f", sum)
	}
}

// TestAgentEvalRoundLimits 轮数上限不得越过硬上限（契约 2.1 / 2.6），
// 且评测台的缺省值必须与 setting 注释里的缺省值一致（评测台刻意不调 setting 读取函数，
// 因为 GetAgentMaxRounds → model.GetSetting 会查库，而安全红线是不碰数据库）。
func TestAgentEvalRoundLimits(t *testing.T) {
	if setting.AgentMaxRoundsHardLimit != 8 {
		t.Errorf("setting 的轮数硬上限必须是 8，got %d", setting.AgentMaxRoundsHardLimit)
	}
	if service.AgentMaxRoundsHardLimit != setting.AgentMaxRoundsHardLimit {
		t.Errorf("service 与 setting 的硬上限必须同值：%d vs %d",
			service.AgentMaxRoundsHardLimit, setting.AgentMaxRoundsHardLimit)
	}
	if agent_eval.DefaultMaxRoundsGenerate != 6 || agent_eval.DefaultMaxRoundsPatch != 4 {
		t.Errorf("评测台缺省轮数应为 6/4（setting 缺省值），got %d/%d",
			agent_eval.DefaultMaxRoundsGenerate, agent_eval.DefaultMaxRoundsPatch)
	}
	if agent_eval.DefaultMaxRoundsGenerate > setting.AgentMaxRoundsHardLimit ||
		agent_eval.DefaultMaxRoundsPatch > setting.AgentMaxRoundsHardLimit {
		t.Error("评测台缺省轮数不能超过硬上限")
	}
	if agent_eval.DefaultMaxNodes != 60 || agent_eval.DefaultMaxPoints != 0 {
		t.Errorf("评测台缺省节点数 60、估价上限 0（不限），got %d/%d",
			agent_eval.DefaultMaxNodes, agent_eval.DefaultMaxPoints)
	}
}

// TestAgentEvalExecutionAxis 可执行轴：默认 skipped、开启后纯静态判定、字符串格式冻结。
func TestAgentEvalExecutionAxis(t *testing.T) {
	catalog := agent_eval.NewMockCatalog()
	c, _ := agent_eval.CaseByID("c01")
	graph := testGraph(
		testNode("n1", "text", map[string]any{"content": "文案"}),
		testNode("n2", "image", map[string]any{"prompt": "图", "model": "gpt-image-1",
			"params": map[string]any{"ratio": "9:16"}}),
		testNode("n3", "video", map[string]any{"prompt": "视频", "model": "kling-v1", "videoMode": "i2v",
			"params": map[string]any{"duration": 5}}),
	)
	graph.Edges = []service.WorkflowEdge{{Source: "n1", Target: "n2"}, {Source: "n2", Target: "n3"}}
	ok := testPlan(graph, 68)

	if got := agent_eval.ExecutionAxis(c, ok, catalog, false); got != agent_eval.ExecSkipped {
		t.Errorf("默认必须 skipped，got %q", got)
	}
	if got := agent_eval.ExecutionAxis(c, ok, catalog, true); got != agent_eval.ExecPassed {
		t.Errorf("合法图应静态通过，got %q", got)
	}
	if within, msg := agent_eval.DurationWithin(c, ok); !within {
		t.Errorf("duration 5 在用例上限 5 之内，got %s", msg)
	}

	// duration 不在模型档位（kling-v1 只有 5/10）
	bad := testGraph(
		testNode("n2", "image", map[string]any{"prompt": "图", "model": "gpt-image-1"}),
		testNode("n3", "video", map[string]any{"prompt": "视频", "model": "kling-v1", "videoMode": "i2v",
			"params": map[string]any{"duration": 20}}),
	)
	bad.Edges = []service.WorkflowEdge{{Source: "n2", Target: "n3"}}
	want := "failed（节点 n3：duration 20 不在 kling-v1 的 5/10）"
	if got := agent_eval.ExecutionAxis(c, testPlan(bad, 0), catalog, true); got != want {
		t.Errorf("失败文案要冻结：\n got %q\nwant %q", got, want)
	}

	// 用例级时长上限：10 秒视频在 c01（上限 5）上应被拦下
	over := testGraph(
		testNode("n1", "image", map[string]any{"prompt": "图", "model": "gpt-image-1"}),
		testNode("n2", "video", map[string]any{"prompt": "视频", "model": "kling-v1", "videoMode": "i2v",
			"params": map[string]any{"duration": 10}}),
	)
	over.Edges = []service.WorkflowEdge{{Source: "n1", Target: "n2"}}
	if within, msg := agent_eval.DurationWithin(c, testPlan(over, 0)); within {
		t.Error("10 秒视频超出 c01 的 5 秒上限，应判不通过")
	} else if !strings.Contains(msg, "超过用例上限 5") {
		t.Errorf("失败原因要说清上限，got %s", msg)
	}

	// i2v 没有上游图
	noFrame := testGraph(
		testNode("n1", "text", map[string]any{"content": "文案"}),
		testNode("n2", "video", map[string]any{"prompt": "视频", "model": "kling-v1", "videoMode": "i2v",
			"params": map[string]any{"duration": 5}}),
	)
	noFrame.Edges = []service.WorkflowEdge{{Source: "n1", Target: "n2"}}
	if got := agent_eval.ExecutionAxis(c, testPlan(noFrame, 0), catalog, true); !strings.Contains(got, "没有上游图片节点") {
		t.Errorf("i2v 缺上游图应判失败，got %q", got)
	}
}

// TestAgentEvalRepairedFlag 第一轮出错、第二轮修好 → Repaired=true 且轮数为 2。
func TestAgentEvalRepairedFlag(t *testing.T) {
	c, _ := agent_eval.CaseByID("c01")
	catalog := agent_eval.NewMockCatalog()
	bad := `{"reply":"先给一版","changes":[{"action":"apply_graph","graph":{"version":1,"nodes":[{"id":"n1","kind":"character","x":0,"y":0,"data":{"label":"角色"}}],"edges":[]}}]}`
	good, ok := agent_eval.MockReplyForCase(c)
	if !ok {
		t.Fatal("c01 没有 mock 回复")
	}
	chatter := agent_eval.NewMockChatter(bad, good)
	in := agent_eval.NewLoopInput(c, catalog, "gpt-4o-mini", 0)
	outcome := agent_eval.RunCase(context.Background(), c, chatter, in, agent_eval.ScoreOptions{Catalog: catalog})
	if outcome.Err != nil {
		t.Fatalf("循环报错：%v", outcome.Err)
	}
	if !outcome.Result.Repaired {
		t.Errorf("首轮失败、次轮修好 → Repaired 应为 true（rounds=%d）", outcome.Result.Rounds)
	}
	if outcome.Result.Rounds != 2 {
		t.Errorf("应该用 2 轮，got %d", outcome.Result.Rounds)
	}
	if !outcome.Result.SchemaOk || outcome.Result.Structure != 1 {
		t.Errorf("修复后应满分，got schemaOk=%v structure=%.4f", outcome.Result.SchemaOk, outcome.Result.Structure)
	}
	if len(chatter.Calls()) < 2 {
		t.Error("应该至少调了两次模型（第一轮失败后回灌重试）")
	}
	// 原始输出要按轮次留痕，供 golang-report/agent-eval-raw 落盘
	if len(outcome.Raw) != 2 || outcome.Raw[0].Raw != bad {
		t.Errorf("原始输出应逐轮保留，got %+v", outcome.Raw)
	}
}

// TestAgentEvalLiveGate live 门禁只读环境变量与 .env 文本（不连库、不补齐配置）。
func TestAgentEvalLiveGate(t *testing.T) {
	none := func(string) string { return "" }

	// 1) 什么都没有 → 五项全缺，且绝不能因此判成可跑
	cfg := agent_eval.DetectLive(none, nil)
	if cfg.Live {
		t.Fatal("没有任何配置时必须 live=false")
	}
	for _, want := range []string{
		agent_eval.MissingLiveSwitch, agent_eval.MissingBaseURL, agent_eval.MissingGatewayKey,
		agent_eval.MissingModels, agent_eval.MissingBudget,
	} {
		if !strings.Contains(cfg.Reason(), want) {
			t.Errorf("reason 缺少 %q，got %q", want, cfg.Reason())
		}
	}

	// 2) 与仓库现状一致：.env 有网关地址，EASYAPI_KEY 是空值 → 只缺 Key/清单/预算/开关
	cfg = agent_eval.DetectLive(none, map[string]string{
		"EASYAPI_BASE_URL": "https://gw.example.com",
		"EASYAPI_KEY":      "",
	})
	if strings.Contains(cfg.Reason(), agent_eval.MissingBaseURL) {
		t.Errorf(".env 里有网关地址时不该报缺地址，got %q", cfg.Reason())
	}
	if !strings.Contains(cfg.Reason(), agent_eval.MissingGatewayKey) {
		t.Errorf("空 Key 必须报缺网关 Key，got %q", cfg.Reason())
	}

	// 3) 四项齐备 → live=true，模型清单解析出目录，规划模型取 text 清单第一个
	env := map[string]string{
		agent_eval.EnvLive:    "1",
		agent_eval.EnvBaseURL: "https://gw.example.com",
		agent_eval.EnvModels: `{"text":[{"model":"gpt-4o-mini","type":"text","unit":"count","points":1}],` +
			`"image":[{"model":"gpt-image-1","type":"image","unit":"image","points":8}],` +
			`"video":[{"model":"kling-v1","type":"video","unit":"second","points":12,"durations":[5,10]}]}`,
		agent_eval.EnvBudget: "500",
	}
	cfg = agent_eval.DetectLive(func(k string) string { return env[k] }, map[string]string{agent_eval.EnvEasyAPIKey: "sk-from-dotenv"})
	if !cfg.Live {
		t.Fatalf("四项齐备时必须 live=true，reason=%q", cfg.Reason())
	}
	if cfg.APIKey != "sk-from-dotenv" {
		t.Errorf(".env 的 Key 要能被读到，got %q", cfg.APIKey)
	}
	if cfg.Catalog == nil || cfg.Model != "gpt-4o-mini" {
		t.Errorf("模型清单没装好：catalog=%v model=%q", cfg.Catalog != nil, cfg.Model)
	}
	if cfg.Budget != 500 || cfg.RoundsFor("generate") != agent_eval.DefaultMaxRoundsGenerate {
		t.Errorf("预算/轮数缺省不对：budget=%d rounds=%d", cfg.Budget, cfg.RoundsFor("generate"))
	}
	// 模型清单里没有 text 模型 → 仍然不可跑
	env[agent_eval.EnvModels] = `{"image":[{"model":"gpt-image-1","type":"image","unit":"image","points":8}]}`
	if cfg2 := agent_eval.DetectLive(func(k string) string { return env[k] }, nil); cfg2.Live {
		t.Error("清单里没有 text 模型时不该判成可跑")
	}
}

// TestAgentEvalReportRoundTrip 落盘格式：目录不存在要自己建、字段严格、原始输出按轮留痕。
func TestAgentEvalReportRoundTrip(t *testing.T) {
	root := t.TempDir() // 模拟仓库根：这里还没有 golang-report/
	results := []agent_eval.CaseResult{{
		Id: "c01", Brief: "竖屏短视频", SchemaOk: true, Structure: 1,
		Execution: agent_eval.ExecSkipped, Rounds: 1, Repaired: false,
	}}
	summary := agent_eval.NewSummary(false, "mock 自测", results)
	path, err := agent_eval.WriteSummary(root, summary)
	if err != nil {
		t.Fatalf("写结果失败：%v", err)
	}
	want := filepath.Join(root, "golang-report", "agent-eval.json")
	if path != want {
		t.Fatalf("结果路径应为 %s，got %s", want, path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回结果失败：%v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("结果不是合法 JSON：%v", err)
	}
	// 字段必须严格是 live / reason / cases / stats（工作流按这四个键读）
	if len(decoded) != 4 {
		t.Errorf("顶层字段应为 live/reason/cases/stats 四个，got %v", keysOf(decoded))
	}
	for _, key := range []string{"live", "reason", "cases", "stats"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("结果缺少字段 %s（got %v）", key, keysOf(decoded))
		}
	}
	if _, ok := decoded["stats"].(string); !ok {
		t.Error("stats 必须是字符串（工作流按 string 读）")
	}
	cases, ok := decoded["cases"].([]any)
	if !ok || len(cases) != 1 {
		t.Fatalf("cases 必须是数组，got %T", decoded["cases"])
	}
	item, _ := cases[0].(map[string]any)
	for _, key := range []string{"id", "brief", "schemaOk", "structure", "execution", "rounds", "repaired"} {
		if _, ok := item[key]; !ok {
			t.Errorf("用例结果缺少字段 %s（got %v）", key, keysOf(item))
		}
	}

	// 原始模型输出
	rawPath, err := agent_eval.WriteRaw(root, "c01", []agent_eval.RoundOutput{
		{Round: 1, Raw: `{"reply":"第一轮"}`},
		{Round: 2, Raw: `{"reply":"第二轮"}`, Err: "网关超时"},
	})
	if err != nil {
		t.Fatalf("写原始输出失败：%v", err)
	}
	if rawPath != filepath.Join(root, "golang-report", "agent-eval-raw", "c01.txt") {
		t.Fatalf("原始输出路径不对：%s", rawPath)
	}
	rawBody, _ := os.ReadFile(rawPath)
	text := string(rawBody)
	for _, want := range []string{"--- round 1 ---", `{"reply":"第一轮"}`, "--- round 2 ---", "ERROR: 网关超时"} {
		if !strings.Contains(text, want) {
			t.Errorf("原始输出缺少 %q：\n%s", want, text)
		}
	}

	// 找不到仓库根时不能 panic，只能报错
	if _, err := agent_eval.WriteSummary("", summary); err == nil {
		t.Error("root 为空时应返回错误（由调用方写进 reason），而不是静默成功")
	}
}

// ===== 测试辅助 =====

func testNode(id, kind string, data map[string]any) service.WorkflowNode {
	return service.WorkflowNode{Id: id, Kind: kind, X: 0, Y: 0, Data: data}
}

func testGraph(nodes ...service.WorkflowNode) *service.CanvasWorkflowGraph {
	return &service.CanvasWorkflowGraph{Version: 1, Nodes: nodes, Edges: []service.WorkflowEdge{}}
}

// testPlan 造一个「已经通过校验」的计划，供纯打分测试用（不经过解析与编译）。
func testPlan(graph *service.CanvasWorkflowGraph, points int) *service.AgentPlan {
	return &service.AgentPlan{
		TaskId:  1,
		Status:  "succeeded",
		Reply:   "已按需求搭好",
		Changes: []service.AssistChange{{Action: "apply_graph", Graph: graph}},
		Validate: service.AgentValidateReport{
			OK:         true,
			Errors:     []service.AgentIssue{},
			Warnings:   []service.AgentIssue{},
			Estimate:   service.AgentEstimate{Points: points},
			Normalized: graph,
		},
		Estimate: service.AgentEstimate{Points: points},
		Round:    1,
	}
}

// testNoChangePlan 造一个「什么都没改」的计划（纯提问用例的正确答案）。
func testNoChangePlan(reply string) *service.AgentPlan {
	return &service.AgentPlan{
		TaskId:   1,
		Status:   "succeeded",
		Reply:    reply,
		Changes:  []service.AssistChange{},
		Validate: service.AgentValidateReport{OK: true, Errors: []service.AgentIssue{}, Warnings: []service.AgentIssue{}},
		Round:    1,
	}
}

func dumpIssues(issues []service.AgentIssue) string {
	if len(issues) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		parts = append(parts, issue.Code+"("+issue.NodeId+")")
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
