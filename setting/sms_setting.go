package setting

import (
	"strconv"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
)

// 短信验证码登录配置 key（DB setting 表，PascalCase 键，管理端可热更新）。
// 写入 setting 表后，LoadSmsConfigFromDB() 在下一次 sync ticker（60s）内
// 覆盖 common 包全局变量使配置生效。
const (
	// SmsEnabled 是否走真实 SubMail 发送；false 时验证码打印到日志便于联调
	SmsEnabled = "SmsEnabled"

	// SubMailUrl SubMail API 基础 URL（缺省 https://api-v4.mysubmail.com）
	SubMailUrl = "SubMailUrl"

	// SubMailAppId SubMail 应用 ID
	SubMailAppId = "SubMailAppId"

	// SubMailAppKey SubMail 应用密钥
	SubMailAppKey = "SubMailAppKey"

	// SubMailSignType 签名模式：md5 / sha1 / normal（缺省 md5）
	SubMailSignType = "SubMailSignType"

	// SubMailSmsProject 短信模板 ID（SubMail > 短信 > 项目）
	SubMailSmsProject = "SubMailSmsProject"
)

// IsSmsSettingKey 是否短信配置键。
func IsSmsSettingKey(key string) bool {
	switch key {
	case SmsEnabled, SubMailUrl, SubMailAppId, SubMailAppKey, SubMailSignType, SubMailSmsProject:
		return true
	}
	return false
}

// IsSmsEnabled 短信发送是否启用。
func IsSmsEnabled() bool {
	return GetSettingBool(SmsEnabled)
}

// GetSubMailUrl 获取 SubMail API 基础 URL。
func GetSubMailUrl() string {
	if v := GetSettingString(SubMailUrl); v != "" {
		return v
	}
	return "https://api-v4.mysubmail.com"
}

// GetSubMailAppId 获取 SubMail 应用 ID。
func GetSubMailAppId() string {
	return GetSettingString(SubMailAppId)
}

// GetSubMailAppKey 获取 SubMail 应用密钥。
func GetSubMailAppKey() string {
	return GetSettingString(SubMailAppKey)
}

// GetSubMailSignType 获取签名模式（缺省 md5）。
func GetSubMailSignType() string {
	if v := GetSettingString(SubMailSignType); v != "" {
		return v
	}
	return "md5"
}

// GetSubMailSmsProject 获取短信模板 ID。
func GetSubMailSmsProject() string {
	return GetSettingString(SubMailSmsProject)
}

// LoadSmsConfigFromDB 以 env 为基线、叠加 DB setting 得到生效配置，整体替换 common 的当前配置。
// 每次都从基线重新算：DB 里清空某项就回落到 env，不必重启。
// 在 model.InitSettingMap() 后（main.go）调用；每分钟 sync ticker 中也会调用。
func LoadSmsConfigFromDB() {
	common.SetSmsConfig(EffectiveSmsConfig(common.SmsEnvConfig(), model.GetSetting))
}

// EffectiveSmsConfig 基线 + DB 覆盖（DB 值为空视为未设置）。get 注入便于测试。
func EffectiveSmsConfig(base common.SmsConfig, get func(string) string) common.SmsConfig {
	cfg := base
	if v := strings.TrimSpace(get(SubMailUrl)); v != "" {
		cfg.Url = v
	}
	if v := strings.TrimSpace(get(SubMailAppId)); v != "" {
		cfg.AppId = v
	}
	if v := strings.TrimSpace(get(SubMailAppKey)); v != "" {
		cfg.AppKey = v
	}
	if v := strings.TrimSpace(get(SubMailSignType)); v != "" {
		cfg.SignType = v
	}
	if v := strings.TrimSpace(get(SubMailSmsProject)); v != "" {
		cfg.Project = v
	}
	if v := strings.TrimSpace(get(SmsEnabled)); v != "" {
		cfg.Enabled = v == "true" || v == "1"
	}
	return cfg
}

// SmsSettingsForAdmin 管理端设置列表里的短信项填成实际生效值（含 env 基线），
// 否则配置走 env 时表单显示「未启用、AppId 为空」，运营照着改会和 env 混用。AppKey 只标记是否已配置。
func SmsSettingsForAdmin(settings map[string]string) {
	cfg := common.CurrentSmsConfig()
	settings[SmsEnabled] = strconv.FormatBool(cfg.Enabled)
	settings[SubMailUrl] = cfg.Url
	settings[SubMailAppId] = cfg.AppId
	settings[SubMailSignType] = cfg.SignType
	settings[SubMailSmsProject] = cfg.Project
	settings[SubMailAppKey] = ""
	if cfg.AppKey != "" {
		settings[SubMailAppKey] = MaskedSecret
	}
}
