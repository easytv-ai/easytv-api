package tests

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"easytv-api/model"
	"easytv-api/service/providers"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestSuggestPoints 建议积分换算：成本(元) × 毛利系数(3) × 积分/元(20)，向上取整最少 1，未定价 0。
func TestSuggestPoints(t *testing.T) {
	initMockDB(t) // 默认毛利系数 3 / 积分汇率 20 从 setting 读取（DB 缺省）
	if got := providers.SuggestPoints(0.1); got != 6 {
		t.Fatalf("suggest(0.1) = %d, want 6 (0.1×3×20)", got)
	}
	if got := providers.SuggestPoints(0.001); got != 1 {
		t.Fatalf("suggest(0.001) = %d, want 1 (round up)", got)
	}
	if got := providers.SuggestPoints(0); got != 0 {
		t.Fatalf("suggest(0) = %d, want 0", got)
	}
	if got := providers.SuggestPoints(-1); got != 0 {
		t.Fatalf("suggest(-1) = %d, want 0", got)
	}
	if got := providers.SuggestPoints(0.05); got != 3 {
		t.Fatalf("suggest(0.05) = %d, want 3", got)
	}
}

// fakePricingGateway 模拟网关 /pricing 与 /v1/models（字段与 token.easyapi.com 一致，报价为美元）。
func fakePricingGateway() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/pricing":
			_, _ = w.Write([]byte(`{
				"success": true,
				"group_ratio": {"default": 1},
				"usable_group": {"default": "default"},
				"data": [
					{"model_name":"kling-v1","description":"","category":"Video","quota_type":2,
					 "model_ratio":0.05,"model_price":0,"video_per_second_ratios":{"720p":1,"1080p":1.5},
					 "supported_endpoint_types":["openai"]},
					{"model_name":"gpt-4o-mini","description":"GPT-4o mini","category":"Text","quota_type":0,
					 "model_ratio":0.075,"model_price":0,"completion_ratio":4,"supported_endpoint_types":["openai"]},
					{"model_name":"flux-schnell","description":"","category":"Image","quota_type":1,
					 "model_ratio":0,"model_price":0.003,"supported_endpoint_types":["image-generation"]},
					{"model_name":"gpt-image-1","description":"GPT Image 1","category":"Image","quota_type":0,
					 "model_ratio":2.5,"model_price":0,"completion_ratio":8,"supported_endpoint_types":["image-generation"]},
					{"model_name":"seedance-2.0","description":"Seedance 2.0","category":"Video","quota_type":0,
					 "model_ratio":3.3923,"model_price":0,"completion_ratio":1,
					 "video_ratios":{"480p":1,"720p":1,"1080p":1.108393},
					 "supported_endpoint_types":["api-video","seedance-video-generation"]},
					{"model_name":"kling-2.5","description":"","category":"Video","quota_type":2,
					 "model_ratio":0.05,"model_price":0,"model_discount":0.65,
					 "video_per_second_ratios":{"720p":1,"1080p":1.666667},
					 "supported_endpoint_types":["openai"]}
				]
			}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"kling-v2","supported_endpoint_types":["video"]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"no such route"}}`))
		}
	}))
	return srv
}

// expectEmptyModelQuery 让 model_price 查询返回空（existing 标记为空）。
func expectEmptyModelQuery(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT .* FROM `model_price`").WillReturnRows(sqlmock.NewRows([]string{"id", "model", "type", "label", "unit", "points_per_unit", "member_points_per_unit", "status"}))
}

func near(got, want float64) bool { return got > want-0.0005 && got < want+0.0005 }

// TestFetchGatewayModels /pricing 拉取：按秒 / 按次 / 按量各自折算（美元 × 汇率 7.3，毛利 3，积分/元 20）。
func TestFetchGatewayModels(t *testing.T) {
	mock := initMockDB(t)
	expectEmptyModelQuery(mock)
	srv := fakePricingGateway()
	defer srv.Close()

	models, err := providers.FetchGatewayModels(srv.URL, "sk-test")
	if err != nil {
		t.Fatalf("fetch gateway models: %v", err)
	}
	if len(models) != 6 {
		t.Fatalf("want 6 models, got %d: %+v", len(models), models)
	}
	byModel := map[string]providers.GatewayModel{}
	for _, m := range models {
		byModel[m.Model] = m
	}
	// 按秒：$0.05/秒 × 720p 倍率 1 → ¥0.365/秒 → ceil(21.9)=22
	if m := byModel["kling-v1"]; m.QuotaType != 2 || m.Type != model.TaskTypeVideo || m.Unit != model.PriceUnitSecond || !near(m.PriceYuan, 0.365) || m.SuggestPoints != 22 {
		t.Fatalf("kling-v1: %+v", m)
	}
	// 按量文本：0.075 倍率 = $0.15/百万 token，输出 ×4，按 500 入 + 1000 出估算 → $0.000675 → ¥0.0049 → 1
	if m := byModel["gpt-4o-mini"]; m.QuotaType != 0 || m.Type != model.TaskTypeText || m.Unit != model.PriceUnitCall || !near(m.PriceYuan, 0.0049) || m.SuggestPoints != 1 {
		t.Fatalf("gpt-4o-mini: %+v", m)
	}
	// 按次图片：$0.003/张 → ¥0.0219 → ceil(1.314)=2
	if m := byModel["flux-schnell"]; m.QuotaType != 1 || m.Type != model.TaskTypeImage || m.Unit != model.PriceUnitImage || !near(m.PriceYuan, 0.0219) || m.SuggestPoints != 2 {
		t.Fatalf("flux-schnell: %+v", m)
	}
	// 按量图片：2.5 倍率 = $5/百万 输入、输出 ×8，按 100 入 + 200 出 → $0.0085 → ¥0.0621 → 4
	if m := byModel["gpt-image-1"]; m.Type != model.TaskTypeImage || m.Unit != model.PriceUnitImage || !near(m.PriceYuan, 0.0621) || m.SuggestPoints != 4 {
		t.Fatalf("gpt-image-1: %+v", m)
	}
	// 按量视频：3.3923 倍率 = $6.78/百万 token，12750 token/秒 → $0.0865/秒 → ¥0.6315 → 38
	if m := byModel["seedance-2.0"]; m.Type != model.TaskTypeVideo || m.Unit != model.PriceUnitSecond || !near(m.PriceYuan, 0.6315) || m.SuggestPoints != 38 {
		t.Fatalf("seedance-2.0: %+v", m)
	}
	// /pricing 的 model_discount 平台折扣不计入账单：与 kling-v1 同价，Discount 为 1
	if m := byModel["kling-2.5"]; m.Discount != 1 || !near(m.PriceYuan, 0.365) || m.SuggestPoints != 22 {
		t.Fatalf("kling-2.5: %+v", m)
	}
	for _, m := range models {
		if m.Existing {
			t.Fatalf("none should be existing: %+v", m)
		}
		if m.Estimate == "" {
			t.Fatalf("estimate basis missing: %+v", m)
		}
	}
}

// TestFetchGatewayModelsPartnerDiscount 合作折扣（setting EasyAIPricingPartnerDiscount）计入成本。
func TestFetchGatewayModelsPartnerDiscount(t *testing.T) {
	mock := initMockDB(t)
	expectEmptyModelQuery(mock)
	model.SetSettingCache("EasyAIPricingPartnerDiscount", "0.81")
	t.Cleanup(func() { model.SetSettingCache("EasyAIPricingPartnerDiscount", "") })
	srv := fakePricingGateway()
	defer srv.Close()

	models, err := providers.FetchGatewayModels(srv.URL, "sk-test")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	for _, m := range models {
		if m.Model != "kling-v1" {
			continue
		}
		// $0.05/秒 × 0.81 = $0.0405 → ¥0.29565 → ceil(17.74)=18
		if m.Discount != 0.81 || !near(m.PriceYuan, 0.2957) || m.SuggestPoints != 18 {
			t.Fatalf("kling-v1 with partner discount: %+v", m)
		}
		return
	}
	t.Fatal("kling-v1 missing")
}

// TestFetchGatewayModelsFallback /pricing 失败 → 回退 /v1/models 纯枚举（无定价）。
func TestFetchGatewayModelsFallback(t *testing.T) {
	initMockDB(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/pricing" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
			return
		}
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"kling-v2","supported_endpoint_types":["video"]}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	models, err := providers.FetchGatewayModels(srv.URL, "sk-test")
	if err != nil {
		t.Fatalf("fallback fetch: %v", err)
	}
	if len(models) != 1 || models[0].Model != "kling-v2" || models[0].PriceYuan != 0 {
		t.Fatalf("fallback models: %+v", models)
	}
}

// TestFetchGatewayModelsVideoRatiosMap 按秒模型的分辨率倍率表：优先取 720 档，没有 720 时取最小档。
func TestFetchGatewayModelsVideoRatiosMap(t *testing.T) {
	mock := initMockDB(t)
	expectEmptyModelQuery(mock)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{"model_name":"seedance-2.5","category":"Video","quota_type":2,
				 "model_ratio":0.01532,"model_price":0,
				 "video_per_second_ratios":{"480":1,"720":2.248107},
				 "supported_endpoint_types":["openai"]},
				{"model_name":"wan2.7-t2v","quota_type":2,
				 "model_ratio":0.088626,"model_price":0,
				 "video_per_second_ratios":{"1080":1.666667,"2k":2.5},
				 "supported_endpoint_types":["api-video"]}
			]
		}`))
	}))
	defer srv.Close()

	models, err := providers.FetchGatewayModels(srv.URL, "sk-test")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("want 2 models, got %d", len(models))
	}
	// seedance-2.5：取 720 档 2.248107 → $0.03444/秒 → ¥0.2514 → 16
	m := models[0]
	if m.Type != model.TaskTypeVideo || !near(m.PriceYuan, 0.2514) || m.SuggestPoints != 16 {
		t.Fatalf("seedance-2.5: %+v", m)
	}
	// wan2.7-t2v：无 720 档，取最小 1080 档 1.666667 → $0.1477/秒 → ¥1.0783 → 65；无 category 时按模型名推断为视频
	m = models[1]
	if m.Type != model.TaskTypeVideo || !near(m.PriceYuan, 1.0783) || m.SuggestPoints != 65 {
		t.Fatalf("wan2.7-t2v: %+v", m)
	}
	// 分辨率倍率表的键就是可选档位：统一成 720P / 2K 写法并按清晰度排序，供导入直接写进 model_price
	if models[0].Resolutions != "480P,720P" {
		t.Fatalf("seedance-2.5 resolutions = %q", models[0].Resolutions)
	}
	if models[1].Resolutions != "1080P,2K" {
		t.Fatalf("wan2.7-t2v resolutions = %q", models[1].Resolutions)
	}
}

// TestFetchGatewayModelsResolutionsNonVideo 非视频模型不推分辨率。
func TestFetchGatewayModelsResolutionsNonVideo(t *testing.T) {
	mock := initMockDB(t)
	expectEmptyModelQuery(mock)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"model_name":"gpt-4o-mini","category":"Text","quota_type":0,"model_ratio":0.075,"completion_ratio":4,"video_ratios":{"720p":1}}]}`))
	}))
	defer srv.Close()

	models, err := providers.FetchGatewayModels(srv.URL, "sk-test")
	if err != nil || len(models) != 1 {
		t.Fatalf("fetch: %v %d", err, len(models))
	}
	if models[0].Resolutions != "" {
		t.Fatalf("text model resolutions = %q", models[0].Resolutions)
	}
}

// TestGatewayModelsFromEnv 存量 env 清单解析（可为空，不 panic）。
func TestGatewayModelsFromEnv(t *testing.T) {
	_ = providers.ModelsFromEnv()
}

// TestProviderFromSettingsNoPanic 未配置 key 时注册/刷新空转不 panic（DB setting 缺省）。
func TestProviderFromSettingsNoPanic(t *testing.T) {
	initMockDB(t)
	providers.RegisterProviderFromSettings()
	providers.RefreshProviderFromSettings()
}

// TestParseModelListCompat ParseModelList 兼容旧行为（model:label）。
func TestParseModelListCompat(t *testing.T) {
	models := providers.ParseModelList(model.TaskTypeVideo, "kling-v1:可灵1.0, jimeng_vgfm_t2v_l20 ,,bad:")
	if len(models) != 3 {
		t.Fatalf("want 3 models, got %d", len(models))
	}
	if models[2].Label != "bad" {
		t.Fatalf("label fallback broken: %+v", models[2])
	}
}
