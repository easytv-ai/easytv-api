package tests

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easytv-api/service"
)

// TestGenerateVideoCoverViaOSS 视频地址支持 OSS 截帧参数时抽到 JPEG 并存回存储；相对地址直接跳过。
func TestGenerateVideoCoverViaOSS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.RawQuery, "x-oss-process=video/snapshot") {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("\xff\xd8\xff\xe0fake-jpeg"))
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("fake-mp4"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	old := service.DefaultStorage
	service.DefaultStorage = &service.LocalStorage{Dir: dir, BaseUrl: "https://cdn.example.com"}
	t.Cleanup(func() { service.DefaultStorage = old })

	cover, err := service.GenerateVideoCover(srv.URL+"/easytv/2026/09/10/abc.mp4", "easytv/2026/09/10/abc.mp4")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.HasSuffix(cover, "/easytv/2026/09/10/abc_cover.jpg") {
		t.Fatalf("cover url = %q", cover)
	}
	data, err := os.ReadFile(filepath.Join(dir, "easytv", "2026", "09", "10", "abc_cover.jpg"))
	if err != nil || !strings.HasSuffix(string(data), "fake-jpeg") {
		t.Fatalf("cover file: %v %q", err, data)
	}

	// 相对地址（前端静态文件）无法抽帧：返回空串不报错
	cover, err = service.GenerateVideoCover("/videos/showcase/scenic.mp4", "")
	if err != nil || cover != "" {
		t.Fatalf("relative url: cover=%q err=%v", cover, err)
	}
}
