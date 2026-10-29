package controller

import (
	"strings"
	"testing"
)

func aiChatBoolPtr(v bool) *bool    { return &v }
func aiChatIntPtr(v int) *int       { return &v }
func aiChatStrPtr(v string) *string { return &v }

// TestValidAIChatConfigPayload 表驱动覆盖 AI 对话配置校验的每个边界与枚举（纯函数，不连库、不起 HTTP）。
func TestValidAIChatConfigPayload(t *testing.T) {
	knowledgeMax := strings.Repeat("知", aiChatKnowledgeMaxRunes)       // 8000 个中文字符（24000 字节）
	knowledgeTooLong := strings.Repeat("知", aiChatKnowledgeMaxRunes+1) // 8001 个字符
	knowledgeTrimmed := "  \n" + knowledgeMax + "\n  "                 // 首尾空白裁剪后正好 8000 字

	cases := []struct {
		name    string
		payload aiChatConfigPayload
		priced  bool
		wantErr string
	}{
		{"空 payload（局部更新，不改任何键）", aiChatConfigPayload{}, false, ""},
		{"enabled/free 布尔合法", aiChatConfigPayload{Enabled: aiChatBoolPtr(false), Free: aiChatBoolPtr(true)}, false, ""},

		{"model 空串=清空，回到自动选模型", aiChatConfigPayload{Model: aiChatStrPtr("")}, false, ""},
		{"model 纯空白同样按清空", aiChatConfigPayload{Model: aiChatStrPtr("   ")}, false, ""},
		{"model 是已定价 text 模型", aiChatConfigPayload{Model: aiChatStrPtr("gpt-4o-mini")}, true, ""},
		{"model 裁剪空白后是已定价 text 模型", aiChatConfigPayload{Model: aiChatStrPtr(" gpt-4o-mini ")}, true, ""},
		{"model 未定价", aiChatConfigPayload{Model: aiChatStrPtr("unknown-model")}, false, "对话模型必须是已定价的文本模型"},
		{"model 已定价但不是 text 模型", aiChatConfigPayload{Model: aiChatStrPtr("seedance-2.0")}, false, "对话模型必须是已定价的文本模型"},

		{"max_tokens 下界 0（不发送）", aiChatConfigPayload{MaxTokens: aiChatIntPtr(0)}, false, ""},
		{"max_tokens 上界 32000", aiChatConfigPayload{MaxTokens: aiChatIntPtr(32000)}, false, ""},
		{"max_tokens 超过上界", aiChatConfigPayload{MaxTokens: aiChatIntPtr(32001)}, false, "最大输出 token 必须在 0 到 32000 之间"},
		{"max_tokens 负数", aiChatConfigPayload{MaxTokens: aiChatIntPtr(-1)}, false, "最大输出 token 必须在 0 到 32000 之间"},

		{"thinking_mode 留空", aiChatConfigPayload{ThinkingMode: aiChatStrPtr("")}, false, ""},
		{"thinking_mode off", aiChatConfigPayload{ThinkingMode: aiChatStrPtr("off")}, false, ""},
		{"thinking_mode on", aiChatConfigPayload{ThinkingMode: aiChatStrPtr("on")}, false, ""},
		{"thinking_mode 裁剪空白后合法", aiChatConfigPayload{ThinkingMode: aiChatStrPtr(" on ")}, false, ""},
		{"thinking_mode 非法值", aiChatConfigPayload{ThinkingMode: aiChatStrPtr("ON")}, false, "思考模式只能是 off、on 或留空"},

		{"thinking_effort 留空", aiChatConfigPayload{ThinkingEffort: aiChatStrPtr("")}, false, ""},
		{"thinking_effort low", aiChatConfigPayload{ThinkingEffort: aiChatStrPtr("low")}, false, ""},
		{"thinking_effort medium", aiChatConfigPayload{ThinkingEffort: aiChatStrPtr("medium")}, false, ""},
		{"thinking_effort high", aiChatConfigPayload{ThinkingEffort: aiChatStrPtr("high")}, false, ""},
		{"thinking_effort 非法值", aiChatConfigPayload{ThinkingEffort: aiChatStrPtr("xhigh")}, false, "思考强度只能是 low、medium、high 或留空"},

		{"daily_free_count 0=不限", aiChatConfigPayload{DailyFreeCount: aiChatIntPtr(0)}, false, ""},
		{"daily_free_count 上界 100000", aiChatConfigPayload{DailyFreeCount: aiChatIntPtr(100000)}, false, ""},
		{"daily_free_count 超过上界", aiChatConfigPayload{DailyFreeCount: aiChatIntPtr(100001)}, false, "每日免费次数必须在 0 到 100000 之间"},
		{"daily_free_count 负数", aiChatConfigPayload{DailyFreeCount: aiChatIntPtr(-1)}, false, "每日免费次数必须在 0 到 100000 之间"},

		{"knowledge 留空", aiChatConfigPayload{Knowledge: aiChatStrPtr("")}, false, ""},
		{"knowledge 正好 8000 字符（按字符不是字节）", aiChatConfigPayload{Knowledge: aiChatStrPtr(knowledgeMax)}, false, ""},
		{"knowledge 首尾空白不计入长度", aiChatConfigPayload{Knowledge: aiChatStrPtr(knowledgeTrimmed)}, false, ""},
		{"knowledge 8001 字符超限", aiChatConfigPayload{Knowledge: aiChatStrPtr(knowledgeTooLong)}, false, "平台知识最多 8000 字"},

		{"多字段非法时先报 model", aiChatConfigPayload{
			Model: aiChatStrPtr("unknown-model"), MaxTokens: aiChatIntPtr(99999),
		}, false, "对话模型必须是已定价的文本模型"},
		{"多字段非法时其次报 max_tokens", aiChatConfigPayload{
			MaxTokens: aiChatIntPtr(99999), ThinkingMode: aiChatStrPtr("on"), ThinkingEffort: aiChatStrPtr("xhigh"),
		}, false, "最大输出 token 必须在 0 到 32000 之间"},
		{"多字段非法时其次报 thinking_mode", aiChatConfigPayload{
			ThinkingMode: aiChatStrPtr("bogus"), ThinkingEffort: aiChatStrPtr("xhigh"),
		}, false, "思考模式只能是 off、on 或留空"},
		{"多字段非法时其次报 thinking_effort", aiChatConfigPayload{
			ThinkingEffort: aiChatStrPtr("xhigh"), DailyFreeCount: aiChatIntPtr(-5),
		}, false, "思考强度只能是 low、medium、high 或留空"},
		{"多字段非法时其次报 daily_free_count", aiChatConfigPayload{
			DailyFreeCount: aiChatIntPtr(-5), Knowledge: aiChatStrPtr(knowledgeTooLong),
		}, false, "每日免费次数必须在 0 到 100000 之间"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := tc.payload
			if got := validAIChatConfigPayload(&payload, tc.priced); got != tc.wantErr {
				t.Fatalf("validAIChatConfigPayload(%+v, priced=%v) = %q, want %q", tc.payload, tc.priced, got, tc.wantErr)
			}
		})
	}

	t.Run("nil payload 视为空更新", func(t *testing.T) {
		if got := validAIChatConfigPayload(nil, false); got != "" {
			t.Fatalf("validAIChatConfigPayload(nil, false) = %q, want \"\"", got)
		}
	})
}

// TestValidChatSettingWrite 通用设置入口（PUT /admin/setting/:key）的长度补校验：
// ChatKnowledge 超长必须被拒（堵住绕过专用接口的旁路），其它键一概不拦（保持既有语义）。
func TestValidChatSettingWrite(t *testing.T) {
	knowledgeMax := strings.Repeat("知", aiChatKnowledgeMaxRunes)
	knowledgeTooLong := strings.Repeat("知", aiChatKnowledgeMaxRunes+1)
	knowledgeTrimmed := "  " + knowledgeMax + "\n"

	cases := []struct {
		name    string
		key     string
		value   string
		wantErr string
	}{
		{"ChatKnowledge 正好 8000 字符", "ChatKnowledge", knowledgeMax, ""},
		{"ChatKnowledge 首尾空白不计入长度", "ChatKnowledge", knowledgeTrimmed, ""},
		{"ChatKnowledge 空白值", "ChatKnowledge", "   ", ""},
		{"ChatKnowledge 8001 字符被拒", "ChatKnowledge", knowledgeTooLong, "平台知识最多 8000 字"},
		{"其它键超长不拦（沿用既有语义）", "ChatMaxTokens", knowledgeTooLong, ""},
		{"任意键超长不拦", "SomeOtherSetting", knowledgeTooLong, ""},
		{"空 key 不拦", "", knowledgeTooLong, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validChatSettingWrite(tc.key, tc.value); got != tc.wantErr {
				t.Fatalf("validChatSettingWrite(%q, %d 字节) = %q, want %q", tc.key, len(tc.value), got, tc.wantErr)
			}
		})
	}
}
