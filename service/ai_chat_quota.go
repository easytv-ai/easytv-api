package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"easytv-api/model"
	"easytv-api/setting"
)

// ===== AI 对话每日次数配额（契约第 5 条）=====
//
// 普通用户每天有 FreeCount 次免费对话（后台可配，缺省 100），会员不限次。
// 计数按「人 + 日」记在 ai_chat_usage，消费走 model.IncrChatUsage 的条件自增，超限返回 ErrChatQuotaExceeded。

// ChatQuota 今日 AI 对话配额视图（GET /user/ai-chat/quota 的 JSON）。
type ChatQuota struct {
	Date       string `json:"date"`
	Enabled    bool   `json:"enabled"`
	Free       bool   `json:"free"`
	IsMember   bool   `json:"is_member"`
	Unlimited  bool   `json:"unlimited"`
	DailyLimit int    `json:"daily_limit"`
	Used       int    `json:"used"`
	Remaining  int    `json:"remaining"`
}

// ErrChatQuotaExceeded 今日次数已用完；调用方应映射成给用户看的友好文案。
var ErrChatQuotaExceeded = errors.New("今日 AI 对话次数已用完")

// ChatQuotaExceededReason 建任务 / 重试接口失败时 data.reason（与 concurrency / review_gate 同形）。
// 前端优先认这个码，中文 message 只给旧客户端兜底。
const ChatQuotaExceededReason = "chat_quota_exceeded"

// ChatQuotaExceededMessage 给人看的中文；N 是当日配置上限。
func ChatQuotaExceededMessage(dailyLimit int) string {
	return fmt.Sprintf("今日 AI 对话次数已用完（%d 次），开通会员可无限次使用", dailyLimit)
}

// chatQuotaBase 组装配额视图里与用量无关的部分。
func chatQuotaBase(userId, teamId int, day string) ChatQuota {
	return ChatQuota{
		Date:       day,
		Enabled:    setting.IsAgentEnabled(),
		Free:       setting.IsChatFreeEnabled(),
		IsMember:   IsMemberForPricing(userId, teamId),
		DailyLimit: setting.GetChatDailyFreeCount(),
	}
}

// infinite 会员或每日次数配置为不限（<=0）：Unlimited=true、Remaining=-1，且不读不写计数。
func (q *ChatQuota) infinite() {
	q.Unlimited = true
	q.Remaining = -1
	q.Used = 0
}

// GetChatQuota 今日对话配额（只读，不写计数）。
func GetChatQuota(userId, teamId int) (ChatQuota, error) {
	day := model.ChatUsageDay(time.Now())
	q := chatQuotaBase(userId, teamId, day)
	if q.IsMember || q.DailyLimit <= 0 {
		q.infinite()
		return q, nil
	}
	used, err := model.GetChatUsage(userId, day)
	if err != nil {
		return ChatQuota{}, err
	}
	q.Used = used
	q.Remaining = q.DailyLimit - used
	if q.Remaining < 0 {
		q.Remaining = 0
	}
	return q, nil
}

// ConsumeChatQuota 原子消费一次今日次数；超限返回 ErrChatQuotaExceeded（此时计数不增长）。
// 会员与不限次配置完全不写计数；普通用户由 model.IncrChatUsage 的一条条件自增 SQL 保证并发下不超发。
func ConsumeChatQuota(userId, teamId int) (ChatQuota, error) {
	day := model.ChatUsageDay(time.Now())
	q := chatQuotaBase(userId, teamId, day)
	if q.IsMember || q.DailyLimit <= 0 {
		q.infinite()
		return q, nil
	}
	used, allowed, err := model.IncrChatUsage(userId, day, q.DailyLimit)
	if err != nil {
		return ChatQuota{}, err
	}
	q.Used = used
	q.Remaining = q.DailyLimit - used
	if q.Remaining < 0 {
		q.Remaining = 0
	}
	if !allowed {
		return q, ErrChatQuotaExceeded
	}
	return q, nil
}

// RefundChatQuota 退还一次今日次数（任务失败 / 取消）。count 已经是 0 时忽略，计数不会为负。
// 只适用于「当天消费、当天失败」的路径（入库失败、同步提交失败）；任务跨零点失败 / 取消时
// 消费日与退款日不是同一天，必须用 RefundChatQuotaOn 退消费日，否则次数会退到错误的一天。
func RefundChatQuota(userId int) error {
	return RefundChatQuotaOn(userId, model.ChatUsageDay(time.Now()))
}

// RefundChatQuotaOn 退还指定消费日的一次对话次数：跨零点的失败 / 取消要退消费日，不是退款时刻的日期。
// 消费日由调用方从任务带过来：任务创建时间 model.ChatUsageDay(time.Unix(task.CreatedAt, 0))，
// 或消费时把 ConsumeChatQuota 返回的 ChatQuota.Date 落进任务 params、退款时读回。
// 空日期直接忽略：拿不到消费日时不要退到别的日子（宁可少退一次，也不许把当日 / 次日的计数减错）。
func RefundChatQuotaOn(userId int, day string) error {
	if userId <= 0 || day == "" {
		return nil
	}
	return model.DecrChatUsage(userId, day)
}

// IsFreeChatTask 免费对话任务，两种落库口径：
//  1. 画布对话：agent 任务且创建时 params.agent.free=true；
//  2. 导演助手：params.chat.kind=director 且 params.chat.free=true（单轮文本，不跑 apply_graph）。
//
// free 只能由服务端在建任务时写入，客户端不许自报：所有接受客户端 params 的建任务入口
// （controller/task.go 的 CreateTask、service.CreateBatch）都过 CheckNoClientAgentParams
// 挡住自带的 params.agent / params.chat（复核发现 high）；画布入口由 NewAgentTask /
// NewAgentRetryTask 写 agent.free，导演入口由 NewDirectorChatTask 写 chat.free，
// 通用重试由 RewriteChatFreeFlag 按当前开关重估。
// 创建后只按落库的 free 判定：结算与退款必须用这个口径，不能读实时开关，
// 否则开关中途翻转会让已冻结的积分 / 次数卡住（在途任务按创建时的口径跑完）。
func IsFreeChatTask(task *model.GenerationTask) bool {
	if task == nil || task.Type != model.TaskTypeText {
		return false
	}
	if IsAgentTask(task) {
		var p struct {
			Agent *struct {
				Free bool `json:"free"`
			} `json:"agent"`
		}
		if json.Unmarshal([]byte(task.Params), &p) != nil || p.Agent == nil {
			return false
		}
		return p.Agent.Free
	}
	return directorChatFree(task)
}

// ErrClientAgentParams 客户端自带的 params.agent：对话式工作流任务只能由画布对话接口创建。
var ErrClientAgentParams = errors.New("对话式工作流任务只能通过画布对话接口创建")

// ErrClientChatParams 客户端自带的 params.chat：导演对话只能由 POST /user/ai-chat 创建。
var ErrClientChatParams = errors.New("导演对话任务只能通过对话接口创建")

// CheckNoClientAgentParams 校验 params 里没有客户端自带的非空 agent 对象（免积分标记只能由服务端写）。
// 所有接受客户端 params 的建任务入口（通用建任务 controller.CreateTask、服务端批量编排 CreateBatch……）
// 都必须先过这一关：params 是原样透传的，而 IsFreeChatTask 只看 params.agent.free——放行的话，
// 任何登录用户自带 free=true 就能走免积分分支（跳过估价与预扣，ChatFreeEnabled=false 时也免单，复核发现 high）。
// 键名匹配必须与 encoding/json 的结构体解码同语义（大小写不敏感，Agent / AGENT 都算 agent 段，
// 否则 {"Agent":{"max_rounds":8}} 会绕过闸门：IsAgentTask 认得它并按 8 轮跑，计价却只按 1 轮，复核发现 medium）。
// agent 不存在 / 是空对象 / 不是对象 / 非 JSON 对象都不拦，交给各入口自己的参数校验。
func CheckNoClientAgentParams(params string) error {
	var p map[string]json.RawMessage
	if json.Unmarshal([]byte(params), &p) != nil || p == nil {
		return nil
	}
	for k, raw := range p {
		if strings.EqualFold(k, "agent") {
			var agent map[string]json.RawMessage
			if json.Unmarshal(raw, &agent) == nil && len(agent) > 0 {
				return ErrClientAgentParams
			}
		}
		if strings.EqualFold(k, "chat") {
			var chat map[string]json.RawMessage
			if json.Unmarshal(raw, &chat) == nil && len(chat) > 0 {
				return ErrClientChatParams
			}
		}
	}
	return nil
}

// RewriteChatFreeFlag 按当前开关重估 agent 任务参数里的 free 标记（通用重试专用，与 NewAgentRetryTask 同口径）：
// 源任务带 params.agent 时 free 不能照抄旧值，否则运营关掉 ChatFreeEnabled 后重试旧的免积分任务仍然免单
// （复核发现 medium）。free 一律改成传入值（调用方传 setting.IsChatFreeEnabled()），其余 params 原样保留；
// 没有 agent 段 / agent 不是对象时原样返回。键名匹配与 encoding/json 的结构体解码同语义（大小写不敏感），
// 并把 agent / free 统一成规范小写键，避免新旧两份并存。free 只能由服务端写入，
// 客户端自报的入口已在各建任务入口（CreateTask / CreateBatch）用 CheckNoClientAgentParams 挡住。
func RewriteChatFreeFlag(params string, free bool) string {
	out := rewriteSectionBool(params, "agent", "free", free)
	return rewriteSectionBool(out, "chat", "free", free)
}

// WriteChatQuotaDay 把「这次是否真的消费了次数、消费的是哪一天」落进 params.agent.quota_day：
// 消费了写消费日（yyyy-mm-dd），没消费（会员 / 不限次配置）写空串。退次数只认这个标记（见 ChatQuotaDayOf），
// 否则会员期间建的免费任务失败时会把此前非会员时用掉的次数退回去（复核发现 medium）。
// 没有 agent 段 / agent 不是对象时原样返回。
func WriteChatQuotaDay(params string, day string) string {
	day = strings.TrimSpace(day)
	if sectionExists(params, "agent") {
		return rewriteSectionField(params, "agent", "quota_day", day)
	}
	if sectionExists(params, "chat") {
		return rewriteSectionField(params, "chat", "quota_day", day)
	}
	return params
}

// ChatQuotaDayOf 读任务的消费日标记：tagged=false 表示老任务（标记落地前建的），
// 调用方回退到「按创建时间算消费日」的老口径；tagged=true 且 day 为空表示创建时没消费过，不退。
func ChatQuotaDayOf(task *model.GenerationTask) (day string, tagged bool) {
	if task == nil {
		return "", false
	}
	if day, tagged = quotaDayInSection(task.Params, "agent"); tagged {
		return day, true
	}
	return quotaDayInSection(task.Params, "chat")
}

func quotaDayInSection(params, section string) (day string, tagged bool) {
	var p map[string]json.RawMessage
	if json.Unmarshal([]byte(params), &p) != nil {
		return "", false
	}
	_, sectionRaw, ok := jsonKeyFold(p, section)
	if !ok {
		return "", false
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(sectionRaw, &obj) != nil {
		return "", false
	}
	_, raw, ok := jsonKeyFold(obj, "quota_day")
	if !ok {
		return "", false
	}
	_ = json.Unmarshal(raw, &day)
	return strings.TrimSpace(day), true
}

func sectionExists(params, section string) bool {
	var p map[string]json.RawMessage
	if json.Unmarshal([]byte(params), &p) != nil || p == nil {
		return false
	}
	_, raw, ok := jsonKeyFold(p, section)
	if !ok {
		return false
	}
	var obj map[string]json.RawMessage
	return json.Unmarshal(raw, &obj) == nil && len(obj) > 0
}

// rewriteAgentField 改 params.agent 里的一个字符串键（大小写变体先删干净，再写规范小写键），agent 键本身也归一成小写。
func rewriteAgentField(params, key, value string) string {
	return rewriteSectionField(params, "agent", key, value)
}

func rewriteSectionField(params, section, key, value string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return params
	}
	return rewriteSectionRaw(params, section, key, raw)
}

func rewriteSectionBool(params, section, key string, value bool) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return params
	}
	return rewriteSectionRaw(params, section, key, raw)
}

func rewriteSectionRaw(params, section, key string, value json.RawMessage) string {
	var p map[string]json.RawMessage
	if json.Unmarshal([]byte(params), &p) != nil || p == nil {
		return params
	}
	sectionKey, sectionRaw, ok := jsonKeyFold(p, section)
	if !ok {
		return params
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(sectionRaw, &obj) != nil || len(obj) == 0 {
		return params
	}
	for k := range obj {
		if strings.EqualFold(k, key) {
			delete(obj, k)
		}
	}
	obj[key] = value
	objOut, err := json.Marshal(obj)
	if err != nil {
		return params
	}
	for k := range p {
		if strings.EqualFold(k, section) && k != sectionKey {
			delete(p, k)
		}
	}
	delete(p, sectionKey)
	p[section] = objOut
	out, err := json.Marshal(p)
	if err != nil {
		return params
	}
	return string(out)
}

// jsonKeyFold 在 JSON 对象里按 encoding/json 的结构体解码语义找键：优先精确匹配，其次大小写不敏感。
// 返回实际键名与原始值；找不到返回 ok=false。
func jsonKeyFold(m map[string]json.RawMessage, name string) (string, json.RawMessage, bool) {
	if raw, ok := m[name]; ok {
		return name, raw, true
	}
	for k, raw := range m {
		if strings.EqualFold(k, name) {
			return k, raw, true
		}
	}
	return "", nil, false
}
