package setting

import (
	"fmt"

	"easytv-api/model"
)

// 支付配置 key（存 DB setting 表，运营可热更新）。
const (
	// 充值基础
	KeyMinRecharge   = "MinRecharge"           // 最低充值金额（元），默认 1
	KeyAmountOptions = "RechargeAmountOptions" // 可选充值面额（逗号分隔），默认 10,20,50,100,200,500
	KeyServerAddress = "ServerAddress"         // 站点地址（拼接回调 URL）

	// 微信支付 Native（v3）
	KeyWechatPayAppId       = "WechatPayAppId"
	KeyWechatPayMchId       = "WechatPayMchId"
	KeyWechatPayMchSerialNo = "WechatPayMchSerialNo"
	KeyWechatPayMchPrivKey  = "WechatPayMchPrivateKey" // 商户 API 私钥 PEM
	KeyWechatPayApiV3Key    = "WechatPayAPIv3Key"
	KeyWechatPayNotifyURL   = "WechatPayNotifyURL" // 留空则 ServerAddress + /wechat/webhook
	KeyWechatPayEnabled     = "WechatPayEnabled"

	// 支付宝（当面付/电脑网站支付，证书模式，对齐 easyapi-ai-api）
	KeyAlipayAppId            = "AlipayAppId"
	KeyAlipayAppPrivateKey    = "AlipayAppPrivateKey"
	KeyAlipayPublicKey        = "AlipayPublicKey"        // 支付宝公钥证书（LoadAlipayCertPublicKey）
	KeyAlipayAppCertPublicKey = "AlipayAppCertPublicKey" // 应用公钥证书（LoadAppCertPublicKey）
	KeyAlipayRootCert         = "AlipayRootCert"         // 支付宝根证书（LoadAliPayRootCert）
	KeyAlipayNotifyURL        = "AlipayNotifyUrl"
	KeyAlipayReturnUrl        = "AlipayReturnUrl" // 电脑网站支付回跳地址，留空则 ServerAddress + /console/recharge
	KeyAlipayEnabled          = "AlipayEnabled"
	KeyAlipayF2FEnabled       = "AlipayF2FEnabled"   // 当面付开关；留空跟随 AlipayEnabled（向后兼容）
	KeyAlipayPCWebEnabled     = "AlipayPCWebEnabled" // 电脑网站支付开关
	KeyAlipayIsProduction     = "AlipayIsProduction" // 缺省 true；false 走支付宝沙箱网关（配合沙箱 AppId/密钥/证书）
)

// GetSettingString 读取配置字符串。
func GetSettingString(key string) string {
	return model.GetSetting(key)
}

// GetSettingBool 读取配置布尔值。
func GetSettingBool(key string) bool {
	return model.GetSetting(key) == "true" || model.GetSetting(key) == "1"
}

// IsWechatPayEnabled 微信支付开关。
func IsWechatPayEnabled() bool {
	return GetSettingBool(KeyWechatPayEnabled)
}

// IsAlipayEnabled 支付宝开关（总开关）。
func IsAlipayEnabled() bool {
	return GetSettingBool(KeyAlipayEnabled)
}

// IsAlipayF2FEnabled 支付宝当面付开关；未显式配置时跟随 AlipayEnabled（老现网默认开当面付）。
func IsAlipayF2FEnabled() bool {
	if !IsAlipayEnabled() {
		return false
	}
	v := GetSettingString(KeyAlipayF2FEnabled)
	if v == "" {
		return true
	}
	return v == "true" || v == "1"
}

// IsAlipayPCWebEnabled 支付宝电脑网站支付开关。
func IsAlipayPCWebEnabled() bool {
	return IsAlipayEnabled() && GetSettingBool(KeyAlipayPCWebEnabled)
}

// IsAlipayProduction 是否支付宝正式环境（缺省 true；false = 沙箱联调）。
func IsAlipayProduction() bool {
	v := GetSettingString(KeyAlipayIsProduction)
	return v != "false" && v != "0"
}

// GetMinRecharge 最低充值金额（元）。
func GetMinRecharge() float64 {
	v := GetSettingString(KeyMinRecharge)
	if v == "" {
		return 1
	}
	var n float64
	if _, err := fmt.Sscanf(v, "%f", &n); err != nil || n <= 0 {
		return 1
	}
	return n
}
