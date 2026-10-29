package setting

import "strings"

// 发票配置 key（DB setting 表，PascalCase 键，管理端 PUT /admin/setting/:key 热更新，API.md 21）。
// 发票由第三方「EasyAPI 发票平台」开具，本地不存发票；appKey/appSecret 只在服务端使用。
const (
	// InvoiceEnabled 是否开启发票功能（"true"/"1" 开启，缺省关闭）
	InvoiceEnabled = "InvoiceEnabled"

	// InvoiceServiceUrl 发票平台地址（缺省 https://fapiao-api.easyapi.com）
	InvoiceServiceUrl = "InvoiceServiceUrl"

	// InvoiceAppKey 发票平台 appKey（密钥类，GET /admin/setting 只下发占位值）
	InvoiceAppKey = "InvoiceAppKey"

	// InvoiceAppSecret 发票平台 appSecret（密钥类）
	InvoiceAppSecret = "InvoiceAppSecret"

	// InvoiceTaxRate 开票税率（缺省 "0.01"）
	InvoiceTaxRate = "InvoiceTaxRate"

	// InvoiceItemNo 税收商品编码（缺省 "3040203"）
	InvoiceItemNo = "InvoiceItemNo"

	// InvoiceItemName 开票品名（缺省「技术服务费」）
	InvoiceItemName = "InvoiceItemName"

	// InvoiceCategory 缺省票种（缺省「全电电子普通发票」；用户申请时不传票种即用它）
	InvoiceCategory = "InvoiceCategory"
)

// 发票配置缺省值。
const (
	DefaultInvoiceServiceUrl = "https://fapiao-api.easyapi.com"
	DefaultInvoiceTaxRate    = "0.01"
	DefaultInvoiceItemNo     = "3040203"
	DefaultInvoiceItemName   = "技术服务费"
	DefaultInvoiceCategory   = "全电电子普通发票"
)

// InvoiceConfig 发票生效配置（缺省值已填好）。
type InvoiceConfig struct {
	Enabled    bool
	ServiceUrl string // 已去掉末尾 /
	AppKey     string
	AppSecret  string
	TaxRate    string
	ItemNo     string
	ItemName   string
	Category   string
}

// HasKeys appKey/appSecret 是否都已配置。
func (c InvoiceConfig) HasKeys() bool {
	return c.AppKey != "" && c.AppSecret != ""
}

// GetInvoiceConfig 读取发票生效配置。
func GetInvoiceConfig() InvoiceConfig {
	return InvoiceConfigFrom(GetSettingString)
}

// IsInvoiceEnabled 发票功能是否开启。
func IsInvoiceEnabled() bool {
	return GetSettingBool(InvoiceEnabled)
}

// InvoiceConfigFrom 按 get 读出的原始值计算生效配置（空值回落缺省）。get 注入便于测试。
func InvoiceConfigFrom(get func(string) string) InvoiceConfig {
	val := func(key, def string) string {
		if v := strings.TrimSpace(get(key)); v != "" {
			return v
		}
		return def
	}
	enabled := strings.TrimSpace(get(InvoiceEnabled))
	return InvoiceConfig{
		Enabled:    enabled == "true" || enabled == "1",
		ServiceUrl: strings.TrimRight(val(InvoiceServiceUrl, DefaultInvoiceServiceUrl), "/"),
		AppKey:     val(InvoiceAppKey, ""),
		AppSecret:  val(InvoiceAppSecret, ""),
		TaxRate:    val(InvoiceTaxRate, DefaultInvoiceTaxRate),
		ItemNo:     val(InvoiceItemNo, DefaultInvoiceItemNo),
		ItemName:   val(InvoiceItemName, DefaultInvoiceItemName),
		Category:   val(InvoiceCategory, DefaultInvoiceCategory),
	}
}
