package tests

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/service/providers"

	"github.com/DATA-DOG/go-sqlmock"
)

// ===== 生成链路：HTTP 创建任务 → 预扣积分 → 提交失败 → 自动退回（全同步路径）=====

// TestTaskCreateFreezeAndRefund 模拟用户创建生成任务：
// 1) 鉴权通过；2) 会员判断（个人空间只看自己的会员 → 自己买单）；3) 定价估算（text 1 积分）；
// 4) INSERT 任务；5) 积分批次预扣 freeze 流水并回写 points_frozen；
// 6) provider.Submit 因空 prompt 同步报错（不触达网关）→ FailTask：置 failed + refund 批次/流水。
// 断言响应与所有 SQL 执行顺序。
func TestTaskCreateFreezeAndRefund(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()

	// 注册文本模型 provider（gpt-4o-mini）供 FindProvider 命中（不实际访问网关）
	p := providers.NewEasyAPIProvider("http://127.0.0.1:1", "sk-fake",
		providers.ParseModelList(model.TaskTypeText, "gpt-4o-mini:GPT-4o mini"))
	service.RegisterProvider(p)

	headers := map[string]string{
		"Authorization": "Bearer e2e-token",
		"EasyTV-User":   "8",
	}

	// 1. 鉴权：ValidateAccessToken 读用户
	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "username", "role", "status", "group", "access_token"}).
			AddRow(8, "e2e@easytv.ai", 1, 1, "default", "e2e-token"))

	// 2. 个人空间（不带 EasyTV-Team）：不查团队关系；额度 / 会员价只看自己的会员（无会员）
	mock.ExpectQuery("SELECT .* FROM `membership`").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	// 3. 定价：text gpt-4o-mini = 1 积分/count
	mock.ExpectQuery("SELECT .* FROM `model_price`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "model", "type", "label", "unit", "points_per_unit", "member_points_per_unit", "status"}).
			AddRow(1, "gpt-4o-mini", "text", "GPT-4o mini", "count", 1, 1, 1))

	// 4. 插入任务（返回 id=1；GORM 默认事务包裹）
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `generation_task`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	// 首个任务埋点：已记过，不再写事件
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `track_event`").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// 5. FreezeTaskPoints：任务 team_id=0 → 自己买单，不查团队
	mock.ExpectBegin()
	// 预扣批次：查到 1 个批次 remaining=100 → 扣 1 → 流水 balance_after=99
	mock.ExpectQuery("SELECT .* FROM `point_lot`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "remaining", "expires_at"}).AddRow(7, 8, 100, time.Now().Add(24*time.Hour).Unix()))
	mock.ExpectExec("UPDATE `point_lot` SET `remaining`=remaining - \\?").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(remaining\\),0\\).*FROM `point_lot`").WillReturnRows(
		sqlmock.NewRows([]string{"Total"}).AddRow(99))
	mock.ExpectExec("INSERT INTO `point_log`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE `generation_task` SET `payer_id`=\\?,`points_frozen`=\\?").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	// 6. Submit（prompt 为空 → 同步报错）→ controller 同步 FailTask 并返回失败视图；
	//    退款落库细节由 service 层测试（TestFailTaskRefund）精确断言，这里只验证 HTTP 契约。

	code, resp := do(t, r, "POST", "/user/task", `{"type":"text","model":"gpt-4o-mini","prompt":"","params":{}}`, headers)
	t.Logf("resp: code=%d resp=%v", code, resp)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("create task: code=%d resp=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data["status"] != "failed" || data["error_msg"] != "请填写提示词" {
		t.Fatalf("task should be failed after empty-prompt submit: %v", data)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

// TestTaskFailRefund 失败退回链路：置失败与退回预扣积分在同一事务里提交（service 层精确断言）。
func TestTaskFailRefund(t *testing.T) {
	mock := initMockDB(t)

	mock.ExpectBegin()
	// 1. 置失败
	mock.ExpectExec("UPDATE `generation_task` SET .*WHERE id = \\? AND status IN \\(\\?,\\?\\)").WillReturnResult(sqlmock.NewResult(1, 1))
	// 2. 事务内读任务（points_frozen=1, payer=8）
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "payer_id", "points_frozen", "task_no"}).AddRow(1, 8, 8, 1, "GT-001"))
	// 3. 幂等清零 → 退还批次 → unfreeze 流水
	mock.ExpectExec("UPDATE `generation_task` SET `points_frozen`=\\? WHERE id = \\? AND points_frozen > 0").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `point_lot`").WillReturnResult(sqlmock.NewResult(8, 1))
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(remaining\\),0.*FROM `point_lot`").WillReturnRows(
		sqlmock.NewRows([]string{"Total"}).AddRow(100))
	mock.ExpectExec("INSERT INTO `point_log`").WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()

	if err := service.FailTask(1, "生成失败"); err != nil {
		t.Fatalf("fail task: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestTaskFailRefundRollback 退款写库失败时连「置失败」一起回滚：任务还在进行中，
// 超时扫描 / 再次 FailTask 能重新走一遍，不会出现「已失败但积分一直冻着」。
func TestTaskFailRefundRollback(t *testing.T) {
	mock := initMockDB(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_task` SET .*WHERE id = \\? AND status IN").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "payer_id", "points_frozen", "task_no"}).AddRow(1, 8, 8, 30, "GT-001"))
	mock.ExpectExec("UPDATE `generation_task` SET `points_frozen`=\\?").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `point_lot`").WillReturnError(errors.New("db down"))
	mock.ExpectRollback()

	if err := service.FailTask(1, "生成失败"); err == nil {
		t.Fatal("退款失败时 FailTask 必须返回错误")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestRefundUnrefundedTasks 补偿扫描：已取消但积分还冻着的历史任务补退一次。
func TestRefundUnrefundedTasks(t *testing.T) {
	mock := initMockDB(t)

	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE status IN \\(\\?,\\?\\) AND points_frozen > 0").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "payer_id", "points_frozen", "task_no", "status"}).AddRow(3, 8, 8, 20, "GT-003", "cancelled"))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "payer_id", "points_frozen", "task_no", "status"}).AddRow(3, 8, 8, 20, "GT-003", "cancelled"))
	mock.ExpectExec("UPDATE `generation_task` SET `points_frozen`=\\? WHERE id = \\? AND points_frozen > 0").WillReturnResult(sqlmock.NewResult(3, 1))
	mock.ExpectExec("INSERT INTO `point_lot`").WillReturnResult(sqlmock.NewResult(9, 1))
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(remaining\\),0.*FROM `point_lot`").WillReturnRows(sqlmock.NewRows([]string{"Total"}).AddRow(20))
	mock.ExpectExec("INSERT INTO `point_log`").WillReturnResult(sqlmock.NewResult(3, 1))
	mock.ExpectCommit()

	if n := service.RefundUnrefundedTasks(); n != 1 {
		t.Fatalf("refunded = %d, want 1", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestCompleteTaskWithAssetAfterCancel 生成中途被取消（积分已退）：资产入库随置成功一起回滚，
// 返回 ErrTaskStatus 让 provider 删掉存储里的文件。
func TestCompleteTaskWithAssetAfterCancel(t *testing.T) {
	mock := initMockDB(t)

	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "payer_id", "points_frozen", "task_no", "status"}).
			AddRow(6, 8, 8, 0, "GT-006", "cancelled"))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `asset`").WillReturnResult(sqlmock.NewResult(50, 1))
	mock.ExpectExec("UPDATE `generation_task` SET .*WHERE id = \\? AND status IN").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	asset := &model.Asset{UserId: 8, Type: model.AssetTypeImage, Url: "https://cdn.example.com/x.png", Size: 1024, Source: model.AssetSourceGenerated, TaskId: 6}
	err := service.CompleteTaskWithAsset(6, asset)
	if !errors.Is(err, model.ErrTaskStatus) {
		t.Fatalf("err = %v, want ErrTaskStatus", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestFreezeMemberQuotaCountsReserved 团队成员月度额度要算上进行中任务的预扣：
// 上限 100、已结算 0、另一个任务正在跑占 80，再提交 80 必须拒绝。
func TestFreezeMemberQuotaCountsReserved(t *testing.T) {
	mock := initMockDB(t)
	now := time.Now().Unix()

	// PayerForTeam(8, 3)：成员属于团队 3 → 团队 owner 9 → owner 有团队版会员
	mock.ExpectQuery("SELECT .* FROM `team_member`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "team_id", "user_id", "role"}).AddRow(2, 3, 8, model.TeamRoleMember))
	mock.ExpectQuery("SELECT .* FROM `team`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "owner_id"}).AddRow(3, 9))
	mock.ExpectQuery("SELECT .* FROM `membership`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "edition", "status", "expires_at"}).AddRow(1, 9, model.EditionTeam, "active", now+86400))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `team_member` WHERE user_id = \\? .*FOR UPDATE").WillReturnRows(
		sqlmock.NewRows([]string{"id", "team_id", "user_id", "role", "points_limit", "points_used", "limit_reset_at"}).
			AddRow(2, 3, 8, model.TeamRoleMember, 100, 0, now))
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(points_frozen\\), 0\\) FROM `generation_task`").WillReturnRows(
		sqlmock.NewRows([]string{"n"}).AddRow(80))
	mock.ExpectRollback()

	task := &model.GenerationTask{Id: 7, UserId: 8, TeamId: 3, Type: model.TaskTypeImage, Model: "gpt-image-1", TaskNo: "GT-007"}
	err := service.FreezeTaskPoints(task, 80)
	if err == nil || !strings.Contains(err.Error(), "团队额度不足") {
		t.Fatalf("err = %v, want 团队额度不足", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestCreateTaskRejectsCount 网关一次只出一个产物，count>1 会多扣分少交付，创建接口直接拒绝。
func TestCreateTaskRejectsCount(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	p := providers.NewEasyAPIProvider("http://127.0.0.1:1", "sk-fake",
		providers.ParseModelList(model.TaskTypeImage, "gpt-image-1:GPT Image"))
	service.RegisterProvider(p)

	mock.ExpectQuery("SELECT .* FROM `user`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "username", "role", "status", "group", "access_token"}).
			AddRow(8, "e2e@easytv.ai", 1, 1, "default", "e2e-token"))

	headers := map[string]string{"Authorization": "Bearer e2e-token", "EasyTV-User": "8"}
	_, resp := do(t, r, "POST", "/user/task", `{"type":"image","model":"gpt-image-1","prompt":"雪山","params":{"count":4}}`, headers)
	if resp["success"] != false || !strings.Contains(fmt.Sprint(resp["message"]), "一次只能生成 1 个") {
		t.Fatalf("count=4 应被拒绝: %v", resp)
	}
	if got := service.TaskResultCount(service.WithSingleCount(`{"count":4,"size":"1024x1024"}`)); got != 1 {
		t.Fatalf("WithSingleCount → count=%d", got)
	}
}

// ===== 支付回调入账：纯充值（无订单）=====

// TestCompleteRechargeTopUp 纯充值走账：置成功 → 加额度 → 写资金流水（不触达履约）。
func TestCompleteRechargeTopUp(t *testing.T) {
	mock := initMockDB(t)

	// 1. 查充值单
	mock.ExpectQuery("SELECT .* FROM `recharge`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "amount", "money", "trade_no", "payment_provider", "status"}).
			AddRow(1, 8, 500, 5, "E2E-TOPUP-1", model.PaymentProviderAlipayF2F, "pending"))
	// 2. 置成功（默认事务包裹）
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `recharge` SET .*WHERE trade_no = \\? AND status = \\?").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	// 3. 未命中订单（order 不存在）
	mock.ExpectQuery("SELECT .* FROM `order` WHERE order_no = \\?").WillReturnError(model.ErrNotFound)
	// 4. 加额度 + 读快照 + 写资金流水（各自默认事务包裹）
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `user` SET `quota`=quota \\+ \\?").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT .* FROM `user` WHERE id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"quota"}).AddRow(500))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `promoter_bill`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := service.CompleteRecharge("E-001", "alipay_f2f", "集成测试充值"); err != nil {
		t.Fatalf("complete recharge: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// ===== 支付回调入账：积分包订单履约 =====
func TestCompleteRechargeFulfillOrder(t *testing.T) {
	mock := initMockDB(t)

	// 1. 充值单
	mock.ExpectQuery("SELECT .* FROM `recharge` WHERE trade_no").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "amount", "money", "trade_no", "payment_method", "payment_provider", "status"}).
			AddRow(1, 8, 100, "100", "E-ORDER-1", "alipay_f2f", "alipay_f2f", "pending"))
	// 2. 置成功
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `recharge` SET .*WHERE trade_no = \\? AND status = \\?").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	// 3. 命中订单
	mock.ExpectQuery("SELECT .* FROM `order` WHERE order_no").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "order_no", "kind", "pack_id", "points", "valid_days", "title", "status", "price"}).
			AddRow(9, 8, "E-ORDER-1", "points", 3, 6000, 365, "6000 积分包", "pending", 100))
	// 4. FulfillOrder 事务：tid 订单 → 置 paid → 发批次 + grant 流水
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `order` WHERE id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "order_no", "kind", "pack_id", "points", "valid_days", "title", "price", "status"}).
			AddRow(9, 8, "E-ORDER-1", "points", 3, 6000, 365, "6000 积分包", 100, "pending"))
	mock.ExpectExec("UPDATE `order` SET .*WHERE id = \\? AND status = \\?").WillReturnResult(sqlmock.NewResult(9, 1))
	mock.ExpectExec("INSERT INTO `point_lot`").WillReturnResult(sqlmock.NewResult(10, 1))
	mock.ExpectQuery("SELECT COALESCE\\(SUM.*FROM `point_lot`").WillReturnRows(sqlmock.NewRows([]string{"Total"}).AddRow(6000))
	mock.ExpectExec("INSERT INTO `point_log`").WillReturnResult(sqlmock.NewResult(10, 1))
	mock.ExpectCommit()

	if err := service.CompleteRecharge("E-ORDER-1", "alipay_f2f", "集成测试订单履约"); err != nil {
		t.Fatalf("complete recharge order: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// ===== 任务成功结算（settle 流水 + cost_points）=====
func TestTaskSettleFlow(t *testing.T) {
	mock := initMockDB(t)

	// 读任务（points_frozen=60，payer=8）
	mock.ExpectQuery("SELECT .* FROM `generation_task` WHERE id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "payer_id", "points_frozen", "task_no", "status"}).
			AddRow(5, 8, 8, 60, "GT-005", "running"))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `generation_task` SET .*cost_points.*WHERE id = \\?").WillReturnResult(sqlmock.NewResult(5, 1))
	mock.ExpectCommit()
	// settle 流水（amount=0）
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(remaining\\),0.*`point_lot`").WillReturnRows(sqlmock.NewRows([]string{"Total"}).AddRow(0))
	mock.ExpectExec("INSERT INTO `point_log`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := service.CompleteTask(5, 0, "https://cdn.example.com/r.mp4", ""); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}
