package providers

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"easytv-api/service"
)

// chatBodyKeys 请求体的键集合（排序后），用于断言只追加了需要的字段。不联网。
func chatBodyKeys(body map[string]any) []string {
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestChatRequestBodyZeroParams 全零 ChatParams：请求体与改动前逐字段一致（只有 model + messages）。
func TestChatRequestBodyZeroParams(t *testing.T) {
	const modelName, prompt = "gpt-4o-mini", "写一个分镜"
	body := chatRequestBody(modelName, prompt, service.ChatParams{})

	if got, want := chatBodyKeys(body), []string{"messages", "model"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("全零请求体键集合 = %v, want %v", got, want)
	}
	if body["model"] != modelName {
		t.Errorf("model = %v, want %s", body["model"], modelName)
	}
	wantMsgs := []map[string]string{{"role": "user", "content": prompt}}
	if !reflect.DeepEqual(body["messages"], wantMsgs) {
		t.Errorf("messages = %#v, want %#v", body["messages"], wantMsgs)
	}

	// 序列化后的报文也必须与改动前完全一致（map 键序固定为字母序）。
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"messages":[{"content":"写一个分镜","role":"user"}],"model":"gpt-4o-mini"}`
	if string(raw) != want {
		t.Fatalf("序列化报文 = %s, want %s", raw, want)
	}
}

// TestChatRequestBodyOptions 逐项与组合：max_tokens / reasoning_effort / thinking 只在配置非空时出现，键名照契约。
func TestChatRequestBodyOptions(t *testing.T) {
	const modelName, prompt = "gpt-4o-mini", "hi"
	cases := []struct {
		name string
		opts service.ChatParams
		want []string
	}{
		{"max_tokens", service.ChatParams{MaxTokens: 2048}, []string{"max_tokens", "messages", "model"}},
		{"max_tokens 为 0 不发送", service.ChatParams{MaxTokens: 0}, []string{"messages", "model"}},
		{"reasoning_effort", service.ChatParams{ThinkingEffort: "high"}, []string{"messages", "model", "reasoning_effort"}},
		{"thinking on", service.ChatParams{ThinkingMode: "on"}, []string{"messages", "model", "thinking"}},
		{"thinking off", service.ChatParams{ThinkingMode: "off"}, []string{"messages", "model", "thinking"}},
		{"thinking 非法值不发送", service.ChatParams{ThinkingMode: "maybe"}, []string{"messages", "model"}},
		{"全开", service.ChatParams{MaxTokens: 32000, ThinkingMode: "on", ThinkingEffort: "medium"},
			[]string{"max_tokens", "messages", "model", "reasoning_effort", "thinking"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := chatRequestBody(modelName, prompt, c.opts)
			if got := chatBodyKeys(body); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("键集合 = %v, want %v", got, c.want)
			}
			if c.opts.MaxTokens > 0 && body["max_tokens"] != c.opts.MaxTokens {
				t.Errorf("max_tokens = %v, want %d", body["max_tokens"], c.opts.MaxTokens)
			}
			if c.opts.ThinkingEffort != "" && body["reasoning_effort"] != c.opts.ThinkingEffort {
				t.Errorf("reasoning_effort = %v, want %s", body["reasoning_effort"], c.opts.ThinkingEffort)
			}
			wantType := ""
			switch c.opts.ThinkingMode {
			case "on":
				wantType = "enabled"
			case "off":
				wantType = "disabled"
			}
			if wantType != "" {
				th, ok := body["thinking"].(map[string]string)
				if !ok || th["type"] != wantType {
					t.Errorf("thinking = %#v, want type=%s", body["thinking"], wantType)
				}
			}
			// messages 的形状与顺序不受可选项影响。
			if !reflect.DeepEqual(body["messages"], []map[string]string{{"role": "user", "content": prompt}}) {
				t.Errorf("messages 被改动了: %#v", body["messages"])
			}
		})
	}
}
