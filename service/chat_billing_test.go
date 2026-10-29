package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"easytv-api/model"
	"easytv-api/setting"

	"github.com/DATA-DOG/go-sqlmock"
)

// ===== 免积分对话 / 每日次数在主链路上的接线（契约第 3 / 4 / 5 / 6 / 7 / 9 / 10 条）=====
//
// 全部走 sqlmock + 内存设置（写法照 service/agent_billing_test.go 与 service/ai_chat_quota_test.go）：
// 不连库、不迁移、不写真实数据。

// freeChatTestTask 免积分对话任务（StartTask 之前，params.agent.free=true）。
func freeChatTestTask() *model.GenerationTask {
	return &model.GenerationTask{
		UserId: 8, Type: model.TaskTypeText, Model: "gpt-4o-mini", Provider: "chat-test",
		Prompt: "做一条橘猫广告", Inputs: "[]",
		Params: `{"agent":{"question":"做一条橘猫广告","mode":"generate","max_rounds":3,"free":true}}`,
	}
}

// paidChatTestTask 开关关闭 / 老任务：params.agent 没有 free 标记，走旧的按轮预扣。
func paidChatTestTask() *model.GenerationTask {
	return &model.GenerationTask{
		UserId: 8, Type: model.TaskTypeText, Model: "gpt-4o-mini", Provider: "chat-test",
		Prompt: "做一条橘猫广告", Inputs: "[]",
		Params: `{"agent":{"question":"做一条橘猫广告","mode":"generate","max_rounds":3}}`,
	}
}

// chatTestProvider 只记提交次数，不触达网关；Models 供 FindProvider 命中。
type chatTestProvider struct {
	submitted int
	err       error
	models    []ModelInfo
}

func (p *chatTestProvider) Name() string { return "chat-test" }

func (p *chatTestProvider) Models() []ModelInfo {
	if len(p.models) > 0 {
		return p.models
	}
	return []ModelInfo{{Provider: "chat-test", Model: "gpt-4o-mini", Type: model.TaskTypeText, Label: "GPT-4o mini"}}
}

func (p *chatTestProvider) Submit(*model.GenerationTask) error {
	p.submitted++
	return p.err
}

// seedChatSetting 内存注入设置（不落库）；用完删掉，避免污染其他用例。
func seedChatSetting(t *testing.T, key, val string) {
	t.Helper()
	model.SetSettingCache(key, val)
	t.Cleanup(func() { model.SetSettingCache(key, "") })
}

// seedChatOffSettings 关掉免积分开关（旧计费路径）。
func seedChatOffSettings(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		setting.AgentEnabled:       "true",
		setting.ChatFreeEnabled:    "false",
		setting.ChatDailyFreeCount: "2",
	} {
		seedChatSetting(t, k, v)
	}
}

func expectChatConcurrencyOK(mock sqlmock.Sqlmock) {
	// CheckConcurrency：非会员 + 进行中任务 0
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `generation_task`").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
}

func expectChatTaskInserted(mock sqlmock.Sqlmock, taskId int64) {
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `generation_task`").WillReturnResult(sqlmock.NewResult(taskId, 1))
	mock.ExpectCommit()
	// 首个任务埋点：已记过，不再写事件
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `track_event`").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
}

// TestStartTaskFreeChatQuotaExceededRejects 次数用完：直接返回 ErrChatQuotaExceeded，
// 不建任务、不提交网关；返回的配额视图带真实用量。
func directorChatTestTask() *model.GenerationTask {
	return &model.GenerationTask{
		UserId: 8, Type: model.TaskTypeText, Model: "gpt-4o-mini", Provider: "chat-test",
		Prompt: "加一个角色放到右边", Inputs: "[]",
		Params: `{"chat":{"kind":"director","free":true}}`,
	}
}

func TestStartTaskDirectorChatQuotaExceededRejects(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)

	expectChatConcurrencyOK(mock)
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectExec(chatQuotaIncrRe).
		WithArgs(8, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 2, 2, 2, 2, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(chatQuotaReadRe).WithArgs(8, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	provider := &chatTestProvider{}
	_, err := StartTask(directorChatTestTask(), provider)
	if !errors.Is(err, ErrChatQuotaExceeded) {
		t.Fatalf("导演助手次数用完应返回 ErrChatQuotaExceeded，得到 %v", err)
	}
	if provider.submitted != 0 {
		t.Fatalf("次数用完不该提交网关，实际 %d 次", provider.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

func TestStartTaskDirectorChatNoPoints(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)

	expectChatConcurrencyOK(mock)
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectExec(chatQuotaIncrRe).
		WithArgs(8, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 2, 2, 2, 2, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(chatQuotaReadRe).WithArgs(8, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	expectChatTaskInserted(mock, 42)

	provider := &chatTestProvider{}
	task, err := StartTask(directorChatTestTask(), provider)
	if err != nil {
		t.Fatalf("start director chat: %v", err)
	}
	if task.Id != 42 || task.PointsFrozen != 0 {
		t.Fatalf("导演助手免费任务应入库且不预扣积分：id=%d frozen=%d", task.Id, task.PointsFrozen)
	}
	if day, tagged := ChatQuotaDayOf(task); !tagged || day == "" {
		t.Fatalf("应记下消费日：%s", task.Params)
	}
	if provider.submitted != 1 {
		t.Fatalf("应提交网关 1 次，实际 %d", provider.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet（导演助手免费任务不许碰 model_price / point_lot / point_log）: %v", err)
	}
}

func TestStartTaskFreeChatQuotaExceededRejects(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)

	expectChatConcurrencyOK(mock)
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectExec(chatQuotaIncrRe).
		WithArgs(8, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 2, 2, 2, 2, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0)) // 到顶：行没动
	mock.ExpectQuery(chatQuotaReadRe).WithArgs(8, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	provider := &chatTestProvider{}
	_, err := StartTask(freeChatTestTask(), provider)
	if !errors.Is(err, ErrChatQuotaExceeded) {
		t.Fatalf("次数用完应返回 ErrChatQuotaExceeded，得到 %v", err)
	}
	if provider.submitted != 0 {
		t.Fatalf("次数用完不该提交网关，实际 %d 次", provider.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestStartTaskFreeChatInsertFailureRefundsQuota 入库失败：把已经扣掉的那一次退回去（不写 point_log）。
func TestStartTaskFreeChatInsertFailureRefundsQuota(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)

	expectChatConcurrencyOK(mock)
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectExec(chatQuotaIncrRe).
		WithArgs(8, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 2, 2, 2, 2, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(chatQuotaReadRe).WithArgs(8, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `generation_task`").WillReturnError(errors.New("db down"))
	mock.ExpectRollback()
	// 退还一次：DecrChatUsage 的 count > 0 条件保证不会退成负数
	mock.ExpectExec(chatQuotaDecrRe).WithArgs(sqlmock.AnyArg(), 8, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	provider := &chatTestProvider{}
	_, err := StartTask(freeChatTestTask(), provider)
	var modelErr *ErrTaskModel
	if !errors.As(err, &modelErr) {
		t.Fatalf("入库失败应返回 ErrTaskModel，得到 %v", err)
	}
	if provider.submitted != 0 {
		t.Fatalf("入库失败不该提交网关，实际 %d 次", provider.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// chatQuotaCreatedAt 免积分任务创建（= 消费）时刻：2026-09-20 11:00 Asia/Shanghai，与
// ai_chat_quota_test.go 的 chatQuotaDay 同一天——退次数必须退消费日（任务创建日），不是退款日。
const chatQuotaCreatedAt = int64(1789873200)

// freeTaskRow 免积分任务行（points_frozen=0、payer_id=0、params 带 free、created_at=消费时刻）。
func freeTaskRow(status string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "user_id", "type", "payer_id", "points_frozen", "task_no", "status", "params", "created_at"}).
		AddRow(41, 8, model.TaskTypeText, 0, 0, "GT-041", status,
			`{"agent":{"question":"做一条橘猫广告","max_rounds":3,"free":true}}`, chatQuotaCreatedAt)
}

// TestFailTaskRefundsChatQuotaOnce 失败路径退次数：payer_id 条件更新（0→user_id）成功才退一次，
// 且扣减与状态 / 标记在同一事务里提交（不是提交后才补）；重复 FailTask 在状态条件更新就失败回滚，不会再退。
func TestFailTaskRefundsChatQuotaOnce(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_task` SET .*WHERE id = \\? AND status IN").
		WillReturnResult(sqlmock.NewResult(41, 1))
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(freeTaskRow(model.TaskStatusRunning))
	// 退次数闸门：payer_id 从 0 置成 user_id（免积分任务没设过买单账户）
	mock.ExpectExec("UPDATE `generation_task` SET `payer_id`=\\? WHERE id = \\? AND payer_id = 0").
		WithArgs(8, 41).WillReturnResult(sqlmock.NewResult(41, 1))
	// 扣减在事务内、退消费日
	mock.ExpectExec(chatQuotaDecrRe).WithArgs(sqlmock.AnyArg(), 8, chatQuotaDay).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	// 失败审计的任务回读
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(freeTaskRow(model.TaskStatusFailed))

	if err := FailTask(41, "生成失败"); err != nil {
		t.Fatalf("fail task: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}

	// 第二次：任务已不在进行中，状态条件更新 0 行 → 回滚，不再退次数
	mock = chatQuotaTestDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_task` SET .*WHERE id = \\? AND status IN").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	if err := FailTask(41, "生成失败"); err == nil {
		t.Fatal("重复 FailTask 应返回状态错误")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestFailTaskRefundsChatQuotaWithHostileParamsText 幂等闸门挂在列上而不是 params 文本上：
// 客户端可以在 params 里带空格写 {"free": true}（IsFreeChatTask 用 JSON 解析，判定为免费任务），
// 也可以在 question 里出现字面量 quota_refunded；两种都不许让失败任务的次数白掉。
func TestFailTaskRefundsChatQuotaWithHostileParamsText(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)

	hostileParams := `{"agent": {"question":"quota_refunded 这个词什么意思？","max_rounds": 3,"free": true}}`
	row := func(payerId int, status string) *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "user_id", "type", "payer_id", "points_frozen", "task_no", "status", "params", "created_at"}).
			AddRow(44, 8, model.TaskTypeText, payerId, 0, "GT-044", status, hostileParams, chatQuotaCreatedAt)
	}

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_task` SET .*WHERE id = \\? AND status IN").WillReturnResult(sqlmock.NewResult(44, 1))
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(row(0, model.TaskStatusRunning))
	// 列闸门：与 params 里的空格 / 字面量无关，照样能判定「还没退过」
	mock.ExpectExec("UPDATE `generation_task` SET `payer_id`=\\? WHERE id = \\? AND payer_id = 0").
		WithArgs(8, 44).WillReturnResult(sqlmock.NewResult(44, 1))
	mock.ExpectExec(chatQuotaDecrRe).WithArgs(sqlmock.AnyArg(), 8, chatQuotaDay).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(row(8, model.TaskStatusFailed))

	if err := FailTask(44, "生成失败"); err != nil {
		t.Fatalf("fail task: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestFailTaskQuotaRefundIsAtomic 退次数与状态 / 标记同一事务（复核发现 low）：
// 扣减失败时整体回滚，不落「标记已落库、次数却没退」又无人补偿的中间态；随后重试能完整退一次。
func TestFailTaskQuotaRefundIsAtomic(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	if got := model.ChatUsageDay(time.Unix(chatQuotaCreatedAt, 0)); got != chatQuotaDay {
		t.Fatalf("fixture 日期不对：%s != %s", got, chatQuotaDay)
	}

	// 第一次：扣减失败 → 状态 / 标记 / 扣减一起回滚
	mock := chatQuotaTestDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_task` SET .*WHERE id = \\? AND status IN").
		WillReturnResult(sqlmock.NewResult(41, 1))
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(freeTaskRow(model.TaskStatusRunning))
	mock.ExpectExec("UPDATE `generation_task` SET `payer_id`=\\? WHERE id = \\? AND payer_id = 0").
		WithArgs(8, 41).WillReturnResult(sqlmock.NewResult(41, 1))
	mock.ExpectExec(chatQuotaDecrRe).WithArgs(sqlmock.AnyArg(), 8, chatQuotaDay).
		WillReturnError(errors.New("db down"))
	mock.ExpectRollback()

	if err := FailTask(41, "生成失败"); err == nil {
		t.Fatal("退次数失败必须让整个事务回滚，不能留下「标记已落库、次数没退」的中间态")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}

	// 重试：任务仍在进行中（上次整体回滚），状态 / 标记 / 次数一起提交
	mock = chatQuotaTestDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_task` SET .*WHERE id = \\? AND status IN").
		WillReturnResult(sqlmock.NewResult(41, 1))
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(freeTaskRow(model.TaskStatusRunning))
	mock.ExpectExec("UPDATE `generation_task` SET `payer_id`=\\? WHERE id = \\? AND payer_id = 0").
		WithArgs(8, 41).WillReturnResult(sqlmock.NewResult(41, 1))
	mock.ExpectExec(chatQuotaDecrRe).WithArgs(sqlmock.AnyArg(), 8, chatQuotaDay).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(freeTaskRow(model.TaskStatusFailed))

	if err := FailTask(41, "生成失败"); err != nil {
		t.Fatalf("retry fail task: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestCancelTaskRefundsChatQuotaOnce 取消路径退次数：与积分退款同一幂等思路，重复取消只退一次。
func TestCancelTaskRefundsChatQuotaOnce(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)

	// CancelTask 预读（记 frozen 金额）
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(freeTaskRow(model.TaskStatusRunning))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_task` SET .*WHERE id = \\? AND user_id = \\? AND status IN").
		WillReturnResult(sqlmock.NewResult(41, 1))
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(freeTaskRow(model.TaskStatusRunning))
	mock.ExpectExec("UPDATE `generation_task` SET `payer_id`=\\? WHERE id = \\? AND payer_id = 0").
		WithArgs(8, 41).WillReturnResult(sqlmock.NewResult(41, 1))
	mock.ExpectExec(chatQuotaDecrRe).WithArgs(sqlmock.AnyArg(), 8, chatQuotaDay).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(freeTaskRow(model.TaskStatusCancelled))

	if err := CancelTask(8, 41); err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}

	// 第二次取消：状态条件更新 0 行 → ErrTaskStatus，不再退次数
	mock = chatQuotaTestDB(t)
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(freeTaskRow(model.TaskStatusCancelled))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_task` SET .*WHERE id = \\? AND user_id = \\? AND status IN").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	if err := CancelTask(8, 41); err == nil {
		t.Fatal("重复取消应返回状态错误")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestStartTaskFreeChatMemberNoCounting 会员不限次：StartTask 完全不碰 ai_chat_usage（读写都没有），
// 也不预扣积分。
func TestStartTaskFreeChatMemberNoCounting(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)

	// CheckConcurrency：会员 → QuotaFor 查会员 + 套餐
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(memberRows(8))
	mock.ExpectQuery("SELECT .* FROM `membership_plan` WHERE id = \\?").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `generation_task`").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// ConsumeChatQuota：会员 → 不登记任何 ai_chat_usage 期望；真去读写会让 sqlmock 报未预期调用
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(memberRows(8))
	expectChatTaskInserted(mock, 42)

	provider := &chatTestProvider{}
	task, err := StartTask(freeChatTestTask(), provider)
	if err != nil {
		t.Fatalf("member start: %v", err)
	}
	if task.Id != 42 || task.PointsFrozen != 0 {
		t.Fatalf("会员免积分任务 = id=%d frozen=%d", task.Id, task.PointsFrozen)
	}
	if provider.submitted != 1 {
		t.Fatalf("应提交网关 1 次，实际 %d", provider.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet（会员不该写 ai_chat_usage）: %v", err)
	}
}

// TestSettleAgentLoopFreeSkipsSettlement 免费模式结算：整段跳过，cost_points 保持 0。
// 空 sqlmock：一旦走 AgentSettlePoints 会因未预期的 model_price 查询失败，回退成 PointsFrozen(6) → 断言失败。
func TestSettleAgentLoopFreeSkipsSettlement(t *testing.T) {
	agentTestDB(t)
	free := &model.GenerationTask{
		Id: 41, UserId: 8, Type: model.TaskTypeText, Model: "gpt-4o-mini", PointsFrozen: 6,
		Params: `{"agent":{"question":"做一条橘猫广告","max_rounds":6,"free":true}}`,
	}
	if got := settleAgentLoop(free, 3, false); got != 0 {
		t.Fatalf("免积分对话不该结算：got %d, want 0", got)
	}
}

// TestSettleAgentLoopPaidTaskSettlesByRounds 开关关闭 / 老任务：保持按实际轮数结算（6 轮预扣、实用 3 轮 → 3 分）。
func TestSettleAgentLoopPaidTaskSettlesByRounds(t *testing.T) {
	mock := agentTestDB(t)
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\? AND status = \\?").WillReturnRows(
		agentPriceRow("gpt-4o-mini", model.TaskTypeText, "count", 1))
	paid := &model.GenerationTask{
		Id: 42, UserId: 8, Type: model.TaskTypeText, Model: "gpt-4o-mini", PointsFrozen: 2,
		Params: `{"agent":{"question":"做一条橘猫广告","max_rounds":6}}`,
	}
	if got := settleAgentLoop(paid, 3, false); got != 3 {
		t.Fatalf("旧口径应按实际 3 轮结算，got %d", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestRunAgentRoundsPassesChatParamsFromSettings 会话参数：ChatMaxTokens / ChatThinkingMode /
// ChatThinkingEffort 从 setting 读出来传给 ChatWithOptions。
func TestRunAgentRoundsPassesChatParamsFromSettings(t *testing.T) {
	seedChatSetting(t, setting.ChatMaxTokens, "512")
	seedChatSetting(t, setting.ChatThinkingMode, "on")
	seedChatSetting(t, setting.ChatThinkingEffort, "high")

	chatter := &agentScriptChatter{replies: []string{agentGoodPlanReply}}
	in := agentLoopInput(chatter)
	in.MaxRounds = 1
	agentRunLoop(t, chatter, in, NoopAgentStepSink{})

	want := ChatParams{MaxTokens: 512, ThinkingMode: "on", ThinkingEffort: "high"}
	if got := chatter.optsAt(0); got != want {
		t.Fatalf("对话参数 = %+v, want %+v", got, want)
	}
}

// TestRunAgentRoundsAddsOnlyOnOnThinking 非法 / 缺省值按不发送处理：缺省 → 全零参数。
func TestRunAgentRoundsChatParamsDefaultsToZero(t *testing.T) {
	chatter := &agentScriptChatter{replies: []string{agentGoodPlanReply}}
	in := agentLoopInput(chatter)
	in.MaxRounds = 1
	agentRunLoop(t, chatter, in, NoopAgentStepSink{})
	if got := chatter.optsAt(0); got != (ChatParams{}) {
		t.Fatalf("缺省配置应传零值参数，得到 %+v", got)
	}
}

// TestRunAgentRoundsInjectsKnowledgeAndFacts 提示词注入：平台知识（setting）与平台事实（调用方注入）都进提示词。
func TestRunAgentRoundsInjectsKnowledgeAndFacts(t *testing.T) {
	seedChatSetting(t, setting.ChatKnowledge, "会员每月赠送 300 积分")
	chatter := &agentScriptChatter{replies: []string{agentGoodPlanReply}}
	in := agentLoopInput(chatter)
	in.MaxRounds = 1
	in.Facts = "当前积分余额：12 分"
	agentRunLoop(t, chatter, in, NoopAgentStepSink{})

	prompt := chatter.promptAt(0)
	for _, want := range []string{"【平台知识】\n会员每月赠送 300 积分", "【平台事实】\n当前积分余额：12 分"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("提示词缺少 %q：\n%s", want, prompt)
		}
	}
}

// TestAgentModelAdminSettingWins 模型优先级（契约第 1 条）：后台配了 AgentModel 就以后台为准，
// 前端传的模型不参与（连它的定价都不会查）。
func TestAgentModelAdminSettingWins(t *testing.T) {
	seedChatSetting(t, setting.AgentModel, "admin-model")
	provider := &chatTestProvider{models: []ModelInfo{
		{Provider: "chat-test", Model: "admin-model", Type: model.TaskTypeText, Label: "后台模型"},
		{Provider: "chat-test", Model: "frontend-model", Type: model.TaskTypeText, Label: "前端模型"},
	}}
	RegisterProvider(provider)
	t.Cleanup(func() { RemoveProvider(provider.Name()) })

	mock := chatQuotaTestDB(t)
	// 只允许查后台模型的定价；若实现反过来先查 frontend-model，WithArgs 不匹配 → 走 planModel → 断言失败
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\? AND status = \\?").
		WithArgs("admin-model", 1).WillReturnRows(agentPriceRow("admin-model", model.TaskTypeText, "count", 1))

	if got := AgentModel("frontend-model"); got != "admin-model" {
		t.Fatalf("后台配置的模型应优先，得到 %q", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestAgentModelRejectsUnpricedAdminSetting 后台配置的模型「已定价 + 已接入」缺一不可：
// 定价被删 / 下架（GetModelPrice 的 SQL 带 status=1，读不到）时 GetAgentModel 返回空，
// 必须回退到请求里的模型，而不是硬用后台模型（后台模型只在创建时校验过定价）。
func TestAgentModelRejectsUnpricedAdminSetting(t *testing.T) {
	seedChatSetting(t, setting.AgentModel, "admin-model")
	provider := &chatTestProvider{models: []ModelInfo{
		{Provider: "chat-test", Model: "admin-model", Type: model.TaskTypeText, Label: "后台模型"},
		{Provider: "chat-test", Model: "frontend-model", Type: model.TaskTypeText, Label: "前端模型"},
	}}
	RegisterProvider(provider)
	t.Cleanup(func() { RemoveProvider(provider.Name()) })

	mock := chatQuotaTestDB(t)
	// 后台模型查不到生效中的定价（已删 / status=0）：GetAgentModel 必须返回空
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\? AND status = \\?").
		WithArgs("admin-model", 1).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	// 请求里的模型已定价 → 回退到它
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\? AND status = \\?").
		WithArgs("frontend-model", 1).WillReturnRows(agentPriceRow("frontend-model", model.TaskTypeText, "count", 1))

	if got := AgentModel("frontend-model"); got != "frontend-model" {
		t.Fatalf("后台模型未定价时必须回退到请求模型，得到 %q", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestAgentModelFallsBackToRequestedWhenSettingEmpty 没配 AgentModel 时沿用前端传的（已定价 text 模型）；
// 未定价 / 非 text 的请求模型会被拒（本用例只覆盖前者：config 未配置 → 回查 DB 得到空）。
func TestAgentModelFallsBackToRequestedWhenSettingEmpty(t *testing.T) {
	provider := &chatTestProvider{models: []ModelInfo{
		{Provider: "chat-test", Model: "admin-model", Type: model.TaskTypeText, Label: "后台模型"},
		{Provider: "chat-test", Model: "frontend-model", Type: model.TaskTypeText, Label: "前端模型"},
	}}
	RegisterProvider(provider)
	t.Cleanup(func() { RemoveProvider(provider.Name()) })

	mock := chatQuotaTestDB(t)
	// AgentModel 未配置：setting 回查 DB 得到空
	mock.ExpectQuery("SELECT .* FROM `setting` WHERE `key` = \\?").WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\? AND status = \\?").
		WithArgs("frontend-model", 1).WillReturnRows(agentPriceRow("frontend-model", model.TaskTypeText, "count", 1))

	if got := AgentModel("frontend-model"); got != "frontend-model" {
		t.Fatalf("未配置后台模型时应沿用请求里的模型，得到 %q", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestAgentFactsFor 平台事实的数据来源（契约第 3 条）：会员状态 / 剩余次数 / 免费开关 / 积分余额。
func TestAgentFactsFor(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)

	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectQuery(chatQuotaReadRe).WithArgs(8, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(remaining\\),0\\).*FROM `point_lot`").
		WillReturnRows(sqlmock.NewRows([]string{"Total"}).AddRow(12))

	facts := agentFactsFor(8, 0, true)
	for _, want := range []string{"不是会员", "本次对话是免费对话，不扣积分", "剩余 1 次", "每日免费 2 次", "积分余额：12 分"} {
		if !strings.Contains(facts, want) {
			t.Fatalf("平台事实缺少 %q：\n%s", want, facts)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}

	// freeChat 由调用方按「任务实际算不算免费」传入，不许这里再读实时开关：
	// 同一份只读数据传 false 时口径必须是「不是免费对话」
	mock = chatQuotaTestDB(t)
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectQuery(chatQuotaReadRe).WithArgs(8, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(remaining\\),0\\).*FROM `point_lot`").
		WillReturnRows(sqlmock.NewRows([]string{"Total"}).AddRow(12))

	paidFacts := agentFactsFor(8, 0, false)
	if !strings.Contains(paidFacts, "本次对话不是免费对话，按积分计费") {
		t.Fatalf("freeChat=false 时口径应写「不是免费对话」：\n%s", paidFacts)
	}
	if strings.Contains(paidFacts, "不扣积分") {
		t.Fatalf("freeChat=false 时不该说免费：\n%s", paidFacts)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestRunAgentLoopFactsFollowTaskFreeFlag 在途免费任务的【平台事实】按任务落库的 free 判定，不按实时开关：
// 任务创建后运营把 ChatFreeEnabled 翻成 false，仍在跑的免费任务提示词必须说「本次对话是免费对话，不扣积分」。
func TestRunAgentLoopFactsFollowTaskFreeFlag(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	// 任务创建时免费；提交之后运营关掉开关（事实若读实时开关就会说「不是免费对话」）
	model.SetSettingCache(setting.ChatFreeEnabled, "false")
	t.Cleanup(func() { model.SetSettingCache(setting.ChatFreeEnabled, "") })

	mock := chatQuotaTestDB(t)
	// RunAgentLoop 起步的只读查询：任务（cancelled 检查）→ 画布 → 会员 ×2 → 今日次数（只读）→ 积分余额
	taskRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "user_id", "type", "canvas_id", "model", "status", "params", "points_frozen", "payer_id", "task_no", "created_at"}).
			AddRow(41, 8, model.TaskTypeText, 5, "gpt-4o-mini", model.TaskStatusRunning,
				`{"agent":{"question":"搭个图","mode":"generate","max_rounds":2,"free":true}}`, 0, 0, "GT-041", chatQuotaCreatedAt)
	}
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(taskRows())
	mock.ExpectQuery("SELECT .* FROM `canvas` WHERE id = \\?").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectQuery(chatQuotaReadRe).WithArgs(8, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(remaining\\),0\\).*FROM `point_lot`").
		WillReturnRows(sqlmock.NewRows([]string{"Total"}).AddRow(7))
	// RunAgentRounds 第 1 轮开始前的取消检查，然后才是模型目录
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(taskRows())
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE status = \\?").
		WillReturnRows(sqlmock.NewRows([]string{"id", "model", "type", "label", "unit", "points_per_unit", "member_points_per_unit", "status"}))

	chatter := &agentScriptChatter{replies: []string{`{"reply":"好的","changes":[]}`}}
	RunAgentLoop(&model.GenerationTask{
		Id: 41, UserId: 8, CanvasId: 5, Type: model.TaskTypeText, Model: "gpt-4o-mini",
		Status: model.TaskStatusRunning,
		Params: `{"agent":{"question":"搭个图","mode":"generate","max_rounds":2,"free":true}}`,
	}, chatter)

	if chatter.calls() == 0 {
		t.Fatal("循环没有调用模型")
	}
	prompt := chatter.promptAt(0)
	if !strings.Contains(prompt, "【平台事实】\n") || !strings.Contains(prompt, "本次对话是免费对话，不扣积分") {
		t.Fatalf("在途免费任务的平台事实必须说免费：\n%s", prompt)
	}
	if strings.Contains(prompt, "不是免费对话") {
		t.Fatalf("提示词按实时开关说「不是免费对话」了：\n%s", prompt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestAgentFactsForOmitsUnverifiedRemaining 次数读不到时不编造「剩余」：
// GetChatUsage 失败 → 整段【平台事实】不输出（旧兜底会把配置上限当剩余次数写给模型）。
func TestAgentFactsForOmitsUnverifiedRemaining(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)

	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectQuery(chatQuotaReadRe).WithArgs(8, sqlmock.AnyArg()).WillReturnError(errors.New("db down"))

	facts := agentFactsFor(8, 0, true)
	if facts != "" {
		t.Fatalf("次数读不到时不该输出平台事实（旧兜底会编「剩余=上限」）：\n%s", facts)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestAgentFactsForUsesTeamOwnerWallet 团队空间任务：平台事实里的积分余额必须是团队 owner 的积分池
// （与 WalletView 团队视图、PayerForTeam 的扣费口径一致），不是成员自己的钱包。
func TestAgentFactsForUsesTeamOwnerWallet(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)

	// GetChatQuota(8, 3)：IsMemberForPricing → PayerForTeam → 成员 + 团队 + owner 团队版会员
	mock.ExpectQuery("SELECT .* FROM `team_member` WHERE user_id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "team_id", "user_id", "role"}).AddRow(2, 3, 8, model.TeamRoleMember))
	mock.ExpectQuery("SELECT .* FROM `team` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "owner_id"}).AddRow(3, 9))
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "status", "expires_at", "edition", "seats"}).
			AddRow(1, 9, model.MembershipStatusActive, int64(1893456000), model.EditionTeam, 1))
	// 余额查 owner(9) 的钱包，不是成员(8) 自己的
	mock.ExpectQuery("SELECT .* FROM `team` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "owner_id"}).AddRow(3, 9))
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(remaining\\),0\\).*FROM `point_lot`").
		WithArgs(9, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"Total"}).AddRow(77))

	facts := agentFactsFor(8, 3, true)
	for _, want := range []string{"是会员", "本次对话是免费对话，不扣积分", "积分余额：77 分"} {
		if !strings.Contains(facts, want) {
			t.Fatalf("团队空间的平台事实缺少 %q：\n%s", want, facts)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestNewAgentRetryTaskWritesFreeFlag 创建时把开关状态写进 params.agent.free：
// 之后运营翻转开关（模拟为 SettingCache 改成 false）不影响在途 / 已建任务。
func TestNewAgentRetryTaskWritesFreeFlag(t *testing.T) {
	seedChatSetting(t, setting.AgentEnabled, "true")
	seedChatSetting(t, setting.ChatFreeEnabled, "true")
	provider := &chatTestProvider{}
	RegisterProvider(provider)
	t.Cleanup(func() { RemoveProvider(provider.Name()) })

	cv := &model.Canvas{Id: 5, UserId: 8}
	src := &model.GenerationTask{
		Id: 41, UserId: 8, CanvasId: 5, Type: model.TaskTypeText, Model: "gpt-4o-mini",
		Params: `{"agent":{"question":"做个片子","max_rounds":3,"free":true}}`,
	}
	task, _, err := NewAgentRetryTask(8, cv, src, "再试一次", 1)
	if err != nil {
		t.Fatalf("retry task: %v", err)
	}
	var p agentTaskParams
	if json.Unmarshal([]byte(task.Params), &p) != nil || p.Agent == nil {
		t.Fatalf("params 解析失败：%s", task.Params)
	}
	if !p.Agent.Free || !IsFreeChatTask(task) {
		t.Fatalf("开关开启时创建的任务应带 free=true：%s", task.Params)
	}

	// 翻转开关后再建：新任务不再免费，但老任务仍按创建时的口径算免费（开关翻转不影响在途任务）
	model.SetSettingCache(setting.ChatFreeEnabled, "false")
	task2, _, err := NewAgentRetryTask(8, cv, src, "再试一次", 1)
	if err != nil {
		t.Fatalf("retry task 2: %v", err)
	}
	if p2 := task2.Params; strings.Contains(p2, `"free":true`) || IsFreeChatTask(task2) {
		t.Fatalf("开关关闭后创建的任务不该再带 free：%s", p2)
	}
	if !IsFreeChatTask(task) {
		t.Fatal("在途任务仍应按创建时的 free 标记算免费")
	}
}

// TestWriteChatQuotaDayRoundTrip 消费日标记：写进 params.agent.quota_day，读回区分「老任务没标记 / 标记为空没消费 / 消费日」。
func TestWriteChatQuotaDayRoundTrip(t *testing.T) {
	base := `{"agent":{"question":"做个片子","max_rounds":3,"free":true}}`
	if _, tagged := ChatQuotaDayOf(&model.GenerationTask{Params: base}); tagged {
		t.Fatal("老任务没有标记")
	}
	withDay := WriteChatQuotaDay(base, "2026-09-24")
	if day, tagged := ChatQuotaDayOf(&model.GenerationTask{Params: withDay}); !tagged || day != "2026-09-24" {
		t.Fatalf("应读回消费日：%s", withDay)
	}
	if !IsFreeChatTask(&model.GenerationTask{Type: model.TaskTypeText, Params: withDay}) {
		t.Fatalf("写标记不应破坏 free：%s", withDay)
	}
	empty := WriteChatQuotaDay(withDay, "")
	if day, tagged := ChatQuotaDayOf(&model.GenerationTask{Params: empty}); !tagged || day != "" {
		t.Fatalf("空标记 = 没消费：%s", empty)
	}
	// 大小写变体统一成规范键
	odd := WriteChatQuotaDay(`{"Agent":{"question":"x","Quota_Day":"2026-01-01","free":true}}`, "2026-09-24")
	if strings.Contains(odd, "Quota_Day") || !strings.Contains(odd, `"quota_day":"2026-09-24"`) || strings.Contains(odd, `"Agent"`) {
		t.Fatalf("应归一成小写键：%s", odd)
	}
}

// TestFailTaskSkipsChatQuotaRefundWhenNotConsumed 创建时是会员 / 不限次（标记为空串）→ 失败不退次数，
// 连幂等闸门的 payer_id 更新都不发（没消费就没得退，复核发现 medium）。
func TestFailTaskSkipsChatQuotaRefundWhenNotConsumed(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)
	row := sqlmock.NewRows([]string{"id", "user_id", "type", "payer_id", "points_frozen", "task_no", "status", "params", "created_at"}).
		AddRow(41, 8, model.TaskTypeText, 0, 0, "GT-041", model.TaskStatusRunning,
			`{"agent":{"question":"做一条橘猫广告","max_rounds":3,"free":true,"quota_day":""}}`, chatQuotaCreatedAt)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_task` SET .*WHERE id = \\? AND status IN").
		WillReturnResult(sqlmock.NewResult(41, 1))
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(row)
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(freeTaskRow(model.TaskStatusFailed))
	if err := FailTask(41, "生成失败"); err != nil {
		t.Fatalf("fail task: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestNewAgentRetryTaskRejectsRunningSource 上一轮还在跑就不许再起一轮（每次重试都消费次数与并发位）。
func TestNewAgentRetryTaskRejectsRunningSource(t *testing.T) {
	seedChatSetting(t, setting.AgentEnabled, "true")
	cv := &model.Canvas{Id: 5, UserId: 8}
	for _, st := range []string{model.TaskStatusPending, model.TaskStatusRunning} {
		src := &model.GenerationTask{Id: 41, UserId: 8, CanvasId: 5, Type: model.TaskTypeText, Model: "gpt-4o-mini", Status: st,
			Params: `{"agent":{"question":"做个片子","max_rounds":3,"free":true}}`}
		if _, _, err := NewAgentRetryTask(8, cv, src, "", 1); err == nil || !strings.Contains(err.Error(), "还在生成中") {
			t.Fatalf("状态 %s 应拒绝重试：%v", st, err)
		}
	}
}
