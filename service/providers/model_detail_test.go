package providers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestFetchModelDetail 详情拉取：路径 / 查询串 / 鉴权与语言头、展示元数据解析。
func TestFetchModelDetail(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotLang string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query().Get("model_name")
		gotAuth, gotLang = r.Header.Get("Authorization"), r.Header.Get("Accept-Language")
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"id": 219,
				"model_name": "openai/gpt-image-2.5",
				"description": "生成与编辑图片",
				"icon": "https://cdn.example.com/i.png",
				"tags": "图片",
				"category": "图片",
				"vendor": { "id": 1, "name": "OpenAI", "description": "…", "icon": "" },
				"endpoints": { "image-edit": { "path": "/v1/images/edits", "method": "POST" } },
				"supported_endpoint_types": ["openai"],
				"params": { "image": { "input": ["text", "image"], "output": ["image"],
				                       "properties": { "size": { "type": "string" } } } },
				"model_ratio": 2.5, "model_price": 0, "quota_type": 0,
				"model_discount": 0.85, "enable_groups": ["default"], "billing_expr": "…"
			},
			"supported_endpoint": { "image-generation": { "path": "/v1/images/generations", "method": "POST" } },
			"pricing_version": "162c1496"
		}`))
	}))
	defer srv.Close()

	detail, err := fetchModelDetail(srv.URL, "/pricing/detail", "sk-test", "openai/gpt-image-2.5", "en")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if gotPath != "/pricing/detail" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotQuery != "openai/gpt-image-2.5" {
		t.Fatalf("model_name = %q（带斜杠的模型名要能原样带过去）", gotQuery)
	}
	if gotAuth != "Bearer sk-test" || gotLang != "en" {
		t.Fatalf("headers = %q / %q", gotAuth, gotLang)
	}
	if detail.Model != "openai/gpt-image-2.5" || detail.Description != "生成与编辑图片" ||
		detail.Tags != "图片" || detail.Category != "图片" || detail.Icon == "" {
		t.Fatalf("got %+v", detail)
	}
	if detail.Vendor == nil || detail.Vendor.Id != 1 || detail.Vendor.Name != "OpenAI" {
		t.Fatalf("vendor = %+v", detail.Vendor)
	}
	var eps map[string]GatewayEndpoint
	if err := json.Unmarshal(detail.Endpoints, &eps); err != nil {
		t.Fatalf("endpoints 不是对象：%v（%s）", err, detail.Endpoints)
	}
	if e := eps["image-edit"]; e.Path != "/v1/images/edits" || e.Method != "POST" {
		t.Fatalf("endpoints = %+v", eps)
	}
	if e := detail.SupportedEndpoint["image-generation"]; e.Path != "/v1/images/generations" {
		t.Fatalf("supported_endpoint = %+v", detail.SupportedEndpoint)
	}
	if len(detail.SupportedEndpointTypes) != 1 || detail.SupportedEndpointTypes[0] != "openai" {
		t.Fatalf("supported_endpoint_types = %+v", detail.SupportedEndpointTypes)
	}
	if detail.PricingVersion != "162c1496" {
		t.Fatalf("pricing_version = %q", detail.PricingVersion)
	}
	var params map[string]any
	if err := json.Unmarshal(detail.Params, &params); err != nil {
		t.Fatalf("params 不是合法 JSON：%v（%s）", err, detail.Params)
	}
	if _, ok := params["image"]; !ok {
		t.Fatalf("params = %s", detail.Params)
	}

	// 上游成本口径不进详情结构（这条锁住「不给前端下发倍率」的决定）
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, bad := range []string{"model_ratio", "model_price", "quota_type", "model_discount", "enable_groups", "billing_expr"} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("详情结构里不该有 %s：%s", bad, raw)
		}
	}
}

// TestFetchModelDetailErrors 网关报错要带出原因：404 / 200 + success:false / 坏 JSON / 缺令牌。
func TestFetchModelDetailErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("model_name") {
		case "missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"message":"model not found"}`))
		case "soft-fail":
			_, _ = w.Write([]byte(`{"success":false,"message":"model not found"}`))
		default:
			_, _ = w.Write([]byte(`not json`))
		}
	}))
	defer srv.Close()

	if _, err := fetchModelDetail(srv.URL, "/pricing/detail", "sk-test", "missing", ""); err == nil ||
		!strings.Contains(err.Error(), "model not found") {
		t.Fatalf("404 应带出网关消息：%v", err)
	}
	if _, err := fetchModelDetail(srv.URL, "/pricing/detail", "sk-test", "soft-fail", ""); err == nil ||
		!strings.Contains(err.Error(), "model not found") {
		t.Fatalf("success=false 应报错：%v", err)
	}
	if _, err := fetchModelDetail(srv.URL, "/pricing/detail", "sk-test", "bad", ""); err == nil {
		t.Fatal("坏 JSON 应报错")
	}
	if _, err := fetchModelDetail(srv.URL, "/pricing/detail", "", "any", ""); err == nil {
		t.Fatal("缺令牌应报错")
	}
}

// TestFetchModelDetailEndpointsShapes endpoints 是网关 model.endpoints 列的原样透传：
// 2026-09-17 实测线上是端点类型数组（与 supported_endpoint_types 同值），运营配了自定义端点时
// 是 path/method 对象，两种都要能过（早期按对象类型化会让所有模型的详情都拉不到）。
func TestFetchModelDetailEndpointsShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"数组形态", `["anthropic","openai"]`},
		{"对象形态", `{"image-edit":{"path":"/v1/images/edits","method":"POST"}}`},
		{"字符串形态", `"openai"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"success":true,"data":{"model_name":"m","endpoints":` + c.raw + `}}`))
			}))
			defer srv.Close()

			detail, err := fetchModelDetail(srv.URL, "/pricing/detail", "sk-test", "m", "")
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if string(detail.Endpoints) != c.raw {
				t.Fatalf("endpoints = %s，期望 %s", detail.Endpoints, c.raw)
			}
		})
	}
}

// TestGatewayDetailCache 缓存按「模型 + 语言」分键，成功 60 秒过期；失败也缓存（30 秒），
// 免得网关挂的时候每个匿名请求都去敲一次网关。
func TestGatewayDetailCache(t *testing.T) {
	key := "seedance-2.0|zh"
	gatewayDetailCachePut(key, &GatewayModelDetail{Model: "seedance-2.0"}, nil)
	if d, err, hit := gatewayDetailCacheGet(key); !hit || err != nil || d == nil || d.Model != "seedance-2.0" {
		t.Fatalf("刚写入应命中：%v %v %v", d, err, hit)
	}
	if _, _, hit := gatewayDetailCacheGet("seedance-2.0|en"); hit {
		t.Fatal("不同语言不该命中同一条缓存")
	}

	errKey := "claude-fable-5.1|"
	wantErr := errors.New("网关 502：模型服务临时不可用")
	gatewayDetailCachePut(errKey, nil, wantErr)
	if d, err, hit := gatewayDetailCacheGet(errKey); !hit || d != nil || err == nil || err.Error() != wantErr.Error() {
		t.Fatalf("失败结果应被缓存：%v %v %v", d, err, hit)
	}

	gatewayDetailCache.mu.Lock()
	gatewayDetailCache.items[key] = gatewayDetailCacheItem{detail: &GatewayModelDetail{}, expires: time.Now().Add(-time.Second)}
	gatewayDetailCache.mu.Unlock()
	if _, _, hit := gatewayDetailCacheGet(key); hit {
		t.Fatal("过期后不该命中")
	}
}

// TestFetchModelDetailToleratesMissingOptionalFields 网关省略可选字段（无 vendor / 无 params / 无端点）时不应报错。
func TestFetchModelDetailToleratesMissingOptionalFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept-Language"); got != "" {
			t.Errorf("未传语言时不该带 Accept-Language：%q", got)
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"model_name":"seedance-2.0","category":"video"}}`))
	}))
	defer srv.Close()

	detail, err := fetchModelDetail(srv.URL, "/pricing/detail", "sk-test", "seedance-2.0", "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if detail.Vendor != nil || detail.Endpoints != nil || detail.Params != nil ||
		len(detail.SupportedEndpointTypes) != 0 || detail.PricingVersion != "" {
		t.Fatalf("got %+v", detail)
	}
}
