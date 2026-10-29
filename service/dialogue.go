package service

import (
	"errors"
	"fmt"
	"strings"

	"easytv-api/model"
)

// 对白与正反打（API.md 25.1 / 25.3）：把镜头的对白条目变成配音分段，以及按两人对白生成一组能剪接的镜头。

// VoiceSegment 配音的一段：谁说、用什么音色、说什么。写进配音节点 data.segments，前端提交音频任务时原样带上 params.segments。
type VoiceSegment struct {
	Speaker string `json:"speaker"`
	Name    string `json:"name"`
	Voice   string `json:"voice,omitempty"`
	Style   string `json:"style,omitempty"`
	Text    string `json:"text"`
	Emotion string `json:"emotion,omitempty"`
}

// VoiceSegments 镜头的配音分段：有对白条目按条出（音色取说话人的 Voice），没有就整句台词一段（无说话人）。
func VoiceSegments(plan *model.Plan, s model.PlanShot) []VoiceSegment {
	if len(s.Dialogue) == 0 {
		if strings.TrimSpace(s.Line) == "" {
			return nil
		}
		return []VoiceSegment{{Text: s.Line, Name: "旁白"}}
	}
	out := make([]VoiceSegment, 0, len(s.Dialogue))
	for _, d := range s.Dialogue {
		seg := VoiceSegment{Speaker: d.Speaker, Name: plan.SpeakerName(d.Speaker), Text: d.Text, Emotion: d.Emotion}
		if c := plan.CharacterByKey(d.Speaker); c != nil {
			seg.Voice, seg.Style = c.Voice, c.VoiceStyle
		}
		out = append(out, seg)
	}
	return out
}

// SegmentsForLang 分段文本换成指定语种的译文（没有译文的条目保留原文）。
func SegmentsForLang(plan *model.Plan, s model.PlanShot, lang string) []VoiceSegment {
	segs := VoiceSegments(plan, s)
	if lang == "" || lang == "zh" {
		return segs
	}
	if len(s.Dialogue) == 0 {
		if len(segs) == 1 {
			segs[0].Text = LineForLang(s, lang)
		}
		return segs
	}
	for i := range segs {
		if i < len(s.Dialogue) {
			if v := strings.TrimSpace(s.Dialogue[i].Lines[lang]); v != "" {
				segs[i].Text = v
			}
		}
	}
	return segs
}

// singleVoice 所有分段都是同一个音色时返回它（单说话人镜头直接走普通 TTS）。
func singleVoice(segs []VoiceSegment) string {
	voice := ""
	for _, seg := range segs {
		if seg.Voice == "" {
			continue
		}
		if voice == "" {
			voice = seg.Voice
		} else if voice != seg.Voice {
			return ""
		}
	}
	return voice
}

// voiceNodeExtra 配音节点的 data：text 整句台词（老前端直接用），segments 分段（新前端按段提交），voice 单音色时的缺省。
func voiceNodeExtra(plan *model.Plan, s model.PlanShot, audioModel string) map[string]any {
	extra := map[string]any{"model": audioModel, "text": s.Line,
		"sp": map[string]any{"layer": "voice", "shot": s.No, "shot_id": s.Id}}
	if segs := VoiceSegments(plan, s); len(segs) > 0 {
		extra["segments"] = segs
		if v := singleVoice(segs); v != "" {
			extra["voice"] = v
		}
	}
	return extra
}

// ===== 正反打模板 =====

// DialogueTemplateRequest 两人对白的输入：场景、A / B 两个人物 key、按顺序的台词。
type DialogueTemplateRequest struct {
	Scene   string `json:"scene"`
	A       string `json:"a"`
	B       string `json:"b"`
	Seconds int    `json:"seconds"`
	// InsertAt 插到第几镜之前（1 起）；0 或超出 = 追加到末尾
	InsertAt int `json:"insert_at"`
	Lines    []struct {
		Speaker string `json:"speaker"`
		Text    string `json:"text"`
		Emotion string `json:"emotion"`
	} `json:"lines"`
}

const dialogueTemplateMaxLines = 10

// DialogueTemplateShots 生成一组正反打镜头（纯函数，不改 plan）：
// 全景建立（A 左 B 右）→ 每句台词一个说话人的过肩近景（机位始终在轴线同一侧）→ 每 3 句插一个听者反应镜头 → 收尾双人中景。
// 每个说话镜头只带自己那句对白，配音、字幕、口型都按镜分开。
func DialogueTemplateShots(plan *model.Plan, req DialogueTemplateRequest) ([]model.PlanShot, error) {
	a, b := plan.CharacterByKey(strings.TrimSpace(req.A)), plan.CharacterByKey(strings.TrimSpace(req.B))
	if a == nil || b == nil || a.Key == b.Key {
		return nil, errors.New("请选择两个不同的人物")
	}
	scene := strings.TrimSpace(req.Scene)
	sceneName := ""
	if scene != "" {
		found := false
		for _, sc := range plan.Scenes {
			if sc.Key == scene {
				found, sceneName = true, sc.Name
			}
		}
		if !found {
			return nil, fmt.Errorf("场景 %s 不存在", scene)
		}
	}
	lines := make([]model.PlanLine, 0, len(req.Lines))
	for _, l := range req.Lines {
		text := strings.TrimSpace(l.Text)
		if text == "" {
			continue
		}
		sp := strings.TrimSpace(l.Speaker)
		if sp != a.Key && sp != b.Key {
			return nil, fmt.Errorf("台词「%s」的说话人必须是 %s 或 %s", truncate(text, 12), a.Name, b.Name)
		}
		lines = append(lines, model.PlanLine{Speaker: sp, Text: text, Emotion: strings.TrimSpace(l.Emotion)})
	}
	if len(lines) == 0 {
		return nil, errors.New("至少写一句台词")
	}
	if len(lines) > dialogueTemplateMaxLines {
		return nil, fmt.Errorf("一场对白最多 %d 句，多了请拆成两场", dialogueTemplateMaxLines)
	}
	seconds := req.Seconds
	if seconds < 3 || seconds > 10 {
		seconds = 4
	}
	where := ""
	if sceneName != "" {
		where = sceneName + "，"
	}
	both := []string{a.Key, b.Key}
	side := map[string]string{a.Key: "左", b.Key: "右"}
	shots := []model.PlanShot{{
		Scene: scene, Characters: both, Seconds: seconds, Motion: "固定镜头",
		Prompt: fmt.Sprintf("%s%s在画面左侧、%s在画面右侧，两人同框的中远景，交代两人的位置与空间关系，视线相对", where, a.Name, b.Name),
	}}
	for i, l := range lines {
		speaker, listener := a, b
		if l.Speaker == b.Key {
			speaker, listener = b, a
		}
		emotion := l.Emotion
		if emotion == "" {
			emotion = "自然"
		}
		shots = append(shots, model.PlanShot{
			Scene: scene, Characters: both, Seconds: seconds, Motion: "固定镜头，轻微呼吸感",
			Prompt: fmt.Sprintf("%s从%s肩后越过拍%s的过肩近景，%s位于画面%s侧，机位保持在两人轴线的同一侧，%s说话时的表情与口型，%s的情绪",
				where, listener.Name, speaker.Name, speaker.Name, side[speaker.Key], speaker.Name, emotion),
			Dialogue: []model.PlanLine{l},
		})
		if (i+1)%3 == 0 && i+1 < len(lines) {
			shots = append(shots, model.PlanShot{
				Scene: scene, Characters: both, Seconds: 3, Motion: "固定镜头",
				Prompt: fmt.Sprintf("%s%s的反应近景，正在倾听，位于画面%s侧，眼神看向画面%s侧的对方，不说话",
					where, listener.Name, side[listener.Key], side[speaker.Key]),
			})
		}
	}
	shots = append(shots, model.PlanShot{
		Scene: scene, Characters: both, Seconds: 3, Motion: "固定镜头",
		Prompt: fmt.Sprintf("%s%s与%s的双人中景，对话结束后静默一拍，仍是%s左%s右，机位不越轴", where, a.Name, b.Name, a.Name, b.Name),
	})
	return shots, nil
}

// InsertShots 把模板镜头插进 plan（insertAt 从 1 起，0 / 超出为追加），超过上限报错、plan 不动。
func InsertShots(plan *model.Plan, shots []model.PlanShot, insertAt int) error {
	if len(plan.Shots)+len(shots) > model.ScreenplayMaxShots {
		return fmt.Errorf("%w：加上这 %d 个镜头会有 %d 镜，超过 %d 的上限，请先删减或拆成两集",
			model.ErrPlanLimit, len(shots), len(plan.Shots)+len(shots), model.ScreenplayMaxShots)
	}
	if insertAt <= 0 || insertAt > len(plan.Shots) {
		plan.Shots = append(plan.Shots, shots...)
		return nil
	}
	i := insertAt - 1
	merged := make([]model.PlanShot, 0, len(plan.Shots)+len(shots))
	merged = append(merged, plan.Shots[:i]...)
	merged = append(merged, shots...)
	merged = append(merged, plan.Shots[i:]...)
	plan.Shots = merged
	return nil
}
