package service

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"easytv-api/model"
)

// pixelAt 取 t 秒处画面的平均 RGB（缩到 1×1 像素）。
func pixelAt(t *testing.T, path string, at float64) (r, g, b int) {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-v", "error", "-ss", strconv.FormatFloat(at, 'f', 2, 64), "-i", path,
		"-frames:v", "1", "-vf", "scale=1:1", "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1")
	out, err := cmd.Output()
	if err != nil || len(out) < 3 {
		t.Fatalf("pixel at %.2f: %v (%d bytes)", at, err, len(out))
	}
	return int(out[0]), int(out[1]), int(out[2])
}

func expectColor(t *testing.T, path string, at float64, name string, want [3]int) {
	t.Helper()
	r, g, b := pixelAt(t, path, at)
	near := func(a, b int) bool { d := a - b; return d < 40 && d > -40 }
	if !near(r, want[0]) || !near(g, want[1]) || !near(b, want[2]) {
		t.Fatalf("%.2f 秒处应为%s %v，实际 [%d %d %d]", at, name, want, r, g, b)
	}
}

// TestStitchRetake 真 ffmpeg：红 1 秒 + 绿 1 秒 + 蓝 1 秒（带原声）的源，把 1–2 秒换成 160×120 无声的黄色片段。
// 拼完必须还是 3 秒、尺寸 320×240、原声保留；0.5 秒红、1.5 秒黄、2.5 秒蓝。
func TestStitchRetake(t *testing.T) {
	if exec.Command("ffmpeg", "-version").Run() != nil {
		t.Skip("no ffmpeg")
	}
	old := UploadDir
	UploadDir = t.TempDir()
	defer func() { UploadDir = old }()
	makeMedia(t, "src.mp4",
		"-f", "lavfi", "-i", "color=c=red:s=320x240:d=1:r=25",
		"-f", "lavfi", "-i", "color=c=green:s=320x240:d=1:r=25",
		"-f", "lavfi", "-i", "color=c=blue:s=320x240:d=1:r=25",
		"-f", "lavfi", "-i", "sine=frequency=300:duration=3",
		"-filter_complex", "[0:v][1:v][2:v]concat=n=3:v=1:a=0[v]", "-map", "[v]", "-map", "3:a",
		"-shortest", "-pix_fmt", "yuv420p")
	makeMedia(t, "clip.mp4", "-f", "lavfi", "-i", "color=c=yellow:s=160x120:d=2:r=30", "-pix_fmt", "yuv420p")
	src := filepath.Join(UploadDir, "src.mp4")
	clip := filepath.Join(UploadDir, "clip.mp4")
	ctx := context.Background()

	red, green, blue, yellow := [3]int{255, 0, 0}, [3]int{0, 128, 0}, [3]int{0, 0, 255}, [3]int{255, 255, 0}

	t.Run("middle keep audio", func(t *testing.T) {
		out, err := stitchRetake(ctx, t.TempDir(), src, clip, 1, 2, true)
		if err != nil {
			t.Fatal(err)
		}
		dur, w, h, audio := ProbeMediaInfo(ctx, out)
		if dur < 2.9 || dur > 3.15 || w != 320 || h != 240 || !audio {
			t.Fatalf("成片 dur=%.2f size=%dx%d audio=%v", dur, w, h, audio)
		}
		expectColor(t, out, 0.5, "红", red)
		expectColor(t, out, 1.5, "黄", yellow)
		expectColor(t, out, 2.5, "蓝", blue)
	})

	t.Run("tail no keep audio", func(t *testing.T) {
		// 换掉最后一秒：没有尾段；不保留原声时新片段那段是静音，但输出仍有音轨
		out, err := stitchRetake(ctx, t.TempDir(), src, clip, 2, 3, false)
		if err != nil {
			t.Fatal(err)
		}
		dur, _, _, audio := ProbeMediaInfo(ctx, out)
		if dur < 2.9 || dur > 3.15 || !audio {
			t.Fatalf("成片 dur=%.2f audio=%v", dur, audio)
		}
		expectColor(t, out, 0.5, "红", red)
		expectColor(t, out, 1.5, "绿", green)
		expectColor(t, out, 2.5, "黄", yellow)
	})

	t.Run("head short clip padded", func(t *testing.T) {
		// 换掉开头 0–2.5 秒（段长比 2 秒的片段还长）：末帧冻结补齐，不能把绿色带回来
		out, err := stitchRetake(ctx, t.TempDir(), src, clip, 0, 2.5, true)
		if err != nil {
			t.Fatal(err)
		}
		dur, _, _, _ := ProbeMediaInfo(ctx, out)
		if dur < 2.9 || dur > 3.15 {
			t.Fatalf("成片 dur=%.2f", dur)
		}
		expectColor(t, out, 0.5, "黄", yellow)
		expectColor(t, out, 2.3, "黄", yellow)
		expectColor(t, out, 2.8, "蓝", blue)
	})
}

func TestPickRetakeDuration(t *testing.T) {
	cases := []struct {
		durations string
		seg       float64
		want      int
	}{
		{"", 2.3, 3},     // 没声明：ceil 且不低于 3
		{"", 7.2, 8},     // 没声明：ceil
		{"", 30, 15},     // 没声明：封顶
		{"4,8,12", 2, 4}, // 声明：≥ 段长的最小档
		{"4,8,12", 5, 8},
		{"4,8,12", 20, 12}, // 都不够：最大档，拼接时冻结末帧
		{"6,7,8,9,10", 6, 6},
	}
	for _, c := range cases {
		p := &model.ModelPrice{Durations: c.durations}
		if got := pickRetakeDuration(p, c.seg); got != c.want {
			t.Errorf("durations=%q seg=%.1f 期望 %d 得 %d", c.durations, c.seg, c.want, got)
		}
	}
}

func TestCheckRetakeRange(t *testing.T) {
	spec := retakeSegmentSpec{Start: 1, End: 5.3, SrcDuration: 5}
	if err := checkRetakeRange(&spec); err != nil || spec.End != 5 {
		t.Fatalf("贴着结尾应收到结尾：err=%v end=%.2f", err, spec.End)
	}
	spec = retakeSegmentSpec{Start: 1, End: 6, SrcDuration: 5}
	if err := checkRetakeRange(&spec); err == nil {
		t.Fatal("超出视频长度应报错")
	}
	spec = retakeSegmentSpec{Start: 1, End: 1.2, SrcDuration: 5}
	if err := checkRetakeRange(&spec); err == nil {
		t.Fatal("段太短应报错")
	}
}

func TestAspectRatioOf(t *testing.T) {
	for _, c := range []struct {
		w, h int
		want string
	}{{1280, 720, "16:9"}, {720, 1280, "9:16"}, {1024, 1024, "1:1"}, {0, 0, "16:9"}, {1440, 1080, "4:3"}} {
		if got := aspectRatioOf(c.w, c.h); got != c.want {
			t.Errorf("%dx%d 期望 %s 得 %s", c.w, c.h, c.want, got)
		}
	}
}

// TestApplyPipelineTask 任务成功但还没拼接的复合作业，对外必须是「处理中」而不是「完成」。
func TestApplyPipelineTask(t *testing.T) {
	ok := &model.GenerationTask{Status: model.TaskStatusSucceeded, ResultAssetId: 9, ResultUrl: "u", Progress: 100}
	j := &model.MediaJob{Engine: model.MediaEnginePipeline}
	applyPipelineTask(j, ok)
	if j.Status != model.MediaStatusRunning || j.OutputAssetId != 0 || j.Progress != 90 || string(j.OutputsView) != "[]" {
		t.Fatalf("应为处理中 90%%，实际 status=%d asset=%d progress=%d outputs=%s", j.Status, j.OutputAssetId, j.Progress, j.OutputsView)
	}
	running := &model.MediaJob{Engine: model.MediaEnginePipeline}
	applyPipelineTask(running, &model.GenerationTask{Status: model.TaskStatusRunning, Progress: 40})
	if running.Status != model.MediaStatusRunning || running.Progress != 40 {
		t.Fatalf("生成中应映射任务进度，实际 status=%d progress=%d", running.Status, running.Progress)
	}
	failed := &model.MediaJob{Engine: model.MediaEnginePipeline}
	applyPipelineTask(failed, &model.GenerationTask{Status: model.TaskStatusFailed, ErrorMsg: "网关超时"})
	if failed.Status != model.MediaStatusFailed || failed.Error != "网关超时" {
		t.Fatalf("任务失败应映射为失败，实际 status=%d err=%q", failed.Status, failed.Error)
	}
	// 拼接完成后壳行自己有产物，保持完成
	done := &model.MediaJob{Engine: model.MediaEnginePipeline, Status: model.MediaStatusDone, OutputAssetId: 3, Outputs: `[{"asset_id":3}]`}
	applyPipelineTask(done, ok)
	if done.Status != model.MediaStatusDone || done.OutputAssetId != 3 {
		t.Fatal("已拼接的作业不该被改成处理中")
	}
}

// TestRetakeRatioFallback 前端读不到时长时只给比例：按源时长换算后再做区间校验。
func TestRetakeRatioFallback(t *testing.T) {
	params := map[string]any{"start_ratio": 0.2, "end_ratio": 0.6}
	spec := retakeSegmentSpec{Start: mediaParamFloat(params, "start", -1), End: mediaParamFloat(params, "end", -1), SrcDuration: 10}
	sr, er := mediaParamFloat(params, "start_ratio", -1), mediaParamFloat(params, "end_ratio", -1)
	if !(sr >= 0 && er > sr && er <= 1) {
		t.Fatal("比例应合法")
	}
	spec.Start, spec.End = sr*spec.SrcDuration, er*spec.SrcDuration
	if err := checkRetakeRange(&spec); err != nil || spec.Start != 2 || spec.End != 6 {
		t.Fatalf("换算错误：err=%v start=%.2f end=%.2f", err, spec.Start, spec.End)
	}
	bad := map[string]any{"start_ratio": 0.7, "end_ratio": 0.3}
	sr, er = mediaParamFloat(bad, "start_ratio", -1), mediaParamFloat(bad, "end_ratio", -1)
	if sr >= 0 && er > sr && er <= 1 {
		t.Fatal("倒置的比例不应通过")
	}
}

// TestRetakeTaskParams 换模型重拍时不能把源任务的模型专属参数带过去（实测 hailuo-02 收到 seedance 的 generate_audio 被网关 400）。
func TestRetakeTaskParams(t *testing.T) {
	src := &model.GenerationTask{Model: "seedance-2.0-fast", Params: `{"aspect_ratio":"16:9","generate_audio":true,"resolution":"720p","count":2}`}
	spec := &retakeSegmentSpec{Model: "hailuo-02", Duration: 6, SrcWidth: 896, SrcHeight: 512}
	p := retakeTaskParams(src, spec)
	if _, has := p["generate_audio"]; has {
		t.Fatal("换模型不该带 generate_audio")
	}
	if _, has := p["resolution"]; has {
		t.Fatal("换模型不该带源模型的分辨率档位")
	}
	if p["aspect_ratio"] != "16:9" || p["duration"] != 6 || p["mode"] != "fl2v" || p["count"] != 1 {
		t.Fatalf("参数不对：%v", p)
	}
	same := retakeTaskParams(src, &retakeSegmentSpec{Model: "seedance-2.0-fast", Duration: 5})
	if _, has := same["generate_audio"]; has {
		t.Fatal("同模型也不该带音频开关（实测老任务的 audio:true 让 hailuo-02 被网关 400）")
	}
	if same["resolution"] != "720p" || same["count"] != 1 || same["duration"] != 5 {
		t.Fatalf("同模型应沿用其余参数：%v", same)
	}
	withAudio := retakeTaskParams(&model.GenerationTask{Model: "hailuo-02", Params: `{"audio":true,"ratio":"16:9"}`}, &retakeSegmentSpec{Model: "hailuo-02", Duration: 6})
	if _, has := withAudio["audio"]; has {
		t.Fatal("audio 键必须去掉")
	}
	none := retakeTaskParams(nil, &retakeSegmentSpec{Model: "hailuo-02", Duration: 6, SrcWidth: 720, SrcHeight: 1280})
	if none["aspect_ratio"] != "9:16" {
		t.Fatalf("无源任务应按尺寸推画幅：%v", none)
	}
}

func TestMediaBaseName(t *testing.T) {
	if got := mediaBaseName("GT1789208650c6Ax2D.mp4"); got != "GT1789208650c6Ax2D" {
		t.Fatalf("应去掉扩展名，得 %q", got)
	}
	if got := mediaBaseName("深夜的面馆"); got != "深夜的面馆" {
		t.Fatalf("无扩展名应原样，得 %q", got)
	}
}
