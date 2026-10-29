package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// TestAdminAccessDeniedSecurityAudit 未登录访问 /admin 记安全事件，同 IP + 方法 + 路由每分钟只记一条；
// 普通用户接口（UserAuth）被拒不记。
func TestAdminAccessDeniedSecurityAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var mu sync.Mutex
	var got []*model.AuditLog
	restore := model.ReplaceAuditLogWriter(func(entries []*model.AuditLog) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, entries...)
		return nil
	})
	defer restore()

	engine := gin.New()
	ok := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
	engine.GET("/admin/audit-denied-test", AdminAuth(), ok)
	engine.DELETE("/admin/audit-denied-test", RootAuth(), ok)
	engine.GET("/user/audit-denied-test", UserAuth(), ok)

	do := func(method, path, ip string) {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = ip + ":12345"
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
	}
	do("GET", "/admin/audit-denied-test", "10.0.0.1")
	do("GET", "/admin/audit-denied-test", "10.0.0.1") // 一分钟内重复，去重
	do("GET", "/admin/audit-denied-test", "10.0.0.2") // 不同 IP
	do("DELETE", "/admin/audit-denied-test", "10.0.0.1")
	do("GET", "/user/audit-denied-test", "10.0.0.1") // 非管理接口
	if !model.FlushAuditLogs(time.Second) {
		t.Fatal("flush 超时")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("应记 3 条，实际 %d", len(got))
	}
	e := got[0]
	if e.Category != model.AuditCategorySecurity || e.Action != model.AuditActionAdminAccessDenied || e.Success ||
		e.UserId != 0 || e.Ip != "10.0.0.1" || e.Method != "GET" || e.Route != "/admin/audit-denied-test" {
		t.Fatalf("entry = %+v", e)
	}
	if e.Other.Op == nil || e.Other.Op.Params["reason"] != "not_logged_in" || e.Other.Op.Params["required_role"] != "admin" {
		t.Fatalf("params = %+v", e.Other.Op)
	}
	if got[2].Other.Op.Params["required_role"] != "root" {
		t.Fatalf("root 路由 required_role = %v", got[2].Other.Op.Params["required_role"])
	}
}
