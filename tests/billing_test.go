package tests

import (
	"net/http"
	"testing"
	"time"

	"easytv-api/model"
	"easytv-api/service"

	"github.com/DATA-DOG/go-sqlmock"
)

func planRow() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "code", "name", "featured", "monthly_origin", "monthly_price", "monthly_points",
		"add_on_points", "add_on_price", "concurrent", "storage_gb", "sort_order", "status",
	}).AddRow(2, "creator", "创作", true, 399, 259, 1800, 800, 69, 10, 130, 1, 1)
}

func packRow() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "code", "points", "valid_days", "origin", "member_price", "featured", "sort_order", "status"}).
		AddRow(3, "p365", 6000, 365, 299, 199, true, 2, 1)
}

// TestBillingQuoteMembership 会员报价与前端 quoteMembership 一致：259×12×0.62 = 1927。
func TestBillingQuoteMembership(t *testing.T) {
	mock := initMockDB(t)
	// 匿名（userId=0）不查会员
	mock.ExpectQuery("SELECT .* FROM `membership_plan` WHERE code = \\?").WillReturnRows(planRow())

	q, err := service.BuildQuote(service.QuoteRequest{Kind: "membership", PlanCode: "creator", Edition: "creator", Cycle: "year"}, 0)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if q.Price != 1927 || q.Origin != 4788 || q.Points != 1800 || q.Months != 12 || q.Seats != 1 {
		t.Fatalf("unexpected quote: %+v", q)
	}
	if q.Title != "创作会员 · 创作 · 按年购买" {
		t.Fatalf("title = %q", q.Title)
	}

	// 团队版 3 席按季 + 加购：259×3×0.85×3×0.9 = 1782.8→1783，加购 69×3×0.9 = 186.3→186
	mock.ExpectQuery("SELECT .* FROM `membership_plan` WHERE code = \\?").WillReturnRows(planRow())
	q, err = service.BuildQuote(service.QuoteRequest{Kind: "membership", PlanCode: "creator", Edition: "team", Cycle: "quarter", Seats: 3, AddOn: true}, 0)
	if err != nil {
		t.Fatalf("team quote: %v", err)
	}
	if q.Price != 1783+186 || q.Points != 1800*3+800*3 || q.AddOnPrice != 186 {
		t.Fatalf("unexpected team quote: %+v", q)
	}

	// 席位越界
	mock.ExpectQuery("SELECT .* FROM `membership_plan` WHERE code = \\?").WillReturnRows(planRow())
	if _, err := service.BuildQuote(service.QuoteRequest{Kind: "membership", PlanCode: "creator", Edition: "team", Cycle: "month", Seats: 1}, 0); err == nil {
		t.Fatal("seats below min should fail")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestBillingQuotePointPack 积分包：非会员原价，会员会员价。
func TestBillingQuotePointPack(t *testing.T) {
	mock := initMockDB(t)
	// 登录用户：先查会员（无记录）再查积分包
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("SELECT .* FROM `point_pack` WHERE code = \\?").WillReturnRows(packRow())
	q, err := service.BuildQuote(service.QuoteRequest{Kind: "points", PackCode: "p365"}, 8)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if q.Price != 299 || q.Points != 6000 || q.ValidDays != 365 || q.IsMember {
		t.Fatalf("non-member quote: %+v", q)
	}

	// 有效会员 → 会员价
	future := int64(4102444800) // 2100-01-01
	mock.ExpectQuery("SELECT .* FROM `membership` WHERE user_id = \\?").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "plan_code", "expires_at", "status"}).AddRow(1, 8, "creator", future, "active"))
	mock.ExpectQuery("SELECT .* FROM `point_pack` WHERE code = \\?").WillReturnRows(packRow())
	q, err = service.BuildQuote(service.QuoteRequest{Kind: "points", PackCode: "p365"}, 8)
	if err != nil {
		t.Fatalf("member quote: %v", err)
	}
	if q.Price != 199 || !q.IsMember || q.Title != "6000 积分 · 有效期 1 年" {
		t.Fatalf("member quote: %+v", q)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestEstimateTaskPoints 视频按秒×数量，会员价生效；未定价拒绝。
func TestEstimateTaskPoints(t *testing.T) {
	mock := initMockDB(t)
	priceRow := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "model", "type", "label", "unit", "points_per_unit", "member_points_per_unit", "status"}).
			AddRow(1, "kling-v1", "video", "可灵", "second", 8, 6, 1)
	}
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\?").WillReturnRows(priceRow())
	points, _, err := service.EstimateTaskPoints("video", "kling-v1", `{"duration":10,"count":2}`, false)
	if err != nil || points != 8*10*2 {
		t.Fatalf("estimate: points=%d err=%v", points, err)
	}
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\?").WillReturnRows(priceRow())
	points, _, err = service.EstimateTaskPoints("video", "kling-v1", `{"duration":"5s"}`, true)
	if err != nil || points != 6*5 {
		t.Fatalf("member estimate: points=%d err=%v", points, err)
	}
	// 类型不匹配
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\?").WillReturnRows(priceRow())
	if _, _, err := service.EstimateTaskPoints("image", "kling-v1", `{}`, false); err == nil {
		t.Fatal("type mismatch should fail")
	}
	// 未定价
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\?").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	if _, _, err := service.EstimateTaskPoints("video", "unknown", `{}`, false); err != model.ErrNotFound {
		t.Fatalf("unpriced should be ErrNotFound, got %v", err)
	}
}

// TestEstimateTaskPointsPromo 促销价（T18）：有效期内取 min(标准价/会员价, 促销价)，过期或高于原价时忽略。
func TestEstimateTaskPointsPromo(t *testing.T) {
	mock := initMockDB(t)
	now := time.Now().Unix()
	cols := []string{"id", "model", "type", "label", "unit", "points_per_unit", "member_points_per_unit", "promo_points_per_unit", "promo_label", "promo_starts_at", "promo_ends_at", "status"}
	row := func(promo int, starts, ends int64) *sqlmock.Rows {
		return sqlmock.NewRows(cols).AddRow(1, "seedance-2.0", "video", "Seedance", "second", 10, 8, promo, "首发 5 折", starts, ends, 1)
	}
	// 促销 5 分，非会员 10 → 5；会员 8 → 5
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\?").WillReturnRows(row(5, now-60, now+3600))
	points, _, err := service.EstimateTaskPoints("video", "seedance-2.0", `{"duration":4}`, false)
	if err != nil || points != 5*4 {
		t.Fatalf("promo estimate: points=%d err=%v", points, err)
	}
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\?").WillReturnRows(row(5, now-60, now+3600))
	points, _, err = service.EstimateTaskPoints("video", "seedance-2.0", `{"duration":4}`, true)
	if err != nil || points != 5*4 {
		t.Fatalf("promo member estimate: points=%d err=%v", points, err)
	}
	// 促销 9 分高于会员价 8：会员仍按 8
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\?").WillReturnRows(row(9, 0, 0))
	points, _, err = service.EstimateTaskPoints("video", "seedance-2.0", `{"duration":1}`, true)
	if err != nil || points != 8 {
		t.Fatalf("promo above member price should be ignored: points=%d err=%v", points, err)
	}
	// 已过期：按标准价
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\?").WillReturnRows(row(5, now-7200, now-3600))
	points, _, err = service.EstimateTaskPoints("video", "seedance-2.0", `{"duration":1}`, false)
	if err != nil || points != 10 {
		t.Fatalf("expired promo should be ignored: points=%d err=%v", points, err)
	}
	// 未开始：按标准价
	mock.ExpectQuery("SELECT .* FROM `model_price` WHERE model = \\?").WillReturnRows(row(5, now+3600, 0))
	points, _, err = service.EstimateTaskPoints("video", "seedance-2.0", `{"duration":1}`, false)
	if err != nil || points != 10 {
		t.Fatalf("not-started promo should be ignored: points=%d err=%v", points, err)
	}
}

// TestBillingRoutesRequireAuth 付费域用户/管理路由未登录拒绝，公开路由可访问。
func TestBillingRoutesRequireAuth(t *testing.T) {
	mock := initMockDB(t)
	r := initTestRouter()
	for _, tc := range []struct{ method, path string }{
		{"GET", "/user/wallet"}, {"POST", "/user/order"}, {"GET", "/user/order/1"},
		{"GET", "/admin/order"}, {"POST", "/admin/wallet/1/adjust"}, {"PUT", "/admin/billing/promo"},
	} {
		code, resp := do(t, r, tc.method, tc.path, `{}`, nil)
		if code != http.StatusOK || resp["success"] != false {
			t.Fatalf("%s %s should require auth: %v", tc.method, tc.path, resp)
		}
	}
	// 公开：定价页数据
	mock.ExpectQuery("SELECT .* FROM `membership_plan` WHERE status = \\?").WillReturnRows(planRow())
	mock.ExpectQuery("SELECT .* FROM `point_pack` WHERE status = \\?").WillReturnRows(packRow())
	code, resp := do(t, r, "GET", "/billing/plans", "", nil)
	if code != http.StatusOK || resp["success"] != true {
		t.Fatalf("billing plans: %v", resp)
	}
	data := resp["data"].(map[string]any)
	if len(data["plans"].([]any)) != 1 || len(data["packs"].([]any)) != 1 || data["promo"] == nil {
		t.Fatalf("plans payload: %v", data)
	}
}
