package service

// ChatParams 对话（/v1/chat/completions）的可选请求参数。
// 全零值即历史行为：请求体只有 model + messages，不追加任何字段。
type ChatParams struct {
	MaxTokens      int    // >0 时作为 max_tokens 发送；0 = 不发送
	ThinkingMode   string // "on" → thinking:{type:enabled}，"off" → thinking:{type:disabled}；其它值不发送
	ThinkingEffort string // 非空时作为 reasoning_effort 发送（low / medium / high）
}
