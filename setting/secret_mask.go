package setting

import "regexp"

// MaskedSecret 管理端设置列表里密钥类配置的占位值（manage 识别全星号为「已配置、不回显」）。
const MaskedSecret = "********"

// secretSettingKey 密钥类设置键：网关 / 七牛 / 短信 / 支付私钥 / OAuth 密钥。不匹配 password（PasswordLoginEnabled 是开关）。
// 公钥与证书不是秘密，不在此列（manage 依旧只在用户重新填写时才提交它们）。
var secretSettingKey = regexp.MustCompile(`(?i)(secret|privatekey|appkey|gatewaykey|accesskey|accesstoken|apiv3key)`)

// IsSecretSettingKey 是否密钥类设置键。
func IsSecretSettingKey(key string) bool {
	return secretSettingKey.MatchString(key)
}

// MaskSecretSettings 把密钥类设置的非空值替换成占位值（原地修改）。
// 管理端设置是只写的：明文不下发，前端内存和浏览器调试工具里都看不到。
func MaskSecretSettings(settings map[string]string) {
	for k, v := range settings {
		if v != "" && IsSecretSettingKey(k) {
			settings[k] = MaskedSecret
		}
	}
}
