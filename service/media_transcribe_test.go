package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"easytv-api/model"
)

// ===== 语音转字幕 / 去停顿（L8，API.md 35）=====

func TestBuildTranscriptSRT(t *testing.T) {
	segs := []TranscribeSegment{
		{Start: 0, End: 2.5, Text: "大家好"},
		{Start: 2.5, End: 6, Text: "今天讲三件事。第一件是选题，第二件是脚本，第三件是剪辑，每一件都很重要，别跳过，然后我们再聊聊发布节奏和数据复盘。"},
		{Start: 6, End: 6.05, Text: "谢谢", Speaker: "SPEAKER_1"},
	}
	srt := BuildTranscriptSRT(segs)
	if !strings.HasPrefix(srt, "1\n00:00:00,000 --> 00:00:02,500\n大家好\n\n") {
		t.Fatalf("第一条 cue 不对：\n%s", srt)
	}
	cues := transcriptCues(segs)
	// 第二段 42 字以上：按句末标点拆成两条，长句再按逗号拆
	if len(cues) < 4 {
		t.Fatalf("长段应被拆开，得到 %d 条：%+v", len(cues), cues)
	}
	for _, c := range cues {
		if n := len([]rune(c.Text)); n > srtLineMax+len([]rune("说话人 1：")) {
			t.Errorf("cue 超长 %d：%s", n, c.Text)
		}
		if c.End < c.Start {
			t.Errorf("cue 时间倒挂：%+v", c)
		}
	}
	// 拆出来的 cue 时间连续且落在原段内
	if cues[1].Start != 2.5 || cues[len(cues)-2].End != 6 {
		t.Errorf("拆分后的首尾时间应贴合原段：%+v", cues)
	}
	last := cues[len(cues)-1]
	if !strings.HasPrefix(last.Text, "说话人 1：") || last.End-last.Start < 0.2 {
		t.Errorf("说话人前缀 / 最短时长不对：%+v", last)
	}
	if !strings.Contains(srt, "00:00:06,000 --> 00:00:06,200\n说话人 1：谢谢") {
		t.Errorf("SRT 缺说话人 cue：\n%s", srt)
	}
	if BuildTranscriptSRT(nil) != "" {
		t.Error("空输入应得空串")
	}
}

func TestEvenTranscriptSegments(t *testing.T) {
	segs := evenTranscriptSegments("第一句。第二句很长很长很长很长！第三句?", 10)
	if len(segs) != 3 {
		t.Fatalf("应按句拆成 3 段：%+v", segs)
	}
	if segs[0].Start != 0 || segs[2].End != 10 || segs[1].Start != segs[0].End {
		t.Errorf("时间应连续铺满整段：%+v", segs)
	}
	if segs[1].End-segs[1].Start <= segs[0].End-segs[0].Start {
		t.Errorf("长句应分到更多时间：%+v", segs)
	}
	if srtClock(3661.5) != "01:01:01,500" {
		t.Errorf("srtClock: %s", srtClock(3661.5))
	}
}

func TestSilenceRanges(t *testing.T) {
	out := "[silencedetect @ 0x1] silence_start: 1.25\n[silencedetect @ 0x1] silence_end: 2.75 | silence_duration: 1.5\n[silencedetect @ 0x1] silence_start: 3.9\n"
	silences := parseSilenceIntervals(out, 4)
	if len(silences) != 2 || silences[0] != (timeRange{1.25, 2.75}) || silences[1] != (timeRange{3.9, 4}) {
		t.Fatalf("解析静音区间不对：%+v", silences)
	}
	keep := keepRanges(silences, 4, 0.15)
	// 第二段静音只有 0.1 秒，留 pad 后剪不了；第一段剪 [1.4, 2.6]
	if len(keep) != 2 || keep[0] != (timeRange{0, 1.4}) || keep[1] != (timeRange{2.6, 4}) {
		t.Fatalf("保留区间不对：%+v", keep)
	}
	if got := selectExpr(keep); got != "between(t,0.000,1.400)+between(t,2.600,4.000)" {
		t.Errorf("select 表达式：%s", got)
	}
	if keepRanges(nil, 4, 0.15) != nil {
		t.Error("没有静音应返回 nil")
	}
}

// TestMediaRemoveSilenceFFmpeg 真跑 ffmpeg：4 秒正弦音、中间 1.25~2.75 秒静音，去停顿后时长应缩掉约 1.5 秒（留两头 0.15 秒 pad）。
func TestMediaRemoveSilenceFFmpeg(t *testing.T) {
	if !FFmpegAvailable() || !FFprobeAvailable() {
		t.Skip("no ffmpeg")
	}
	ctx := context.Background()
	work := t.TempDir()
	srcPath := filepath.Join(work, "tone.m4a")
	if out, err := RunFFmpeg(ctx, "-y", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=16000:duration=4",
		"-af", "volume=enable='between(t,1.25,2.75)':volume=0", "-c:a", "aac", srcPath); err != nil {
		t.Fatalf("造素材失败：%v %s", err, out)
	}
	src := &model.Asset{Id: 7, Type: model.AssetTypeAudio, Name: "口播.m4a"}
	products, err := runMediaAction(ctx, work, mustAction(t, "remove_silence"), src, srcPath, map[string]any{})
	if err != nil {
		t.Fatalf("去停顿失败：%v", err)
	}
	if len(products) != 1 || products[0].Type != model.AssetTypeAudio || products[0].Warning != "" {
		t.Fatalf("产物不对：%+v", products)
	}
	shrunk := 4 - products[0].Duration
	if shrunk < 1.5-0.4 || shrunk > 1.5+0.4 {
		t.Errorf("应缩短约 1.5 秒（留 pad 后约 1.2 秒），实际缩短 %.2f 秒（产物 %.2f 秒）", shrunk, products[0].Duration)
	}

	// 阈值调到听不见静音的程度：没有可剪的停顿，原样交回并带 warning
	products, err = runMediaAction(ctx, filepath.Join(work), mustAction(t, "remove_silence"), src, srcPath, map[string]any{"threshold": -80.0, "min_silence": 3.0})
	if err != nil {
		t.Fatalf("无停顿场景失败：%v", err)
	}
	if products[0].Warning == "" || products[0].WarningCode != model.MediaWarningNoSilence {
		t.Errorf("没检测到停顿时应带 warning_code=%s：%+v", model.MediaWarningNoSilence, products[0])
	}
}

func TestTranscribeOutputWarning(t *testing.T) {
	text, code, params := transcribeOutputWarning(60, false)
	if text != "" || code != "" || params != nil {
		t.Fatalf("正常转写不该带警告：text=%q code=%q params=%v", text, code, params)
	}

	text, code, params = transcribeOutputWarning(30*60, false)
	if code != model.MediaWarningTranscribeTruncated {
		t.Fatalf("超长应标截断：code=%q", code)
	}
	if params["minutes"] != 20 {
		t.Fatalf("截断分钟应为 20：%v", params)
	}
	if !strings.Contains(text, "只转写了前 20 分钟") {
		t.Fatalf("截断文案不对：%q", text)
	}

	text, code, params = transcribeOutputWarning(60, true)
	if code != model.MediaWarningTranscribeNoTimestamps || params != nil {
		t.Fatalf("无时间戳：code=%q params=%v", code, params)
	}
	if !strings.Contains(text, "未返回时间戳") {
		t.Fatalf("无时间戳文案不对：%q", text)
	}

	text, code, params = transcribeOutputWarning(40*60, true)
	if code != model.MediaWarningTranscribeTruncated {
		t.Fatalf("同时出现时优先截断：code=%q", code)
	}
	if params["minutes"] != 20 {
		t.Fatalf("同时出现时仍带 minutes：%v", params)
	}
	if !strings.Contains(text, "只转写了前 20 分钟") || !strings.Contains(text, "未返回时间戳") {
		t.Fatalf("同时出现应拼两句：%q", text)
	}
}
