package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"
)

// ===== 网关模型 / 定价拉取（管理端 /admin/gateway/fetch-models 用）=====
//
// 网关（new-api 体系）的报价口径，全部是美元：
//   - quota_type 0 按量：model_ratio 为 token 倍率，1 倍率 = 2 美元 / 百万 token（1 美元 = 500000 quota）；
//     输出 token 再乘 completion_ratio。视频类按量模型 token 数由分辨率 × 帧率 × 时长决定，video_ratios 是分辨率倍率。
//   - quota_type 1 按次：model_price 为美元 / 次；视频再乘 video_per_second_ratios 的分辨率倍率。
//   - quota_type 2 按秒：model_ratio 为美元 / 秒，video_per_second_ratios 是分辨率倍率。
//   - 折扣：2026-09-10 对照 token 日志（GET /log/token?key=）验证，账单只乘合作折扣 partner_discount
//     （不在 /pricing 里，配置为 setting EasyAIPricingPartnerDiscount）；/pricing 的 model_discount 平台折扣不计入。
//     group_ratio 分组倍率照常计入。
//
// 折算：成本(美元) × 汇率(EasyAIPricingUsdRate) = 元，再 × 毛利系数 × 积分/元 = 建议积分。
// 按量模型折成 次 / 张 / 秒 时使用 setting.GetPricingEstimate 的假设用量（缺省值按真实扣费校准），分辨率倍率优先取 720p 档。

// usdPerRatioToken 1 倍率 = 2 美元 / 百万 token。
const usdPerRatioToken = 2.0 / 1_000_000

// GatewayModel 从网关拉取的单个模型（/pricing 与 /v1/models 合并后的视图）。
type GatewayModel struct {
	Model         string   `json:"model"`                    // 模型标识
	Label         string   `json:"label"`                    // 展示名
	Category      string   `json:"category"`                 // 网关侧分类
	Endpoints     []string `json:"supported_endpoint_types"` // 支持端点
	QuotaType     int      `json:"quota_type"`               // 0 按量 1 按次 2 按秒
	Type          string   `json:"type"`                     // 推断的任务类型 text / image / video / audio
	Unit          string   `json:"unit"`                     // 建议计价单位 call / image / second
	Ratio         float64  `json:"ratio"`                    // 网关原始倍率 / 单价
	Discount      float64  `json:"discount"`                 // 计入的合作折扣，1 = 无折扣
	CostUsd       float64  `json:"cost_usd"`                 // 每计价单位成本（美元）
	PriceYuan     float64  `json:"price_yuan"`               // 每计价单位成本（元）
	SuggestPoints int      `json:"suggest_points"`           // 建议积分 = ceil(元 × 毛利系数 × 积分/元)，最少 1；未定价 0
	Estimate      string   `json:"estimate"`                 // 折算依据说明
	Resolutions   string   `json:"resolutions"`              // 视频模型支持的分辨率（逗号分隔，从网关分辨率倍率表推出；非视频为空）
	Existing      bool     `json:"existing"`                 // 是否已在 model_price 表
}

// gatewayPricingModel /pricing 的单条模型（只取需要的字段）。
type gatewayPricingModel struct {
	Model                string             `json:"model_name"`
	Description          string             `json:"description"`
	Category             string             `json:"category"`
	QuotaType            int                `json:"quota_type"`
	ModelRatio           float64            `json:"model_ratio"`
	ModelPrice           float64            `json:"model_price"`
	CompletionRatio      float64            `json:"completion_ratio"`
	VideoPerSecondRatio  float64            `json:"video_per_second_ratio"`
	VideoRatios          map[string]float64 `json:"video_ratios"`
	VideoPerSecondRatios map[string]float64 `json:"video_per_second_ratios"`
	Endpoints            []string           `json:"supported_endpoint_types"`
}

// gatewayPricingResp /pricing 响应。
type gatewayPricingResp struct {
	Success     bool                  `json:"success"`
	Data        []gatewayPricingModel `json:"data"`
	GroupRatio  map[string]float64    `json:"group_ratio"`
	UsableGroup map[string]string     `json:"usable_group"`
}

// gatewayModelsResp /v1/models 响应（fallback，无定价）。
type gatewayModelsResp struct {
	Data []struct {
		Id        string   `json:"id"`
		Endpoints []string `json:"supported_endpoint_types"`
	} `json:"data"`
}

// FetchGatewayModels 从网关拉取模型列表与定价。
// 优先 GET /pricing（含定价）；失败或为空时 fallback 到 /v1/models（无定价，仅枚举）。
func FetchGatewayModels(baseUrl, apiKey string) ([]GatewayModel, error) {
	if apiKey == "" {
		return nil, errors.New("网关令牌未配置（EasyAPIGatewayKey 为空）")
	}
	models, err := fetchPricingModels(baseUrl, apiKey)
	if err != nil || len(models) == 0 {
		if err != nil {
			common.SysLog("fetch gateway pricing failed, fallback to /v1/models: " + err.Error())
		}
		return fetchPlainModels(baseUrl, apiKey)
	}
	return models, nil
}

func gatewayGet(baseUrl, path, apiKey string) ([]byte, error) {
	return gatewayGetLang(baseUrl, path, apiKey, "")
}

// gatewayGetLang 同 gatewayGet，额外带上 Accept-Language（网关据此本地化 description / tags / category）。
func gatewayGetLang(baseUrl, path, apiKey, lang string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseUrl, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if strings.TrimSpace(lang) != "" {
		req.Header.Set("Accept-Language", lang)
	}
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("网关 %d：%s", res.StatusCode, gatewayErrorMessage(data))
	}
	return data, nil
}

func fetchPricingModels(baseUrl, apiKey string) ([]GatewayModel, error) {
	// 定价接口可能不在调用域名上（setting EasyAPIGatewayPricingBaseUrl），留空才用网关地址
	if v := strings.TrimSpace(setting.GetSettingString(setting.EasyAPIGatewayPricingBaseUrl)); v != "" {
		baseUrl = strings.TrimRight(v, "/")
	}
	data, err := gatewayGet(baseUrl, setting.GetGatewayPricingPath(), apiKey)
	if err != nil {
		return nil, err
	}
	var body gatewayPricingResp
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	groupRatio := defaultGroupRatio(body)
	existing := modelPriceExistingMap()
	list := make([]GatewayModel, 0, len(body.Data))
	for _, m := range body.Data {
		list = append(list, buildGatewayModel(m, groupRatio, existing[m.Model]))
	}
	return list, nil
}

// buildGatewayModel 把网关报价折算成建议积分视图。
func buildGatewayModel(m gatewayPricingModel, groupRatio float64, existing bool) GatewayModel {
	est := estimateGatewayCost(m, groupRatio)
	raw := m.ModelRatio
	if m.QuotaType == 1 {
		raw = m.ModelPrice
	}
	yuan := est.usd * setting.GetPricingUsdRate()
	return GatewayModel{
		Model:         m.Model,
		Label:         firstNonEmpty(m.Description, m.Model),
		Category:      m.Category,
		Endpoints:     m.Endpoints,
		QuotaType:     m.QuotaType,
		Type:          est.typ,
		Unit:          est.unit,
		Ratio:         raw,
		Discount:      est.discount,
		CostUsd:       roundTo(est.usd, 6),
		PriceYuan:     roundTo(yuan, 4),
		SuggestPoints: SuggestPoints(yuan),
		Estimate:      est.basis,
		Resolutions:   gatewayResolutions(est.typ, m),
		Existing:      existing,
	}
}

// defaultGroupRatio 当前令牌可用分组的倍率（缺省 default，缺失按 1）。
func defaultGroupRatio(body gatewayPricingResp) float64 {
	name := "default"
	if g, ok := body.UsableGroup["default"]; ok && g != "" {
		name = g
	}
	if r, ok := body.GroupRatio[name]; ok && r > 0 {
		return r
	}
	return 1
}

type gatewayCost struct {
	typ, unit, basis string
	usd, discount    float64
}

// estimateGatewayCost 按 quota_type 与任务类型把网关报价折算成「每计价单位的美元成本」。
func estimateGatewayCost(m gatewayPricingModel, groupRatio float64) gatewayCost {
	out := gatewayCost{typ: inferGatewayType(m.Category, m.Endpoints, m.Model), discount: setting.GetPricingPartnerDiscount()}
	est := setting.GetPricingEstimate()
	switch m.QuotaType {
	case 1: // 按次：model_price 美元 / 次
		out.usd = m.ModelPrice
		out.unit = model.PriceUnitCall
		out.basis = fmt.Sprintf("按次 $%.4g/次", m.ModelPrice)
		switch out.typ {
		case model.TaskTypeImage:
			out.unit = model.PriceUnitImage
		case model.TaskTypeVideo:
			ratios := m.VideoPerSecondRatios
			if len(ratios) == 0 {
				ratios = m.VideoRatios
			}
			if base, key := resolutionBase(ratios); key != "" {
				out.usd *= base
				out.basis += fmt.Sprintf(" × %s 倍率 %.4g", key, base)
			}
		}
	case 2: // 按秒：model_ratio 美元 / 秒 × 分辨率倍率
		out.unit = model.PriceUnitSecond
		out.usd = m.ModelRatio
		out.basis = fmt.Sprintf("按秒 $%.4g/秒", m.ModelRatio)
		if m.VideoPerSecondRatio > 0 {
			out.usd *= m.VideoPerSecondRatio
			out.basis += fmt.Sprintf(" × 倍率 %.4g", m.VideoPerSecondRatio)
		} else if base, key := resolutionBase(m.VideoPerSecondRatios); key != "" {
			out.usd *= base
			out.basis += fmt.Sprintf(" × %s 倍率 %.4g", key, base)
		}
	default: // 按量：token 倍率折成 次 / 张 / 秒
		cr := m.CompletionRatio
		if cr <= 0 {
			cr = 1
		}
		perToken := m.ModelRatio * usdPerRatioToken
		perMillion := m.ModelRatio * 2
		switch out.typ {
		case model.TaskTypeVideo:
			out.unit = model.PriceUnitSecond
			out.usd = perToken * est.VideoTokensPerSecond
			out.basis = fmt.Sprintf("按量 $%.4g/百万 token，按 %.0f token/秒估算（按 seedance 实际扣费校准）", perMillion, est.VideoTokensPerSecond)
			if base, key := resolutionBase(m.VideoRatios); key != "" && base != 1 {
				out.usd *= base
				out.basis += fmt.Sprintf(" × %s 倍率 %.4g", key, base)
			}
		case model.TaskTypeImage:
			out.unit = model.PriceUnitImage
			out.usd = perToken * (est.ImageInputTokens + est.ImageTokens*cr)
			out.basis = fmt.Sprintf("按量 输入 $%.4g/百万 token、输出 ×%.4g，按每张 %.0f 输出 token 估算", perMillion, cr, est.ImageTokens)
		case model.TaskTypeAudio:
			out.unit = model.PriceUnitCall
			out.usd = perToken * (est.AudioInputTokens + est.AudioOutputTokens*cr)
			out.basis = fmt.Sprintf("按量 输入 $%.4g/百万 token、输出 ×%.4g，按每次 %.0f 输出 token 估算", perMillion, cr, est.AudioOutputTokens)
		default:
			out.unit = model.PriceUnitCall
			out.usd = perToken * (est.TextInputTokens + est.TextOutputTokens*cr)
			out.basis = fmt.Sprintf("按量 输入 $%.4g/百万 token、输出 ×%.4g，按每次 %.0f 输入 + %.0f 输出 token 估算", perMillion, cr, est.TextInputTokens, est.TextOutputTokens)
		}
	}
	if out.discount > 0 && out.discount < 1 {
		out.usd *= out.discount
		out.basis += fmt.Sprintf("，合作折扣 %.3g", out.discount)
	}
	if groupRatio > 0 && groupRatio != 1 {
		out.usd *= groupRatio
		out.basis += fmt.Sprintf("，分组倍率 %.4g", groupRatio)
	}
	return out
}

// inferGatewayType 由网关分类 / 端点 / 模型名推断任务类型。
func inferGatewayType(category string, endpoints []string, modelName string) string {
	cat := strings.ToLower(strings.TrimSpace(category))
	eps := strings.ToLower(strings.Join(endpoints, ","))
	name := strings.ToLower(modelName)
	switch {
	case cat == "video" || strings.Contains(eps, "video"):
		return model.TaskTypeVideo
	case cat == "image" || strings.Contains(eps, "image"):
		return model.TaskTypeImage
	case cat == "audio" || strings.Contains(eps, "tts") || strings.Contains(eps, "speech") || strings.Contains(eps, "audio"):
		return model.TaskTypeAudio
	case cat != "":
		return model.TaskTypeText
	}
	// 网关未标分类：按模型名兜底
	switch {
	case strings.Contains(name, "video") || strings.Contains(name, "t2v") || strings.Contains(name, "i2v") || strings.Contains(name, "r2v"):
		return model.TaskTypeVideo
	case strings.Contains(name, "image") || strings.Contains(name, "seedream") || strings.Contains(name, "flux"):
		return model.TaskTypeImage
	case strings.Contains(name, "tts") || strings.Contains(name, "speech"):
		return model.TaskTypeAudio
	}
	return model.TaskTypeText
}

// resolutionBase 分辨率倍率表取基准档：优先 720p，否则取最小值；空表返回 (1, "")。
// resolutionOrder 分辨率排序权重（小在前）；不认识的排最后，按字面排。
var resolutionOrder = map[string]int{"360P": 1, "480P": 2, "512P": 3, "540P": 4, "720P": 5, "768P": 6, "1080P": 7, "2K": 8, "4K": 9}

// normalizeGatewayResolution 网关分辨率键（"720"/"720p"/"1080P"/"2k"）→ 统一成 "720P" / "2K" 这样的写法；认不出返回空。
func normalizeGatewayResolution(key string) string {
	v := strings.ToUpper(strings.TrimSpace(key))
	if v == "" {
		return ""
	}
	if strings.HasSuffix(v, "K") {
		if _, err := strconv.Atoi(strings.TrimSuffix(v, "K")); err == nil {
			return v
		}
		return ""
	}
	digits := strings.TrimSuffix(v, "P")
	n, err := strconv.Atoi(digits)
	if err != nil || n <= 0 {
		return ""
	}
	return digits + "P"
}

// gatewayResolutions 视频模型支持的分辨率：网关的分辨率倍率表（video_ratios / video_per_second_ratios）
// 的键就是可选档位，去重排序后给管理端导入，省去人工录入。
func gatewayResolutions(taskType string, m gatewayPricingModel) string {
	if taskType != model.TaskTypeVideo {
		return ""
	}
	seen := map[string]bool{}
	list := []string{}
	for _, ratios := range []map[string]float64{m.VideoPerSecondRatios, m.VideoRatios} {
		for key := range ratios {
			v := normalizeGatewayResolution(key)
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			list = append(list, v)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		oi, oj := resolutionOrder[list[i]], resolutionOrder[list[j]]
		if oi != oj {
			if oi == 0 || oj == 0 {
				return oj == 0
			}
			return oi < oj
		}
		return list[i] < list[j]
	})
	return strings.Join(list, ",")
}

func resolutionBase(mp map[string]float64) (float64, string) {
	if len(mp) == 0 {
		return 1, ""
	}
	for k, v := range mp {
		if v > 0 && strings.Contains(k, "720") {
			return v, k
		}
	}
	minKey, minVal, found := "", 0.0, false
	for k, v := range mp {
		if v <= 0 {
			continue
		}
		if !found || v < minVal || (v == minVal && k < minKey) {
			minKey, minVal, found = k, v, true
		}
	}
	if !found {
		return 1, ""
	}
	return minVal, minKey
}

// SuggestPoints 成本（元）× 毛利系数 × 积分/元 → 建议积分，向上取整最少 1；未定价返回 0。
func SuggestPoints(yuan float64) int {
	if yuan <= 0 {
		return 0
	}
	points := int(math.Ceil(yuan*setting.GetPricingMarkupRate()*setting.GetPricingPointsRate() - 1e-9))
	if points < 1 {
		points = 1
	}
	return points
}

func fetchPlainModels(baseUrl, apiKey string) ([]GatewayModel, error) {
	data, err := gatewayGet(baseUrl, "/v1/models", apiKey)
	if err != nil {
		return nil, err
	}
	var body gatewayModelsResp
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	existing := modelPriceExistingMap()
	list := make([]GatewayModel, 0, len(body.Data))
	for _, m := range body.Data {
		typ := inferGatewayType("", m.Endpoints, m.Id)
		list = append(list, GatewayModel{
			Model:     m.Id,
			Label:     m.Id,
			Endpoints: m.Endpoints,
			Type:      typ,
			Unit:      defaultUnitForType(typ),
			Discount:  1,
			Estimate:  "网关未提供定价",
			Existing:  existing[m.Id],
		})
	}
	return list, nil
}

func defaultUnitForType(typ string) string {
	switch typ {
	case model.TaskTypeVideo:
		return model.PriceUnitSecond
	case model.TaskTypeImage:
		return model.PriceUnitImage
	}
	return model.PriceUnitCall
}

func modelPriceExistingMap() map[string]bool {
	existing := map[string]bool{}
	prices, _ := model.GetModelPrices(true)
	for _, p := range prices {
		existing[p.Model] = true
	}
	return existing
}

func roundTo(v float64, digits int) float64 {
	pow := math.Pow(10, float64(digits))
	return math.Round(v*pow) / pow
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
