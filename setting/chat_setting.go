package setting

import "strings"

// AI 对话（对话式工作流生成）运行参数（DB setting 表，PascalCase 键，管理端 /admin/ai-chat/config 可热更新）。
// 缺省值即下面注释里的值；对话模型沿用 AgentModel（getter GetAgentModel），这里不另开模型键。
const (
	ChatFreeEnabled    = "ChatFreeEnabled"    // "false"/"0" 关闭；缺省开启（同 IsAgentEnabled 的写法）
	ChatMaxTokens      = "ChatMaxTokens"      // 单次最大输出 token，0=不发送该字段；夹到 [0, 32000]
	ChatThinkingMode   = "ChatThinkingMode"   // ""（不发送）/ "off" / "on"；其它值一律按 ""
	ChatThinkingEffort = "ChatThinkingEffort" // ""（不发送）/ "low" / "medium" / "high"；其它值一律按 ""
	ChatDailyFreeCount = "ChatDailyFreeCount" // 每日免费对话次数，默认 100；显式 0 表示不限
	ChatKnowledge      = "ChatKnowledge"      // 平台知识补充文本，注入系统提示词（写入侧限制 ≤8000 字）
)

// IsChatFreeEnabled 对话免费开关："false"/"0" → false；缺省 true。
func IsChatFreeEnabled() bool {
	v := GetSettingString(ChatFreeEnabled)
	return v != "false" && v != "0"
}

// GetChatMaxTokens 单次最大输出 token：非法值回退 0（不发送），夹到 [0, 32000]。
func GetChatMaxTokens() int {
	return clampAgentInt(getIntSetting(ChatMaxTokens, 0), 0, 32000)
}

// GetChatThinkingMode 思考模式：仅 "" / "off" / "on" 合法，其它值按 ""（不发送）。
func GetChatThinkingMode() string {
	switch v := strings.TrimSpace(GetSettingString(ChatThinkingMode)); v {
	case "off", "on":
		return v
	default:
		return ""
	}
}

// GetChatThinkingEffort 思考强度：仅 "" / "low" / "medium" / "high" 合法，其它值按 ""（不发送）。
func GetChatThinkingEffort() string {
	switch v := strings.TrimSpace(GetSettingString(ChatThinkingEffort)); v {
	case "low", "medium", "high":
		return v
	default:
		return ""
	}
}

// GetChatDailyFreeCount 每日免费对话次数：默认 100；显式 0 表示不限（getIntSetting 对负数等非法值回退默认 100）。
func GetChatDailyFreeCount() int {
	return clampAgentInt(getIntSetting(ChatDailyFreeCount, 100), 0, 0)
}

// GetChatKnowledge 平台知识补充文本：只做首尾空白处理，长度上限由写入侧校验拦截。
func GetChatKnowledge() string {
	return strings.TrimSpace(GetSettingString(ChatKnowledge))
}
