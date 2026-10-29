package service

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"easytv-api/model"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const pipelinePlan = `{"title":"深夜修 bug","style":"电影感","aspect":"16:9",
"characters":[{"key":"c1","name":"阿杰","look":"黑框眼镜"}],
"scenes":[{"key":"s1","name":"办公室","look":"冷光"}],
"shots":[{"no":1,"scene":"s1","characters":["c1"],"prompt":"盯着报错","motion":"推近","line":"又是这个 bug","seconds":5},
{"no":2,"scene":"s1","characters":["c1"],"prompt":"测试全绿","motion":"","line":"终于好了","seconds":5}]}`

func mockDB(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.DB = db
	return mock
}

// makeMedia 用 ffmpeg lavfi 在本地上传目录造素材，返回 /upload/... 地址。
func makeMedia(t *testing.T, name string, args ...string) string {
	t.Helper()
	full := filepath.Join(UploadDir, name)
	if out, err := RunFFmpeg(context.Background(), append(append([]string{"-y"}, args...), full)...); err != nil {
		t.Fatalf("make %s: %v %s", name, err, out)
	}
	return "/upload/" + name
}

// TestCreateToExportPipeline 策划案 → 建画布 → （模拟生成结果）→ 镜头视图 → 时间线（裁切 / 换候选 / 只留配音 / 竖屏 1080p）→ 真实 ffmpeg 渲染 → 质检。
// 网关与数据库都不触达：生成结果直接写进 graph，数据库用 sqlmock，素材放本地上传目录。
func TestCreateToExportPipeline(t *testing.T) {
	if exec.Command("ffmpeg", "-version").Run() != nil {
		t.Skip("no ffmpeg")
	}
	old := UploadDir
	UploadDir = t.TempDir()
	defer func() { UploadDir = old }()

	// 1. 策划案 → 画布
	plan, err := ParsePlanText(pipelinePlan)
	if err != nil || plan.Normalize(30) != nil {
		t.Fatalf("plan: %v", err)
	}
	var g map[string]any
	_ = json.Unmarshal([]byte(BuildPlanGraph(plan, BuildModels{Image: "img", Video: "vid", Audio: "aud"})), &g)

	// 2. 模拟生成：两段视频（镜头 1 有原声 4 秒，镜头 2 无声 3 秒）+ 镜头 1 配音 2 秒 + 一个镜头 1 的候选视频
	v1 := makeMedia(t, "v1.mp4", "-f", "lavfi", "-i", "color=c=red:s=320x240:d=4", "-f", "lavfi", "-i", "sine=frequency=300:duration=4", "-shortest", "-pix_fmt", "yuv420p")
	cand := makeMedia(t, "v1b.mp4", "-f", "lavfi", "-i", "color=c=green:s=320x240:d=4", "-pix_fmt", "yuv420p")
	v2 := makeMedia(t, "v2.mp4", "-f", "lavfi", "-i", "color=c=blue:s=320x240:d=3", "-pix_fmt", "yuv420p")
	voice := makeMedia(t, "voice1.mp3", "-f", "lavfi", "-i", "sine=frequency=900:duration=2")
	assetOf := map[string]int{"image-1": 1, "image-2": 2, "image-3": 3, "video-1": 11, "video-2": 12, "audio-1": 21}
	for _, raw := range g["nodes"].([]any) {
		n := raw.(map[string]any)
		if id := assetOf[n["id"].(string)]; id > 0 {
			d := n["data"].(map[string]any)
			d["assetId"], d["taskStatus"] = id, "succeeded"
		}
	}
	graph, _ := json.Marshal(g)
	cv := &model.Canvas{Id: 29, UserId: 8, Graph: string(graph)}

	// 3. 镜头视图
	mock := mockDB(t)
	mock.ExpectQuery("SELECT .* FROM `asset`").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "type", "url", "duration"}).
		AddRow(11, 8, "video", v1, 4).AddRow(12, 8, "video", v2, 3).AddRow(21, 8, "audio", voice, 2).AddRow(2, 8, "image", "/upload/s1.png", 0))
	mock.ExpectQuery("SELECT .* FROM generation_task AS t").WillReturnRows(sqlmock.NewRows([]string{"task_id", "node_id", "asset_id", "url", "duration"}).
		AddRow(90, "video-1", 13, cand, 4).AddRow(80, "video-1", 11, v1, 4))
	shots, err := BuildShots(cv)
	if err != nil {
		t.Fatalf("shots: %v", err)
	}
	if len(shots) != 2 || shots[0].Video.NodeId != "video-1" || shots[0].Image == nil || shots[0].Image.NodeId != "image-2" ||
		shots[0].Voice == nil || shots[0].Voice.AssetId != 21 || len(shots[0].Video.Candidates) != 2 || len(shots[0].CharacterIds) != 1 {
		t.Fatalf("shots = %+v", shots)
	}
	if shots[1].Voice != nil && shots[1].Voice.AssetId != 0 {
		t.Fatalf("镜头 2 没有台词不该有配音: %+v", shots[1].Voice)
	}

	// 4. 时间线：镜头 2 放前面、镜头 1 换成候选 + 裁 1~3 秒 + 只留配音，竖屏 1080p，镜头 2 改字幕
	subtitle := "测试全绿啦"
	tl := &model.Timeline{Aspect: "9:16", Resolution: "1080p", Subtitles: true, Voice: true, Clips: []model.TimelineClip{
		{NodeId: "video-2", Enabled: true, Subtitle: &subtitle, VoiceMode: "mix", OrigVolume: 1, VoiceVolume: 1},
		{NodeId: "video-1", Enabled: true, AssetId: 13, In: 1, Out: 3, VoiceMode: "replace", OrigVolume: 1, VoiceVolume: 1.5},
		{NodeId: "ghost", Enabled: false},
	}}
	tl.Normalize()
	mock.ExpectQuery("SELECT .* FROM `asset`").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "type", "url", "duration"}).AddRow(12, 8, "video", v2, 3))
	mock.ExpectQuery("SELECT .* FROM `asset`").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "type", "url", "duration"}).AddRow(13, 8, "video", cand, 4))
	mock.ExpectQuery("SELECT .* FROM `asset`").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "type", "url", "duration"}).AddRow(21, 8, "audio", voice, 2))
	clips, err := clipsFromTimeline(cv, tl)
	if err != nil {
		t.Fatalf("clips: %v", err)
	}
	if len(clips) != 2 || clips[0].NodeId != "video-2" || clips[0].Text != subtitle || clips[0].VoiceUrl != "" ||
		clips[1].AssetId != 13 || clips[1].VoiceUrl != voice || *clips[1].OrigVolume != 0 || clips[1].Text != "又是这个 bug" {
		t.Fatalf("clips = %+v", clips)
	}

	// 5. 渲染 + 质检：镜头 2 三秒 + 镜头 1 裁成两秒（配音 2 秒 + 0.45 收尾 → 延长到 2.45 秒）
	res, err := renderCompose(context.Background(), t.TempDir(), clips, renderOptions{Aspect: tl.Aspect, Resolution: tl.Resolution, Subtitles: true})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if res.Width != 1080 || res.Height != 1920 || len(res.QC.Issues) != 0 || !res.QC.HasAudio {
		t.Fatalf("qc = %+v warning=%q", res.QC, res.Warning)
	}
	if res.Total < 5.3 || res.Total > 5.6 {
		t.Fatalf("total = %.2f, want ≈5.45", res.Total)
	}
	srt, _ := os.ReadFile(filepath.Join(filepath.Dir(res.Path), "subs.srt"))
	// 镜头 2 在前（0~3 秒）用改过的字幕；镜头 1 从第 3 秒开始（mp3 实际时长略长于 2 秒，结束时间只比到百毫秒）
	if !strings.Contains(string(srt), "00:00:00,200 --> 00:00:02,800\n测试全绿啦") || !strings.Contains(string(srt), "00:00:03,200 --> 00:00:05,2") {
		t.Fatalf("subtitle timeline wrong:\n%s", srt)
	}
}

func TestTimelineNormalizeAndHelpers(t *testing.T) {
	long := strings.Repeat("字", 300)
	tl := &model.Timeline{Aspect: "4:3", Resolution: "4k", BgmVolume: 9, Clips: []model.TimelineClip{
		{NodeId: "video-1", In: -2, Out: 1, VoiceMode: "weird", OrigVolume: -1, VoiceVolume: 5, Subtitle: &long},
		{NodeId: "video-1"},
		{NodeId: " "},
	}}
	tl.Normalize()
	c := tl.Clips[0]
	// 同一节点第二条片段现在保留（复审 D04），只有空 node_id 被丢掉
	if tl.Aspect != "16:9" || tl.Resolution != "720p" || tl.BgmVolume != 2 || len(tl.Clips) != 2 ||
		c.In != 0 || c.Out != 1 || c.VoiceMode != "mix" || c.OrigVolume != 0.35 || c.VoiceVolume != 2 || len([]rune(*c.Subtitle)) != 200 {
		t.Fatalf("normalized = %+v clip=%+v", tl, c)
	}
	if in, d := trimRange(1, 3, 4); in != 1 || d != 2 {
		t.Fatalf("trim 1~3 of 4 = %.1f,%.1f", in, d)
	}
	if in, d := trimRange(5, 0, 4); in != 0 || d != 4 {
		t.Fatalf("入点越界应回落到整段: %.1f,%.1f", in, d)
	}
	if w, h := composeOutputSize("9:16", "1080p"); w != 1080 || h != 1920 {
		t.Fatalf("9:16 1080p = %dx%d", w, h)
	}
	if w, h := composeOutputSize("1:1", "720p"); w != 960 || h != 960 {
		t.Fatalf("1:1 720p = %dx%d", w, h)
	}
	saved := &model.Timeline{Clips: []model.TimelineClip{{NodeId: "video-2", Enabled: false}, {NodeId: "gone", Enabled: true}}}
	merged := MergeTimeline(saved, []Shot{{Video: ShotMedia{NodeId: "video-1", AssetId: 5}, Voice: &ShotMedia{AssetId: 7}}, {Video: ShotMedia{NodeId: "video-2"}}})
	if len(merged.Clips) != 2 || merged.Clips[0].NodeId != "video-2" || merged.Clips[1].NodeId != "video-1" || !merged.Clips[1].Enabled || merged.Clips[1].OrigVolume != 0.35 {
		t.Fatalf("merged = %+v", merged.Clips)
	}
}

func TestSubtitleFontSize(t *testing.T) {
	if got := subtitleFontSize(1280, 720); got != 22 {
		t.Fatalf("横屏 720p = %d, want 22（保持原来的观感）", got)
	}
	if got := subtitleFontSize(1080, 1920); got >= 10 {
		t.Fatalf("竖屏 1080p 字号 %d 太大，会折成多行", got)
	}
}
