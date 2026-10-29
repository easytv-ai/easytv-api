package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasCJK(t *testing.T) {
	cases := map[string]bool{
		"hello world": false,
		"1\n00:00:01": false,
		"你好":          true,
		"Hello 世界":    true,
		"こんにちは":       true,
		"안녕하세요":       true,
	}
	for text, want := range cases {
		if got := hasCJK(text); got != want {
			t.Errorf("hasCJK(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestFontsDirOf(t *testing.T) {
	if fontsDirOf("") != "" {
		t.Error("空路径应返回空")
	}
	// filepath.Dir 按平台归一化分隔符，断言跟着走（Windows 上是反斜杠）
	font := filepath.FromSlash("/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc")
	if dir, want := fontsDirOf(font), filepath.Dir(font); dir != want {
		t.Errorf("fontsDirOf = %q, want %q", dir, want)
	}
}

// TestMissingCJKFontWarning 没有字幕文件、或字幕是纯英文时不提示。
func TestMissingCJKFontWarning(t *testing.T) {
	if w := missingCJKFontWarning(""); w != "" {
		t.Errorf("无字幕不该提示：%q", w)
	}
	dir := t.TempDir()
	en := filepath.Join(dir, "en.srt")
	if err := os.WriteFile(en, []byte("1\n00:00:00,000 --> 00:00:01,000\nhello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if w := missingCJKFontWarning(en); w != "" {
		t.Errorf("英文字幕不该提示：%q", w)
	}
}
