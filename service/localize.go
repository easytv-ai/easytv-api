package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"easytv-api/model"
	"easytv-api/setting"

	"gorm.io/gorm"
)

// 字幕与多语言（F5）：翻译走文本模型；配音按语种先翻译再 TTS；成片按语种取字幕。
// 不做语音转写（网关暂无 STT），字幕文本来自策划案台词或前端手填。

// SupportedLangs 语种 → 展示名。
var SupportedLangs = map[string]string{
	"zh": "中文", "en": "English", "ja": "日本語", "ko": "한국어", "es": "Español", "fr": "Français",
}

var langPromptName = map[string]string{
	"zh": "简体中文", "en": "英文", "ja": "日文", "ko": "韩文", "es": "西班牙文", "fr": "法文",
}

// NormalizeLang 校验语种；未配置或不支持返回空。
func NormalizeLang(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	if l == "" {
		return ""
	}
	if custom := strings.TrimSpace(setting.GetSettingString("ComposeLangs")); custom != "" {
		for _, item := range strings.Split(custom, ",") {
			if strings.ToLower(strings.TrimSpace(item)) == l {
				return l
			}
		}
		return ""
	}
	if _, ok := SupportedLangs[l]; ok {
		return l
	}
	return ""
}

// TranslateText 用文本模型翻译（只返回译文）。
func TranslateText(text, targetLang string) (string, error) {
	text = strings.TrimSpace(text)
	lang := NormalizeLang(targetLang)
	if text == "" || lang == "" {
		return "", errors.New("请提供文本与目标语种")
	}
	if lang == "zh" {
		return text, nil
	}
	modelName := planModel()
	if modelName == "" {
		return "", errors.New("暂无可用的文本模型")
	}
	chatter, ok := FindProvider(model.TaskTypeText, modelName).(TextChatter)
	if !ok {
		return "", errors.New("当前网关不支持翻译")
	}
	name := langPromptName[lang]
	if name == "" {
		name = lang
	}
	prompt := fmt.Sprintf("把下面的内容翻译成%s，只输出译文，不要解释、不要引号、保留换行：\n\n%s", name, text)
	out, err := chatter.Chat(modelName, prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// WithVoiceParam 往 params JSON 里写入 voice。
func WithVoiceParam(paramsJSON, voice string) string {
	if voice == "" {
		return paramsJSON
	}
	m := map[string]any{}
	if paramsJSON != "" {
		_ = json.Unmarshal([]byte(paramsJSON), &m)
	}
	m["voice"] = voice
	b, _ := json.Marshal(m)
	return string(b)
}

// LocalizeScreenplay 把策划案台词整份翻译，写进 plan.shots[].lines[lang]。
func LocalizeScreenplay(userId int, sp *model.Screenplay, targetLang string) (*model.Plan, error) {
	lang := NormalizeLang(targetLang)
	if lang == "" {
		return nil, errors.New("不支持的语种")
	}
	plan, err := sp.ParsePlan()
	if err != nil {
		return nil, err
	}
	// 翻译单元：有对白条目的镜头按条翻（每条各自存译文，整镜译文用空格拼回），没有的整句台词一条
	type unit struct{ shot, line int }
	lines := make([]string, 0, len(plan.Shots))
	units := make([]unit, 0, len(plan.Shots))
	for i, s := range plan.Shots {
		if len(s.Dialogue) > 0 {
			for j, d := range s.Dialogue {
				if strings.TrimSpace(d.Text) != "" {
					lines = append(lines, fmt.Sprintf("%d. %s", len(lines)+1, d.Text))
					units = append(units, unit{i, j})
				}
			}
			continue
		}
		if strings.TrimSpace(s.Line) != "" {
			lines = append(lines, fmt.Sprintf("%d. %s", len(lines)+1, s.Line))
			units = append(units, unit{i, -1})
		}
	}
	if len(lines) == 0 {
		return plan, nil
	}
	translated, err := TranslateText(strings.Join(lines, "\n"), lang)
	if err != nil {
		return nil, err
	}
	parts := splitNumbered(translated, len(lines))
	joined := map[int][]string{}
	for n, u := range units {
		if n >= len(parts) || strings.TrimSpace(parts[n]) == "" {
			continue
		}
		text := strings.TrimSpace(parts[n])
		if u.line >= 0 {
			d := &plan.Shots[u.shot].Dialogue[u.line]
			if d.Lines == nil {
				d.Lines = map[string]string{}
			}
			d.Lines[lang] = text
			joined[u.shot] = append(joined[u.shot], text)
			continue
		}
		if plan.Shots[u.shot].Lines == nil {
			plan.Shots[u.shot].Lines = map[string]string{}
		}
		plan.Shots[u.shot].Lines[lang] = text
	}
	for i, texts := range joined {
		if plan.Shots[i].Lines == nil {
			plan.Shots[i].Lines = map[string]string{}
		}
		plan.Shots[i].Lines[lang] = strings.Join(texts, " ")
	}
	b, _ := json.Marshal(plan)
	// 译文写回也算改稿：修订号 +1，已建画布会标 stale（复审 D02）
	if err := model.UpdateScreenplayFields(sp.Id, map[string]any{"plan": string(b), "revision": gorm.Expr("revision + 1")}); err != nil {
		return nil, err
	}
	Track(userId, "screenplay_localize", map[string]any{"screenplay_id": sp.Id, "lang": lang})
	return plan, nil
}

// splitNumbered 把「1. xxx」的多行译文按序号拆开；拆不出就按行数均分。
func splitNumbered(text string, want int) []string {
	out := make([]string, 0, want)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i := strings.IndexAny(line, ".、）)"); i > 0 && i <= 3 {
			if _, err := fmt.Sscanf(line[:i], "%d", new(int)); err == nil {
				line = strings.TrimSpace(line[i+1:])
			}
		}
		out = append(out, line)
	}
	return out
}

// LineForLang 取某镜在目标语种下的台词，缺译文回退原文。
func LineForLang(s model.PlanShot, lang string) string {
	if lang != "" && lang != "zh" && s.Lines != nil {
		if v := strings.TrimSpace(s.Lines[lang]); v != "" {
			return v
		}
	}
	return s.Line
}
