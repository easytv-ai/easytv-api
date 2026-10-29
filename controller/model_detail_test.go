package controller

import (
	"encoding/json"
	"strings"
	"testing"

	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/service/providers"
)

func viewJSON(t *testing.T, view map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// TestBuildModelDetailViewMerged 网关可用时：展示元数据（网关）与积分定价（本地）合并，口径见 API.md 28.1。
func TestBuildModelDetailViewMerged(t *testing.T) {
	info := service.ModelInfo{Provider: "easyapi", Model: "seedance-2.0", Type: model.TaskTypeVideo, Label: "Seedance 2.0"}
	price := &model.ModelPrice{
		Model: "seedance-2.0", Type: model.TaskTypeVideo, Unit: model.PriceUnitSecond,
		PointsPerUnit: 30, MemberPointsPerUnit: 24,
		Durations: "4,8,12", Resolutions: "720p,1080p", Abilities: "upscale",
	}
	price.FillEffective(1000)
	detail := &providers.GatewayModelDetail{
		Model: "seedance-2.0", Description: "视频生成", Tags: "视频", Category: "视频",
		Vendor:    &providers.GatewayVendor{Id: 12, Name: "字节跳动"},
		Endpoints: json.RawMessage(`["api-video"]`),
		SupportedEndpoint: map[string]providers.GatewayEndpoint{
			"video-generation": {Path: "/v1/videos", Method: "POST"},
		},
		SupportedEndpointTypes: []string{"openai"},
		Params:                 json.RawMessage(`{"video":{"input":["text"]}}`),
		PricingVersion:         "162c1496",
	}

	view := buildModelDetailView(info, price, detail)

	if view["model"] != "seedance-2.0" || view["provider"] != "easyapi" ||
		view["type"] != model.TaskTypeVideo || view["label"] != "Seedance 2.0" {
		t.Fatalf("基础字段 = %+v", view)
	}
	if view["gateway_available"] != true || view["description"] != "视频生成" ||
		view["category"] != "视频" || view["tags"] != "视频" || view["pricing_version"] != "162c1496" {
		t.Fatalf("展示元数据 = %+v", view)
	}
	if v, ok := view["vendor"].(*providers.GatewayVendor); !ok || v.Name != "字节跳动" {
		t.Fatalf("vendor = %+v", view["vendor"])
	}
	if e, ok := view["endpoints"].(json.RawMessage); !ok || string(e) != `["api-video"]` {
		t.Fatalf("endpoints = %+v（原样透传网关的 model.endpoints）", view["endpoints"])
	}
	if e, ok := view["supported_endpoint"].(map[string]providers.GatewayEndpoint); !ok || e["video-generation"].Method != "POST" {
		t.Fatalf("supported_endpoint = %+v", view["supported_endpoint"])
	}
	// 定价字段与 /generation/models 同口径
	if view["unit"] != model.PriceUnitSecond || view["points_per_unit"] != 30 || view["member_points_per_unit"] != 24 ||
		view["effective_points_per_unit"] != 30 || view["effective_member_points_per_unit"] != 24 || view["priced"] != true {
		t.Fatalf("定价字段 = %+v", view)
	}
	if d, ok := view["durations"].([]int); !ok || len(d) != 3 || d[0] != 4 || d[2] != 12 {
		t.Fatalf("durations = %+v", view["durations"])
	}
	if r, ok := view["resolutions"].([]string); !ok || len(r) != 2 || r[0] != "720P" {
		t.Fatalf("resolutions = %+v", view["resolutions"])
	}
	// 能力位与 /generation/models 同口径（声明优先，未声明的按模型名推断）
	if a, ok := view["abilities"].([]string); !ok || !strings.Contains(strings.Join(a, ","), "upscale") {
		t.Fatalf("abilities = %+v，应含声明的 upscale", view["abilities"])
	}
	// 视频模型没有参考图输入方式
	if view["image_input"] != "" {
		t.Fatalf("image_input = %+v", view["image_input"])
	}

	raw := viewJSON(t, view)
	for _, want := range []string{
		`"gateway_available":true`,
		`"params":{"video"`,
		`"endpoints":["api-video"]`,
		`"supported_endpoint_types":["openai"]`,
		`"vendor":{"id":12,"name":"字节跳动"}`,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("响应缺 %s：%s", want, raw)
		}
	}
	// 上游成本口径不下发
	for _, bad := range []string{"model_ratio", "model_price", "completion_ratio", "quota_type", "enable_groups", "billing_expr"} {
		if strings.Contains(raw, bad) {
			t.Fatalf("响应里不该出现 %s：%s", bad, raw)
		}
	}
}

// TestBuildModelDetailViewDegraded 网关不可用时降级：定价照常，展示元数据为空、gateway_available=false，
// 且空列表序列化成 [] 而不是 null。
func TestBuildModelDetailViewDegraded(t *testing.T) {
	info := service.ModelInfo{Provider: "easyapi", Model: "gpt-image-2", Type: model.TaskTypeImage, Label: "GPT Image 2"}
	price := &model.ModelPrice{
		Model: "gpt-image-2", Type: model.TaskTypeImage, Unit: model.PriceUnitImage,
		PointsPerUnit: 8, PromoPointsPerUnit: 5, PromoLabel: "首发 5 折",
	}
	price.FillEffective(1000)

	view := buildModelDetailView(info, price, nil)

	if view["gateway_available"] != false || view["description"] != "" || view["tags"] != "" ||
		view["category"] != "" || view["icon"] != "" || view["pricing_version"] != "" || view["vendor"] != nil {
		t.Fatalf("降级后展示元数据应为空 = %+v", view)
	}
	if view["points_per_unit"] != 8 || view["promo_points_per_unit"] != 5 || view["promo_label"] != "首发 5 折" ||
		view["promo_active"] != true || view["effective_points_per_unit"] != 5 {
		t.Fatalf("降级后定价字段应照常 = %+v", view)
	}
	// 图片模型的参考图输入方式按模型名推断
	if view["image_input"] != model.ImageInputEdits {
		t.Fatalf("image_input = %+v", view["image_input"])
	}

	raw := viewJSON(t, view)
	for _, want := range []string{
		`"gateway_available":false`,
		`"params":{}`,
		`"endpoints":null`,
		`"supported_endpoint":{}`,
		`"supported_endpoint_types":[]`,
		`"vendor":null`,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("降级响应缺 %s：%s", want, raw)
		}
	}
}
