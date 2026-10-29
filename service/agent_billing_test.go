package service

import (
	"testing"

	"easytv-api/model"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// ===== 计费与落库（agent-contract 2.8 / 6.1 / 6.2 / 5.4）=====
//
// 全部走 sqlmock：不连库、不迁移、不写真实数据。

func agentTestDB(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock new failed: %v", err)
	}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open failed: %v", err)
	}
	model.DB = db
	return mock
}

func agentPriceRow(modelName, taskType, unit string, points int) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "model", "type", "label", "unit", "points_per_unit", "member_points_per_unit", "status"}).
		AddRow(1, modelName, taskType, modelName, unit, points, points, 1)
}

// TestAgentRoundsInParams 读 params.agent.max_rounds：缺失/非法 → 0，超过硬上限 → 8。
func TestAgentRoundsInParams(t *testing.T) {
	cases := []struct {
		params string
		want   int
	}{
		{`{"agent":{"question":"x","max_rounds":6}}`, 6},
		{`{"agent":{"max_rounds":3}}`, 3},
		{`{"agent":{"max_rounds":99}}`, AgentMaxRoundsHardLimit},
		{`{"agent":{"max_rounds":-2}}`, 0},
		{`{"agent":{"question":"x"}}`, 0},
		{`{"count":1}`, 0},
		{`not json`, 0},
		{``, 0},
	}
	for _, tc := range cases {
		if got := AgentRoundsInParams(tc.params); got != tc.want {
			t.Fatalf("AgentRoundsInParams(%s) = %d, want %d", tc.params, got, tc.want)
		}
	}
}

// TestEstimateTaskPointsAgentRounds 预扣：text 任务按 params.agent.max_rounds 计轮数（单价 × 轮数）。
func TestEstimateTaskPointsAgentRounds(t *testing.T) {
	mock := agentTestDB(t)
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\? AND status = \\?").WillReturnRows(
		agentPriceRow("gpt-4o-mini", model.TaskTypeText, "count", 1))

	points, _, err := EstimateTaskPoints(model.TaskTypeText, "gpt-4o-mini", `{"agent":{"max_rounds":6}}`, false)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if points != 6 {
		t.Fatalf("预扣 6 轮 = %d 积分，want 6", points)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}

	// 没有 agent 段时行为不变（count 归一后仍是 1 轮）
	mock = agentTestDB(t)
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\? AND status = \\?").WillReturnRows(
		agentPriceRow("gpt-4o-mini", model.TaskTypeText, "count", 1))
	if points, _, err = EstimateTaskPoints(model.TaskTypeText, "gpt-4o-mini", `{"count":1}`, false); err != nil || points != 1 {
		t.Fatalf("普通文本任务 = %d 积分 err=%v，want 1", points, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}

	// agent.max_rounds=1 时同样是一轮
	mock = agentTestDB(t)
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\? AND status = \\?").WillReturnRows(
		agentPriceRow("gpt-4o-mini", model.TaskTypeText, "count", 1))
	if points, _, err = EstimateTaskPoints(model.TaskTypeText, "gpt-4o-mini", `{"agent":{"max_rounds":1}}`, false); err != nil || points != 1 {
		t.Fatalf("单轮 agent 任务 = %d 积分 err=%v，want 1", points, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestAgentSettlePoints 结算：按实际轮数重新估价（预扣 6 轮、实用 3 轮 → 只该收 3 轮）。
func TestAgentSettlePoints(t *testing.T) {
	mock := agentTestDB(t)
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\? AND status = \\?").WillReturnRows(
		agentPriceRow("gpt-4o-mini", model.TaskTypeText, "count", 1))
	actual, err := AgentSettlePoints(model.TaskTypeText, "gpt-4o-mini", 3, false)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if actual != 3 {
		t.Fatalf("实际 3 轮应结算 3 积分，得到 %d", actual)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
	// 0 轮按 1 轮兜底（任务至少跑了一轮）
	mock = agentTestDB(t)
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\? AND status = \\?").WillReturnRows(
		agentPriceRow("gpt-4o-mini", model.TaskTypeText, "count", 1))
	if actual, err = AgentSettlePoints(model.TaskTypeText, "gpt-4o-mini", 0, false); err != nil || actual != 1 {
		t.Fatalf("0 轮 = %d 积分 err=%v，want 1", actual, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestRefundTaskPointsPartial 按实际轮数退差：预扣 6、实收 3 → 退 3，
// points_frozen 减 3，退款批次与 unfreeze 流水金额都是 3。
func TestRefundTaskPointsPartial(t *testing.T) {
	mock := agentTestDB(t)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "payer_id", "points_frozen", "task_no", "status"}).
			AddRow(41, 8, 8, 6, "GT-041", "running"))
	mock.ExpectExec("UPDATE `generation_task` SET `points_frozen`=points_frozen - \\? WHERE id = \\? AND points_frozen >= \\?").
		WithArgs(3, 41, 3).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `point_lot`").
		WithArgs(8, model.LotSourceRefund, 0, 41, 3, 3, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(77, 1))
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(remaining\\),0\\).*FROM `point_lot`").
		WillReturnRows(sqlmock.NewRows([]string{"Total"}).AddRow(3))
	mock.ExpectExec("INSERT INTO `point_log`").
		WithArgs(8, model.PointLogUnfreeze, 3, 3, model.PointsBizTask, "GT-041", "按实际 3 轮结算，退回 3 积分", 0, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(88, 1))
	mock.ExpectCommit()

	refunded, err := RefundTaskPointsPartial(41, 3, "按实际 3 轮结算，退回 3 积分")
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if refunded != 3 {
		t.Fatalf("实际退回 = %d，want 3", refunded)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestRefundTaskPointsPartialIdempotent refund<=0 或已经退过（points_frozen=0）时不动账。
func TestRefundTaskPointsPartialIdempotent(t *testing.T) {
	agentTestDB(t)
	if n, err := RefundTaskPointsPartial(41, 0, "不退"); err != nil || n != 0 {
		t.Fatalf("refund=0 不该动账：n=%d err=%v", n, err)
	}

	mock := agentTestDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "payer_id", "points_frozen", "task_no", "status"}).
			AddRow(42, 8, 8, 0, "GT-042", "running"))
	mock.ExpectCommit()
	if n, err := RefundTaskPointsPartial(42, 5, "再退一次"); err != nil || n != 0 {
		t.Fatalf("预扣已清零时不该退：n=%d err=%v", n, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestRefundTaskPointsPartialClampsToFrozen 要求退的比剩余预扣多时，只退到 0，不让 points_frozen 变负。
func TestRefundTaskPointsPartialClampsToFrozen(t *testing.T) {
	mock := agentTestDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "payer_id", "points_frozen", "task_no", "status"}).
			AddRow(43, 8, 8, 2, "GT-043", "running"))
	mock.ExpectExec("UPDATE `generation_task` SET `points_frozen`=points_frozen - \\? WHERE id = \\? AND points_frozen >= \\?").
		WithArgs(2, 43, 2).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `point_lot`").WillReturnResult(sqlmock.NewResult(78, 1))
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(remaining\\),0\\).*FROM `point_lot`").
		WillReturnRows(sqlmock.NewRows([]string{"Total"}).AddRow(2))
	mock.ExpectExec("INSERT INTO `point_log`").WillReturnResult(sqlmock.NewResult(89, 1))
	mock.ExpectCommit()

	if n, err := RefundTaskPointsPartial(43, 9, "多退了也只退到 0"); err != nil || n != 2 {
		t.Fatalf("应退到剩余预扣 2：n=%d err=%v", n, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestInsertCanvasAgentStepFailedStep 失败步落库必须是 ok=false（OK 字段不能带 default 标签）。
func TestInsertCanvasAgentStepFailedStep(t *testing.T) {
	mock := agentTestDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `canvas_agent_step`").
		WithArgs(7, 3, 8, 2, "plan", "", "ok=false errors=1", false, "", int64(1700000000)).
		WillReturnResult(sqlmock.NewResult(5, 1))
	mock.ExpectCommit()

	step := &model.CanvasAgentStep{
		TaskId: 7, CanvasId: 3, UserId: 8, Round: 2, Tool: "plan",
		ResultSummary: "ok=false errors=1", OK: false, CreatedAt: 1700000000,
	}
	if err := model.InsertCanvasAgentStep(step); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet（失败步的 ok 必须显式写 false）: %v", err)
	}
}

// TestGetCanvasAgentStepsOrdering 步骤按轮次与写入顺序返回。
func TestGetCanvasAgentStepsOrdering(t *testing.T) {
	mock := agentTestDB(t)
	mock.ExpectQuery("SELECT .* FROM `canvas_agent_step` WHERE task_id = \\? ORDER BY round asc, id asc").WillReturnRows(
		sqlmock.NewRows([]string{"id", "task_id", "round", "tool", "result_summary", "ok"}).
			AddRow(1, 7, 1, "list_templates", "命中 2 条", true).
			AddRow(2, 7, 2, "plan", "ok=true", true))
	steps, err := model.GetCanvasAgentSteps(7)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(steps) != 2 || steps[0].Round != 1 || steps[1].Tool != "plan" {
		t.Fatalf("steps = %+v", steps)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// ===== 免积分对话的新计费口径（契约第 4 / 5 / 8 条）=====
//
// 开关开启（缺省）时对话任务不再走积分：points_frozen 保持 0、不写 point_log、结算跳过（cost_points=0），
// 改为一次每日对话次数；开关关闭时保持旧的按轮预扣与退差（TestSettleAgentLoopPaidTaskSettlesByRounds 覆盖退差口径）。

// TestStartTaskFreeChatNoPoints 免费模式：StartTask 扣一次次数、不估价、不预扣积分、不写 point_log。
// 任何计费分支被走到都会让 sqlmock 报未预期调用（model_price / point_lot / point_log），StartTask 随即报错。
func TestStartTaskFreeChatNoPoints(t *testing.T) {
	seedChatQuotaSettings(t, "2")
	mock := chatQuotaTestDB(t)

	expectChatConcurrencyOK(mock)
	// ConsumeChatQuota：非会员 → 原子自增一次（limit=2）
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectExec(chatQuotaIncrRe).
		WithArgs(8, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 2, 2, 2, 2, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(chatQuotaReadRe).WithArgs(8, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	expectChatTaskInserted(mock, 41)

	provider := &chatTestProvider{}
	task, err := StartTask(freeChatTestTask(), provider)
	if err != nil {
		t.Fatalf("start free chat task: %v", err)
	}
	if task.Id != 41 || task.PointsFrozen != 0 {
		t.Fatalf("免积分任务应入库且 points_frozen=0：id=%d frozen=%d", task.Id, task.PointsFrozen)
	}
	// 结算口径：cost_points 由 PointsFrozen 写进任务（CompleteTask），免费模式恒为 0
	if got := settleAgentLoop(task, 3, false); got != 0 {
		t.Fatalf("免积分任务 cost_points 应保持 0，得到 %d", got)
	}
	if provider.submitted != 1 {
		t.Fatalf("应提交网关 1 次，实际 %d", provider.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet（免积分任务不许碰 model_price / point_lot / point_log）: %v", err)
	}
}

// TestStartTaskPaidChatKeepsOldBilling ChatFreeEnabled=false：不消费次数，保持旧的按轮预扣
// （3 轮 → 预扣 3 分、写 freeze 流水与 points_frozen）。
func TestStartTaskPaidChatKeepsOldBilling(t *testing.T) {
	seedChatOffSettings(t)
	mock := chatQuotaTestDB(t)

	expectChatConcurrencyOK(mock)
	// 估价：非会员再查一次会员，text 1 分/轮 × 3 轮
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(nonMemberRows())
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\? AND status = \\?").WillReturnRows(
		agentPriceRow("gpt-4o-mini", model.TaskTypeText, "count", 1))
	expectChatTaskInserted(mock, 43)
	// 预扣 3 分（照 tests/integration_test.go TestTaskCreateFreezeAndRefund 的期望写法）
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `point_lot`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "remaining", "expires_at"}).AddRow(7, 8, 100, 1893456000))
	mock.ExpectExec("UPDATE `point_lot` SET `remaining`=remaining - \\?").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(remaining\\),0\\).*FROM `point_lot`").
		WillReturnRows(sqlmock.NewRows([]string{"Total"}).AddRow(97))
	mock.ExpectExec("INSERT INTO `point_log`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE `generation_task` SET `payer_id`=\\?,`points_frozen`=\\?").WillReturnResult(sqlmock.NewResult(43, 1))
	mock.ExpectCommit()

	provider := &chatTestProvider{}
	task, err := StartTask(paidChatTestTask(), provider)
	if err != nil {
		t.Fatalf("paid start: %v", err)
	}
	if task.PointsFrozen != 3 {
		t.Fatalf("旧计费路径应预扣 3 分，得到 %d", task.PointsFrozen)
	}
	if provider.submitted != 1 {
		t.Fatalf("应提交网关 1 次，实际 %d", provider.submitted)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet（开关关闭时不许碰 ai_chat_usage）: %v", err)
	}
}
