package service

import (
	"strings"

	"easytv-api/model"
)

// SkillGuideMaxChars 执行规则与输出格式各自的长度上限。
// 契约整段会挤占上下文，太长反而把用户真正的需求顶出窗口。
const SkillGuideMaxChars = 4000

// ApplySkillGuide 把技能声明的执行规则与输出格式拼到提示词最前面。
//
// 调用方负责只对文本任务使用：图片 / 视频的提示词直接送进模型，
// 混入「按以下步骤执行」这类指令只会把画面带偏。
func ApplySkillGuide(s *model.Skill, prompt string) string {
	if s == nil {
		return prompt
	}
	guide := strings.TrimSpace(s.Guide)
	format := strings.TrimSpace(s.OutputFormat)
	if guide == "" && format == "" {
		return prompt
	}
	var b strings.Builder
	if guide != "" {
		b.WriteString("【执行规则】\n")
		b.WriteString(guide)
		b.WriteString("\n\n")
	}
	if format != "" {
		b.WriteString("【输出格式】\n")
		b.WriteString(format)
		b.WriteString("\n\n")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return strings.TrimSpace(b.String())
	}
	b.WriteString("【用户需求】\n")
	b.WriteString(prompt)
	return b.String()
}
