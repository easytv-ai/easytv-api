package tests

import (
	"testing"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/router"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

// TestSumUserInvoiceablePaidQuery 可开票实付：只算本人 paid 且 price>0，排除 manual（管理端标记已支付）与空渠道。
func TestSumUserInvoiceablePaidQuery(t *testing.T) {
	mock := initMockDB(t)
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(price\\),0\\) AS total FROM `order` WHERE user_id = \\? AND status = \\? AND price > 0 AND payment_method NOT IN \\(\\?,\\?\\)").
		WithArgs(8, model.OrderStatusPaid, "manual", "").
		WillReturnRows(sqlmock.NewRows([]string{"total"}).AddRow(199.9))

	got, err := model.SumUserInvoiceablePaid(8)
	if err != nil || got != 199.9 {
		t.Fatalf("paid=%v err=%v", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestSumUserInvoiceableTopupQuery 纯充值实付：成功单、有渠道，且 trade_no 对不上套餐订单；扣掉已退金额。
func TestSumUserInvoiceableTopupQuery(t *testing.T) {
	mock := initMockDB(t)
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(CASE WHEN money > IFNULL\\(refunded_amount,0\\) THEN money - refunded_amount ELSE 0 END\\),0\\) AS total FROM `recharge` WHERE \\(user_id = \\? AND status = \\? AND money > 0 AND payment_method <> '' AND payment_method <> \\?\\) AND trade_no NOT IN \\(SELECT `order_no` FROM `order` WHERE user_id = \\? AND order_no <> \\?\\)").
		WithArgs(8, common.RechargeStatusSuccess, model.OrderPaymentMethodManual, 8, "").
		WillReturnRows(sqlmock.NewRows([]string{"total"}).AddRow(50.5))

	got, err := model.SumUserInvoiceableTopup(8)
	if err != nil || got != 50.5 {
		t.Fatalf("topup=%v err=%v", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet: %v", err)
	}
}

// TestCreatorWithdrawRoutesRegistered 创作者提现接口挂在登录用户组下。
func TestCreatorWithdrawRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	router.SetApiRouter(engine)
	want := map[string]bool{
		"GET /user/creator/summary":   false,
		"POST /user/creator/exchange": false,
		"POST /user/creator/withdraw": false,
		"GET /user/creator/withdraws": false,
	}
	for _, r := range engine.Routes() {
		if _, ok := want[r.Method+" "+r.Path]; ok {
			want[r.Method+" "+r.Path] = true
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("缺少路由 %s", k)
		}
	}
}

// TestInvoiceRoutesRegistered 发票三个接口挂在登录用户组下。
func TestInvoiceRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	router.SetApiRouter(engine)
	want := map[string]bool{
		"GET /user/invoice/summary":      false,
		"GET /user/invoice/access-token": false,
		"POST /user/invoice/apply":       false,
	}
	for _, r := range engine.Routes() {
		if _, ok := want[r.Method+" "+r.Path]; ok {
			want[r.Method+" "+r.Path] = true
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("缺少路由 %s", k)
		}
	}
}
