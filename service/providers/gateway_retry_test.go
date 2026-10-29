package providers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGatewayRetryOn429ThenOK(t *testing.T) {
	gatewayRetryBase = 0
	defer func() { gatewayRetryBase = 1500 * time.Millisecond }()
	var hits int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		b := make([]byte, 64)
		k, _ := r.Body.Read(b)
		bodies = append(bodies, string(b[:k]))
		if n <= 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limit"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	p := NewEasyAPIProvider(srv.URL, "k", nil)
	raw, err := p.doRaw(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"x"}`), "application/json")
	if err != nil || string(raw) != `{"ok":true}` {
		t.Fatalf("两次 429 后第三次应成功：%v %s", err, raw)
	}
	if hits != 3 || len(bodies) != 3 || bodies[2] != `{"model":"x"}` {
		t.Fatalf("应重发三次且每次带完整请求体：hits=%d bodies=%v", hits, bodies)
	}
}

func TestGatewayRetryGivesUp(t *testing.T) {
	gatewayRetryBase = 0
	defer func() { gatewayRetryBase = 1500 * time.Millisecond }()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit"}}`))
	}))
	defer srv.Close()
	p := NewEasyAPIProvider(srv.URL, "k", nil)
	_, err := p.doRaw(http.MethodPost, "/v1/chat/completions", nil, "")
	if err == nil || !strings.Contains(err.Error(), "排队较多") {
		t.Fatalf("重试用尽应回 429 人话：%v", err)
	}
	if hits != 3 {
		t.Fatalf("最多 1 次 + 2 次重试：%d", hits)
	}
}

func TestGatewayNoRetryOn400(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":400,"msg":"unsupported duration: 5"}`))
	}))
	defer srv.Close()
	p := NewEasyAPIProvider(srv.URL, "k", nil)
	if _, err := p.doRaw(http.MethodPost, "/v1/videos", nil, ""); err == nil {
		t.Fatal("400 应直接失败")
	}
	if hits != 1 {
		t.Fatalf("400 不该重试：%d", hits)
	}
}

func TestGatewayRetryDelay(t *testing.T) {
	if d := gatewayRetryDelay("3", 1); d != 3*time.Second {
		t.Fatalf("Retry-After 优先：%s", d)
	}
	if d := gatewayRetryDelay("600", 1); d != gatewayRetryAfterCap {
		t.Fatalf("Retry-After 封顶：%s", d)
	}
	if d := gatewayRetryDelay("", 2); d != 2*gatewayRetryBase {
		t.Fatalf("无头按次数线性：%s", d)
	}
}
