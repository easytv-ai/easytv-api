package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easytv-api/model"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestMediaActionRegistry 动作表自身的一致性：标识唯一、引擎合法、模型动作都挂了能力位。
func TestMediaActionRegistry(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range mediaActions() {
		if a.Action == "" || a.Label == "" {
			t.Fatalf("动作缺少标识或名称：%+v", a)
		}
		if seen[a.Action] {
			t.Fatalf("动作标识重复：%s", a.Action)
		}
		seen[a.Action] = true
		switch a.Engine {
		case model.MediaEngineLocal, MediaEngineExisting, model.MediaEnginePipeline:
		case model.MediaEngineModel:
			if a.Ability == "" {
				t.Fatalf("模型动作 %s 没有声明能力位", a.Action)
			}
			if !model.IsValidAbility(a.Ability) {
				t.Fatalf("模型动作 %s 的能力位 %s 不在白名单里", a.Action, a.Ability)
			}
			if a.InputRole == "" {
				t.Fatalf("模型动作 %s 没有声明素材角色", a.Action)
			}
		default:
			t.Fatalf("动作 %s 的引擎 %q 非法", a.Action, a.Engine)
		}
		if a.Engine == MediaEngineExisting && a.Endpoint == "" {
			t.Fatalf("既有动作 %s 没有给出接口地址", a.Action)
		}
		if len(a.Source) == 0 || len(a.Outputs) == 0 {
			t.Fatalf("动作 %s 缺少源/产物类型声明", a.Action)
		}
	}
	// 工具条上的入口都在表里
	for _, want := range []string{
		"crop", "grid_split", "montage", "trim", "extract_audio", "mute_video", "mix_bgm", "split_av", "extract_frame", "extract_frames",
		"storyboard", "compress", "upscale", "remove_subtitle", "vocal_separation", "bgm_separation",
		"matting", "subject_remove", "subject_modify", "subject_replace", "motion_capture", "depth_extract",
		"retake_segment", "retake", "extend", "regenerate", "compose",
	} {
		if FindMediaAction(want) == nil {
			t.Errorf("动作表缺少 %s", want)
		}
	}
}

// TestMediaActionAvailability 能力位没配模型时，模型动作必须明确不可用而不是假装可用。
func TestMediaActionAvailability(t *testing.T) {
	mock := mockDB(t)
	// 开关一次、定价一次 → 定价为空 = 没有任何能力模型
	mock.ExpectQuery("SELECT .* FROM `setting`").WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))
	mock.ExpectQuery("SELECT .* FROM `model_price`").WillReturnRows(sqlmock.NewRows([]string{"model"}))
	views := MediaActions()
	if len(views) == 0 {
		t.Fatal("动作清单为空")
	}
	modelActions := 0
	for _, v := range views {
		if v.Engine != model.MediaEngineModel {
			continue
		}
		modelActions++
		if v.Available {
			t.Errorf("动作 %s 没有能力模型时不该可用", v.Action)
		}
		if v.Reason == "" || len(v.Models) != 0 {
			t.Errorf("动作 %s 应带不可用原因：%+v", v.Action, v)
		}
	}
	if modelActions == 0 {
		t.Fatal("动作表里应有模型动作")
	}
	var depth *MediaActionView
	for i := range views {
		if views[i].Action == "depth_extract" {
			depth = &views[i]
			break
		}
	}
	if depth == nil {
		t.Fatal("清单应包含 depth_extract")
	}
	if depth.Available || !strings.Contains(depth.Reason, "深度提取") {
		t.Fatalf("没配模型时深度提取应不可用：%+v", depth)
	}
}

func TestAdminMediaActionsGapNoModel(t *testing.T) {
	mock := mockDB(t)
	mock.ExpectQuery("SELECT .* FROM `setting`").WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))
	mock.ExpectQuery("SELECT .* FROM `model_price`").WillReturnRows(sqlmock.NewRows([]string{"model"}))
	views, _ := AdminMediaActions()
	var upscale *AdminMediaActionView
	for i := range views {
		if views[i].Action == "upscale" {
			upscale = &views[i]
			break
		}
	}
	if upscale == nil {
		t.Fatal("盘点应包含 upscale")
	}
	if upscale.Available || upscale.Gap != MediaGapNoModel || len(upscale.PricedModels) != 0 {
		t.Fatalf("没定价时 upscale 应是 no_model：%+v", upscale)
	}
}

func TestMediaInputRoleDepthExtract(t *testing.T) {
	action := FindMediaAction("depth_extract")
	if action == nil {
		t.Fatal("缺少 depth_extract")
	}
	if got := mediaInputRole(action, &model.Asset{Type: model.AssetTypeImage}); got != "image" {
		t.Fatalf("图片深度提取角色 = %q", got)
	}
	if got := mediaInputRole(action, &model.Asset{Type: model.AssetTypeVideo}); got != "reference_video" {
		t.Fatalf("视频深度提取角色 = %q", got)
	}
	if got := mediaInputRole(FindMediaAction("motion_capture"), &model.Asset{Type: model.AssetTypeVideo}); got != "reference_video" {
		t.Fatalf("动作捕捉角色不应被改：%q", got)
	}
}

func TestMediaPreferTypeDepthExtract(t *testing.T) {
	action := FindMediaAction("depth_extract")
	if action == nil {
		t.Fatal("缺少 depth_extract")
	}
	if got := mediaPreferType(action, &model.Asset{Type: model.AssetTypeImage}); got != model.AssetTypeImage {
		t.Fatalf("图片深度提取优先类型 = %q", got)
	}
	if got := mediaPreferType(action, &model.Asset{Type: model.AssetTypeVideo}); got != model.AssetTypeVideo {
		t.Fatalf("视频深度提取优先类型 = %q", got)
	}
	if got := mediaPreferType(FindMediaAction("motion_capture"), &model.Asset{Type: model.AssetTypeVideo}); got != model.TaskTypeVideo {
		t.Fatalf("动作捕捉优先类型不应被改：%q", got)
	}
}

// TestMediaRectAndScaleFilter 参数解析：裁切框与输出尺寸。
func TestMediaRectAndScaleFilter(t *testing.T) {
	if _, ok := parseMediaRect(map[string]any{"rect": map[string]any{"x": 0.1, "y": 0.2, "w": 0.5, "h": 0.4}}); !ok {
		t.Error("合法裁切框应通过")
	}
	if _, ok := parseMediaRect(map[string]any{"rect": map[string]any{"x": 0.0, "y": 0.0, "w": 0.0, "h": 0.4}}); ok {
		t.Error("零宽裁切框应被拒绝")
	}
	if _, ok := parseMediaRect(map[string]any{"rect": map[string]any{"x": 0.5, "y": 0.0, "w": 0.6, "h": 0.5}}); ok {
		t.Error("越界裁切框应被拒绝")
	}
	if _, ok := parseMediaRect(map[string]any{"rect": `{"x":0.25,"y":0,"w":0.5,"h":1}`}); !ok {
		t.Error("字符串形式的裁切框应通过")
	}

	if v, err := mediaScaleFilter(""); err != nil || v != "" {
		t.Errorf("空尺寸应返回空：%q %v", v, err)
	}
	if v, _ := mediaScaleFilter("720p"); v != "scale=-2:720" {
		t.Errorf("720p → %q", v)
	}
	if v, _ := mediaScaleFilter("1280x720"); v != "scale=1280:720" {
		t.Errorf("1280x720 → %q", v)
	}
	if _, err := mediaScaleFilter("大一点"); err == nil {
		t.Error("非法尺寸应报错")
	}
}

// TestMediaLocalActions 本地动作端到端：真跑 ffmpeg，校验产物类型、时长与尺寸。
func TestMediaLocalActions(t *testing.T) {
	if !FFmpegAvailable() {
		t.Skip("no ffmpeg")
	}
	ctx := context.Background()
	work := t.TempDir()
	srcPath := filepath.Join(work, "src.mp4")
	srcArgs := []string{
		"-y", "-f", "lavfi", "-i", "color=c=red:s=320x240:d=4",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4", "-shortest", "-pix_fmt", "yuv420p", srcPath,
	}
	if out, err := RunFFmpeg(ctx, srcArgs...); err != nil {
		t.Fatalf("造素材失败：%v %s", err, out)
	}
	src := &model.Asset{Id: 11, Type: model.AssetTypeVideo, Name: "测试片.mp4"}
	outDir := func() string { d, _ := os.MkdirTemp(work, "out-"); return d }

	// 画面裁切：框选 1/4 面积 → 160x120；再缩放到 720p 高
	t.Run("crop", func(t *testing.T) {
		dir := outDir()
		products, err := runMediaAction(ctx, dir, mustAction(t, "crop"), src, srcPath,
			map[string]any{"rect": map[string]any{"x": 0.25, "y": 0.25, "w": 0.5, "h": 0.5}})
		if err != nil {
			t.Fatalf("crop: %v", err)
		}
		if len(products) != 1 || products[0].Type != model.AssetTypeVideo {
			t.Fatalf("crop 产物不对：%+v", products)
		}
		if _, w, h, _ := ProbeMediaInfo(ctx, products[0].Path); w != 160 || h != 120 {
			t.Errorf("裁切尺寸 = %dx%d，want 160x120", w, h)
		}

		dir = outDir()
		products, err = runMediaAction(ctx, dir, mustAction(t, "crop"), src, srcPath,
			map[string]any{"rect": map[string]any{"x": 0, "y": 0, "w": 1.0, "h": 1.0}, "size": "720p"})
		if err != nil {
			t.Fatalf("crop+size: %v", err)
		}
		if _, w, h, _ := ProbeMediaInfo(ctx, products[0].Path); h != 720 || w <= 0 {
			t.Errorf("缩放后尺寸 = %dx%d，want 高 720", w, h)
		}
	})

	t.Run("crop_requires_rect", func(t *testing.T) {
		if _, err := runMediaAction(ctx, outDir(), mustAction(t, "crop"), src, srcPath, nil); err == nil {
			t.Error("没给裁切框时应报错")
		}
	})

	// 片段截取：1 秒起取 2 秒
	t.Run("trim", func(t *testing.T) {
		products, err := runMediaAction(ctx, outDir(), mustAction(t, "trim"), src, srcPath,
			map[string]any{"start": 1.0, "duration": 2.0})
		if err != nil {
			t.Fatalf("trim: %v", err)
		}
		dur, _, _, hasAudio := ProbeMediaInfo(ctx, products[0].Path)
		if dur < 1.5 || dur > 2.6 {
			t.Errorf("截取时长 = %.2f，want ≈2", dur)
		}
		if !hasAudio {
			t.Error("截取后应保留音轨")
		}
	})

	// 视频分割：1.5 秒处切成两段，前段 ≈1.5s、后段 ≈2.5s，顺序固定；切点贴边要拒绝
	t.Run("split", func(t *testing.T) {
		products, err := runMediaAction(ctx, outDir(), mustAction(t, "split"), src, srcPath, map[string]any{"at": 1.5})
		if err != nil {
			t.Fatalf("split: %v", err)
		}
		if len(products) != 2 {
			t.Fatalf("分割产物数 = %d，want 2", len(products))
		}
		if products[0].Duration < 1.2 || products[0].Duration > 1.8 || products[1].Duration < 2.2 || products[1].Duration > 2.8 {
			t.Errorf("两段时长 = %.2f / %.2f，want ≈1.5 / ≈2.5", products[0].Duration, products[1].Duration)
		}
		if products[0].Label != "分割 1" || products[1].Label != "分割 2" {
			t.Errorf("标签 = %q / %q", products[0].Label, products[1].Label)
		}
		if _, err := runMediaAction(ctx, outDir(), mustAction(t, "split"), src, srcPath, map[string]any{"at": 3.9}); err == nil {
			t.Error("贴近片尾的分割点应被拒绝")
		}
		if _, err := runMediaAction(ctx, outDir(), mustAction(t, "split"), src, srcPath, nil); err == nil {
			t.Error("没给分割点应报错")
		}
	})

	// 音轨提取 / 无声视频 / 分离
	t.Run("split_av", func(t *testing.T) {
		products, err := runMediaAction(ctx, outDir(), mustAction(t, "split_av"), src, srcPath, nil)
		if err != nil {
			t.Fatalf("split_av: %v", err)
		}
		if len(products) != 2 {
			t.Fatalf("分离应有两个产物：%+v", products)
		}
		var audio, video *mediaProduct
		for i := range products {
			switch products[i].Type {
			case model.AssetTypeAudio:
				audio = &products[i]
			case model.AssetTypeVideo:
				video = &products[i]
			}
		}
		if audio == nil || video == nil {
			t.Fatalf("分离产物类型不对：%+v", products)
		}
		if _, _, _, hasAudio := ProbeMediaInfo(ctx, audio.Path); !hasAudio {
			t.Error("音轨文件里应有音轨")
		}
		if _, _, _, hasAudio := ProbeMediaInfo(ctx, video.Path); hasAudio {
			t.Error("无声视频不该有音轨")
		}
	})

	// 取帧：首帧 / 末帧 / 指定秒
	t.Run("extract_frame", func(t *testing.T) {
		for _, at := range []string{"first", "last", "2"} {
			products, err := runMediaAction(ctx, outDir(), mustAction(t, "extract_frame"), src, srcPath, map[string]any{"at": at})
			if err != nil {
				t.Fatalf("extract_frame %s: %v", at, err)
			}
			if len(products) != 1 || products[0].Type != model.AssetTypeImage {
				t.Fatalf("取帧产物不对：%+v", products)
			}
			if st, err := os.Stat(products[0].Path); err != nil || st.Size() == 0 {
				t.Errorf("取帧 %s 没有产出文件", at)
			}
		}
	})

	// 抽帧：3 张，按 1 秒间隔
	t.Run("extract_frames", func(t *testing.T) {
		products, err := runMediaAction(ctx, outDir(), mustAction(t, "extract_frames"), src, srcPath,
			map[string]any{"count": 3, "interval": 1.0})
		if err != nil {
			t.Fatalf("extract_frames: %v", err)
		}
		if len(products) != 3 {
			t.Fatalf("抽帧数 = %d，want 3", len(products))
		}
		for _, p := range products {
			if p.Type != model.AssetTypeImage {
				t.Errorf("抽帧产物类型不对：%+v", p)
			}
		}
	})

	// 抽帧上限：要 100 张也只给 mediaFramesMax
	t.Run("extract_frames_capped", func(t *testing.T) {
		products, err := runMediaAction(ctx, outDir(), mustAction(t, "extract_frames"), src, srcPath,
			map[string]any{"count": 100, "interval": 0.2})
		if err != nil {
			t.Fatalf("extract_frames: %v", err)
		}
		if len(products) > mediaFramesMax {
			t.Errorf("抽帧数 %d 超过上限 %d", len(products), mediaFramesMax)
		}
	})

	// 抽帧 spread：4 秒素材要 5 张 → 均匀铺满整段，时间点递增且最后一张贴近片尾；interval 模式也带时间点
	t.Run("extract_frames_spread", func(t *testing.T) {
		products, err := runMediaAction(ctx, outDir(), mustAction(t, "extract_frames"), src, srcPath,
			map[string]any{"mode": "spread", "count": 5})
		if err != nil {
			t.Fatalf("extract_frames spread: %v", err)
		}
		if len(products) != 5 {
			t.Fatalf("抽帧数 = %d，want 5", len(products))
		}
		for i, p := range products {
			if p.Type != model.AssetTypeImage {
				t.Errorf("产物类型不对：%+v", p)
			}
			if i > 0 && p.Time <= products[i-1].Time {
				t.Errorf("时间点应递增：%v", products)
			}
		}
		if products[0].Time != 0 || products[4].Time < 3.5 || products[4].Time > 4 {
			t.Errorf("首帧应在 0 秒、末帧贴近 4 秒：%v / %v", products[0].Time, products[4].Time)
		}
		// 容器时长比画面长（音轨 5 秒、画面 4 秒）：末帧取不到时往前退，仍要凑齐 5 张
		longer := filepath.Join(work, "longer.mp4")
		if out, err := RunFFmpeg(ctx, "-y", "-f", "lavfi", "-i", "color=c=blue:s=320x240:d=4",
			"-f", "lavfi", "-i", "sine=frequency=440:duration=5", "-pix_fmt", "yuv420p", longer); err != nil {
			t.Fatalf("造长音轨素材失败：%v %s", err, out)
		}
		mixed, err := runMediaAction(ctx, outDir(), mustAction(t, "extract_frames"), &model.Asset{Id: 12, Type: model.AssetTypeVideo, Name: "长音轨.mp4"}, longer,
			map[string]any{"mode": "spread", "count": 5})
		if err != nil {
			t.Fatalf("extract_frames spread（长音轨）: %v", err)
		}
		if len(mixed) != 5 || mixed[4].Time > 4 || mixed[4].Time < 3 {
			t.Errorf("长音轨素材应退到画面末尾取满 5 张：%+v", mixed)
		}
		interval, err := runMediaAction(ctx, outDir(), mustAction(t, "extract_frames"), src, srcPath,
			map[string]any{"count": 3, "interval": 1.0})
		if err != nil {
			t.Fatalf("extract_frames interval: %v", err)
		}
		if len(interval) != 3 || interval[2].Time != 2 {
			t.Errorf("interval 模式第 3 帧应在 2 秒：%+v", interval)
		}
	})

	// 压缩：降到 120 高
	t.Run("compress", func(t *testing.T) {
		products, err := runMediaAction(ctx, outDir(), mustAction(t, "compress"), src, srcPath, map[string]any{"target_height": 120})
		if err != nil {
			t.Fatalf("compress: %v", err)
		}
		if _, _, h, _ := ProbeMediaInfo(ctx, products[0].Path); h != 120 {
			t.Errorf("压缩后高度 = %d，want 120", h)
		}
	})

	// 没有音轨的素材提取音轨要给出人话
	t.Run("extract_audio_without_audio", func(t *testing.T) {
		silent := filepath.Join(work, "silent.mp4")
		if out, err := RunFFmpeg(ctx, "-y", "-f", "lavfi", "-i", "color=c=blue:s=160x120:d=1", "-pix_fmt", "yuv420p", silent); err != nil {
			t.Fatalf("造无声素材失败：%v %s", err, out)
		}
		_, err := runMediaAction(ctx, outDir(), mustAction(t, "extract_audio"), src, silent, nil)
		if err == nil || !strings.Contains(err.Error(), "没有音轨") {
			t.Errorf("无声素材应给出明确错误，实际：%v", err)
		}
	})
}

func mustAction(t *testing.T, name string) *MediaAction {
	t.Helper()
	a := FindMediaAction(name)
	if a == nil {
		t.Fatalf("动作 %s 不存在", name)
	}
	return a
}

// TestSpreadFrameTimes 均匀取帧点：首尾内收、递增、数量对；太短或只要一张时只取 0 秒。
func TestSpreadFrameTimes(t *testing.T) {
	times := spreadFrameTimes(10, 5)
	if len(times) != 5 || times[0] != 0 || times[4] != 9.7 {
		t.Errorf("spreadFrameTimes(10,5) = %v", times)
	}
	for i := 1; i < len(times); i++ {
		if times[i] <= times[i-1] {
			t.Errorf("应递增：%v", times)
		}
	}
	if got := spreadFrameTimes(0.1, 4); len(got) != 1 || got[0] != 0 {
		t.Errorf("太短应只取 0 秒：%v", got)
	}
	if got := spreadFrameTimes(10, 1); len(got) != 1 || got[0] != 0 {
		t.Errorf("只要一张应取 0 秒：%v", got)
	}
}

func TestFillMediaJobDuration(t *testing.T) {
	src := &model.Asset{Duration: 12.9}
	params := map[string]any{"resolution": "1080P"}
	fillMediaJobDuration(params, src)
	if params["duration"] != 12 {
		t.Fatalf("应从素材时长写入整数秒，得到 %#v", params["duration"])
	}
	params["duration"] = 3
	fillMediaJobDuration(params, src)
	if params["duration"] != 3 {
		t.Fatalf("请求已带 duration 不应覆盖，得到 %#v", params["duration"])
	}
	empty := map[string]any{}
	fillMediaJobDuration(empty, &model.Asset{Duration: 0.4})
	if _, ok := empty["duration"]; ok {
		t.Fatalf("不足 1 秒不应写入，得到 %#v", empty)
	}
}
