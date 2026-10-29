package setting

import (
	"testing"

	"easytv-api/common"
)

func TestEffectiveSmsConfig(t *testing.T) {
	base := common.SmsConfig{Enabled: true, Url: "https://env", AppId: "env-id", AppKey: "env-key", SignType: "md5", Project: "env-p"}

	db := map[string]string{SubMailAppId: "db-id"}
	cfg := EffectiveSmsConfig(base, func(k string) string { return db[k] })
	if cfg.AppId != "db-id" || cfg.AppKey != "env-key" || !cfg.Enabled {
		t.Fatalf("DB 覆盖单项: %+v", cfg)
	}

	// DB 清空后回落到 env，不必重启
	db = map[string]string{SubMailAppId: " "}
	if cfg := EffectiveSmsConfig(base, func(k string) string { return db[k] }); cfg.AppId != "env-id" {
		t.Fatalf("清空应回落 env: %+v", cfg)
	}

	db = map[string]string{SmsEnabled: "false"}
	if cfg := EffectiveSmsConfig(base, func(k string) string { return db[k] }); cfg.Enabled {
		t.Fatalf("DB 显式关闭: %+v", cfg)
	}
}

func TestMaskSecretSettings(t *testing.T) {
	m := map[string]string{
		"EasyAPIGatewayKey":       "sk-1",
		"SubMailAppKey":           "k",
		"WechatPayMchPrivateKey":  "pem",
		"GitHubOAuthClientSecret": "s",
		"PasswordLoginEnabled":    "true",
		"AlipayPublicKey":         "pub",
		"QiniuSecretKey":          "",
		"GrowthInviteBaseUrl":     "https://www.easytv.ai",
	}
	MaskSecretSettings(m)
	for _, k := range []string{"EasyAPIGatewayKey", "SubMailAppKey", "WechatPayMchPrivateKey", "GitHubOAuthClientSecret"} {
		if m[k] != MaskedSecret {
			t.Errorf("%s 应脱敏: %q", k, m[k])
		}
	}
	if m["PasswordLoginEnabled"] != "true" || m["AlipayPublicKey"] != "pub" || m["QiniuSecretKey"] != "" || m["GrowthInviteBaseUrl"] == MaskedSecret {
		t.Errorf("非密钥 / 空值不应动: %v", m)
	}
}
