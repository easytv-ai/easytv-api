package controller

import (
	"encoding/json"
	"errors"
	"strings"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/service/providers"

	"github.com/gin-gonic/gin"
)

// GetGenerationModelDetail 模型详情（GET /generation/models/detail，匿名，见 API.md 28）。
// 展示元数据（说明文案 / 供应商 / 参数属性表 / 端点定义）来自网关 GET /pricing/detail，
// 积分定价来自本地 model_price；网关不可用时只降级展示元数据，定价照常返回。
func GetGenerationModelDetail(c *gin.Context) {
	name := strings.TrimSpace(c.Query("model"))
	if name == "" {
		name = strings.TrimSpace(c.Query("model_name"))
	}
	if name == "" {
		common.ApiErrorMsg(c, "缺少模型标识 model")
		return
	}
	// 先按可用模型清单过滤（与 /generation/models 同一数据源），不在清单里的不查网关
	info, ok := findModelInfo(name)
	if !ok {
		common.ApiErrorMsg(c, "模型不存在或未上架")
		return
	}
	price, err := model.GetModelPrice(name)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			common.ApiErrorMsg(c, "模型不存在或未上架")
			return
		}
		respondModelError(c, err, "查询模型失败")
		return
	}
	price.FillEffective(common.GetTimestamp())

	// Accept-Language 透传给网关：上游据此本地化 description / tags / category
	// 失败原因由 providers 侧记日志（失败结果会短暂缓存，不会刷日志），这里只降级
	detail, _ := providers.FetchGatewayModelDetail(name, c.GetHeader(constant.HeaderAcceptLanguage))
	common.ApiSuccess(c, buildModelDetailView(info, price, detail))
}

// findModelInfo 在可用模型清单里找条目，保证 provider / type / label 与 /generation/models 完全一致。
func findModelInfo(name string) (service.ModelInfo, bool) {
	for _, m := range service.ListModels() {
		if m.Model == name {
			return m, true
		}
	}
	return service.ModelInfo{}, false
}

// buildModelDetailView 合成详情响应：本地积分定价 + 网关展示元数据（detail 为 nil 表示网关不可用）。
// 展示元数据字段名沿用网关；上游成本口径（model_ratio / quota_type / enable_groups 等）不下发。
func buildModelDetailView(info service.ModelInfo, price *model.ModelPrice, detail *providers.GatewayModelDetail) gin.H {
	view := gin.H{
		"model":    info.Model,
		"provider": info.Provider,
		"type":     info.Type,
		"label":    info.Label,

		"unit":                             price.Unit,
		"points_per_unit":                  price.PointsPerUnit,
		"member_points_per_unit":           price.MemberPointsPerUnit,
		"priced":                           true,
		"promo_points_per_unit":            price.PromoPointsPerUnit,
		"promo_label":                      price.PromoLabel,
		"promo_starts_at":                  price.PromoStartsAt,
		"promo_ends_at":                    price.PromoEndsAt,
		"promo_active":                     price.PromoActive,
		"effective_points_per_unit":        price.EffectivePointsPerUnit,
		"effective_member_points_per_unit": price.EffectiveMemberPointsPerUnit,
		"campaign_promo_label":             price.CampaignPromoLabel,
		"durations":                        []int{},
		"resolutions":                      []string{},
		"image_input":                      "",
		"abilities":                        price.EffectiveAbilities(),

		// 网关展示元数据：拿不到时保持空值，前端按 gateway_available 决定要不要渲染详情区块
		"category":                 "",
		"description":              "",
		"icon":                     "",
		"tags":                     "",
		"vendor":                   nil,
		"endpoints":                json.RawMessage("null"),
		"supported_endpoint":       map[string]providers.GatewayEndpoint{},
		"supported_endpoint_types": []string{},
		"params":                   json.RawMessage("{}"),
		"pricing_version":          "",
		"gateway_available":        false,
	}
	if d := price.AllowedDurations(); len(d) > 0 {
		view["durations"] = d
	}
	if r := price.AllowedResolutions(); len(r) > 0 {
		view["resolutions"] = r
	}
	if info.Type == model.TaskTypeImage {
		view["image_input"] = price.EffectiveImageInput()
	}
	if detail == nil {
		return view
	}
	view["category"] = detail.Category
	view["description"] = detail.Description
	view["icon"] = detail.Icon
	view["tags"] = detail.Tags
	view["vendor"] = detail.Vendor
	view["pricing_version"] = detail.PricingVersion
	view["gateway_available"] = true
	if len(detail.Endpoints) > 0 && json.Valid(detail.Endpoints) {
		view["endpoints"] = detail.Endpoints
	}
	if len(detail.SupportedEndpoint) > 0 {
		view["supported_endpoint"] = detail.SupportedEndpoint
	}
	if len(detail.SupportedEndpointTypes) > 0 {
		view["supported_endpoint_types"] = detail.SupportedEndpointTypes
	}
	// 网关已剔除 params 里的上游字段名 / 文档来源 / 置信度，且空表或坏 JSON 直接省略
	if len(detail.Params) > 0 && json.Valid(detail.Params) {
		view["params"] = detail.Params
	}
	return view
}
