package agent_eval

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"easytv-api/service"
)

// ===== 打分口径（契约 10.2，权重与判定全部冻结）=====

// 结构轴四项权重，和恒为 1.0（有自测断言这一点）。
const (
	WeightKinds = 0.40 // 期望 kind 集合命中率
	WeightNodes = 0.20 // 节点数是否落在区间
	WeightEdges = 0.25 // 必需连线命中率
	WeightFrame = 0.15 // 首帧（video/director 有上游 image）
)

// WeightSum 四项权重之和，必须等于 1.0。
func WeightSum() float64 { return WeightKinds + WeightNodes + WeightEdges + WeightFrame }

// 评测用的循环缺省值。**刻意不调 setting.GetAgent*()**：那些读取函数走 DB 背后的运营配置，
// 而本评测台的安全红线是不碰数据库（契约 0.2）。这里的值与 setting 的缺省值逐字对应（契约 2.1）。
const (
	DefaultMaxRoundsGenerate = 6
	DefaultMaxRoundsPatch    = 4
	DefaultMaxNodes          = 60
	DefaultMaxPoints         = 0 // 打分层字段，绝不注入 Expect.MaxPoints（契约 2.6）
	DefaultRoundTimeout      = 90 * time.Second
	DefaultTotalTimeout      = 480 * time.Second
)

// 可执行轴的三个冻结字符串（契约 10.2）。
const (
	ExecSkipped = "skipped（未开启 AGENT_EVAL_EXECUTE）"
	ExecPassed  = "passed（静态可执行）"
)

// criticalCodes 关键错误：命中任一 → Structure = 0 且 SchemaOk = false（契约 10.2）。
// 防止「图很大但根本跑不了」拿高分。
var criticalCodes = []string{
	"UNKNOWN_KIND",
	"CYCLE",
	"MODEL_NOT_PRICED",
	"MODEL_NOT_FOUND",
	"MODEL_TYPE_MISMATCH",
	"EDGE_DANGLING",
	"MISSING_REQUIRED",
	"VIDEO_NEEDS_FRAME",
	"ASSET_FORBIDDEN",
}

// CriticalCodes 关键错误码清单（副本）。
func CriticalCodes() []string {
	out := make([]string, 0, len(criticalCodes))
	out = append(out, criticalCodes...)
	return out
}

// IsCriticalCode 是不是关键错误码。
func IsCriticalCode(code string) bool {
	for _, c := range criticalCodes {
		if c == code {
			return true
		}
	}
	return false
}

// ===== 结果类型（契约 10.2，字段名冻结：工作流按这些键读 JSON）=====

// CaseResult 一条用例的打分结果。
type CaseResult struct {
	Id        string  `json:"id"`
	Brief     string  `json:"brief"`
	SchemaOk  bool    `json:"schemaOk"`
	Structure float64 `json:"structure"`
	Execution string  `json:"execution"`
	Rounds    int     `json:"rounds"`
	Repaired  bool    `json:"repaired"`
}

// Summary 落盘结果（契约 10.2 / 10.3）。
type Summary struct {
	Live   bool         `json:"live"`
	Reason string       `json:"reason"`
	Cases  []CaseResult `json:"cases"`
	Stats  string       `json:"stats"` // 必须是字符串：工作流按 string 读
}

// ScoreOptions 打分需要的注入项。
type ScoreOptions struct {
	// Catalog 可执行轴的静态判定用；nil 时该轴只在开启开关时报 failed。
	Catalog service.AgentModelCatalog
	// Execute 是否开启可执行轴（环境变量 AGENT_EVAL_EXECUTE=1）。
	Execute bool
}

// ===== schema 轴 =====

// criticalHit 计划里有没有关键错误。
func criticalHit(plan *service.AgentPlan) (string, bool) {
	if plan == nil {
		return "", false
	}
	for _, issue := range plan.Validate.Errors {
		if IsCriticalCode(issue.Code) {
			return issue.Code, true
		}
	}
	return "", false
}

// SchemaOk 校验器是否通过（契约 10.2）：
//   - 默认 = plan.Validate.OK；
//   - AllowNoChange 用例走例外：changes 为空且 reply 非空 = 模型正确地什么都没改；
//   - 命中关键错误一律 false。
func SchemaOk(c Case, plan *service.AgentPlan) bool {
	if plan == nil {
		return false
	}
	if _, hit := criticalHit(plan); hit {
		return false
	}
	if c.Expect.AllowNoChange {
		return len(plan.Changes) == 0 && strings.TrimSpace(plan.Reply) != ""
	}
	return plan.Validate.OK
}

// ===== 结构轴 =====

// Structure 结构分 0~1（契约 10.2）。判定顺序是冻结的：
// 关键错误归零 → AllowNoChange 分支 → 空图硬零 → 四项加权 → 超预算打对折。
func Structure(c Case, plan *service.AgentPlan) float64 {
	if plan == nil {
		return 0
	}
	if _, hit := criticalHit(plan); hit {
		return 0
	}
	if c.Expect.AllowNoChange {
		if len(plan.Changes) == 0 && strings.TrimSpace(plan.Reply) != "" {
			return 1
		}
		return 0
	}
	graph := plan.Validate.Normalized
	// 反作弊：空图（normalized 为空）硬零，防止「什么都没出还拿分」。
	if graph == nil || len(graph.Nodes) == 0 {
		return 0
	}
	score := WeightKinds*kindHitScore(c.Expect.Kinds, graph) +
		WeightNodes*nodeRangeScore(c.Expect, len(graph.Nodes)) +
		WeightEdges*edgeHitScore(c.Expect.MustEdges, graph) +
		WeightFrame*frameScore(c.Expect.NeedFirstFrame, graph)
	// 预算：打对折，不是硬零——合法但偏贵的方案仍有信息量，不该和「根本没出图」同分。
	if c.Expect.MaxPoints > 0 && plan.Estimate.Points > c.Expect.MaxPoints {
		score *= 0.5
	}
	return round6(score)
}

// kindHitScore |期望 kinds ∩ 实际 kinds| / |期望 kinds|；期望为空记满分。
func kindHitScore(expected []string, graph *service.CanvasWorkflowGraph) float64 {
	if len(expected) == 0 {
		return 1
	}
	actual := make(map[string]bool, len(graph.Nodes))
	for _, n := range graph.Nodes {
		actual[n.Kind] = true
	}
	hit := 0
	for _, kind := range expected {
		if actual[kind] {
			hit++
		}
	}
	return float64(hit) / float64(len(expected))
}

// nodeRangeScore 落在区间记 1，否则按到区间的距离线性衰减：n<Min 用 n/Min，n>Max 用 Max/n。
func nodeRangeScore(e Expect, n int) float64 {
	if e.MinNodes > 0 && n < e.MinNodes {
		return float64(n) / float64(e.MinNodes)
	}
	if e.MaxNodes > 0 && n > e.MaxNodes {
		return float64(e.MaxNodes) / float64(n)
	}
	return 1
}

// edgeHitScore 按 kind 对匹配 normalized.edges，命中数 / 期望数；期望为空记满分。
func edgeHitScore(expected [][2]string, graph *service.CanvasWorkflowGraph) float64 {
	if len(expected) == 0 {
		return 1
	}
	kindByID := make(map[string]string, len(graph.Nodes))
	for _, n := range graph.Nodes {
		kindByID[n.Id] = n.Kind
	}
	type pair struct{ source, target string }
	actual := make(map[pair]bool, len(graph.Edges))
	for _, e := range graph.Edges {
		actual[pair{kindByID[e.Source], kindByID[e.Target]}] = true
	}
	hit := 0
	for _, want := range expected {
		if actual[pair{want[0], want[1]}] {
			hit++
		}
	}
	return float64(hit) / float64(len(expected))
}

// frameScore NeedFirstFrame 为 false 记满分；为 true 时要求存在 video/director 节点，其上游含 image。
func frameScore(need bool, graph *service.CanvasWorkflowGraph) float64 {
	if !need {
		return 1
	}
	kindByID := make(map[string]string, len(graph.Nodes))
	for _, n := range graph.Nodes {
		kindByID[n.Id] = n.Kind
	}
	hasImageUpstream := make(map[string]bool, len(graph.Edges))
	for _, e := range graph.Edges {
		if kindByID[e.Source] == "image" {
			hasImageUpstream[e.Target] = true
		}
	}
	for _, n := range graph.Nodes {
		if (n.Kind == "video" || n.Kind == "director") && hasImageUpstream[n.Id] {
			return 1
		}
	}
	return 0
}

// ===== 可执行轴（纯静态，永不提交真实任务、永不写库）=====

// ExecutionAxis 可执行轴判定（契约 10.2 / 12.3）：
//   - 默认 skipped（未开启 AGENT_EVAL_EXECUTE）；
//   - 开启后做纯静态判定：每个生成类节点模型可解析、参数在档位内、i2v/fl2v 视频有上游图片，
//     外加用例级的 MaxDuration 上限。**不提交任何真实任务**——评测台的执行轴从设计上就是静态的。
func ExecutionAxis(c Case, plan *service.AgentPlan, catalog service.AgentModelCatalog, execute bool) string {
	if !execute {
		return ExecSkipped
	}
	if plan == nil {
		return "failed（没有产出计划）"
	}
	// 图本身没过校验就不可能执行（error 级问题会被前端丢弃），先报这个
	if n := len(plan.Validate.Errors); n > 0 {
		return fmt.Sprintf("failed（图有 %d 个校验错误，不能执行）", n)
	}
	graph := plan.Validate.Normalized
	if graph == nil || len(graph.Nodes) == 0 {
		return "failed（没有可执行的图）"
	}
	if catalog == nil {
		return "failed（没有模型目录，无法静态判定）"
	}
	kindByID := make(map[string]string, len(graph.Nodes))
	for _, n := range graph.Nodes {
		kindByID[n.Id] = n.Kind
	}
	hasImageUpstream := make(map[string]bool, len(graph.Edges))
	for _, e := range graph.Edges {
		if kindByID[e.Source] == "image" {
			hasImageUpstream[e.Target] = true
		}
	}
	for _, n := range graph.Nodes {
		taskType := kindTaskType(n.Kind)
		if taskType == "" || taskType == "text" {
			continue
		}
		model := stringField(n.Data, "model")
		if model == "" {
			return fmt.Sprintf("failed（节点 %s：缺 model）", n.Id)
		}
		hint, err := catalog.Resolve(taskType, model)
		if err != nil {
			return fmt.Sprintf("failed（节点 %s：模型 %s 不可用（%s））", n.Id, model, err.Error())
		}
		params := mapField(n.Data, "params")
		if d := intField(params, "duration"); d > 0 {
			if len(hint.Durations) > 0 && !intIn(d, hint.Durations) {
				return fmt.Sprintf("failed（节点 %s：duration %d 不在 %s 的 %s）",
					n.Id, d, model, joinInts(hint.Durations))
			}
			if c.Expect.MaxDuration > 0 && d > c.Expect.MaxDuration {
				return fmt.Sprintf("failed（节点 %s：duration %d 超过用例上限 %d）",
					n.Id, d, c.Expect.MaxDuration)
			}
		}
		if res := stringField(params, "resolution"); res != "" && len(hint.Resolutions) > 0 && !stringIn(res, hint.Resolutions) {
			return fmt.Sprintf("failed（节点 %s：resolution %s 不在 %s 的 %s）",
				n.Id, res, model, strings.Join(hint.Resolutions, "/"))
		}
		if n.Kind == "video" || n.Kind == "director" {
			mode := stringField(n.Data, "videoMode")
			if mode == "" {
				mode = stringField(params, "mode")
			}
			if (mode == "i2v" || mode == "fl2v") && !hasImageUpstream[n.Id] {
				return fmt.Sprintf("failed（节点 %s：%s 模式没有上游图片节点）", n.Id, mode)
			}
		}
	}
	return ExecPassed
}

// DurationWithin 用例级的时长上限判定（MaxDuration <= 0 时不检查）。
// 单独导出，便于自测直接断言「时长在档位内」这条期望。
func DurationWithin(c Case, plan *service.AgentPlan) (bool, string) {
	if c.Expect.MaxDuration <= 0 || plan == nil {
		return true, ""
	}
	graph := plan.Validate.Normalized
	if graph == nil {
		return true, ""
	}
	for _, n := range graph.Nodes {
		if n.Kind != "video" && n.Kind != "director" {
			continue
		}
		d := intField(mapField(n.Data, "params"), "duration")
		if d > c.Expect.MaxDuration {
			return false, fmt.Sprintf("节点 %s：duration %d 超过用例上限 %d", n.Id, d, c.Expect.MaxDuration)
		}
	}
	return true, ""
}

// ===== 汇总 =====

// BuildStats 冻结格式（契约 10.2）：
//
//	12 条用例：校验通过 11/12（91.7%），结构均分 0.83，修复轮命中 3 条，平均 1.4 轮；live=true
func BuildStats(cases []CaseResult, live bool) string {
	if len(cases) == 0 {
		return "未跑（配置缺失）"
	}
	passed, repaired, rounds := 0, 0, 0
	sum := 0.0
	for _, c := range cases {
		if c.SchemaOk {
			passed++
		}
		if c.Repaired {
			repaired++
		}
		rounds += c.Rounds
		sum += c.Structure
	}
	total := len(cases)
	return fmt.Sprintf("%d 条用例：校验通过 %d/%d（%.1f%%），结构均分 %.2f，修复轮命中 %d 条，平均 %.1f 轮；live=%t",
		total, passed, total, float64(passed)*100/float64(total),
		sum/float64(total), repaired, float64(rounds)/float64(total), live)
}

// NewSummary 组装落盘结构（Cases 永远是非 nil 数组，JSON 里是 [] 而不是 null）。
func NewSummary(live bool, reason string, cases []CaseResult) Summary {
	if cases == nil {
		cases = []CaseResult{}
	}
	return Summary{Live: live, Reason: reason, Cases: cases, Stats: BuildStats(cases, live)}
}

// ===== 单条用例的打分与执行 =====

// Score 三轴打分。
func Score(c Case, plan *service.AgentPlan, rounds int, repaired bool, opts ScoreOptions) CaseResult {
	return CaseResult{
		Id:        c.Id,
		Brief:     c.Brief,
		SchemaOk:  SchemaOk(c, plan),
		Structure: Structure(c, plan),
		Execution: ExecutionAxis(c, plan, opts.Catalog, opts.Execute),
		Rounds:    rounds,
		Repaired:  repaired,
	}
}

// RoundOutput 一轮的原始模型输出（落盘到 golang-report/agent-eval-raw/<id>.txt）。
type RoundOutput struct {
	Round int
	Raw   string
	Err   string
}

// RunOutcome 跑一条用例的完整产物。
type RunOutcome struct {
	Result CaseResult
	Plan   *service.AgentPlan
	Rounds int
	Raw    []RoundOutput
	Err    error
}

// NewLoopInput 组装一次评测用的循环输入。
//
// MaxPoints 一律 0（契约 2.6：Expect.MaxPoints 是打分层字段，注入进来会变成 OVER_BUDGET 硬错，
// 让「超预算打对折」那条路径永远走不到）；MaxNodes 用缺省 60，不读 setting（不碰库）。
func NewLoopInput(c Case, catalog service.AgentModelCatalog, model string, maxRounds int) service.AgentLoopInput {
	if maxRounds <= 0 {
		if c.Mode == "generate" {
			maxRounds = DefaultMaxRoundsGenerate
		} else {
			maxRounds = DefaultMaxRoundsPatch
		}
	}
	in := service.AgentLoopInput{
		Model:        model,
		Question:     c.Brief,
		Mode:         c.Mode,
		MaxRounds:    maxRounds,
		RoundTimeout: DefaultRoundTimeout,
		TotalTimeout: DefaultTotalTimeout,
		MaxNodes:     DefaultMaxNodes,
		MaxPoints:    DefaultMaxPoints,
		Nodes:        CanvasNodesFor(c),
		Catalog:      catalog,
	}
	if mock, ok := catalog.(*MockCatalog); ok {
		in.Pricer = CatalogPricer{Catalog: mock}
	}
	return in
}

// RunCase 跑一条用例：RunAgentRounds（纯函数，不连库、不扣积分）→ 三轴打分。
// in.Question / in.Mode 会被用例覆盖，调用方只需要给模型、目录、超时等。
func RunCase(ctx context.Context, c Case, chatter service.AgentChatter, in service.AgentLoopInput, opts ScoreOptions) RunOutcome {
	in.Question = c.Brief
	in.Mode = c.Mode
	if in.Catalog == nil {
		in.Catalog = opts.Catalog
	}
	if in.Pricer == nil {
		if mock, ok := opts.Catalog.(*MockCatalog); ok {
			in.Pricer = CatalogPricer{Catalog: mock}
		}
	}
	res := service.RunAgentRounds(ctx, chatter, in, service.NoopAgentStepSink{})

	raw := make([]RoundOutput, 0, len(res.Raw)+1)
	for i, item := range res.Raw {
		raw = append(raw, RoundOutput{Round: i + 1, Raw: item})
	}
	if res.Err != nil {
		raw = append(raw, RoundOutput{Round: len(res.Raw) + 1, Err: res.Err.Error()})
	}
	return RunOutcome{
		Result: Score(c, res.Plan, res.RoundsUsed, res.Repaired, opts),
		Plan:   res.Plan,
		Rounds: res.RoundsUsed,
		Raw:    raw,
		Err:    res.Err,
	}
}

// ===== 小工具 =====

// kindTaskType 节点 kind → 任务类型。优先用 service 的 kind 注册表（唯一真相源），
// 取不到时退回本地表（评测台不该因为注册表缺一项就崩）。
func kindTaskType(kind string) string {
	if s := service.AgentKindSchema(kind); s != nil && s.TaskType != "" {
		return s.TaskType
	}
	switch kind {
	case "text":
		return "text"
	case "image", "panorama":
		return "image"
	case "video", "director":
		return "video"
	case "audio":
		return "audio"
	}
	return ""
}

func stringField(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	v, ok := data[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func mapField(data map[string]any, key string) map[string]any {
	if data == nil {
		return nil
	}
	m, _ := data[key].(map[string]any)
	return m
}

func intField(params map[string]any, key string) int {
	if params == nil {
		return 0
	}
	switch v := params[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return 0
}

func intIn(v int, list []int) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func stringIn(v string, list []string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func joinInts(list []int) string {
	parts := make([]string, 0, len(list))
	for _, item := range list {
		parts = append(parts, fmt.Sprintf("%d", item))
	}
	return strings.Join(parts, "/")
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }
