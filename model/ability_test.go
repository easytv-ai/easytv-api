package model

import "testing"

// TestNormalizeAbilities 管理端写入的能力串：丢掉不认识的名字、去重、保序。
func TestNormalizeAbilities(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"upscale", "upscale"},
		{" UPSCALE , matting ", "upscale,matting"},
		{"upscale,upscale,matting", "upscale,matting"},
		{"upscale,不存在的能力,matting", "upscale,matting"},
		{"remove_subtitle,vocal_separation,bgm_separation", "remove_subtitle,vocal_separation,bgm_separation"},
	}
	for _, c := range cases {
		if got := NormalizeAbilities(c.in); got != c.want {
			t.Errorf("NormalizeAbilities(%q) = %q，want %q", c.in, got, c.want)
		}
	}
}

// TestInferAbilities 没打标签时按模型名推断能力。
func TestInferAbilities(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{"topaz-upscale-2x", AbilityUpscale},
		{"vendor/video-upscale", AbilityUpscale},
		{"video-upscale-pro", AbilityUpscale},
		{"subtitle-eraser", AbilityRemoveSubtitle},
		{"demucs-v4", AbilityVocalSeparation},
		{"bgm-separator", AbilityBgmSeparation},
		{"rvc-matte", AbilityMatting},
		{"subject-remove-v2", AbilitySubjectRemove},
		{"subject-replace-v2", AbilitySubjectReplace},
		{"mocap-estimator", AbilityMotionCapture},
		{"vda-small", AbilityDepthExtract},
		{"depth-anything-v2", AbilityDepthExtract},
	}
	for _, c := range cases {
		got := InferAbilities(c.model)
		found := false
		for _, a := range got {
			if a == c.want {
				found = true
			}
		}
		if !found {
			t.Errorf("InferAbilities(%q) = %v，应包含 %s", c.model, got, c.want)
		}
	}
	// 参考视频：seedance 2.x / wan2.6+ / Kling O1、3.0、Omni 默认能吃 reference_video，其它视频模型不行
	for _, name := range []string{"seedance-2.0-fast", "seedance-2.5", "wan2.6-r2v-flash"} {
		if p := (&ModelPrice{Model: name}); !p.HasAbility(AbilityVideoReference) {
			t.Errorf("%q 应推断出 %s：%v", name, AbilityVideoReference, InferAbilities(name))
		}
	}
	for _, name := range []string{"sora-2", "hailuo-02", "veo-3.1", "seedance-1.5", "wan2.6-t2v", "wan2.7-t2v", "Kling-3.0", "Kling-2.6", "Kling-O1", "Kling-3.0-Omni"} {
		if p := (&ModelPrice{Model: name}); p.HasAbility(AbilityVideoReference) {
			t.Errorf("%q 不该推断出 %s", name, AbilityVideoReference)
		}
	}
	// 普通生成模型不该被误判成后处理能力
	for _, name := range []string{"seedream-4.0", "kling-v2", "gpt-4o-mini", "hailuo-02"} {
		if got := InferAbilities(name); len(got) != 0 {
			t.Errorf("InferAbilities(%q) = %v，应为空", name, got)
		}
	}
	// 人声分离模型同时能出伴奏
	vocal := InferAbilities("demucs-v4")
	if len(vocal) != 2 {
		t.Errorf("人声分离模型应同时具备背景音能力：%v", vocal)
	}
}

// TestModelPriceEffectiveAbilities 声明可覆盖推断，且生效能力是两者的并集。
func TestModelPriceEffectiveAbilities(t *testing.T) {
	p := &ModelPrice{Model: "video-upscale-pro", Abilities: "matting"}
	if !p.HasAbility(AbilityMatting) {
		t.Error("声明的能力应生效")
	}
	if !p.HasAbility(AbilityUpscale) {
		t.Error("没声明的模型名推断能力也应生效")
	}
	got := p.EffectiveAbilities()
	if len(got) != 2 {
		t.Errorf("生效能力应是并集：%v", got)
	}
	if !IsValidAbility(AbilityUpscale) || IsValidAbility("nope") {
		t.Error("能力白名单校验不对")
	}
}
