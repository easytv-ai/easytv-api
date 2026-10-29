package tests

import (
	"strings"
	"testing"

	"easytv-api/service"
	"easytv-api/setting"
)

// TestGenerateQiniuKey key 格式：<project_code>/<日期>/<毫秒时间戳><后缀>。
func TestGenerateQiniuKey(t *testing.T) {
	initMockDB(t) // GetQiniuProjectCode 走 setting 读取（缺省回退需要 DB 可用）
	// 缺省 project_code=easytv
	key := service.GenerateQiniuKey(".png")
	if !strings.HasPrefix(key, "easytv/20") || !strings.HasSuffix(key, ".png") {
		t.Fatalf("bad key: %s", key)
	}
	if len(strings.Split(key, "/")) != 5 {
		t.Fatalf("key should have 5 segments: %s", key)
	}
}

// TestGenerateQiniuUploadToken 上传 token 非空且为三段 base64（不校验签名内容，仅验证流程可用）。
func TestGenerateQiniuUploadToken(t *testing.T) {
	initMockDB(t)
	token := service.GenerateQiniuUploadToken("easytv", "")
	if token == "" {
		t.Fatal("token should not be empty")
	}
	// 七牛 token 是 "ak:sign:policy" 三段 base64
	parts := strings.Split(token, ":")
	if len(parts) != 3 {
		t.Fatalf("token should have 3 parts: %s", token)
	}
}

// TestQiniuSettingDefaults 缺省配置回退值。
func TestQiniuSettingDefaults(t *testing.T) {
	initMockDB(t)
	if setting.GetQiniuBucketName() != "easyapi" {
		t.Fatalf("bucket default: %s", setting.GetQiniuBucketName())
	}
	if setting.GetQiniuProjectCode() != "easytv" {
		t.Fatalf("project code default: %s", setting.GetQiniuProjectCode())
	}
	if setting.GetQiniuDomain() != "https://qiniu.easyapi.com" {
		t.Fatalf("domain default: %s", setting.GetQiniuDomain())
	}
	// 未配置 AK/SK → 未启用
	if setting.IsQiniuEnabled() {
		t.Fatal("qiniu should be disabled without keys")
	}
}