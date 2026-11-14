package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easytv-api/model"
	"easytv-api/service"
)

func TestGatewayReachable(t *testing.T) {
	yes := []string{
		"https://cdn.example.com/a.png",
		"http://media.example.org:8080/upload/a.png",
		"https://8.8.8.8/a.png",
	}
	no := []string{
		"", "/upload/a.png", "ftp://example.com/a.png", "data:image/png;base64,AAAA",
		"http://localhost:8383/upload/a.png", "http://127.0.0.1/a.png", "http://[::1]/a.png",
		"http://10.0.0.5/a.png", "http://192.168.1.20:8383/a.png", "http://172.16.3.1/a.png",
		"http://169.254.1.1/a.png", "http://0.0.0.0/a.png",
		"http://api:8383/upload/a.png", "http://easytv.local/a.png", "http://nas.lan/a.png",
		"http://app.localhost/a.png", "http://svc.internal/a.png",
	}
	for _, u := range yes {
		if !gatewayReachable(u) {
			t.Errorf("应判为网关可达：%s", u)
		}
	}
	for _, u := range no {
		if gatewayReachable(u) {
			t.Errorf("应判为网关不可达：%q", u)
		}
	}
}

func useUploadDir(t *testing.T, baseUrl string) string {
	t.Helper()
	dir := t.TempDir()
	oldDir, oldBase := service.UploadDir, service.UploadBaseUrl
	service.UploadDir, service.UploadBaseUrl = dir, baseUrl
	t.Cleanup(func() { service.UploadDir, service.UploadBaseUrl = oldDir, oldBase })
	return dir
}

// 1x1 PNG
var tinyPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0,
	0x1f, 0x15, 0xc4, 0x89, 0, 0, 0, 0x0d, 'I', 'D', 'A', 'T', 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0xf0, 0x1f, 0, 5, 0, 1, 0xff,
	0x89, 0x99, 0x3d, 0x1d, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

func writeUpload(t *testing.T, dir, key string, data []byte) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalUploadKey(t *testing.T) {
	useUploadDir(t, "http://localhost:8383/upload")
	cases := map[string]string{
		"/upload/u8/a.png":                          "u8/a.png",
		"/upload/u8/a.png?v=2":                      "u8/a.png",
		"http://localhost:8383/upload/u8/a.png":     "u8/a.png",
		"http://localhost:8383/upload/u8/a.png#t":   "u8/a.png",
		"https://cdn.example.com/upload/u8/a.png":   "",
		"http://localhost:8383/images/tutorial.png": "",
		"http://localhost:8383/upload/":             "",
	}
	for raw, want := range cases {
		got, ok := localUploadKey(raw)
		if (want != "") != ok || got != want {
			t.Errorf("localUploadKey(%q) = %q, %v；期望 %q", raw, got, ok, want)
		}
	}
}

func TestGatewayAssetUrl(t *testing.T) {
	dir := useUploadDir(t, "http://localhost:8383/upload")
	writeUpload(t, dir, "u8/a.png", tinyPNG)

	// 公网地址原样给
	pub := &model.Asset{Id: 1, Type: model.AssetTypeImage, Url: "https://cdn.example.com/a.png"}
	if got, err := gatewayAssetUrl(pub); err != nil || got != pub.Url {
		t.Fatalf("公网图片：%q %v", got, err)
	}

	// 本地磁盘存储的图片：两种地址写法都从磁盘读出来内联，不发 HTTP 请求
	for _, raw := range []string{"/upload/u8/a.png", "http://localhost:8383/upload/u8/a.png"} {
		got, err := gatewayAssetUrl(&model.Asset{Id: 2, Type: model.AssetTypeImage, Url: raw})
		if err != nil {
			t.Fatalf("%s：%v", raw, err)
		}
		if !strings.HasPrefix(got, "data:image/png;base64,") {
			t.Fatalf("%s 应内联成 PNG data URL，得到 %.40s", raw, got)
		}
	}

	// 网关拉不到的视频 / 音频：不内联，报错里说清要配什么
	for _, typ := range []string{model.AssetTypeVideo, "audio"} {
		_, err := gatewayAssetUrl(&model.Asset{Id: 3, Type: typ, Url: "/upload/u8/a.mp4"})
		if err == nil || !strings.Contains(err.Error(), "UPLOAD_BASE_URL") {
			t.Fatalf("%s 应报错并提示配置：%v", typ, err)
		}
	}

	// 超过内联上限的图片
	writeUpload(t, dir, "u8/big.png", append(append([]byte{}, tinyPNG...), make([]byte, inlineRefMaxBytes)...))
	if _, err := gatewayAssetUrl(&model.Asset{Id: 4, Type: model.AssetTypeImage, Url: "/upload/u8/big.png"}); err == nil {
		t.Fatal("超过内联上限应报错")
	}

	// 目录穿越
	if _, err := gatewayAssetUrl(&model.Asset{Id: 5, Type: model.AssetTypeImage, Url: "/upload/../secret.png"}); err == nil {
		t.Fatal("带 .. 的地址应拒绝")
	}
}
