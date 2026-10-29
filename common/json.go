package common

import "encoding/json"

// Marshal 序列化任意值到 JSON 字节。
func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal 反序列化 JSON 字节到 v。
func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// UnmarshalJsonStr 反序列化 JSON 字符串到 v。
func UnmarshalJsonStr(raw string, v any) error {
	return json.Unmarshal([]byte(raw), v)
}

// DecodeJsonToMap 解析 JSON 字符串为 map。
func DecodeJsonToMap(raw string) (map[string]any, error) {
	m := make(map[string]any)
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}
