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
	// 输入类型里有 video 也不给 video_reference：role 枚举只有图片，实测 Kling 带参考视频会被上游当成图片拒掉
	if len(f.Abilities) != 1 || f.Abilities[0] != "native_audio" {
		t.Errorf("abilities = %v", f.Abilities)
	}
	if f.VideoReference || f.VideoReferenceKnown {
		t.Errorf("Kling 这条通道没有输入素材表，不该下结论：%+v", f)
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

// TestParseGatewayVideoReference 参考视频只看 input_references 的 role / type 枚举（API.md 28.3）。
func TestParseGatewayVideoReference(t *testing.T) {
	// wan3.0：role 里有 reference_video，正是这个模型当前缺的能力位
	wan3 := json.RawMessage(`{"video":{"input":["text","image","audio","video"],"properties":{
		"prompt":{"type":"string"},
		"input_references":{"type":"array","items":{"type":"object","properties":{
			"role":{"type":"string","enum":["first_frame","last_frame","reference_image","reference_video","reference_audio"]},
			"type":{"type":"string","enum":["image","video","audio"]}}}}}}}`)
	f, ok := ParseGatewayParams(wan3)
	if !ok || !f.VideoReference || !f.VideoReferenceKnown {
		t.Fatalf("wan3.0 应判定能收参考视频：%+v ok=%v", f, ok)
	}
	if !hasAbility(f.Abilities, "video_reference") {
		t.Errorf("abilities = %v，应含 video_reference", f.Abilities)
	}

	// Kling 3.0 Omni：上游 input 写了 video，但 role / type 只有图片，必须判否
	kling := json.RawMessage(`{"video":{"input":["text","image","video"],"properties":{
		"input_references":{"type":"array","items":{"type":"object","properties":{
			"role":{"type":"string","enum":["first_frame","last_frame","reference_image"]},
			"type":{"type":"string","enum":["image"]}}}}}}}`)
	f, _ = ParseGatewayParams(kling)
	if f.VideoReference || !f.VideoReferenceKnown {
		t.Fatalf("Kling 的 role 只有图片，不该判成能收参考视频：%+v", f)
	}

	// 网关没给输入素材表（如 wan2.6-r2v 的 params 是 {}）：不给结论，交给按名推断
	if f, _ := ParseGatewayParams(json.RawMessage(`{"video":{"properties":{"duration":{"min":5,"max":5}}}}`)); f.VideoReferenceKnown {
		t.Fatalf("没有输入素材表时不该下结论：%+v", f)
	}
}

func TestSpellResolution(t *testing.T) {
	kling := []string{"720P", "1080P"}
	if got := spellResolution("720p", kling); got != "720P" {
		t.Fatalf("Kling 枚举是大写，送出 %q", got)
	}
	if got := spellResolution("1080P", kling); got != "1080P" {
		t.Fatalf("已经是枚举原文，送出 %q", got)
	}
	if got := spellResolution("2K", kling); got != "2K" {
		t.Fatalf("枚举对不上应保持原文，送出 %q", got)
	}
	if got := spellResolution("720P", nil); got != "720p" {
		t.Fatalf("没有参数表时应改成广场默认的小写，送出 %q", got)
	}
	seedance := []string{"480p", "720p", "1080p"}
	if got := spellResolution("720P", seedance); got != "720p" {
		t.Fatalf("Seedance 枚举是小写，送出 %q", got)
	}
	if got, ok := matchDeclared("16:9", []string{"16:9", "9:16", "1:1"}); !ok || got != "16:9" {
		t.Fatalf("比例 = %q ok=%v", got, ok)
	}
}

func hasAbility(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
