package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

func captureAudit(t *testing.T) (get func() []*model.AuditLog) {
	t.Helper()
	var mu sync.Mutex
	var got []*model.AuditLog
	restore := model.ReplaceAuditLogWriter(func(entries []*model.AuditLog) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, entries...)
		return nil
	})
	t.Cleanup(restore)
	return func() []*model.AuditLog {
		if !model.FlushAuditLogs(time.Second) {
			t.Fatal("flush 超时")
		}
		mu.Lock()
		defer mu.Unlock()
		return append([]*model.AuditLog(nil), got...)
	}
}

// TestSmsLoginWrongCodeAudit 验证码错误记安全事件，手机号只存脱敏值。
func TestSmsLoginWrongCodeAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	get := captureAudit(t)
	engine := gin.New()
	engine.POST("/user/sms_login", SmsLogin)
	req := httptest.NewRequest(http.MethodPost, "/user/sms_login", strings.NewReader(`{"mobile":"13900001111","code":"000000"}`))
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "验证码错误") {
		t.Fatalf("body = %s", w.Body.String())
	}
	logs := get()
	if len(logs) != 1 {
		t.Fatalf("应记 1 条，实际 %d", len(logs))
	}
	e := logs[0]
	if e.Category != model.AuditCategorySecurity || e.Action != model.AuditActionSmsLoginFailed || e.Success ||
		e.Username != "139****1111" || e.Other.Op.Params["reason"] != "wrong_code" || e.Route != "/user/sms_login" {
		t.Fatalf("entry = %+v op=%+v", e, e.Other.Op)
	}
	raw, _ := e.Other.Value()
	if strings.Contains(raw.(string), "13900001111") {
		t.Fatalf("不应含明文手机号: %s", raw)
	}
}

// TestInvoiceApplyAuditParams 开票审计只带金额 / 单号 / 失败原因。
func TestInvoiceApplyAuditParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	get := captureAudit(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/user/invoice/apply", nil)
	c.Set("id", 8)
	c.Set("username", "u8@example.com")
	c.Set("role", 1)
	recordInvoiceApply(c, 88.8, "ETI2026091510300081234", "")
	recordInvoiceApply(c, 99, "", "amount_exceeded")
	logs := get()
	if len(logs) != 2 {
		t.Fatalf("应记 2 条，实际 %d", len(logs))
	}
	ok, fail := logs[0], logs[1]
	if !ok.Success || ok.Category != model.AuditCategoryUsage || ok.Action != model.AuditActionInvoiceApply || ok.UserId != 8 ||
		ok.Other.Op.Params["out_order_no"] != "ETI2026091510300081234" || len(ok.Other.Op.Params) != 2 {
		t.Fatalf("success entry = %+v op=%+v", ok, ok.Other.Op)
	}
	if fail.Success || fail.Other.Op.Params["reason"] != "amount_exceeded" || len(fail.Other.Op.Params) != 3 {
		t.Fatalf("fail entry = %+v op=%+v", fail, fail.Other.Op)
	}
}
