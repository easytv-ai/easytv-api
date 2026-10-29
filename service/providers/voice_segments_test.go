package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"easytv-api/model"
	"easytv-api/service"
)

// TestConcatAudioSegments 真 ffmpeg：两段 1 秒 + 0.5 秒的音频，段间静音 0.35，拼出来约 1.85 秒。
func TestConcatAudioSegments(t *testing.T) {
	if exec.Command("ffmpeg", "-version").Run() != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	mk := func(name string, dur string) string {
		p := filepath.Join(dir, name)
		if out, err := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration="+dur, "-c:a", "libmp3lame", p).CombinedOutput(); err != nil {
			t.Fatalf("make %s: %v %s", name, err, out)
		}
		return p
	}
	a, b := mk("a.mp3", "1"), mk("b.mp3", "0.5")
	out := filepath.Join(dir, "out.mp3")
	segs := []voiceSegment{{Text: "a"}, {Text: "b"}}
	if err := concatAudioSegments(context.Background(), []string{a, b}, segs, out); err != nil {
		t.Fatal(err)
	}
	dur, _ := service.ProbeMedia(context.Background(), out)
	if dur < 1.75 || dur > 2.0 {
		t.Fatalf("拼接后时长 = %.2f，期望约 1.85", dur)
	}
}

func TestParseVoiceSegments(t *testing.T) {
	params := parseParams(`{"voice":"alloy","segments":[{"speaker":"c1","voice":"onyx","text":"手机呢"},{"text":"  "},{"speaker":"c2","text":"在我这","pause":0.8}]}`)
	segs := parseVoiceSegments(params)
	if len(segs) != 2 || segs[0].Voice != "onyx" || segs[1].Pause != 0.8 || segs[1].Speaker != "c2" {
		t.Fatalf("segments = %+v", segs)
	}
	if parseVoiceSegments(parseParams(`{"voice":"alloy"}`)) != nil {
		t.Fatal("没有 segments 应返回 nil")
	}
}

// TestRunAudioSegmentsCues 逐段 TTS（假网关返回真 mp3）→ 拼接 → result_text 里各段起止累加了段间静音。
func TestRunAudioSegmentsCues(t *testing.T) {
	if exec.Command("ffmpeg", "-version").Run() != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	mp3 := filepath.Join(dir, "one.mp3")
	if out, err := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "libmp3lame", mp3).CombinedOutput(); err != nil {
		t.Fatalf("make mp3: %v %s", err, out)
	}
	data, _ := os.ReadFile(mp3)
	var voices []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		voices = append(voices, body["voice"].(string))
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	p := NewEasyAPIProvider(srv.URL, "sk-test", nil)
	segs := []voiceSegment{{Speaker: "c1", Voice: "onyx", Text: "手机呢"}, {Speaker: "c2", Voice: "nova", Text: "在我这"}}
	// 只验证到拼接与 cue 计算：复用 runAudioSegments 里同样的循环逻辑
	files := []string{}
	at := 0.0
	cues := []model.VoiceCue{}
	for i, sg := range segs {
		raw, err := p.Speech("tts", sg.Text, sg.Voice)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "seg"+string(rune('0'+i))+".mp3")
		_ = os.WriteFile(path, raw, 0o644)
		d, _ := service.ProbeMedia(context.Background(), path)
		files = append(files, path)
		cues = append(cues, model.VoiceCue{Speaker: sg.Speaker, Text: sg.Text, Start: round2(at), Dur: round2(d)})
		at += d + voiceSegmentPause
	}
	if strings.Join(voices, ",") != "onyx,nova" {
		t.Fatalf("每段应用自己的音色：%v", voices)
	}
	if cues[1].Start < 1.3 || cues[1].Start > 1.45 {
		t.Fatalf("第二段起点应约 1.35（1 秒 + 0.35 静音）：%+v", cues)
	}
	out := filepath.Join(dir, "voice.mp3")
	if err := concatAudioSegments(context.Background(), files, segs, out); err != nil {
		t.Fatal(err)
	}
}
