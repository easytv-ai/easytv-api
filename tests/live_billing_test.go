package tests

import (
	"os"
	"testing"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
)

// TestLiveBillingFlow 真实数据库上的履约闭环（需要 EASYTV_LIVE_DB=1 且 .env 可连库）：
// 积分包订单履约 → 余额增加；会员订单履约 → 会员生效 + 首月积分；退款 → 会员失效 + 未消耗积分回收。
// 使用 seed 创建的演示账号，对生产数据只增加演示账号自己的记录。
func TestLiveBillingFlow(t *testing.T) {
	if os.Getenv("EASYTV_LIVE_DB") != "1" {
		t.Skip("set EASYTV_LIVE_DB=1 to run against the real database")
	}
	common.EnvFile = "../.env"
	if err := common.LoadEnvFile(); err != nil {
		t.Fatalf("load env: %v", err)
	}
	common.InitEnv()
	if err := model.InitDB(); err != nil {
		t.Fatalf("init db: %v", err)
	}
	_ = model.InitSettingMap()

	user, err := model.GetUserByEmail(common.GetEnvOrDefaultString("SEED_USER", "demo@easytv.ai"))
	if err != nil {
		t.Fatalf("demo user missing, run cmd/seed first: %v", err)
	}
	uid := user.Id
	before := model.GetPointsBalance(nil, uid)

	// 1. 积分包
	o, q, err := service.CreateOrder(uid, service.QuoteRequest{Kind: "points", PackCode: "p30"})
	if err != nil {
		t.Fatalf("create points order: %v", err)
	}
	if q.Points != 600 {
		t.Fatalf("p30 should be 600 points, got %d", q.Points)
	}
	if err := service.FulfillOrder(o.Id, "manual"); err != nil {
		t.Fatalf("fulfill points order: %v", err)
	}
	if err := service.FulfillOrder(o.Id, "manual"); err != model.ErrOrderStatus {
		t.Fatalf("second fulfill must be rejected, got %v", err)
	}
	afterPack := model.GetPointsBalance(nil, uid)
	if afterPack != before+600 {
		t.Fatalf("balance after pack: %d, want %d", afterPack, before+600)
	}

	// 2. 会员（创作版月付 + 加购）
	mo, mq, err := service.CreateOrder(uid, service.QuoteRequest{Kind: "membership", PlanCode: "creator", Edition: "creator", Cycle: "month", AddOn: true})
	if err != nil {
		t.Fatalf("create membership order: %v", err)
	}
	if mq.Points != 1800+800 {
		t.Fatalf("membership+add_on points = %d", mq.Points)
	}
	if err := service.FulfillOrder(mo.Id, "manual"); err != nil {
		t.Fatalf("fulfill membership: %v", err)
	}
	m := model.GetActiveMembership(uid)
	if m == nil || m.PlanCode != "creator" || m.MonthlyPoints != 1800 {
		t.Fatalf("membership not active after fulfill: %+v", m)
	}
	afterMember := model.GetPointsBalance(nil, uid)
	if afterMember != afterPack+2600 {
		t.Fatalf("balance after membership: %d, want %d", afterMember, afterPack+2600)
	}
	if !service.WalletView(uid, 0)["is_member"].(bool) {
		t.Fatal("wallet view should report member")
	}

	// 3. 任务预扣与退回（会员价：kling-v1 6 积分/秒 × 5 秒）
	task := &model.GenerationTask{UserId: uid, Type: "video", Provider: "easyapi", Model: "kling-v1", Prompt: "live test", Params: `{"duration":5}`}
	if err := task.Insert(); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	points, _, err := service.EstimateTaskPoints("video", "kling-v1", task.Params, true)
	if err != nil || points != 30 {
		t.Fatalf("estimate: %d %v", points, err)
	}
	if err := service.FreezeTaskPoints(task, points); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if got := model.GetPointsBalance(nil, uid); got != afterMember-30 {
		t.Fatalf("balance after freeze: %d, want %d", got, afterMember-30)
	}
	if err := service.FailTask(task.Id, "live test fail"); err != nil {
		t.Fatalf("fail task: %v", err)
	}
	if got := model.GetPointsBalance(nil, uid); got != afterMember {
		t.Fatalf("balance after refund: %d, want %d", got, afterMember)
	}
	// 幂等：再退一次不应重复入账
	_ = service.FailTask(task.Id, "again")
	if got := model.GetPointsBalance(nil, uid); got != afterMember {
		t.Fatalf("double refund detected: %d", got)
	}

	// 4. 退款：会员失效 + 该单积分回收（2600 未消耗）
	revoked, err := service.RefundOrder(mo.Id, "live test refund", 1)
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if revoked != 2600 {
		t.Fatalf("revoked = %d, want 2600", revoked)
	}
	if model.GetActiveMembership(uid) != nil {
		t.Fatal("membership should be terminated after refund")
	}
	if got := model.GetPointsBalance(nil, uid); got != afterPack {
		t.Fatalf("balance after refund: %d, want %d", got, afterPack)
	}
	t.Logf("live billing flow ok: user=%d balance %d -> %d", uid, before, model.GetPointsBalance(nil, uid))
}
