package service

import (
	"strings"
	"testing"
)

func TestMixBgmFilter(t *testing.T) {
	withAudio := mixBgmFilter(true, 0.2, 8, 2)
	if !strings.Contains(withAudio, "[0:a][bg]amix") || !strings.Contains(withAudio, "volume=0.20") {
		t.Fatalf("有原声时应混音：%s", withAudio)
	}
	musicOnly := mixBgmFilter(false, 0.2, 8, 2)
	if strings.Contains(musicOnly, "amix") || strings.Contains(musicOnly, "[0:a]") {
		t.Fatalf("没有原声时不应引用原音轨：%s", musicOnly)
	}
}

func TestMixBgmVolumeClamp(t *testing.T) {
	if mixBgmVolume(nil) != mixBgmVolumeDefault {
		t.Fatalf("缺省音量应为 %.2f", mixBgmVolumeDefault)
	}
	if got := mixBgmVolume(map[string]any{"volume": 0}); got != 0.05 {
		t.Fatalf("过小音量应抬到 0.05，实际 %v", got)
	}
	if got := mixBgmVolume(map[string]any{"volume": 3}); got != 1 {
		t.Fatalf("过大音量应压到 1，实际 %v", got)
	}
}

func TestMixBgmFade(t *testing.T) {
	start, length := mixBgmFade(10)
	if start != 8 || length != 2 {
		t.Fatalf("10 秒视频应在结尾 2 秒淡出，实际 start=%v len=%v", start, length)
	}
	start, length = mixBgmFade(2)
	if start < 0 || length <= 0 || start+length > 2.01 {
		t.Fatalf("短视频淡出不能超出时长：start=%v len=%v", start, length)
	}
}
