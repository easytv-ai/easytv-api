package service

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"easytv-api/model"
)

func TestReviewGateCheck(t *testing.T) {
	reviews := map[int]*model.ShotReview{
		10: {AssetId: 10, Status: model.ShotReviewApproved},
		11: {AssetId: 11, Status: model.ShotReviewRejected, Reason: "identity"},
	}
	first := []GateInput{{AssetId: 10, Role: "first_frame"}}
	pending := []GateInput{{AssetId: 12, Role: "first_frame"}}
	rejected := []GateInput{{AssetId: 11, Role: "first_frame"}}
	if err := ReviewGateCheck(ReviewGateOff, rejected, reviews); err != nil {
		t.Fatal("关闭时不该拦")
	}
	if err := ReviewGateCheck(ReviewGateRejected, rejected, reviews); err == nil || !strings.Contains(err.Error(), "人物不对") {
		t.Fatalf("退回模式应拦下并带原因：%v", err)
	}
	if err := ReviewGateCheck(ReviewGateRejected, pending, reviews); err != nil {
		t.Fatal("退回模式下待审不拦")
	}
	if err := ReviewGateCheck(ReviewGateApproved, pending, reviews); err == nil {
		t.Fatal("通过模式下待审的分镜图应拦")
	}
	if err := ReviewGateCheck(ReviewGateApproved, first, reviews); err != nil {
		t.Fatalf("已通过应放行：%v", err)
	}
	// 通过模式只看首帧：参考图待审不拦
	if err := ReviewGateCheck(ReviewGateApproved, []GateInput{{AssetId: 10, Role: "first_frame"}, {AssetId: 12, Role: "reference"}}, reviews); err != nil {
		t.Fatalf("参考图待审不该拦：%v", err)
	}
	if NormalizeReviewGate(" Approved ") != ReviewGateApproved || NormalizeReviewGate("x") != ReviewGateOff {
		t.Fatal("门禁值归一化不对")
	}
}

// TestRenderVoiceTrack 真 ffmpeg：两镜各 2 秒，只有第二镜有 1 秒配音 → 对白轨 4 秒，配音落在 2.15 秒起。
func TestRenderVoiceTrack(t *testing.T) {
	if exec.Command("ffmpeg", "-version").Run() != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	voice := filepath.Join(dir, "v.mp3")
	if out, err := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "sine=frequency=880:duration=1", "-c:a", "libmp3lame", voice).CombinedOutput(); err != nil {
		t.Fatalf("make voice: %v %s", err, out)
	}
	out, err := renderVoiceTrack(context.Background(), dir, []voiceTrackPart{{Dur: 2}, {Path: voice, Dur: 2, Volume: 1}})
	if err != nil {
		t.Fatal(err)
	}
	dur, has := ProbeMedia(context.Background(), out)
	if !has || dur < 3.9 || dur > 4.2 {
		t.Fatalf("对白轨 dur=%.2f has=%v", dur, has)
	}
	// 前 2 秒静音、2.2–3 秒有声
	probe := func(from, to string) string {
		o, _ := exec.Command("ffmpeg", "-i", out, "-af", "atrim="+from+":"+to+",volumedetect", "-f", "null", "-").CombinedOutput()
		for _, line := range strings.Split(string(o), "\n") {
			if strings.Contains(line, "mean_volume") {
				return line[strings.Index(line, "mean_volume"):]
			}
		}
		return ""
	}
	if s := probe("0.2", "1.8"); !strings.Contains(s, "-91") {
		t.Fatalf("第一镜应静音：%s", s)
	}
	if s := probe("2.3", "3.0"); strings.Contains(s, "-91") || s == "" {
		t.Fatalf("第二镜应有配音：%s", s)
	}
	if p, err := renderVoiceTrack(context.Background(), dir, []voiceTrackPart{{Dur: 2}}); err != nil || p != "" {
		t.Fatalf("没有配音应返回空：%q %v", p, err)
	}
}

func TestBlockStaticItemsByGate(t *testing.T) {
	reviews := map[int]*model.ShotReview{11: {AssetId: 11, Status: model.ShotReviewRejected, Reason: "identity"}}
	items := []*model.GenerationBatchItem{
		{NodeId: "video-1", Type: model.TaskTypeVideo, Status: model.BatchItemWaiting, Inputs: `[{"asset_id":11,"role":"first_frame"}]`},
		{NodeId: "video-2", Type: model.TaskTypeVideo, Status: model.BatchItemWaiting, Inputs: `[]`, Depends: `[{"node_id":"image-2","role":"first_frame"}]`},
		{NodeId: "image-1", Type: model.TaskTypeImage, Status: model.BatchItemWaiting, Inputs: `[{"asset_id":11,"role":"image"}]`},
	}
	blockStaticItemsByGate(items, ReviewGateRejected, reviews)
	if items[0].Status != model.BatchItemBlocked || !strings.Contains(items[0].Error, "人物不对") {
		t.Fatalf("静态退回输入应在创建时标 blocked：%+v", items[0])
	}
	if items[1].Status != model.BatchItemWaiting || items[2].Status != model.BatchItemWaiting {
		t.Fatal("依赖产物的视频条目与图片条目不该在创建时被拦")
	}
}
