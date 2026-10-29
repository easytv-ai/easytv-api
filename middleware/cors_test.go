package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newCORSEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS())
	r.GET("/api/status", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return r
}

func doRequest(r *gin.Engine, method, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/status", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func assertAllowed(t *testing.T, w *httptest.ResponseRecorder, origin string) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for %s", w.Code, origin)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("origin %s: Allow-Origin = %q, want echoed origin", origin, got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("origin %s: Allow-Credentials = %q, want true", origin, got)
	}
}

// CORS 无条件放行：任意来源（含外部域名、任意协议/端口）均回显 Origin 并允许凭据。
func TestCORS_AnyOriginAllowed(t *testing.T) {
	r := newCORSEngine()
	for _, origin := range []string{
		"https://easytv.ai", "https://manage.easytv.ai", "http://easytv.ai",
		"https://www.easytv.ai", "https://api.manage.easytv.ai", "https://test.easytv.ai",
		"https://evil.com", "https://fakeeasytv.ai", "https://easytv.ai.evil.com",
		"https://localhost.evil.com", "https://app.partner.io:8443", "http://127.0.0.1:5173",
	} {
		assertAllowed(t, doRequest(r, http.MethodGet, origin), origin)
	}
}

func TestCORS_Preflight(t *testing.T) {
	r := newCORSEngine()
	req := httptest.NewRequest(http.MethodOptions, "/api/status", nil)
	req.Header.Set("Origin", "https://manage.easytv.ai")
	req.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://manage.easytv.ai" {
		t.Errorf("Allow-Origin = %q, want echoed origin", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("Allow-Methods must be set on preflight")
	}
	if got := w.Header().Get("Access-Control-Max-Age"); got == "" {
		t.Error("Max-Age must be set on preflight")
	}
}

// 任意来源的预检同样放行并返回 204。
func TestCORS_PreflightAnyOrigin(t *testing.T) {
	r := newCORSEngine()
	req := httptest.NewRequest(http.MethodOptions, "/api/status", nil)
	req.Header.Set("Origin", "https://evil.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://evil.com" {
		t.Errorf("Allow-Origin = %q, want echoed origin", got)
	}
}

func TestCORS_NoOrigin(t *testing.T) {
	r := newCORSEngine()
	w := doRequest(r, http.MethodGet, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want empty without Origin header", got)
	}
}
