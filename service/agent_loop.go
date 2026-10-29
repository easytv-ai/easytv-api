package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"
)

// ===== 对话式工作流生成：多轮循环（API.md 32，agent-contract 2.6 / 第 5 / 6 / 7 节）=====

const (
	// AgentMaxRoundsHardLimit 轮数硬上限（与 setting.AgentMaxRoundsHardLimit 同值 8，两处各写一次，注释互相指认），
	// 防止运营把值配成 10000 烧积分。
	AgentMaxRoundsHardLimit = 8
	// agentNoProgressLimit 连续这么多轮校验错误集合完全不变就停。
	agentNoProgressLimit = 2
	// agentMaxParseRetry 同一轮内解析失败后的重试次数（重试不额外计轮、不额外计费）。
	agentMaxParseRetry = 1
	// agentMaxSteps 单任务最多落多少条 canvas_agent_step（防止模型无限调工具把表写爆）。
	agentMaxSteps = 40
	// agentToolFeedMax 回灌给模型的工具结果最多留几条（避免提示词无限膨胀）。
	agentToolFeedMax = 6
	// agentToolFeedLineMax 每条工具结果最多留多少字；agentTemplateGraphMax 每条模板压缩后的 graph 上限。
	agentToolFeedLineMax  = 4000
	agentTemplateGraphMax = 1200
)

// AgentChatter 多轮循环需要的文本调用能力。*providers.EasyAPIProvider 已经满足（ChatWithOptions）。
// 可选参数（max_tokens / thinking）由 agentChat 从 setting 读后传入，零值即历史请求体。
type AgentChatter interface {
	ChatWithOptions(modelName, prompt string, opts ChatParams) (string, error)
}

// AgentStepRecord 一轮的落库内容。
type AgentStepRecord struct {
	Round  int
	Tool   string
	Args   string
	Result string
	OK     bool
	Err    string
}

// AgentStepSink 落库出口。实现必须自己吞掉错误：记日志失败不能中断循环。
type AgentStepSink interface {
	Step(r AgentStepRecord)
}

// NoopAgentStepSink 不落库（测试、评测台用）。
type NoopAgentStepSink struct{}

func (NoopAgentStepSink) Step(AgentStepRecord) {}

// AgentTemplate 检索到的一条可复用底座。
type AgentTemplate struct {
	Source  string `json:"source"` // workflow / skill / example
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Graph   string `json:"graph,omitempty"` // 压缩后的 graph（compactAgentGraph：只留节点 id/kind/label/model/短提示词与连线，≤ agentTemplateGraphMax 字）
}

// AgentNodeRecord 节点上次生成的真实记录（服务端查库补全，不信前端）。
// 字段与 assistLastTask 逐字相同，生产入口用字段拷贝互相转换。
type AgentNodeRecord struct {
	Model  string `json:"model"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Params string `json:"params,omitempty"`
}

// AgentLoopInput 多轮循环的全部输入。
type AgentLoopInput struct {
	UserId       int
	TeamId       int
	Model        string // 文本模型
	Question     string
	Mode         string // generate / patch
	MaxRounds    int
	RoundTimeout time.Duration
	TotalTimeout time.Duration
	// MaxNodes / MaxPoints 校验与编译的上限，由调用方注入（RunAgentRounds 是纯函数，不读 setting）。
	// 两者都 <= 0 时 RunAgentRounds 内部兜底成 60 / 0（不限）。
	MaxNodes     int
	MaxPoints    int
	Nodes        []AssistNode                // 前端给的画布上下文
	LastTasks    map[string]*AgentNodeRecord // 服务端补的真实任务记录（键 = 节点 id）
	History      []AssistTurn
	Canvas       *model.Canvas                // 只读；nil 表示画布不可读（get_canvas 工具据此回话）
	Catalog      AgentModelCatalog            // 必填
	Pricer       AgentPricer                  // 必填（regenerate 估价）
	IsMember     bool
	// Facts 平台事实（会员 / 免费对话 / 剩余次数 / 积分余额）正文，由调用方查好后注入。
	// RunAgentRounds 是纯函数（不查库、单测里可能没有 DB），所以不在循环里现查；评测台 / 单测留空即可。
	Facts        string
	Templates    func(keyword string, limit int) []AgentTemplate // nil 时 list_templates 工具返回「（没有可复用模板）」
	Cancelled    func() bool                                     // 每轮开始前调用；返回 true 立即停止。nil 表示不检查
	Now          func() time.Time                                // 注入时钟（测总超时用）；nil → time.Now
}

// AgentLoopResult 循环结果。
type AgentLoopResult struct {
	Plan       *AgentPlan
	RoundsUsed int
	Repaired   bool     // 是否靠修复轮才通过（首轮 ok=false 且最终 ok=true）
	Raw        []string // 每轮的原始模型输出（评测落盘用）
	Err        error    // 硬错误：一次都没解析出 JSON / 网关一直报错
}

// RunAgentRounds 纯循环：不碰数据库、不碰积分、不碰任务状态。
// 只做「调模型 → 解析 → 跑工具 → 再调 → 直到产出计划或用尽轮数」。
// 评测台与单测直接用这个函数（传 mock chatter + NoopAgentStepSink）。
func RunAgentRounds(ctx context.Context, chatter AgentChatter, in AgentLoopInput, sink AgentStepSink) *AgentLoopResult {
	res := &AgentLoopResult{Raw: []string{}}
	if sink == nil {
		sink = NoopAgentStepSink{}
	}
	if chatter == nil {
		res.Err = errors.New("模型不可用")
		return res
	}
	now := in.Now
	if now == nil {
		now = time.Now
	}
	maxRounds := in.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 1
	}
	if maxRounds > AgentMaxRoundsHardLimit {
		maxRounds = AgentMaxRoundsHardLimit
	}
	if in.MaxNodes <= 0 {
		in.MaxNodes = 60
	}
	if in.MaxPoints < 0 {
		in.MaxPoints = 0
	}
	if in.RoundTimeout <= 0 {
		in.RoundTimeout = 90 * time.Second
	}
	if in.TotalTimeout <= 0 {
		in.TotalTimeout = 480 * time.Second
	}

	opts := AgentValidateOptions{
		Catalog: in.Catalog, IsMember: in.IsMember, MaxNodes: in.MaxNodes, MaxPoints: in.MaxPoints,
		UserId: in.UserId, TeamId: in.TeamId,
	}
	byId := map[string]assistNodeRef{}
	for _, n := range in.Nodes {
		byId[n.Id] = assistNodeRef{Id: n.Id, Kind: n.Kind, Label: n.Label, Prompt: n.Prompt}
	}
	var templates []AgentTemplate
	if in.Templates != nil {
		templates = in.Templates(in.Question, 3)
	}

	start := now()
	var lastPlan *AgentPlan
	var lastErr error
	var issues []AgentIssue
	var toolFeed []string
	var lastSig string
	noProgress := 0
	sawFailedPlan := false

	for round := 1; round <= maxRounds; round++ {
		// 任务被取消：立即停（不再改任务状态、不再动积分，CancelTask 已经退过分）
		if in.Cancelled != nil && in.Cancelled() {
			res.Plan = lastPlan
			return res
		}
		// 总超时
		if now().Sub(start) >= in.TotalTimeout {
			break
		}
		res.RoundsUsed = round
		// parseRetry 每轮开头归零：重试额度是「每轮 1 次」，不是「每任务 1 次」
		parseRetry := 0
		for {
			prompt := BuildAgentPrompt(AgentPromptInput{
				Mode: in.Mode, Kinds: AgentKindSchemas(), Models: agentCatalogHints(in.Catalog),
				Templates: templates, Canvas: AgentCanvasText(in.Nodes, in.LastTasks), History: in.History,
				Question: in.Question, Issues: issues, ToolFeed: toolFeed, Round: round, MaxRounds: maxRounds,
				Knowledge: chatKnowledgeFromSetting(), Facts: in.Facts,
			})
			raw, err := agentChat(ctx, chatter, in.Model, prompt, in.RoundTimeout)
			if err != nil {
				// 网络错不消耗「无进展」计数
				lastErr = err
				sink.Step(AgentStepRecord{Round: round, Tool: "chat", Result: "网关调用失败", OK: false, Err: clipRunes(err.Error(), 300)})
				break
			}
			res.Raw = append(res.Raw, raw)
			parsed := ParseAgentReply(raw, byId, AgentParseOptions{})
			if parsed.Kind == "tool" {
				summary, ok := runAgentTool(parsed.Tool, parsed.Args, in)
				line := fmt.Sprintf("[工具 %s] %s", parsed.Tool, summary)
				if !ok {
					line = fmt.Sprintf("[工具 %s] 失败：%s", parsed.Tool, summary)
				}
				sink.Step(AgentStepRecord{Round: round, Tool: parsed.Tool, Args: clipRunes(string(parsed.Args), 500),
					Result: clipRunes(summary, 1000), OK: ok, Err: agentStepErr(ok, summary)})
				toolFeed = appendAgentToolFeed(toolFeed, line)
				break // 工具轮消耗一轮
			}
			if parsed.Kind != "plan" {
				if parseRetry < agentMaxParseRetry {
					parseRetry++ // 同一轮内重试：round 不前进 → 不额外计轮、不额外计费
					continue
				}
				sink.Step(AgentStepRecord{Round: round, Tool: "parse", Result: "模型没有返回 JSON", OK: false, Err: "模型没有返回 JSON"})
				lastErr = errors.New("模型没有返回 JSON")
				break
			}
			plan := BuildAgentPlan(parsed, opts, in.Pricer, round, maxRounds, in.Mode)
			plan.Question = in.Question
			sink.Step(AgentStepRecord{Round: round, Tool: "plan", OK: plan.Validate.OK,
				Result: fmt.Sprintf("ok=%v errors=%d warnings=%d changes=%d", plan.Validate.OK,
					len(plan.Validate.Errors), len(plan.Validate.Warnings), len(plan.Changes))})
			if plan.Validate.OK {
				res.Plan, res.Repaired = plan, sawFailedPlan
				return res
			}
			sawFailedPlan = true
			// 连续两轮错误签名完全不变 → 无进展，停
			sig := agentErrorSignature(plan.Validate.Errors)
			if sig == lastSig {
				noProgress++
			} else {
				noProgress = 0
			}
			lastSig = sig
			lastPlan = plan
			issues = sortAgentIssues(append(append([]AgentIssue{}, plan.Validate.Errors...), plan.Validate.Warnings...))
			if noProgress >= agentNoProgressLimit {
				res.Plan = lastPlan
				return res
			}
			break
		}
	}
	if lastPlan != nil {
		res.Plan = lastPlan
		return res
	}
	if lastErr == nil {
		lastErr = errors.New("模型没有返回可用的计划")
	}
	res.Err = lastErr
	return res
}

func agentStepErr(ok bool, summary string) string {
	if ok {
		return ""
	}
	return clipRunes(summary, 300)
}

func agentCatalogHints(catalog AgentModelCatalog) map[string][]AgentModelHint {
	if catalog == nil {
		return nil
	}
	return catalog.All()
}

func appendAgentToolFeed(feed []string, line string) []string {
	// 每条工具结果也封顶：list_templates 一次最多 8 条模板、每条带压缩后的 graph，不封顶的话
	// 六条工具结果就能把每轮提示词撑到十万字（复核发现 medium）
	feed = append(feed, clipRunes(line, agentToolFeedLineMax))
	if len(feed) > agentToolFeedMax {
		feed = feed[len(feed)-agentToolFeedMax:]
	}
	return feed
}

// agentChat 在 goroutine 里跑一轮模型调用，超时/取消都不阻塞循环。
func agentChat(ctx context.Context, chatter AgentChatter, modelName, prompt string, timeout time.Duration) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	type outcome struct {
		text string
		err  error
	}
	ch := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- outcome{err: fmt.Errorf("模型调用异常：%v", r)}
			}
		}()
		text, err := chatter.ChatWithOptions(modelName, prompt, chatParamsFromSetting())
		ch <- outcome{text, err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case o := <-ch:
		return o.text, o.err
	case <-timer.C:
		return "", fmt.Errorf("本轮模型调用超时（%s）", timeout)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// chatParamsFromSetting 本轮对话的可选参数（ChatMaxTokens / ChatThinkingMode / ChatThinkingEffort）。
// 这三个键的缺省值就是零值（0 / "" / ""），所以「缓存里没有 → 零值」与 getter 的缺省回退完全一致；
// 只看内存缓存、不触发 DB 回查，RunAgentRounds 才敢在没有初始化 DB 的单测 / 评测台里跑。
func chatParamsFromSetting() ChatParams {
	cached := model.GetAllSettings()
	p := ChatParams{}
	if _, ok := cached[setting.ChatMaxTokens]; ok {
		p.MaxTokens = setting.GetChatMaxTokens()
	}
	if _, ok := cached[setting.ChatThinkingMode]; ok {
		p.ThinkingMode = setting.GetChatThinkingMode()
	}
	if _, ok := cached[setting.ChatThinkingEffort]; ok {
		p.ThinkingEffort = setting.GetChatThinkingEffort()
	}
	return p
}

// chatKnowledgeFromSetting 运营配置的平台知识补充文本；缺省空。同 chatParamsFromSetting 只认内存缓存。
func chatKnowledgeFromSetting() string {
	if _, ok := model.GetAllSettings()[setting.ChatKnowledge]; !ok {
		return ""
	}
	return setting.GetChatKnowledge()
}

// agentWalletOwner 【平台事实】里「当前积分余额」看谁的钱包：个人空间（teamId<=0）是自己；
// 团队空间是团队 owner 的积分池——与 service.WalletView / teamWalletView 的展示口径、
// PayerForTeam + FreezeTaskPoints 的扣费口径一致（任务存下的 team_id 说了算）。
// 团队查不到时退回本人，保证仍给出一个已核实的数字。
func agentWalletOwner(userId, teamId int) int {
	if teamId <= 0 {
		return userId
	}
	if owner := TeamOwnerOf(teamId); owner > 0 {
		return owner
	}
	return userId
}

// agentFactsFor 组装【平台事实】正文（会员 / 免费对话 / 剩余次数 / 积分余额）。
// freeChat 必须是「这个任务实际算不算免费」：在途任务用 IsFreeChatTask(task)（创建时落库的 free 标记，
// 运营翻转开关不影响它），待创建的新任务用创建那一刻的 setting.IsChatFreeEnabled()——
// 不能用实时开关代替任务标记，否则开关翻转后提示词会和实际不计费的口径不一致。
// 只读查询；次数读不到时整段返回空（不输出【平台事实】）——宁可让模型按「不太确定」回话，
// 也不许把配置上限冒充成剩余次数（agent_facts 的原则：只写已核实的事实）。userId<=0（评测台）同样返回空。
func agentFactsFor(userId, teamId int, freeChat bool) string {
	if userId <= 0 {
		return ""
	}
	quota, err := GetChatQuota(userId, teamId)
	if err != nil {
		// 次数读失败时「剩余」不可核实；AgentFactsText 没有「未知」分支，这里整段不写而不是编一个数字
		common.SysError(fmt.Sprintf("agent: chat quota read failed user=%d: %v", userId, err))
		return ""
	}
	return AgentFactsText(AgentFactsInput{
		IsMember:      quota.IsMember,
		FreeChat:      freeChat,
		DailyLimit:    quota.DailyLimit,
		Remaining:     quota.Remaining,
		PointsBalance: model.GetPointsBalance(nil, agentWalletOwner(userId, teamId)),
	})
}

// ===== 任务参数（冻结结构，第 13 节）=====

// AgentParams 存进 generation_task.params 的 agent 段。
type AgentParams struct {
	Question   string         `json:"question"`
	Mode       string         `json:"mode"`
	MaxRounds  int            `json:"max_rounds"`
	RetryOf    int            `json:"retry_of,omitempty"`
	Nodes      []agentNodeRef `json:"nodes"`
	History    []AssistTurn   `json:"history"`
	PlanBytes  int            `json:"plan_bytes"`
	RoundsUsed int            `json:"rounds_used"`
	// Free 创建时是否命中免积分对话开关（契约第 7 条）：一次落库，之后运营翻转开关不影响在途任务。
	Free bool `json:"free,omitempty"`
}

type agentTaskParams struct {
	Agent *AgentParams `json:"agent"`
}

// AgentRequest POST /user/canvas/:id/agent 请求体。
type AgentRequest struct {
	Question  string       `json:"question"`
	Mode      string       `json:"mode"` // generate / patch，缺省 patch
	Model     string       `json:"model"`
	Nodes     []AssistNode `json:"nodes"`
	History   []AssistTurn `json:"history"`
	MaxRounds int          `json:"max_rounds"`
}

func normalizeAgentMode(mode string) string {
	if strings.TrimSpace(mode) == "generate" {
		return "generate"
	}
	return "patch"
}

// agentClampRounds 轮数夹到 [1, 8]；<=0 返回 0 表示「用 setting 的默认值」。
func agentClampRounds(n int) int {
	if n <= 0 {
		return 0
	}
	if n > AgentMaxRoundsHardLimit {
		return AgentMaxRoundsHardLimit
	}
	return n
}

// AgentModel 规划用的文本模型：后台配置（setting.AgentModel，已定价 + 已接入）优先 → 请求指定
// （必须已定价且是 text 模型）→ planModel()。也就是运营配了模型就以后台为准，前端传的模型只在没配时生效。
func AgentModel(requested string) string {
	if m := setting.GetAgentModel(); m != "" && FindProvider(model.TaskTypeText, m) != nil {
		return m
	}
	requested = strings.TrimSpace(requested)
	if requested != "" && FindProvider(model.TaskTypeText, requested) != nil {
		if p, err := model.GetModelPrice(requested); err == nil && p.Type == model.TaskTypeText {
			return requested
		}
	}
	return planModel()
}

// IsAgentTask 该任务是不是对话式工作流任务：params.agent 存在、question 非空、max_rounds > 0。
func IsAgentTask(task *model.GenerationTask) bool {
	if task == nil || task.Type != model.TaskTypeText {
		return false
	}
	var p agentTaskParams
	if json.Unmarshal([]byte(task.Params), &p) != nil || p.Agent == nil {
		return false
	}
	if strings.TrimSpace(p.Agent.Question) == "" {
		return false
	}
	n := p.Agent.MaxRounds
	if n <= 0 {
		return false
	}
	if n > AgentMaxRoundsHardLimit {
		n = AgentMaxRoundsHardLimit
	}
	return true
}

// agentLastTasks 服务端补节点上次生成的真实记录（与 NewAssistTask 同一套归属校验）。
func agentLastTasks(cv *model.Canvas, nodes []AssistNode) map[string]*assistLastTask {
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
	return last
}

func agentNodeRecords(last map[string]*assistLastTask) map[string]*AgentNodeRecord {
	out := map[string]*AgentNodeRecord{}
	for id, lt := range last {
		out[id] = &AgentNodeRecord{Model: lt.Model, Status: lt.Status, Error: lt.Error, Params: lt.Params}
	}
	return out
}

// agentNodeRef 存进任务 params 的画布节点上下文。
// 比老 assist 的 assistNodeRef 多存 content / has_media / selected / upstream / downstream：
// 多轮循环在 worker 里每轮重新拼提示词（RunAgentRounds → AgentCanvasText），只能读这份快照，
// 少一个字段模型就少看见一截画布——丢了 upstream/downstream 它不知道谁连着谁，
// 丢了 selected 就认不出用户指的是哪个节点。老任务的 params 缺这些键时按零值读，不影响回放。
type agentNodeRef struct {
	Id         string          `json:"id"`
	Kind       string          `json:"kind"`
	Label      string          `json:"label"`
	Prompt     string          `json:"prompt"`
	Content    string          `json:"content,omitempty"`
	HasMedia   bool            `json:"has_media,omitempty"`
	Selected   bool            `json:"selected,omitempty"`
	Upstream   []string        `json:"upstream,omitempty"`
	Downstream []string        `json:"downstream,omitempty"`
	LastTask   *assistLastTask `json:"last_task,omitempty"`
}

func agentNodeRefs(nodes []AssistNode, last map[string]*assistLastTask) []agentNodeRef {
	out := make([]agentNodeRef, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, agentNodeRef{
			Id: n.Id, Kind: n.Kind, Label: n.Label, Prompt: n.Prompt,
			Content: n.Content, HasMedia: n.HasMedia, Selected: n.Selected,
			Upstream: n.Upstream, Downstream: n.Downstream,
			LastTask: last[n.Id],
		})
	}
	return out
}

// agentLoopNodes 把 params 里的画布快照还原成循环要的上下文。
// RunAgentLoop 与单测共用这一条路径：存了什么就得读回什么，少一个字段模型就少看见一截画布。
func agentLoopNodes(refs []agentNodeRef) ([]AssistNode, map[string]*AgentNodeRecord) {
	nodes := make([]AssistNode, 0, len(refs))
	last := map[string]*AgentNodeRecord{}
	for _, n := range refs {
		nodes = append(nodes, AssistNode{
			Id: n.Id, Kind: n.Kind, Label: n.Label, Prompt: n.Prompt,
			Content: n.Content, HasMedia: n.HasMedia, Selected: n.Selected,
			Upstream: n.Upstream, Downstream: n.Downstream,
		})
		if n.LastTask == nil {
			continue
		}
		// assistLastTask → AgentNodeRecord 逐字段拷贝（对外可构造性要求 AgentLoopInput 只用导出类型）
		last[n.Id] = &AgentNodeRecord{Model: n.LastTask.Model, Status: n.LastTask.Status, Error: n.LastTask.Error, Params: n.LastTask.Params}
	}
	return nodes, last
}

// NewAgentTask 校验请求、补全上次生成记录、组装文本任务（未入库）。签名与 NewAssistTask 同形。
func NewAgentTask(userId int, cv *model.Canvas, req AgentRequest) (*model.GenerationTask, GenerationProvider, error) {
	// 运营开关（setting.AgentEnabled，缺省开启）：关掉后不再接受新任务，已在跑的任务照旧跑完
	if !setting.IsAgentEnabled() {
		return nil, nil, errors.New("对话式工作流生成暂未开启")
	}
	if cv == nil {
		return nil, nil, errors.New("画布不存在")
	}
	question := strings.TrimSpace(req.Question)
	if question == "" {
		return nil, nil, errors.New("请输入问题")
	}
	if len([]rune(question)) > assistMaxQuestion {
		return nil, nil, fmt.Errorf("问题太长了，最多 %d 字", assistMaxQuestion)
	}
	mode := normalizeAgentMode(req.Mode)
	modelName := AgentModel(req.Model)
	if modelName == "" {
		return nil, nil, errors.New("暂无可用的文本模型")
	}
	provider := FindProvider(model.TaskTypeText, modelName)
	if provider == nil {
		return nil, nil, fmt.Errorf("模型 %s 暂未接入", modelName)
	}
	nodes := sanitizeAssistNodes(req.Nodes)
	last := agentLastTasks(cv, nodes)
	history := trimAssistHistory(req.History)
	maxRounds := agentClampRounds(req.MaxRounds)
	if maxRounds <= 0 {
		maxRounds = setting.GetAgentMaxRounds(mode)
	}
	// 一次性读开关：提示词事实与落库的 free 标记必须来自同一个瞬间
	freeChat := setting.IsChatFreeEnabled()
	prompt := BuildAgentPrompt(AgentPromptInput{
		Mode: mode, Kinds: AgentKindSchemas(), Models: agentCatalogHints(DBModelCatalog{}),
		Canvas: AgentCanvasText(nodes, agentNodeRecords(last)), History: history,
		Question: question, Round: 1, MaxRounds: maxRounds,
		Knowledge: setting.GetChatKnowledge(),
		// 建任务时还没解析团队空间（resolveTaskTeam 在控制器里后才调），事实按个人空间算，不为此改签名。
		Facts: agentFactsFor(userId, 0, freeChat),
	})
	params := agentTaskParams{Agent: &AgentParams{
		Question: question, Mode: mode, MaxRounds: maxRounds,
		Nodes: agentNodeRefs(nodes, last), History: history,
		Free: freeChat,
	}}
	raw, _ := json.Marshal(params)
	return &model.GenerationTask{
		UserId:    userId,
		ProjectId: cv.ProjectId,
		CanvasId:  cv.Id,
		Type:      model.TaskTypeText,
		Provider:  provider.Name(),
		Model:     modelName,
		Prompt:    prompt,
		Params:    string(raw),
		Inputs:    "[]",
	}, provider, nil
}

// agentRetryRounds 重试的轮数：显式指定就用它（夹到 [1,8]），没指定按运营配置给（patch 4 / generate 6）。
// 不能兜底成 1：回灌的修法提示本身就在让模型「用 search_models 查一个可用的模型」，
// 只给一轮的话这次工具调用就把轮数用光，循环拿不到计划直接 FailTask，
// 用户看到的是硬失败而不是修好的方案，方案卡连同校验报告一起消失。
func agentRetryRounds(mode string, requested int) int {
	if n := agentClampRounds(requested); n > 0 {
		return n
	}
	return setting.GetAgentMaxRounds(mode)
}

// NewAgentRetryTask 基于上一轮的 agent 任务再建一条（省一次全量上下文）。
func NewAgentRetryTask(userId int, cv *model.Canvas, src *model.GenerationTask, note string, maxRounds int) (*model.GenerationTask, GenerationProvider, error) {
	if !setting.IsAgentEnabled() {
		return nil, nil, errors.New("对话式工作流生成暂未开启")
	}
	if cv == nil || src == nil || src.UserId != userId || src.CanvasId != cv.Id || src.Type != model.TaskTypeText {
		return nil, nil, model.ErrNotFound
	}
	// 上一轮还在跑就不许再起一轮：每次重试都消费一次当日次数与一个并发位（复核发现）
	if src.Status == model.TaskStatusPending || src.Status == model.TaskStatusRunning {
		return nil, nil, errors.New("上一轮还在生成中，等它结束再重试")
	}
	var srcParams agentTaskParams
	if json.Unmarshal([]byte(src.Params), &srcParams) != nil || srcParams.Agent == nil || strings.TrimSpace(srcParams.Agent.Question) == "" {
		return nil, nil, model.ErrNotFound
	}
	provider := FindProvider(model.TaskTypeText, src.Model)
	if provider == nil {
		return nil, nil, fmt.Errorf("模型 %s 暂未接入", src.Model)
	}
	mode := normalizeAgentMode(srcParams.Agent.Mode)
	rounds := agentRetryRounds(mode, maxRounds)
	note = clipRunes(strings.TrimSpace(note), 500)
	// 上一轮校验报告（result_text 里存的就是 AgentPlan）
	report := ""
	var stored AgentPlan
	if json.Unmarshal([]byte(src.ResultText), &stored) == nil && len(stored.Validate.Errors) > 0 {
		lines := []string{"上一轮校验报告："}
		for _, e := range stored.Validate.Errors {
			line := "- " + e.Code
			if e.NodeId != "" {
				line += "(" + e.NodeId + ")"
			}
			line += " " + e.Message
			if e.Hint != "" {
				line += "（修法：" + e.Hint + "）"
			}
			lines = append(lines, line)
		}
		report = strings.Join(lines, "\n")
	}
	prompt := src.Prompt
	if report != "" {
		prompt += "\n\n" + report
	}
	if note != "" {
		prompt += "\n\n补充要求：" + note
	}
	history := srcParams.Agent.History
	// AgentParams 里没有别的自由文本字段，把「报告 + 补充要求」作为一轮用户反馈带进循环的提示词
	feedback := report
	if note != "" {
		if feedback != "" {
			feedback += "\n"
		}
		feedback += "补充要求：" + note
	}
	if feedback != "" {
		history = append(append([]AssistTurn{}, history...), AssistTurn{Role: "user", Content: clipRunes(feedback, 500)})
	}
	if len(history) > assistMaxHistory {
		history = history[len(history)-assistMaxHistory:]
	}
	params := agentTaskParams{Agent: &AgentParams{
		Question: srcParams.Agent.Question, Mode: mode, MaxRounds: rounds, RetryOf: src.Id,
		Nodes: srcParams.Agent.Nodes, History: history,
		Free: setting.IsChatFreeEnabled(),
	}}
	raw, _ := json.Marshal(params)
	return &model.GenerationTask{
		UserId:         userId,
		ProjectId:      cv.ProjectId,
		CanvasId:       cv.Id,
		ParentTaskId:   src.Id,
		Type:           model.TaskTypeText,
		Provider:       provider.Name(),
		Model:          src.Model,
		Prompt:         prompt,
		Params:         string(raw),
		Inputs:         "[]",
		PointsFrozen:   0,
	}, provider, nil
}

// dbAgentStepSink 生产落库出口：每轮写一条 canvas_agent_step，并顺带推进任务进度。
// 所有错误自己吞掉（记日志失败不能中断循环）。
type dbAgentStepSink struct {
	taskId    int
	canvasId  int
	userId    int
	maxRounds int
	count     int
}

func (s *dbAgentStepSink) Step(r AgentStepRecord) {
	if s.maxRounds > 0 {
		_ = model.SetTaskProgress(s.taskId, 10+r.Round*80/s.maxRounds)
	}
	if s.count >= agentMaxSteps {
		return
	}
	s.count++
	_ = model.InsertCanvasAgentStep(&model.CanvasAgentStep{
		TaskId: s.taskId, CanvasId: s.canvasId, UserId: s.userId, Round: r.Round,
		Tool: clipRunes(r.Tool, 32), ArgsSummary: clipRunes(r.Args, 500), ResultSummary: clipRunes(r.Result, 1000),
		OK: r.OK, Error: clipRunes(r.Err, 300),
	})
}

// settleAgentLoop 对话任务成功后的结算，返回本次实际消耗积分（由 CompleteTask 写进 cost_points）。
// 免积分对话（契约第 5 条）：整段跳过按轮结算与退差，cost_points 保持 0；
// 其余任务沿用旧口径：按实际轮数重新估价，多预扣的部分退回。
func settleAgentLoop(task *model.GenerationTask, used int, isMember bool) int {
	if IsFreeChatTask(task) {
		return 0
	}
	actual, err := AgentSettlePoints(model.TaskTypeText, task.Model, used, isMember)
	if err != nil {
		// 估不出来就不退差：宁可多收也不出错账
		actual = task.PointsFrozen
	}
	if refund := task.PointsFrozen - actual; refund > 0 {
		if _, err := RefundTaskPointsPartial(task.Id, refund, fmt.Sprintf("按实际 %d 轮结算，退回 %d 积分", used, refund)); err != nil {
			common.SysError(fmt.Sprintf("agent: partial refund failed task=%d: %v", task.Id, err))
		}
	}
	return actual
}

// RunAgentLoop 生产入口（由 providers 在任务已置 running 后调用）：包住 RunAgentRounds，
// 负责进度、每轮落库、任务状态与计费。内部自己结束任务，不向调用方返回错误。
func RunAgentLoop(task *model.GenerationTask, chatter AgentChatter) {
	if task == nil || chatter == nil {
		return
	}
	var params agentTaskParams
	if json.Unmarshal([]byte(task.Params), &params) != nil || params.Agent == nil {
		return
	}
	ap := params.Agent
	if strings.TrimSpace(ap.Question) == "" {
		return
	}
	mode := normalizeAgentMode(ap.Mode)
	maxRounds := agentClampRounds(ap.MaxRounds)
	if maxRounds <= 0 {
		maxRounds = setting.GetAgentMaxRounds(mode)
	}
	cancelled := func() bool {
		t, err := model.GetTaskById(task.Id)
		return err != nil || t.Status != model.TaskStatusRunning
	}
	if cancelled() {
		return
	}
	cv, _ := model.GetCanvasById(task.CanvasId) // 只读；拿不到就是画布不可读
	nodes, last := agentLoopNodes(ap.Nodes)
	isMember := IsMemberForPricing(task.UserId, task.TeamId)
	in := AgentLoopInput{
		UserId: task.UserId, TeamId: task.TeamId, Model: task.Model, Question: ap.Question, Mode: mode,
		MaxRounds: maxRounds, RoundTimeout: setting.GetAgentRoundTimeout(), TotalTimeout: setting.GetAgentTotalTimeout(),
		MaxNodes: setting.GetAgentMaxNodes(), MaxPoints: setting.GetAgentMaxPoints(),
		Nodes: nodes, LastTasks: last, History: ap.History, Canvas: cv,
		Catalog: DBModelCatalog{}, Pricer: dbAssistPricer{isMember: isMember}, IsMember: isMember,
		// 事实按任务创建时落库的 free 标记渲染：开关翻转后仍在跑的免费任务，提示词口径也不能变
		Facts: agentFactsFor(task.UserId, task.TeamId, IsFreeChatTask(task)),
		Templates: func(keyword string, limit int) []AgentTemplate {
			return RetrieveAgentTemplates(task.UserId, task.TeamId, keyword, limit)
		},
		Cancelled: cancelled,
	}
	sink := &dbAgentStepSink{taskId: task.Id, canvasId: task.CanvasId, userId: task.UserId, maxRounds: maxRounds}
	result := RunAgentRounds(context.Background(), chatter, in, sink)
	// 任务被取消：CancelTask 已经全额退分，这里不再改状态、不再动积分
	if cancelled() {
		return
	}
	plan := result.Plan
	if plan == nil {
		Track(task.UserId, "agent_plan_failed", map[string]any{"task_id": task.Id, "rounds": result.RoundsUsed})
		_ = FailTask(task.Id, "模型没有按要求返回 JSON，请重试")
		return
	}
	used := result.RoundsUsed
	if used < 1 {
		used = 1
	}
	if used > maxRounds {
		used = maxRounds
	}
	// 免积分对话（契约第 5 条）：整段跳过按轮结算与退差，cost_points 保持 0；状态与对账字段照常。
	actual := settleAgentLoop(task, used, isMember)
	plan.TaskId, plan.Status = task.Id, model.TaskStatusSucceeded
	stored, _ := json.Marshal(plan)
	// 对账字段（6.4）：实际轮数与计划字节数写回任务 params
	ap.RoundsUsed, ap.PlanBytes = used, len(stored)
	if b, err := json.Marshal(agentTaskParams{Agent: ap}); err == nil {
		if err := model.DB.Model(&model.GenerationTask{}).Where("id = ?", task.Id).Update("params", string(b)).Error; err != nil {
			common.SysError(fmt.Sprintf("agent: save params failed task=%d: %v", task.Id, err))
		}
	}
	if err := CompleteTask(task.Id, 0, "", string(stored)); err != nil {
		common.SysLog(fmt.Sprintf("agent: complete task %d failed: %v", task.Id, err))
		return
	}
	Track(task.UserId, "agent_plan_ready", map[string]any{
		"task_id": task.Id, "mode": mode, "rounds": used, "points": actual,
		"changes": len(plan.Changes), "estimate": plan.EstimatedPoints, "repaired": result.Repaired,
	})
}
