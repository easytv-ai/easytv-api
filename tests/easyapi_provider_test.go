package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/service/providers"
)

// fakeGateway 模拟 EasyAPI 网关（new-api 契约）。
func fakeGateway(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"无效的令牌"}}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/chat/completions":
			if req["model"] != "gpt-4o-mini" {
				t.Errorf("chat model = %v", req["model"])
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"  第一镜：山脚仰拍。  "}}]}`))
		case "/v1/images/generations":
			if req["size"] != "1792x1024" {
				t.Errorf("image size = %v", req["size"])
			}
			_, _ = w.Write([]byte(`{"data":[{"url":"https://cdn.example.com/a.png"}]}`))
		case "/v1/audio/speech":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("ID3fake-mp3-bytes"))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"no such route"}}`))
		}
	}))
	return srv, &seen
}

// TestEasyAPIProviderModels 模型清单解析与注册表查找。
func TestEasyAPIProviderModels(t *testing.T) {
	models := providers.ParseModelList(model.TaskTypeVideo, "kling-v1:可灵 1.0, jimeng_vgfm_t2v_l20 ,,bad:")
	if len(models) != 3 {
		t.Fatalf("expect 3 models, got %d: %+v", len(models), models)
	}
	if models[0].Model != "kling-v1" || models[0].Label != "可灵 1.0" || models[0].Type != "video" || models[0].Provider != "easyapi" {
		t.Fatalf("unexpected first model: %+v", models[0])
	}
	if models[1].Label != "jimeng_vgfm_t2v_l20" || models[2].Label != "bad" {
		t.Fatalf("label fallback broken: %+v", models[1:])
	}

	p := providers.NewEasyAPIProvider("http://127.0.0.1:1", "sk-test", models)
	service.RegisterProvider(p)
	if service.FindProvider("video", "kling-v1") == nil {
		t.Fatal("registered model should be found")
	}
	if service.FindProvider("image", "kling-v1") != nil {
		t.Fatal("type mismatch must not match")
	}
	if got := len(service.ListModels()); got < 3 {
		t.Fatalf("ListModels should include registered models, got %d", got)
	}
}

// TestEasyAPIProviderCalls 文本 / 图片 / 语音三类网关调用的请求组装与响应解析（不触达 DB）；
// 视频走 /v1/videos，见 TestV2VideoAPIFlow。
func TestEasyAPIProviderCalls(t *testing.T) {
	srv, seen := fakeGateway(t)
	defer srv.Close()
	p := providers.NewEasyAPIProvider(srv.URL+"/", "sk-test", nil)

	text, err := p.Chat("gpt-4o-mini", "写一个分镜")
	if err != nil || text != "第一镜：山脚仰拍。" {
		t.Fatalf("chat: text=%q err=%v", text, err)
	}

	url, b64, err := p.GenerateImage("gpt-image-1", "雪山", "1792x1024")
	if err != nil || url != "https://cdn.example.com/a.png" || b64 != "" {
		t.Fatalf("image: url=%q b64=%q err=%v", url, b64, err)
	}

	audio, err := p.Speech("tts-1", "你好", "")
	if err != nil || string(audio) != "ID3fake-mp3-bytes" {
		t.Fatalf("speech: len=%d err=%v", len(audio), err)
	}

	want := []string{
		"POST /v1/chat/completions",
		"POST /v1/images/generations",
		"POST /v1/audio/speech",
	}
	if len(*seen) != len(want) {
		t.Fatalf("calls = %v", *seen)
	}
	for i := range want {
		if (*seen)[i] != want[i] {
			t.Fatalf("call %d = %q, want %q", i, (*seen)[i], want[i])
		}
	}
}

// TestEasyAPIProviderAuthError 401 要翻成运营能照着做的一句话（原文进日志，见 gateway_error.go）。
func TestEasyAPIProviderAuthError(t *testing.T) {
	srv, _ := fakeGateway(t)
	defer srv.Close()
	p := providers.NewEasyAPIProvider(srv.URL, "sk-wrong", nil)
	_, err := p.Chat("gpt-4o-mini", "hi")
	if err == nil || !strings.Contains(err.Error(), "网关鉴权失败") {
		t.Fatalf("unexpected err: %v", err)
	}
}

// TestEasyAPIProviderUnmappedError 没有映射规则的错误仍然把网关原文带出来，别把排查线索吞掉。
func TestEasyAPIProviderUnmappedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"weird model quirk"}}`))
	}))
	defer srv.Close()
	p := providers.NewEasyAPIProvider(srv.URL, "sk-test", nil)
	_, err := p.Chat("gpt-4o-mini", "hi")
	if err == nil || !strings.Contains(err.Error(), "weird model quirk") {
		t.Fatalf("未映射的错误应保留原文: %v", err)
	}
}

// ===== 新版 /v1/videos API 测试 =====

// fakeV2VideoGateway 模拟 /v1/videos 任务端点的网关。
func fakeV2VideoGateway(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"无效的令牌"}}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/videos":
			if req["model"] != "bytedance/seedance-2.0" {
				t.Errorf("v2 video model = %v", req["model"])
			}
			if req["prompt"] != "golden retriever on beach" {
				t.Errorf("v2 video prompt = %v", req["prompt"])
			}
			refs, _ := req["input_references"].([]any)
			if len(refs) != 2 {
				t.Errorf("expected 2 input_references, got %d", len(refs))
			}
			if refs != nil {
				r0 := refs[0].(map[string]any)
				if r0["type"] != "image" || r0["role"] != "first_frame" {
					t.Errorf("ref[0] = %v", r0)
				}
				r1 := refs[1].(map[string]any)
				if r1["type"] != "video" || r1["role"] != "reference_video" {
					t.Errorf("ref[1] = %v", r1)
				}
			}
			if req["resolution"] != "720p" {
				t.Errorf("resolution should be top-level: %v", req["resolution"])
			}
			if req["aspect_ratio"] != "16:9" {
				t.Errorf("aspect_ratio should be top-level: %v", req["aspect_ratio"])
			}
			if req["generate_audio"] != true {
				t.Errorf("generate_audio = %v", req["generate_audio"])
			}
			if req["seed"] != float64(42) {
				t.Errorf("seed = %v", req["seed"])
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"code":200,"msg":"","data":{"job_id":"video_v2_1","status":"in_progress","model":"bytedance/seedance-2.0","status_url":"http://127.0.0.1/v1/videos/video_v2_1","message":"任务已提交"}}`))
		case r.Method == "GET" && r.URL.Path == "/v1/videos/video_v2_1":
			_, _ = w.Write([]byte(`{"code":200,"msg":"","data":{"job_id":"video_v2_1","status":"completed","model":"bytedance/seedance-2.0","download_url":"https://cdn.example.com/video_v2_1.mp4","duration":6,"resolution":"720p","aspect_ratio":"16:9","generate_audio":true,"billed_cost":"1.08","billing_status":"settled"}}`))
		case r.Method == "GET" && r.URL.Path == "/v1/videos/video_v2_failed":
			_, _ = w.Write([]byte(`{"code":200,"msg":"","data":{"job_id":"video_v2_failed","status":"failed","message":"内容不合规"}}`))
		case r.Method == "GET" && r.URL.Path == "/v1/videos/video_v2_nx":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"msg":"任务不存在"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"no such route"}}`))
		}
	}))
	return srv, &seen
}

// TestV2VideoAPIFlow 测试新版视频 API 的请求组装与响应解析。
func TestV2VideoAPIFlow(t *testing.T) {
	srv, seen := fakeV2VideoGateway(t)
	defer srv.Close()
	p := providers.NewEasyAPIProvider(srv.URL+"/", "sk-test", nil)

	inputRefs := []providers.InputReference{
		{Type: "image", Url: "https://cdn.example.com/portrait.jpg", Role: "first_frame"},
		{Type: "video", Url: "https://cdn.example.com/style.mp4", Role: "reference_video"},
	}
	params := map[string]any{
		"resolution":     "720p",
		"aspect_ratio":   "16:9",
		"duration":       float64(6),
		"generate_audio": true,
		"seed":           float64(42),
	}
	jobId, err := p.CreateVideosAPITask("bytedance/seedance-2.0", "golden retriever on beach", inputRefs, params)
	if err != nil {
		t.Fatalf("CreateVideosAPITask err: %v", err)
	}
	if jobId != "video_v2_1" {
		t.Fatalf("job_id = %q, want video_v2_1", jobId)
	}

	state, err := p.QueryVideosAPITask("video_v2_1")
	if err != nil {
		t.Fatalf("QueryVideosAPITask err: %v", err)
	}
	if state.Status != "succeeded" || state.Url != "https://cdn.example.com/video_v2_1.mp4" {
		t.Fatalf("query result = %+v", state)
	}

	bad, err := p.QueryVideosAPITask("video_v2_failed")
	if err != nil || bad.Status != "failed" || bad.Error != "内容不合规" {
		t.Fatalf("query failed: %+v err=%v", bad, err)
	}

	// 404 任务被 callRaw 捕获为 HTTP 错误，返回 error
	_, err = p.QueryVideosAPITask("video_v2_nx")
	if err == nil || err.Error() != "网关返回 404：任务不存在" {
		t.Fatalf("expected 404 error, got: %v", err)
	}

	want := []string{
		"POST /v1/videos",
		"GET /v1/videos/video_v2_1",
		"GET /v1/videos/video_v2_failed",
		"GET /v1/videos/video_v2_nx",
	}
	if len(*seen) != len(want) {
		t.Fatalf("calls = %v", *seen)
	}
	for i := range want {
		if (*seen)[i] != want[i] {
			t.Fatalf("call %d = %q, want %q", i, (*seen)[i], want[i])
		}
	}
}

// TestVideoRoleType 素材角色 → type/role 映射全覆盖。
func TestVideoRoleType(t *testing.T) {
	cases := []struct {
		input    string
		wantType string
		wantRole string
		wantOK   bool
	}{
		{"first_frame", "image", "first_frame", true},
		{"image", "image", "first_frame", true}, // 向后兼容归一
		{"last_frame", "image", "last_frame", true},
		{"reference", "image", "reference", true},
		{"reference_video", "video", "reference_video", true},
		{"reference_audio", "audio", "reference_audio", true},
		{"character", "image", "reference", true}, // 画布角色参考 / 主体库
		{"scene", "image", "reference", true},
		{"style", "image", "reference", true},
		{"voice", "audio", "reference_audio", true},
		{"unknown_role", "", "", false},
	}
	for _, c := range cases {
		typ, role, ok := providers.VideoRoleType(c.input)
		if ok != c.wantOK || typ != c.wantType || role != c.wantRole {
			t.Errorf("videoRoleType(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.input, typ, role, ok, c.wantType, c.wantRole, c.wantOK)
		}
	}
}
