package controller

import (
	"fmt"
	"strconv"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/service/providers"
	"easytv-api/setting"

	"github.com/gin-gonic/gin"
)

// gatewayConfigView 网关/存储配置视图（敏感值只回传是否有值）。
func gatewayConfigView() gin.H {
	return gin.H{
		"gateway": gin.H{
			"base_url":    setting.GetGatewayBaseUrl(),
			"has_api_key": setting.GetGatewayKey() != "",
			"enabled":     setting.IsGatewayEnabled(),
			// 账户余额巡检（API.md 38）：控制台系统访问令牌只回是否有值
			"has_access_token": setting.GetGatewayAccessToken() != "",
			"user_id":          setting.GetGatewayUserId(),
		},
		"pricing": gin.H{
			"markup_rate":      setting.GetPricingMarkupRate(),
			"points_rate":      setting.GetPricingPointsRate(),
			"usd_rate":         setting.GetPricingUsdRate(),
			"partner_discount": setting.GetPricingPartnerDiscount(),
			"path":             setting.GetGatewayPricingPath(),
			"base_url":         setting.GetGatewayPricingBaseUrl(),
		},
		"upload_base_url": setting.GetUploadBaseUrl(),
		"cover":           service.CoverCapabilities(),
		"qiniu": gin.H{
			"has_keys":     setting.IsQiniuEnabled(),
			"bucket":       setting.GetQiniuBucketName(),
			"project_code": setting.GetQiniuProjectCode(),
			"domain":       setting.GetQiniuDomain(),
		},
	}
}

// AdminGetGatewayConfig 网关/存储配置（GET /admin/gateway/config，敏感值脱敏）。
func AdminGetGatewayConfig(c *gin.Context) {
	common.ApiSuccess(c, gatewayConfigView())
}

// AdminUpdateGatewayConfig 更新网关/存储配置（PUT /admin/gateway/config）。
// 写入 setting 后立即重建 provider 与存储，无需等 60s 周期。
func AdminUpdateGatewayConfig(c *gin.Context) {
	var payload struct {
		BaseUrl *string `json:"base_url"`
		ApiKey  *string `json:"api_key"`
		Enabled *bool   `json:"enabled"`
		// 账户余额巡检用的控制台访问令牌与用户 ID（API.md 38）
		AccessToken *string `json:"access_token"`
		UserId      *string `json:"user_id"`

		MarkupRate      *float64 `json:"markup_rate"`
		PointsRate      *float64 `json:"points_rate"`
		UsdRate         *float64 `json:"usd_rate"`
		PartnerDiscount *float64 `json:"partner_discount"`
		PricingPath     *string  `json:"pricing_path"`
		PricingBaseUrl  *string  `json:"pricing_base_url"`

		UploadBaseUrl *string `json:"upload_base_url"`

		QiniuAccessKey   *string `json:"qiniu_access_key"`
		QiniuSecretKey   *string `json:"qiniu_secret_key"`
		QiniuBucket      *string `json:"qiniu_bucket"`
		QiniuProjectCode *string `json:"qiniu_project_code"`
		QiniuDomain      *string `json:"qiniu_domain"`
	}
	if err := common.DecodeJson(c.Request.Body, &payload); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if payload.BaseUrl != nil {
		v := strings.TrimRight(strings.TrimSpace(*payload.BaseUrl), "/")
		if v == "" {
			common.ApiErrorMsg(c, "网关地址不能为空")
			return
		}
		if err := model.UpdateSetting(setting.EasyAPIGatewayBaseUrl, v); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.ApiKey != nil {
		// 允许置空关闭网关；非空时去掉首尾空白与可能的 Bearer 前缀
		key := strings.TrimSpace(*payload.ApiKey)
		key = strings.TrimPrefix(key, "Bearer ")
		if err := model.UpdateSetting(setting.EasyAPIGatewayKey, key); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.AccessToken != nil {
		tok := strings.TrimPrefix(strings.TrimSpace(*payload.AccessToken), "Bearer ")
		if err := model.UpdateSetting(setting.EasyAPIGatewayAccessToken, tok); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
		service.InvalidateGatewayBalanceCache()
	}
	if payload.UserId != nil {
		if err := model.UpdateSetting(setting.EasyAPIGatewayUserId, strings.TrimSpace(*payload.UserId)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
		service.InvalidateGatewayBalanceCache()
	}
	if payload.Enabled != nil {
		val := "false"
		if *payload.Enabled {
			val = "true"
		}
		if err := model.UpdateSetting(setting.EasyAPIGatewayEnabled, val); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.MarkupRate != nil {
		if *payload.MarkupRate <= 0 {
			common.ApiErrorMsg(c, "毛利系数必须为正")
			return
		}
		if err := model.UpdateSetting(setting.EasyAIPricingMarkupRate, strconv.FormatFloat(*payload.MarkupRate, 'f', -1, 64)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.PointsRate != nil {
		if *payload.PointsRate <= 0 {
			common.ApiErrorMsg(c, "积分/元汇率必须为正")
			return
		}
		if err := model.UpdateSetting(setting.EasyAIPricingPointsRate, strconv.FormatFloat(*payload.PointsRate, 'f', -1, 64)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.UsdRate != nil {
		if *payload.UsdRate <= 0 {
			common.ApiErrorMsg(c, "汇率必须为正")
			return
		}
		if err := model.UpdateSetting(setting.EasyAIPricingUsdRate, strconv.FormatFloat(*payload.UsdRate, 'f', -1, 64)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.PartnerDiscount != nil {
		if *payload.PartnerDiscount <= 0 || *payload.PartnerDiscount > 1 {
			common.ApiErrorMsg(c, "合作折扣必须在 0 到 1 之间")
			return
		}
		if err := model.UpdateSetting(setting.EasyAIPricingPartnerDiscount, strconv.FormatFloat(*payload.PartnerDiscount, 'f', -1, 64)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.PricingPath != nil {
		// 空值恢复默认（/pricing）；非空时归一化为 "/xxx"
		norm := strings.Trim(strings.TrimSpace(*payload.PricingPath), "/")
		if err := model.UpdateSetting(setting.EasyAPIGatewayPricingPath, norm); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.PricingBaseUrl != nil {
		// 空值 = 与网关同域名；非空要求带协议，避免拼出无效地址
		v := strings.TrimRight(strings.TrimSpace(*payload.PricingBaseUrl), "/")
		if v != "" && !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
			common.ApiErrorMsg(c, "定价地址必须以 http:// 或 https:// 开头")
			return
		}
		if err := model.UpdateSetting(setting.EasyAPIGatewayPricingBaseUrl, v); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.UploadBaseUrl != nil {
		v := strings.TrimRight(strings.TrimSpace(*payload.UploadBaseUrl), "/")
		if err := model.UpdateSetting(setting.UploadBaseUrl, v); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.QiniuAccessKey != nil {
		if err := model.UpdateSetting(setting.QiniuAccessKey, strings.TrimSpace(*payload.QiniuAccessKey)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.QiniuSecretKey != nil {
		if err := model.UpdateSetting(setting.QiniuSecretKey, strings.TrimSpace(*payload.QiniuSecretKey)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.QiniuBucket != nil {
		if err := model.UpdateSetting(setting.QiniuBucketName, strings.TrimSpace(*payload.QiniuBucket)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.QiniuProjectCode != nil {
		if err := model.UpdateSetting(setting.QiniuProjectCode, strings.TrimSpace(*payload.QiniuProjectCode)); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}
	if payload.QiniuDomain != nil {
		if err := model.UpdateSetting(setting.QiniuDomain, strings.TrimRight(strings.TrimSpace(*payload.QiniuDomain), "/")); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
	}

	// 立即生效：重建 provider 与存储（失败不阻塞，60s 周期任务兜底重试）
	providers.RefreshProviderFromSettings()
	service.ApplyStorageConfig()
	common.ApiSuccess(c, gatewayConfigView())
}

// AdminFetchGatewayModels 从网关拉取模型列表与定价（GET /admin/gateway/fetch-models）。
func AdminFetchGatewayModels(c *gin.Context) {
	models, err := providers.FetchGatewayModels(setting.GetGatewayBaseUrl(), setting.GetGatewayKey())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"base_url": setting.GetGatewayBaseUrl(),
		"models":   models,
	})
}

// gatewayModelImportItem 导入用模型条目（model 必填；其余缺省取合理默认）。
type gatewayModelImportItem struct {
	Model               string `json:"model"`
	Label               string `json:"label"`
	Type                string `json:"type"`
	Unit                string `json:"unit"`
	PointsPerUnit       int    `json:"points_per_unit"`
	MemberPointsPerUnit int    `json:"member_points_per_unit"`
	// Resolutions 视频模型支持的分辨率（逗号分隔），fetch-models 从网关分辨率倍率表推出；已存在的模型只在原值为空时补上
	Resolutions string `json:"resolutions"`
}

// AdminImportGatewayModels 把网关模型导入 model_price（POST /admin/gateway/models/import）。
// 已存在的模型只补 label/type/unit（不动已调价格）；新模型必须带正积分，缺省或 0 的跳过并在 skipped_models 里返回，由运营手动定价。
// 新模型默认按 GrowthNewModelPromo* 写入首发价（T18），请求 auto_promo=false 可关闭本次导入的自动首发价。
func AdminImportGatewayModels(c *gin.Context) {
	var req struct {
		Models    []gatewayModelImportItem `json:"models"`
		AutoPromo *bool                    `json:"auto_promo"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if len(req.Models) == 0 {
		common.ApiErrorMsg(c, "请至少提供一个模型")
		return
	}
	existingPrices, _ := model.GetModelPrices(false)
	existing := map[string]*model.ModelPrice{}
	for _, p := range existingPrices {
		existing[p.Model] = p
	}

	autoPromo := req.AutoPromo == nil || *req.AutoPromo
	created, updated, skipped, promoted := 0, 0, 0, 0
	skippedModels := make([]string, 0)
	promotedModels := make([]string, 0)
	for _, m := range req.Models {
		name := strings.TrimSpace(m.Model)
		if name == "" {
			continue
		}
		taskType := strings.TrimSpace(m.Type)
		if !model.IsValidTaskType(taskType) {
			taskType = model.TaskTypeText
		}
		unit := strings.TrimSpace(m.Unit)
		switch unit {
		case model.PriceUnitSecond, model.PriceUnitImage, model.PriceUnitCall:
		default:
			unit = model.PriceUnitCall
		}
		label := firstNonEmpty(strings.TrimSpace(m.Label), name)
		if old, ok := existing[name]; ok {
			// 已存在：仅补齐 label/type/unit，不改价格与状态
			fields := map[string]any{}
			if old.Label != label {
				fields["label"] = label
			}
			if old.Type != taskType {
				fields["type"] = taskType
			}
			if old.Unit != unit {
				fields["unit"] = unit
			}
			// 分辨率只补空：运营手工填过就不覆盖
			if res := model.NormalizeResolutions(m.Resolutions); res != "" && strings.TrimSpace(old.Resolutions) == "" {
				fields["resolutions"] = res
			}
			if len(fields) > 0 {
				_ = model.UpdateModelPriceFields(old.Id, fields)
			}
			updated++
			continue
		}
		points := m.PointsPerUnit
		if points <= 0 {
			// 网关没给建议积分的模型不能默认 1 分入库，跳过交给运营手动定价
			skipped++
			skippedModels = append(skippedModels, name)
			continue
		}
		member := m.MemberPointsPerUnit
		if member < 0 {
			member = 0
		}
		p := &model.ModelPrice{
			Model:               name,
			Type:                taskType,
			Label:               label,
			Unit:                unit,
			PointsPerUnit:       points,
			MemberPointsPerUnit: member,
			Resolutions:         model.NormalizeResolutions(m.Resolutions),
		}
		withPromo := autoPromo && service.ApplyNewModelPromo(p)
		if err := p.Insert(); err != nil {
			common.SysError(fmt.Sprintf("import model price failed: %s: %v", name, err))
			skipped++
			skippedModels = append(skippedModels, name)
			continue
		}
		created++
		if withPromo {
			promoted++
			promotedModels = append(promotedModels, name)
		}
	}
	common.ApiSuccess(c, gin.H{
		"created": created, "updated": updated, "skipped": skipped, "skipped_models": skippedModels,
		"promoted": promoted, "promoted_models": promotedModels,
	})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// AdminGatewayBalance GET /admin/gateway/balance?refresh=1：网关令牌剩余额度与近 30 天用量（API.md 38）。
// 网关没接或不支持账单接口时 success=false，前端显示「—」即可。
func AdminGatewayBalance(c *gin.Context) {
	b, err := service.CachedGatewayBalance(c.Query("refresh") == "1")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	threshold := setting.GetGatewayBalanceAlertUSD()
	common.ApiSuccess(c, gin.H{
		"remain_usd":    b.RemainUSD,
		"used_usd":      b.UsedUSD,
		"unlimited":     b.Unlimited,
		"source":        b.Source,
		"fetched_at":    b.FetchedAt,
		"threshold_usd": threshold,
		"low":           threshold > 0 && !b.Unlimited && b.RemainUSD < threshold,
	})
}
