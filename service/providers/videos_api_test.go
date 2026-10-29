package providers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestParseVideosAPIResponseFormats 包裹格式、直通格式、字段类型不一致时都要能解析出 ID / 状态 / 地址 / 失败原因。
func TestParseVideosAPIResponseFormats(t *testing.T) {
	cases := []struct {
		name                        string
		raw                         string
		wantId, wantStatus, wantUrl string
		wantMsg                     string
		wantProgress                int
	}{
		{"包裹格式成功", `{"code":200,"msg":"","data":{"job_id":"v1","status":"completed","download_url":"https://cdn/x.mp4"}}`,
			"v1", "completed", "https://cdn/x.mp4", "", -1},
		{"包裹格式 code 为字符串", `{"code":"success","data":{"job_id":"v2","status":"in_progress"}}`,
			"v2", "in_progress", "", "", -1},
		{"包裹格式 job_id 为数字", `{"code":200,"data":{"job_id":12345,"status":"queued"}}`,
			"12345", "queued", "", "", -1},
		{"直通格式进行中", `{"id":"task_a","object":"video","status":"in_progress","progress":42}`,
			"task_a", "in_progress", "", "", 42},
		{"直通格式成功无地址", `{"id":"task_b","status":"completed","progress":100}`,
			"task_b", "completed", "", "", 100},
		{"直通格式 metadata 里带地址", `{"id":"task_c","status":"completed","metadata":{"url":"https://cdn/c.mp4"}}`,
			"task_c", "completed", "https://cdn/c.mp4", "", -1},
		{"直通格式 error 对象", `{"id":"task_d","status":"failed","error":{"code":"moderation","message":"content policy violation"}}`,
			"task_d", "failed", "", "content policy violation", -1},
		{"error 为字符串", `{"id":"task_e","status":"failed","error":"quota exceeded"}`,
			"task_e", "failed", "", "quota exceeded", -1},
		{"包裹格式失败 message", `{"code":200,"data":{"job_id":"v3","status":"failed","message":"内容不合规"}}`,
			"v3", "failed", "", "内容不合规", -1},
		{"失败且无 ID", `{"status":"failed","error":{"message":"bad prompt"}}`,
			"", "failed", "", "bad prompt", -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := parseVideosAPIResponse([]byte(c.raw))
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if d.JobId != c.wantId || d.Status != c.wantStatus || d.DownloadUrl != c.wantUrl || d.Message != c.wantMsg || d.Progress != c.wantProgress {
				t.Fatalf("got %+v", d)
			}
		})
	}

	if _, err := parseVideosAPIResponse([]byte(`{"code":500,"msg":"上游繁忙","data":{}}`)); err == nil || !strings.Contains(err.Error(), "上游繁忙") {
		t.Fatalf("包裹格式错误码应带出 msg: %v", err)
	}
	if _, err := parseVideosAPIResponse([]byte(`{"foo":1}`)); err == nil {
		t.Fatal("没有 ID 和状态应报错")
	}
}

// TestVideoFailMessage 异步失败原因与同步报错走同一套人话规则。
func TestVideoFailMessage(t *testing.T) {
	if got := videoFailMessage("Content policy violation", "failed"); !strings.Contains(got, "内容审核") {
		t.Fatalf("审核失败应翻成人话: %q", got)
	}
	if got := videoFailMessage("", "failed"); !strings.Contains(got, "没有给出原因") || strings.Contains(got, "failed") {
		t.Fatalf("无原因时保留状态: %q", got)
	}
	if got := videoFailMessage("weird", "failed"); got != "网关生成失败：weird" {
		t.Fatalf("未映射的保留原文: %q", got)
	}
}

// TestVideosAPIRequestAndContentFallback 请求体参数映射；成功但响应无地址时带鉴权取 /content。
func TestVideosAPIRequestAndContentFallback(t *testing.T) {
	var created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/videos":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &created)
			_, _ = w.Write([]byte(`{"id":"task_x","status":"queued"}`))
		case "GET /v1/videos/task_x":
			_, _ = w.Write([]byte(`{"id":"task_x","status":"completed","progress":100}`))
		case "GET /v1/videos/task_x/content":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("mp4-bytes"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	p := NewEasyAPIProvider(srv.URL, "sk-test", nil)

	id, err := p.CreateVideosAPITask("sora-2", "星轨", nil, taskParams{
		"duration": float64(8), "ratio": "9:16", "audio": true,
		"count": float64(1), "mode": "fl2v", "negative_prompt": "模糊",
	})
	if err != nil || id != "task_x" {
		t.Fatalf("create: id=%q err=%v", id, err)
	}
	if created["generate_audio"] != true {
		t.Errorf("audio 应映射为 generate_audio: %v", created["generate_audio"])
	}
	if created["aspect_ratio"] != "9:16" {
		t.Errorf("ratio 应映射为 aspect_ratio: %v", created["aspect_ratio"])
	}
	meta, _ := created["metadata"].(map[string]any)
	if len(meta) != 1 || meta["negative_prompt"] != "模糊" {
		t.Errorf("metadata 只应透传白名单字段: %v", meta)
	}

	state, err := p.QueryVideosAPITask("task_x")
	if err != nil || state.Status != videoStatusSucceeded || state.Url != "" {
		t.Fatalf("query: %+v err=%v", state, err)
	}
	body, err := p.openVideoContent("task_x")
	if err != nil {
		t.Fatalf("content: %v", err)
	}
	defer body.Close()
	if b, _ := io.ReadAll(body); string(b) != "mp4-bytes" {
		t.Fatalf("content body = %q", b)
	}
}

// TestCreateVideoFailedWithoutId 创建即失败且没有 ID 时，报失败原因而不是「未返回任务 ID」。
func TestCreateVideoFailedWithoutId(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"failed","error":{"message":"unsupported duration"}}`))
	}))
	defer srv.Close()
	p := NewEasyAPIProvider(srv.URL, "sk-test", nil)
	_, err := p.CreateVideosAPITask("sora-2", "x", nil, taskParams{})
	if err == nil || !strings.Contains(err.Error(), "不支持这个时长") {
		t.Fatalf("err = %v", err)
	}
}

// TestGatewayNotFoundDetect 404 保留状态码供轮询快速失败，错误文案不变。
func TestGatewayNotFoundDetect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/videos/gone" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"msg":"任务不存在"}`))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream"}}`))
	}))
	defer srv.Close()
	p := NewEasyAPIProvider(srv.URL, "sk-test", nil)
	_, err := p.QueryVideosAPITask("gone")
	if !isGatewayNotFound(err) || err.Error() != "网关返回 404：任务不存在" {
		t.Fatalf("404 应可识别且文案不变: %v", err)
	}
	_, err = p.QueryVideosAPITask("other")
	if err == nil || isGatewayNotFound(err) {
		t.Fatalf("5xx 不应当成 404: %v", err)
	}
}

// TestCreateVideosAPITaskRetriesWithoutGenerateAudio 网关回「model does not support setting generate_audio」时去掉开关重提一次。
func TestCreateVideosAPITaskRetriesWithoutGenerateAudio(t *testing.T) {
	calls := 0
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if _, has := body["generate_audio"]; has {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":400,"msg":"model does not support setting generate_audio","data":{}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"task_y","status":"queued"}`))
	}))
	defer srv.Close()
	p := NewEasyAPIProvider(srv.URL, "sk-test", nil)
	id, err := p.CreateVideosAPITask("sora-2", "口播", nil, taskParams{"duration": float64(8), "audio": true, "count": float64(1)})
	if err != nil || id != "task_y" {
		t.Fatalf("create: id=%q err=%v", id, err)
	}
	if calls != 2 || bodies[0]["generate_audio"] != true {
		t.Fatalf("应先带开关再去掉重提：calls=%d first=%v", calls, bodies[0])
	}
	if _, has := bodies[1]["generate_audio"]; has {
		t.Fatal("重提时应去掉 generate_audio")
	}
}

// TestVideoErrorMessageFields 失败原因的字段名各家不一样，嵌在 metadata / result 里的也要能取到。
func TestVideoErrorMessageFields(t *testing.T) {
	cases := map[string]string{
		`{"status":"failed","failure_reason":"reference video not supported"}`:          "reference video not supported",
		`{"status":"failed","error":{"message":"bad input"}}`:                           "bad input",
		`{"status":"failed","metadata":{"error_message":"upstream 1210"}}`:              "upstream 1210",
		`{"code":200,"data":{"status":"FAILED","result":{"reason":"content blocked"}}}`: "content blocked",
		`{"status":"failed"}`: "",
	}
	for raw, want := range cases {
		data, err := parseVideosAPIResponse([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if data.Message != want {
			t.Errorf("%s → %q, want %q", raw, data.Message, want)
		}
	}
}
