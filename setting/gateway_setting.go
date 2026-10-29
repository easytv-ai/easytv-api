package setting

import (
	"strconv"
	"strings"

	"easytv-api/common"
)

// EasyAPI 大模型网关配置（DB setting 表，PascalCase 键，管理端 /admin/gateway/config 可热更新）。
// 环境变量 EASYAPI_* 仅作首次启动的迁移 fallback（见 model.SeedModelsFromEnvIfEmpty），不再参与运行期生成。
const (
	// EasyAPIGatewayBaseUrl 网关地址，缺省 https://api.easyapi.com。
	EasyAPIGatewayBaseUrl = "EasyAPIGatewayBaseUrl"
	// EasyAPIGatewayKey 网关令牌（必填，缺失不注册 provider）。
	EasyAPIGatewayKey = "EasyAPIGatewayKey"
	// EasyAPIGatewayEnabled 显式开关："false"/"0" 关闭；缺省且有 Key 即开启。
	EasyAPIGatewayEnabled = "EasyAPIGatewayEnabled"
	// UploadBaseUrl 上传文件对外前缀（图生视频素材需公网可访问）；为空表示由本服务 /upload/ 提供。
	UploadBaseUrl = "UploadBaseUrl"

	// EasyAIPricingMarkupRate 网关成本 → 建议积分的毛利系数（默认 3.0）。
	EasyAIPricingMarkupRate = "EasyAIPricingMarkupRate"
	// EasyAIPricingPointsRate 积分/元汇率（默认 20，与积分包 p30..p730 的隐式汇率一致）。
	EasyAIPricingPointsRate = "EasyAIPricingPointsRate"
	// EasyAPIGatewayPricingPath 网关定价接口路径，缺省 /pricing。
	EasyAPIGatewayPricingPath = "EasyAPIGatewayPricingPath"
	// EasyAPIGatewayPricingBaseUrl 定价接口所在站点，留空用网关地址。
	// 网关控制台和调用入口常常不是同一个域名（如调用 api.easyapi.com、定价在 token.easyapi.com），
	// 这时填控制台地址，fetch-models 才能拿到定价与视频分辨率档位。
	EasyAPIGatewayPricingBaseUrl = "EasyAPIGatewayPricingBaseUrl"
	// GatewayBalanceAlertUSD 网关剩余额度低于这个美元数就给管理员发站内通知（API.md 38），默认 20；0 关闭。
	GatewayBalanceAlertUSD = "GatewayBalanceAlertUSD"
	// EasyAPIGatewayAccessToken 网关控制台的「系统访问令牌」（用户级，不是 sk 调用令牌）：
	// 查账户余额要走 /api/user/self，sk 令牌不限额时账单接口只回占位值，看不到真实余额。选填。
	EasyAPIGatewayAccessToken = "EasyAPIGatewayAccessToken"
	// EasyAPIGatewayUserId 与访问令牌配套的网关用户 ID（请求头 EasyAPI-User）。选填。
	EasyAPIGatewayUserId = "EasyAPIGatewayUserId"
	// EasyAIPricingUsdRate 人民币 / 美元汇率（网关报价为美元），默认 7.3。
	EasyAIPricingUsdRate = "EasyAIPricingUsdRate"
	// EasyAIPricingPartnerDiscount 网关对本令牌的合作折扣（token 日志 partner_discount），默认 1 不打折。
	// 2026-09-10 对照 token.easyapi.com 实际扣费：只有该折扣生效，/pricing 里的 model_discount（平台折扣）不计入账单。
	EasyAIPricingPartnerDiscount = "EasyAIPricingPartnerDiscount"

	// 按量计费模型折算成 次 / 张 / 秒 时的假设用量（token），缺省值按 2026-09-10 真实扣费校准，运营可按统计调整。
	EasyAIPricingTextInputTokens      = "EasyAIPricingTextInputTokens"      // 默认 500
	EasyAIPricingTextOutputTokens     = "EasyAIPricingTextOutputTokens"     // 默认 1000
	EasyAIPricingImageTokens          = "EasyAIPricingImageTokens"          // 默认 200（网关对 gpt-image 1792×1024 实记 139 输出 token）
	EasyAIPricingVideoTokensPerSecond = "EasyAIPricingVideoTokensPerSecond" // 默认 12750（按 seedance-2.0 / fast 实际每秒价格反推：倍率 × 0.0255 美元/秒）
	EasyAIPricingAudioOutputTokens    = "EasyAIPricingAudioOutputTokens"    // 默认 1000（约 200 字中文对应 1000 音频 token）
)

// PricingEstimate 按量模型折算用的假设用量。
type PricingEstimate struct {
	TextInputTokens      float64
	TextOutputTokens     float64
	ImageInputTokens     float64 // 图片提示词 token，固定 100
	ImageTokens          float64
	VideoTokensPerSecond float64
	AudioInputTokens     float64 // 语音输入文本 token，固定 100
	AudioOutputTokens    float64
}

// GetPricingEstimate 读取假设用量（setting 优先，非法回退默认）。
func GetPricingEstimate() PricingEstimate {
	return PricingEstimate{
		TextInputTokens:      getFloatSetting(EasyAIPricingTextInputTokens, 500),
		TextOutputTokens:     getFloatSetting(EasyAIPricingTextOutputTokens, 1000),
		ImageInputTokens:     100,
		ImageTokens:          getFloatSetting(EasyAIPricingImageTokens, 200),
		VideoTokensPerSecond: getFloatSetting(EasyAIPricingVideoTokensPerSecond, 12750),
		AudioInputTokens:     100,
		AudioOutputTokens:    getFloatSetting(EasyAIPricingAudioOutputTokens, 1000),
	}
}

// GetPricingUsdRate 人民币 / 美元汇率，非法时回退默认 7.3。
func GetPricingUsdRate() float64 {
	return getFloatSetting(EasyAIPricingUsdRate, 7.3)
}

// GetPricingPartnerDiscount 合作折扣 (0,1]，非法或大于 1 时按 1。
func GetPricingPartnerDiscount() float64 {
	v := getFloatSetting(EasyAIPricingPartnerDiscount, 1)
	if v > 1 {
		return 1
	}
	return v
}

// GetGatewayBaseUrl 网关地址（setting 优先，回退 env）。
func GetGatewayBaseUrl() string {
	if v := GetSettingString(EasyAPIGatewayBaseUrl); v != "" {
		return v
	}
	return common.GetEnvOrDefaultString("EASYAPI_BASE_URL", "https://api.easyapi.com")
}

// GetGatewayKey 网关令牌（setting 优先，回退 env）。
func GetGatewayKey() string {
	if v := GetSettingString(EasyAPIGatewayKey); v != "" {
		return v
	}
	return common.GetEnvOrDefaultString("EASYAPI_KEY", "")
}

// GetGatewayPricingBaseUrl 定价接口所在站点，留空回退网关地址。
func GetGatewayPricingBaseUrl() string {
	if v := strings.TrimRight(strings.TrimSpace(GetSettingString(EasyAPIGatewayPricingBaseUrl)), "/"); v != "" {
		return v
	}
	return GetGatewayBaseUrl()
}

// GetGatewayPricingPath 网关定价接口路径，缺省 /pricing；两侧空白与首尾斜杠归一化。
func GetGatewayPricingPath() string {
	v := strings.Trim(GetSettingString(EasyAPIGatewayPricingPath), " /")
	if v == "" {
		return "/pricing"
	}
	return "/" + v
}

// GetGatewayPricingDetailPath 网关模型详情接口路径：定价路径 + /detail（缺省 /pricing/detail）。
// 详情与定价同站同前缀，所以不单独开 setting 键。
func GetGatewayPricingDetailPath() string {
	return GetGatewayPricingPath() + "/detail"
}

// IsGatewayEnabled 网关开关：显式 "false"/"0" 关闭；缺省时有 Key 即开启。
func IsGatewayEnabled() bool {
	if v := GetSettingString(EasyAPIGatewayEnabled); v == "false" || v == "0" {
		return false
	}
	return GetGatewayKey() != ""
}

// GetUploadBaseUrl 上传文件对外前缀（setting 优先，回退 env）。
func GetUploadBaseUrl() string {
	if v := GetSettingString(UploadBaseUrl); v != "" {
		return v
	}
	return common.GetEnvOrDefaultString("UPLOAD_BASE_URL", "")
}

// GetPricingMarkupRate 网关成本 → 建议积分毛利系数，非法时回退默认 3.0。
func GetPricingMarkupRate() float64 {
	return getFloatSetting(EasyAIPricingMarkupRate, 3.0)
}

// GetPricingPointsRate 积分/元汇率，非法时回退默认 20。
func GetPricingPointsRate() float64 {
	return getFloatSetting(EasyAIPricingPointsRate, 20)
}

func getFloatSetting(key string, def float64) float64 {
	v := GetSettingString(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return def
	}
	return f
}

// GetGatewayBalanceAlertUSD 网关余额提醒阈值（美元）；≤0 表示关闭提醒。
func GetGatewayBalanceAlertUSD() float64 {
	return getFloatSetting(GatewayBalanceAlertUSD, 20)
}

// GetGatewayAccessToken 网关控制台系统访问令牌（选填，查账户余额用）。
func GetGatewayAccessToken() string {
	return strings.TrimSpace(GetSettingString(EasyAPIGatewayAccessToken))
}

// GetGatewayUserId 与访问令牌配套的网关用户 ID。
func GetGatewayUserId() string {
	return strings.TrimSpace(GetSettingString(EasyAPIGatewayUserId))
}
