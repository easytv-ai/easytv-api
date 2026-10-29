package providers

import (
	"encoding/json"
	"testing"
)

// TestParseGatewayParams 网关 Kling 系列的参数表 → durations / resolutions / abilities（API.md 28.3）。
func TestParseGatewayParams(t *testing.T) {
	kling := json.RawMessage(`{"video":{"input":["text","image","video"],"output":["video"],"properties":{
		"prompt":{"type":"string"},
		"duration":{"type":"integer","min":3,"max":15,"default":5},
		"resolution":{"type":"string","enum":["720P","1080P"],"default":"720P"},
		"aspect_ratio":{"type":"string","enum":["16:9","9:16","1:1"]},
		"generate_audio":{"type":"boolean","default":false}}}}`)
	f, ok := ParseGatewayParams(kling)
	if !ok {
		t.Fatal("应解析出结构化参数")
	}
	if len(f.Durations) != 13 || f.Durations[0] != 3 || f.Durations[12] != 15 {
		t.Errorf("durations = %v", f.Durations)
	}
	if len(f.Resolutions) != 2 || f.Resolutions[1] != "1080P" {
		t.Errorf("resolutions = %v", f.Resolutions)
	}
	if len(f.AspectRatios) != 3 {
		t.Errorf("aspect ratios = %v", f.AspectRatios)
	}
	// 输入类型里有 video 也不给 video_reference：实测 Kling 带参考视频会被上游当成图片拒掉
	if len(f.Abilities) != 1 || f.Abilities[0] != "native_audio" {
		t.Errorf("abilities = %v", f.Abilities)
	}

	// enum 时长优先于范围，去重排序；字符串形式的数字也认
	enum := json.RawMessage(`{"video":{"properties":{"duration":{"enum":["12",4,8,8,"x"],"min":1,"max":100}}}}`)
	f, ok = ParseGatewayParams(enum)
	if !ok || len(f.Durations) != 3 || f.Durations[0] != 4 || f.Durations[2] != 12 {
		t.Errorf("enum durations = %v ok=%v", f.Durations, ok)
	}

	// 范围太宽（连续值）不展开
	wide := json.RawMessage(`{"video":{"properties":{"duration":{"min":1,"max":120}}}}`)
	if f, ok = ParseGatewayParams(wide); ok || len(f.Durations) != 0 {
		t.Errorf("过宽范围不该展开：%v ok=%v", f.Durations, ok)
	}

	// 空 / 没有结构化参数
	for _, raw := range []string{"", "{}", "null", `{"video":{"input":["text"],"properties":{"prompt":{"type":"string"}}}}`} {
		if _, ok := ParseGatewayParams(json.RawMessage(raw)); ok {
			t.Errorf("%q 不该算有结构化参数", raw)
		}
	}
}
