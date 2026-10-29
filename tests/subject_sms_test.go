package tests

import (
	"testing"

	"easytv-api/common"
	"easytv-api/model"
)

// TestSubjectKindsAndLangs 主体类型与语种白名单。
func TestSubjectKindsAndLangs(t *testing.T) {
	for _, k := range []string{"character", "scene", "prop"} {
		if !model.SubjectKinds[k] {
			t.Fatalf("%s 应是合法主体类型", k)
		}
	}
	if model.SubjectKinds["vehicle"] {
		t.Fatal("未知主体类型不该通过")
	}
}

// TestParseSmsTestAccounts 测试手机号配置：格式不对的项跳过，验证码至少 6 位。
func TestParseSmsTestAccounts(t *testing.T) {
	got := common.ParseSmsTestAccounts(" 13800000000:123456, bad, 1390000000:654321, 13700000000:12, 13600000000:888888 ")
	if len(got) != 2 || got["13800000000"] != "123456" || got["13600000000"] != "888888" {
		t.Fatalf("parsed = %v", got)
	}
	if len(common.ParseSmsTestAccounts("")) != 0 {
		t.Fatal("空配置应为空")
	}
}
