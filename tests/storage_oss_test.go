package tests

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easytv-api/service"
)

// TestOSSStorageSignAndUpload 用模拟 OSS 校验签名 V1、对象路径、ACL 头与返回 URL。
func TestOSSStorageSignAndUpload(t *testing.T) {
	const secret = "test-secret"
	var got struct {
		method, path, contentType, acl, auth, date string
		body                                       string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.method, got.path, got.body = r.Method, r.URL.Path, string(body)
		got.contentType = r.Header.Get("Content-Type")
		got.acl = r.Header.Get("x-oss-object-acl")
		got.auth = r.Header.Get("Authorization")
		got.date = r.Header.Get("Date")
		if r.Method == http.MethodPut && r.ContentLength != int64(len(body)) {
			t.Errorf("content-length %d != %d", r.ContentLength, len(body))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	env := map[string]string{
		"OSS_BUCKET": "easytv-media", "OSS_ENDPOINT": "oss-cn-hangzhou.aliyuncs.com",
		"OSS_ACCESS_KEY_ID": "AKID", "OSS_ACCESS_KEY_SECRET": secret,
		"OSS_BASE_URL": "https://cdn.easytv.ai", "OSS_PREFIX": "/easytv/",
	}
	getenv := func(k, def string) string {
		if v, ok := env[k]; ok {
			return v
		}
		return def
	}
	getbool := func(k string, def bool) bool { return def }
	s := service.NewOSSStorageFromEnv(getenv, getbool)
	if s == nil {
		t.Fatal("bucket set, storage should be created")
	}
	s.WithAPIBase(srv.URL)

	url, err := s.Save("2026/09/09/a.mp4", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if url != "https://cdn.easytv.ai/easytv/2026/09/09/a.mp4" {
		t.Fatalf("url = %s", url)
	}
	if got.method != "PUT" || got.path != "/easytv/2026/09/09/a.mp4" || got.body != "hello" {
		t.Fatalf("request = %+v", got)
	}
	if got.contentType != "video/mp4" || got.acl != "public-read" {
		t.Fatalf("headers = %+v", got)
	}
	// 重算签名
	stringToSign := s.StringToSignForTest("PUT", got.contentType, got.date, "x-oss-object-acl:public-read\n", "2026/09/09/a.mp4")
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(stringToSign))
	want := "OSS AKID:" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if got.auth != want {
		t.Fatalf("authorization = %s, want %s", got.auth, want)
	}

	if err := s.Remove("2026/09/09/a.mp4"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got.method != "DELETE" || got.path != "/easytv/2026/09/09/a.mp4" || !strings.HasPrefix(got.auth, "OSS AKID:") {
		t.Fatalf("delete request = %+v", got)
	}

	// 未配置桶 → 不启用
	if service.NewOSSStorageFromEnv(func(k, def string) string { return "" }, getbool) != nil {
		t.Fatal("empty bucket must return nil")
	}
	// 路径穿越拒绝
	if _, err := s.Save("../etc/passwd", strings.NewReader("x")); err == nil {
		t.Fatal("unsafe key must be rejected")
	}
}
