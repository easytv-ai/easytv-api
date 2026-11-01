package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"easytv-api/model"
	"easytv-api/setting"
)

// ===== 多轮循环：四条终止路径 + 工具回灌 + 解析重试（agent-contract 5.1 / 5.2 / 验收 8）=====

type agentScriptChatter struct {
	replies []string
	delay   time.Duration
	mu      sync.Mutex
	prompts []string
	opts    []ChatParams
}

func (c *agentScriptChatter) Chat(modelName, prompt string) (string, error) {
	c.mu.Lock()
	c.prompts = append(c.prompts, prompt)
	i := len(c.prompts) - 1
	c.mu.Unlock()
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	if len(c.replies) == 0 {
		return "", errors.New("脚本没准备回复")
	}
	if i < len(c.replies) {
		return c.replies[i], nil
	}
	return c.replies[len(c.replies)-1], nil
}

// ChatWithOptions 适配冻结的 AgentChatter 接口；opts 记下来供对话参数用例断言。
func (c *agentScriptChatter) ChatWithOptions(modelName, prompt string, opts ChatParams) (string, error) {
	c.mu.Lock()
	c.opts = append(c.opts, opts)
	c.mu.Unlock()
	return c.Chat(modelName, prompt)
}

func (c *agentScriptChatter) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.prompts)
}

func (c *agentScriptChatter) promptAt(i int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i < 0 || i >= len(c.prompts) {
		return ""
	}
	return c.prompts[i]
}

func (c *agentScriptChatter) optsAt(i int) ChatParams {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i < 0 || i >= len(c.opts) {
		return ChatParams{}
	}
	return c.opts[i]
}

type recordingSink struct{ steps []AgentStepRecord }

func (s *recordingSink) Step(r AgentStepRecord) { s.steps = append(s.steps, r) }

func (s *recordingSink) findByTool(tool string) *AgentStepRecord {
	for i := range s.steps {
		if s.steps[i].Tool == tool {
			return &s.steps[i]
		}
	}
	return nil
}

// 每一轮都产出一张「模型没接入」的图：error 级问题、签名稳定。
const agentFailingPlanReply = `{"reply":"搭图","changes":[{"action":"apply_graph","graph":{"version":1,"nodes":[` +
	`{"id":"n1","kind":"image","x":0,"y":0,"data":{"label":"图","prompt":"橘猫","model":"not-a-model"}}],"edges":[]}}]}`

const agentGoodPlanReply = `{"reply":"搭好了","changes":[{"action":"apply_graph","graph":{"version":1,"nodes":[` +
	`{"id":"n1","kind":"text","x":0,"y":0,"data":{"label":"脚本","content":"橘猫的十五秒"}},` +
	`{"id":"n2","kind":"image","x":400,"y":0,"data":{"label":"首帧","prompt":"橘猫","model":"gpt-image-1"}}],` +
	`"edges":[{"source":"n1","target":"n2"}]}}]}`

const agentToolReply = `{"thought":"先看看有哪些模型","tool":"search_models","args":{"type":"image"}}`

func agentLoopInput(chatter AgentChatter) AgentLoopInput {
	return AgentLoopInput{
		UserId: 8, Model: "gpt-4o-mini", Question: "做一条橘猫广告", Mode: "generate",
		MaxRounds: 3, Catalog: agentTestCatalog(), Pricer: &fakeAssistPricer{available: map[string]int{"gpt-image-1": 20}},
		Nodes: []AssistNode{{Id: "n1", Kind: "text", Label: "脚本"}},
	}
}

func agentRunLoop(t *testing.T, chatter AgentChatter, in AgentLoopInput, sink AgentStepSink) *AgentLoopResult {
	t.Helper()
	res := RunAgentRounds(context.Background(), chatter, in, sink)
	if res == nil {
		t.Fatal("RunAgentRounds 返回 nil")
	}
	return res
}

// TestAgentLoopStopsAtMaxRounds 终止路径一：轮数上限。
func TestAgentLoopStopsAtMaxRounds(t *testing.T) {
	chatter := &agentScriptChatter{replies: []string{agentFailingPlanReply}}
	in := agentLoopInput(chatter)
	in.MaxRounds = 2
	res := agentRunLoop(t, chatter, in, NoopAgentStepSink{})
	if chatter.calls() != 2 || res.RoundsUsed != 2 {
		t.Fatalf("calls=%d RoundsUsed=%d", chatter.calls(), res.RoundsUsed)
	}
	if res.Plan == nil || res.Plan.Validate.OK {
		t.Fatalf("轮数用尽应降级返回最后一版计划：%+v", res.Plan)
	}
	if res.Err != nil {
		t.Fatalf("有产出就不该是硬错误：%v", res.Err)
	}
}

// TestAgentLoopStopsOnNoProgress 终止路径二：连续两轮错误签名完全不变。
// 第 3 轮签名与第 2 轮相同 → noProgress=2 → 停（不等轮数上限）。
func TestAgentLoopStopsOnNoProgress(t *testing.T) {
	chatter := &agentScriptChatter{replies: []string{agentFailingPlanReply}}
	in := agentLoopInput(chatter)
	in.MaxRounds = 6
	res := agentRunLoop(t, chatter, in, NoopAgentStepSink{})
	if chatter.calls() != 3 || res.RoundsUsed != 3 {
		t.Fatalf("应在第 3 轮判无进展停下：calls=%d RoundsUsed=%d", chatter.calls(), res.RoundsUsed)
	}
	if res.Plan == nil || res.Err != nil {
		t.Fatalf("无进展也应返回降级计划：plan=%v err=%v", res.Plan, res.Err)
	}
}

// TestAgentLoopStopsOnTotalTimeout 终止路径三：总超时（注入时钟）。
func TestAgentLoopStopsOnTotalTimeout(t *testing.T) {
	chatter := &agentScriptChatter{replies: []string{agentFailingPlanReply}}
	in := agentLoopInput(chatter)
	in.MaxRounds = 6
	in.TotalTimeout = 250 * time.Second
	base := time.Unix(1000, 0)
	calls := 0
	in.Now = func() time.Time {
		calls++
		return base.Add(time.Duration(calls-1) * 100 * time.Second)
	}
	res := agentRunLoop(t, chatter, in, NoopAgentStepSink{})
	if chatter.calls() != 2 || res.RoundsUsed != 2 {
		t.Fatalf("总超时应停在第 3 轮检查：calls=%d RoundsUsed=%d", chatter.calls(), res.RoundsUsed)
	}
	if res.Plan == nil {
		t.Fatal("超时也应返回降级计划")
	}
}

// TestAgentLoopStopsWhenCancelled 终止路径四：任务被取消（第 2 轮开始前返回 true）。
func TestAgentLoopStopsWhenCancelled(t *testing.T) {
	chatter := &agentScriptChatter{replies: []string{agentToolReply, agentGoodPlanReply}}
	in := agentLoopInput(chatter)
	in.MaxRounds = 6
	checks := 0
	in.Cancelled = func() bool {
		checks++
		return checks > 1 // 第 1 轮放行，第 2 轮开始前说已取消
	}
	res := agentRunLoop(t, chatter, in, NoopAgentStepSink{})
	if chatter.calls() != 1 || res.RoundsUsed != 1 {
		t.Fatalf("取消后不该再调模型：calls=%d RoundsUsed=%d", chatter.calls(), res.RoundsUsed)
	}
	if res.Plan != nil {
		t.Fatalf("取消时不该产出计划：%+v", res.Plan)
	}
	if res.Err != nil {
		t.Fatalf("取消不是硬错误（RunAgentLoop 据此不动任务状态与积分）：%v", res.Err)
	}
}

// TestAgentLoopParseRetryPerRound 每一轮解析失败都能重试一次（重试不额外计轮、不额外计费）。
func TestAgentLoopParseRetryPerRound(t *testing.T) {
	chatter := &agentScriptChatter{replies: []string{"我觉得可以这样改", "再想想", "还是不给你 JSON"}}
	in := agentLoopInput(chatter)
	in.MaxRounds = 2
	res := agentRunLoop(t, chatter, in, NoopAgentStepSink{})
	if chatter.calls() != 4 {
		t.Fatalf("两轮各重试一次 → 应调 4 次，实际 %d", chatter.calls())
	}
	if res.RoundsUsed != 2 {
		t.Fatalf("重试不额外计轮：RoundsUsed=%d", res.RoundsUsed)
	}
	if res.Err == nil || res.Plan != nil {
		t.Fatalf("一次都没解析出 JSON 才是硬错误：err=%v plan=%v", res.Err, res.Plan)
	}
}

// TestAgentLoopToolFeedback 工具轮消耗一轮，结果按 [工具 x] 回灌进下一轮提示词。
func TestAgentLoopToolFeedback(t *testing.T) {
	chatter := &agentScriptChatter{replies: []string{agentToolReply, agentGoodPlanReply}}
	in := agentLoopInput(chatter)
	sink := &recordingSink{}
	res := agentRunLoop(t, chatter, in, sink)
	if res.Plan == nil || !res.Plan.Validate.OK {
		t.Fatalf("第二轮应拿到通过的计划：%+v err=%v", res.Plan, res.Err)
	}
	if res.RoundsUsed != 2 || chatter.calls() != 2 {
		t.Fatalf("工具轮消耗一轮：calls=%d RoundsUsed=%d", chatter.calls(), res.RoundsUsed)
	}
	if res.Repaired {
		t.Fatal("首轮没有 ok=false 的计划，不该算修复轮")
	}
	if step := sink.findByTool("search_models"); step == nil || !step.OK || step.Round != 1 {
		t.Fatalf("工具步留痕不对：%+v", step)
	}
	if !strings.Contains(chatter.promptAt(1), "[工具 search_models]") {
		t.Fatalf("第二轮提示词没带上工具结果：\n%s", chatter.promptAt(1))
	}
	if !strings.Contains(chatter.promptAt(1), "gpt-image-1") {
		t.Fatal("提示词应带上模型清单")
	}
}

// TestAgentLoopRepaired 首轮 ok=false、最终 ok=true → Repaired。
func TestAgentLoopRepaired(t *testing.T) {
	chatter := &agentScriptChatter{replies: []string{agentFailingPlanReply, agentGoodPlanReply}}
	in := agentLoopInput(chatter)
	in.MaxRounds = 4
	sink := &recordingSink{}
	res := agentRunLoop(t, chatter, in, sink)
	if res.Plan == nil || !res.Plan.Validate.OK || !res.Repaired {
		t.Fatalf("应靠修复轮通过：plan=%+v repaired=%v", res.Plan, res.Repaired)
	}
	if res.RoundsUsed != 2 {
		t.Fatalf("RoundsUsed = %d", res.RoundsUsed)
	}
	// 上一轮的错误要回灌给模型
	if !strings.Contains(chatter.promptAt(1), "MODEL_NOT_FOUND") {
		t.Fatalf("第二轮提示词没带上上一轮错误：\n%s", chatter.promptAt(1))
	}
	if res.Plan.Question != in.Question {
		t.Fatalf("计划应带上原始问题：%q", res.Plan.Question)
	}
}

// TestAgentLoopRoundTimeout 单轮超时：本轮记失败、进下一轮（上限仍受 MaxRounds 约束）。
func TestAgentLoopRoundTimeout(t *testing.T) {
	chatter := &agentScriptChatter{replies: []string{agentGoodPlanReply}, delay: 300 * time.Millisecond}
	in := agentLoopInput(chatter)
	in.MaxRounds = 2
	in.RoundTimeout = 20 * time.Millisecond
	sink := &recordingSink{}
	res := agentRunLoop(t, chatter, in, sink)
	if res.RoundsUsed != 2 || chatter.calls() != 2 {
		t.Fatalf("超时也要走完轮数：calls=%d RoundsUsed=%d", chatter.calls(), res.RoundsUsed)
	}
	if res.Plan != nil || res.Err == nil {
		t.Fatalf("两轮都超时 → 硬错误：plan=%v err=%v", res.Plan, res.Err)
	}
	if step := sink.findByTool("chat"); step == nil || step.OK {
		t.Fatalf("超时必须留下失败步：%+v", step)
	}
}

// TestAgentLoopMaxPointsNotInjected 打分层字段不许注入 MaxPoints：传 0 时超贵方案也不该被 OVER_BUDGET 丢掉。
func TestAgentLoopMaxPointsNotInjected(t *testing.T) {
	chatter := &agentScriptChatter{replies: []string{agentGoodPlanReply}}
	in := agentLoopInput(chatter)
	in.MaxRounds = 1
	in.MaxPoints = 0
	res := agentRunLoop(t, chatter, in, NoopAgentStepSink{})
	if res.Plan == nil || !res.Plan.Validate.OK {
		t.Fatalf("MaxPoints=0 时不该判超预算：%+v", res.Plan)
	}
	in.MaxPoints = 1
	chatter2 := &agentScriptChatter{replies: []string{agentGoodPlanReply}}
	in2 := agentLoopInput(chatter2)
	in2.MaxRounds = 1
	in2.MaxPoints = 1
	res2 := agentRunLoop(t, chatter2, in2, NoopAgentStepSink{})
	if res2.Plan == nil || !agentHasCode(res2.Plan.Validate.Errors, AgentCodeOverBudget) {
		t.Fatalf("MaxPoints=1 时应判 OVER_BUDGET：%+v", res2.Plan)
	}
}

// TestIsAgentTask 判定入口（7.2 的判空路径）。
func TestIsAgentTask(t *testing.T) {
	agentParams := `{"agent":{"question":"做个片子","mode":"generate","max_rounds":6}}`
	cases := []struct {
		name string
		task *model.GenerationTask
		want bool
	}{
		{"正常", &model.GenerationTask{Type: model.TaskTypeText, Params: agentParams}, true},
		{"nil 任务", nil, false},
		{"非文本任务", &model.GenerationTask{Type: model.TaskTypeImage, Params: agentParams}, false},
		{"没有 agent 段", &model.GenerationTask{Type: model.TaskTypeText, Params: `{"count":1}`}, false},
		{"params 不是 JSON", &model.GenerationTask{Type: model.TaskTypeText, Params: `not json`}, false},
		{"问题为空", &model.GenerationTask{Type: model.TaskTypeText, Params: `{"agent":{"question":"  ","max_rounds":6}}`}, false},
		{"轮数为 0", &model.GenerationTask{Type: model.TaskTypeText, Params: `{"agent":{"question":"x","max_rounds":0}}`}, false},
		{"轮数超硬上限仍走多轮", &model.GenerationTask{Type: model.TaskTypeText, Params: `{"agent":{"question":"x","max_rounds":99}}`}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAgentTask(tc.task); got != tc.want {
				t.Fatalf("IsAgentTask = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAgentClampRoundsAndMode 轮数夹取与 mode 归一。
func TestAgentClampRoundsAndMode(t *testing.T) {
	if agentClampRounds(0) != 0 || agentClampRounds(-3) != 0 {
		t.Fatal("<=0 表示用 setting 默认值")
	}
	if agentClampRounds(3) != 3 || agentClampRounds(99) != AgentMaxRoundsHardLimit {
		t.Fatal("轮数必须夹到 [1,8]")
	}
	if AgentMaxRoundsHardLimit != 8 {
		t.Fatal("硬上限必须是 8（与 setting.AgentMaxRoundsHardLimit 同值）")
	}
	if normalizeAgentMode("generate") != "generate" || normalizeAgentMode("") != "patch" ||
		normalizeAgentMode("随便") != "patch" || normalizeAgentMode("patch") != "patch" {
		t.Fatal("mode 归一不对")
	}
}

// TestNewAgentTaskRespectsSwitch 运营开关（setting.AgentEnabled）：关掉后不再接受新任务。
func TestNewAgentTaskRespectsSwitch(t *testing.T) {
	model.SetSettingCache(setting.AgentEnabled, "false")
	defer model.SetSettingCache(setting.AgentEnabled, "true")

	cv := &model.Canvas{Id: 5, UserId: 8}
	if _, _, err := NewAgentTask(8, cv, AgentRequest{Question: "做个片子"}); err == nil || !strings.Contains(err.Error(), "暂未开启") {
		t.Fatalf("开关关掉时应拒绝：%v", err)
	}
	if _, _, err := NewAgentRetryTask(8, cv, &model.GenerationTask{Id: 41, UserId: 8, CanvasId: 5, Type: model.TaskTypeText,
		Params: `{"agent":{"question":"做个片子","max_rounds":1}}`}, "", 1); err == nil || !strings.Contains(err.Error(), "暂未开启") {
		t.Fatalf("重试也要走开关：%v", err)
	}

	model.SetSettingCache(setting.AgentEnabled, "true")
	if _, _, err := NewAgentTask(8, cv, AgentRequest{Question: "   "}); err == nil || err.Error() != "请输入问题" {
		t.Fatalf("开关打开后应先报问题为空：%v", err)
	}
}

// TestAgentParamsKeepsCanvasContext 画布上下文必须原样存进 params、再原样读回来：
// 循环每轮都靠这份快照重拼提示词，少存一个字段模型就少看见一截画布。
func TestAgentParamsKeepsCanvasContext(t *testing.T) {
	nodes := []AssistNode{
		{Id: "n1", Kind: "text", Label: "脚本", Content: "15 秒竖屏广告", Downstream: []string{"n2"}},
		{Id: "n2", Kind: "image", Label: "产品首帧", Prompt: "产品特写", HasMedia: true,
			Selected: true, Upstream: []string{"n1"}, Downstream: []string{"n3"}},
		{Id: "n3", Kind: "video", Label: "主镜头", Prompt: "推近", Upstream: []string{"n2"}},
	}
	last := map[string]*assistLastTask{"n2": {Model: "gpt-image-1", Status: "failed", Error: "审核没过"}}

	// 与 NewAgentTask 一样落库，再与 RunAgentLoop 一样读回来
	raw, err := json.Marshal(agentTaskParams{Agent: &AgentParams{
		Question: "做条广告", Mode: "patch", MaxRounds: 4,
		Nodes: agentNodeRefs(nodes, last),
	}})
	if err != nil {
		t.Fatalf("params 序列化失败：%v", err)
	}
	var back agentTaskParams
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("params 反序列化失败：%v", err)
	}
	got, records := agentLoopNodes(back.Agent.Nodes)
	if !reflect.DeepEqual(got, nodes) {
		t.Fatalf("画布上下文往返后变了：\n存前 %+v\n读回 %+v", nodes, got)
	}
	if records["n2"] == nil || records["n2"].Model != "gpt-image-1" || records["n2"].Status != "failed" {
		t.Fatalf("上次生成记录丢了：%+v", records["n2"])
	}

	// 模型真正看到的那段文本：连线拓扑、选区、文本正文、已有产物都得在
	text := AgentCanvasText(got, records)
	for _, want := range []string{"选中 1 个", "★ ", "上游：n1（脚本）", "下游：n3（主镜头）", "文本内容：15 秒竖屏广告", "已有生成结果"} {
		if !strings.Contains(text, want) {
			t.Fatalf("画布上下文缺了 %q：\n%s", want, text)
		}
	}
}

// TestAgentRetryRounds 重试轮数：没指定时按运营配置给，不能兜底成 1
// （只给一轮的话模型一调工具就把轮数用光，循环拿不到计划会直接判任务失败）。
func TestAgentRetryRounds(t *testing.T) {
	model.SetSettingCache(setting.AgentMaxRoundsPatch, "4")
	model.SetSettingCache(setting.AgentMaxRoundsGenerate, "6")
	defer func() {
		model.SetSettingCache(setting.AgentMaxRoundsPatch, "")
		model.SetSettingCache(setting.AgentMaxRoundsGenerate, "")
	}()

	if got := agentRetryRounds("patch", 0); got != 4 {
		t.Fatalf("patch 没指定轮数应按运营配置给 4，得到 %d", got)
	}
	if got := agentRetryRounds("generate", 0); got != 6 {
		t.Fatalf("generate 没指定轮数应按运营配置给 6，得到 %d", got)
	}
	if got := agentRetryRounds("patch", 2); got != 2 {
		t.Fatalf("显式指定的轮数要照用，得到 %d", got)
	}
	if got := agentRetryRounds("patch", 99); got != AgentMaxRoundsHardLimit {
		t.Fatalf("显式指定也要夹到硬上限，得到 %d", got)
	}
}
