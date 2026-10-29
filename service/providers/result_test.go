package providers

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOpenResult 产物地址返回错误页 / 非媒体内容时要失败，不能当成 png/mp4 入库结算。
func TestOpenResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG"))
		case "/expired":
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("AccessDenied"))
		case "/html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html>login</html>"))
		}
	}))
	defer srv.Close()

	body, err := openResult(srv.URL + "/ok.png")
	if err != nil {
		t.Fatalf("ok: %v", err)
	}
	data, _ := io.ReadAll(body)
	body.Close()
	if string(data) != "\x89PNG" {
		t.Fatalf("ok body = %q", data)
	}
	if _, err := openResult(srv.URL + "/expired"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("403 应失败: %v", err)
	}
	if _, err := openResult(srv.URL + "/html"); err == nil {
		t.Fatal("HTML 错误页应失败")
	}
}

// TestLimitedCounter 统计字节数；超过上限报错而不是静默截断。
func TestLimitedCounter(t *testing.T) {
	c := &limitedCounter{r: bytes.NewReader(make([]byte, 100)), limit: 100}
	if _, err := io.Copy(io.Discard, c); err != nil || c.n != 100 {
		t.Fatalf("恰好到上限: n=%d err=%v", c.n, err)
	}
	c = &limitedCounter{r: bytes.NewReader(make([]byte, 101)), limit: 100}
	if _, err := io.Copy(io.Discard, c); !errors.Is(err, errResultTooLarge) {
		t.Fatalf("超限应报错: n=%d err=%v", c.n, err)
	}
}
