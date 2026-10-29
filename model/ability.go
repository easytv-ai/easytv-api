package model

import "strings"

// 模型「能力位」（API.md 23）：媒体工具条上的 AI 动作按能力匹配模型，而不是写死模型名。
// 运营在管理端给某个模型打上能力标签（model_price.abilities），对应按钮就从「暂未接入」变可用。

const (
	AbilityUpscale         = "upscale"          // 超分 / 高清
	AbilityRemoveSubtitle  = "remove_subtitle"  // 智能去字幕
	AbilityVocalSeparation = "vocal_separation" // 人声提取
	AbilityBgmSeparation   = "bgm_separation"   // 背景音提取
	AbilityMatting         = "matting"          // 智能抠像
	AbilitySubjectRemove   = "subject_remove"   // 主体消除
	AbilitySubjectModify   = "subject_modify"   // 主体修改
	AbilitySubjectReplace  = "subject_replace"  // 主体替换
	AbilityMotionCapture   = "motion_capture"   // 深度动作捕捉
	AbilityDepthExtract    = "depth_extract"    // 深度提取（深度图 / 深度视频）
	// AbilityNativeAudio 原生带声：视频模型能按提示词里的台词直接生成说话声（口播预设只列这类模型）
	AbilityNativeAudio = "native_audio"
	// AbilityVideoReference 参考视频：视频模型接收 reference_video 素材（全能参考 / 爆款复刻只列这类模型，
	// 否则提交到网关才被拒）。按名推断 seedance-2 / wan r2v；运营可在定价表单改。
	AbilityVideoReference = "video_reference"
	// AbilityTranscribe 语音转写：接收音频文件出带时间戳的文本（网关 /v1/audio/transcriptions，
	// gpt-4o-transcribe 系）；画布「语音转字幕」只列这类模型。按名推断 transcribe / whisper。
	AbilityTranscribe = "transcribe"
)

// AllAbilities 管理端可写的能力白名单（顺序即前端展示顺序）。
func AllAbilities() []string {
	return []string{
		AbilityUpscale, AbilityRemoveSubtitle, AbilityVocalSeparation, AbilityBgmSeparation,
		AbilityMatting, AbilitySubjectRemove, AbilitySubjectModify, AbilitySubjectReplace, AbilityMotionCapture,
		AbilityDepthExtract,
		AbilityNativeAudio, AbilityVideoReference, AbilityTranscribe,
	}
}

// IsValidAbility 校验单个能力标识。
func IsValidAbility(v string) bool {
	for _, a := range AllAbilities() {
		if a == v {
			return true
		}
	}
	return false
}

// NormalizeAbilities 清洗管理端传来的能力串：小写、去空白、丢非法值、去重、保序。
// 非法值直接丢掉而不是报错：运营从网关导入的模型清单里可能带着我们不认识的能力名。
func NormalizeAbilities(raw string) string {
	seen := map[string]bool{}
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		v := strings.ToLower(strings.TrimSpace(part))
		if v == "" || !IsValidAbility(v) || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	joined := strings.Join(out, ",")
	if len(joined) > 255 {
		return joined[:255]
	}
	return joined
}

// AllowedAbilities 解析 Abilities 字段。
func (p *ModelPrice) AllowedAbilities() []string {
	if strings.TrimSpace(p.Abilities) == "" {
		return nil
	}
	out := []string{}
	for _, part := range strings.Split(p.Abilities, ",") {
		if v := strings.ToLower(strings.TrimSpace(part)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// DeclaresAbility 是否显式声明了该能力（只看 abilities 字段）。
func (p *ModelPrice) DeclaresAbility(ability string) bool {
	for _, a := range p.AllowedAbilities() {
		if a == ability {
			return true
		}
	}
	return false
}

// HasAbility 是否具备该能力：显式声明优先，没打标签时按模型名推断（见 InferAbilities）。
func (p *ModelPrice) HasAbility(ability string) bool {
	if p.DeclaresAbility(ability) {
		return true
	}
	for _, a := range InferAbilities(p.Model) {
		if a == ability {
			return true
		}
	}
	return false
}

// EffectiveAbilities 声明了就用声明的，否则按模型名推断；两者都有时取并集。
func (p *ModelPrice) EffectiveAbilities() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, a := range append(p.AllowedAbilities(), InferAbilities(p.Model)...) {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// InferAbilities 按模型名推断能力（对标 InferImageInput 的写法）：运营没打标签时，
// 名字里已经写清楚用途的模型也能直接用上，不必等配置。
func InferAbilities(modelName string) []string {
	m := strings.ToLower(strings.TrimSpace(modelName))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	out := []string{}
	add := func(a string) { out = append(out, a) }
	switch {
	case strings.Contains(m, "upscale"), strings.Contains(m, "超分"):
		add(AbilityUpscale)
	}
	switch {
	case strings.Contains(m, "subtitle"), strings.Contains(m, "去字幕"), strings.Contains(m, "eraser"):
		add(AbilityRemoveSubtitle)
	}
	switch {
	case strings.Contains(m, "vocal"), strings.Contains(m, "demucs"), strings.Contains(m, "spleeter"), strings.Contains(m, "人声"):
		// 人声分离与背景音分离是同一类模型的两种产物，名字里带 vocal 的两边都能出
		add(AbilityVocalSeparation)
		add(AbilityBgmSeparation)
	}
	switch {
	case strings.Contains(m, "bgm"), strings.Contains(m, "accompaniment"), strings.Contains(m, "karaoke"),
		strings.Contains(m, "伴奏"), strings.Contains(m, "背景音"):
		add(AbilityBgmSeparation)
	}
	switch {
	case strings.Contains(m, "matting"), strings.Contains(m, "-matte"), strings.Contains(m, "抠像"):
		add(AbilityMatting)
	}
	switch {
	case strings.Contains(m, "subject-remove"), strings.Contains(m, "object-remove"), strings.Contains(m, "主体消除"):
		add(AbilitySubjectRemove)
	}
	switch {
	case strings.Contains(m, "subject-modify"), strings.Contains(m, "subject-edit"), strings.Contains(m, "主体修改"):
		add(AbilitySubjectModify)
	}
	switch {
	case strings.Contains(m, "subject-replace"), strings.Contains(m, "object-replace"), strings.Contains(m, "主体替换"):
		add(AbilitySubjectReplace)
	}
	switch {
	case strings.Contains(m, "motion-capture"), strings.Contains(m, "mocap"), strings.Contains(m, "动捕"), strings.Contains(m, "动作捕捉"):
		add(AbilityMotionCapture)
	}
	switch {
	case strings.Contains(m, "depth-extract"), strings.Contains(m, "depth_extract"),
		strings.Contains(m, "depth-anything"), strings.Contains(m, "vda-"),
		strings.Contains(m, "深度提取"):
		add(AbilityDepthExtract)
	}
	if inferNativeAudio(m) {
		add(AbilityNativeAudio)
	}
	if inferVideoReference(m) {
		add(AbilityVideoReference)
	}
	if strings.Contains(m, "transcribe") || strings.Contains(m, "whisper") {
		add(AbilityTranscribe)
	}
	return out
}

// inferVideoReference 按模型名判断能否吃参考视频（2026-09 网关清单：seedance 2.x 全能参考、wan r2v 系列）。
// Kling O1 / Omni 上游本身支持视频参考，但 2026-09-18 实测经本网关提交必败（网关只走图片参考字段，
// 上游回 Image format is invalid），所以不在其列；sora / hailuo / veo 与 wan t2v 只收文字或图片。运营可显式勾选或去掉。
func inferVideoReference(m string) bool {
	switch {
	case strings.HasPrefix(m, "seedance-2"):
		return true
	case strings.HasPrefix(m, "wan") && strings.Contains(m, "r2v"):
		return true
	}
	return false
}

// inferNativeAudio 按模型名判断能否原生出声（2026-09 网关清单：sora-2、wan2.6+、Kling 2.6 / 3.0 / O1 / Omni、seedance 2.x、veo 3）。
// 运营可在定价表单显式勾选或去掉；这里只是没打标签时的缺省。
func inferNativeAudio(m string) bool {
	switch {
	case strings.HasPrefix(m, "sora"):
		return true
	case strings.HasPrefix(m, "veo-3"), strings.HasPrefix(m, "veo3"):
		return true
	case strings.HasPrefix(m, "seedance-2"):
		return true
	case strings.HasPrefix(m, "wan2.6"), strings.HasPrefix(m, "wan2.7"), strings.HasPrefix(m, "wan3"):
		return true
	case strings.HasPrefix(m, "kling-o1"), strings.HasPrefix(m, "kling-3"), strings.HasPrefix(m, "kling-2.6"), strings.Contains(m, "omni"):
		return true
	}
	return false
}
