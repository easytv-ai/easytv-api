package service

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easytv-api/model"

	"github.com/DATA-DOG/go-sqlmock"
)

// 短剧复审（2026-09-15）回归用例：由 review-evidence/2026-09-15/drama_review_test.go 改造而来。
// D01 配音变长不能把已裁掉的画面带回来；D02 改稿同步画布；D04 同一素材多次使用；D06 短片段字幕；D07 BGM 音量 0。

// TestReviewTrimWithLongVoice D01：源视频「红 1 秒 + 蓝 2 秒」，选 0~1 秒，加 2 秒配音：
// 段落延长到 2.45 秒，但 1.5 秒处必须仍是冻结的红色末帧，不能出现被裁掉的蓝色。真跑 ffmpeg，没装就跳过。
func TestReviewTrimWithLongVoice(t *testing.T) {
	if exec.Command("ffmpeg", "-version").Run() != nil {
		t.Skip("no ffmpeg")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()
	src := filepath.Join(dir, "red-then-blue.mp4")
	voice := filepath.Join(dir, "voice.wav")
	if out, err := RunFFmpeg(ctx, "-y", "-f", "lavfi", "-i", "color=c=red:s=128x128:r=30:d=1", "-f", "lavfi", "-i", "color=c=blue:s=128x128:r=30:d=2",
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]", "-map", "[v]", "-c:v", "libx264", "-pix_fmt", "yuv420p", src); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	if out, err := RunFFmpeg(ctx, "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=2", voice); err != nil {
		t.Fatalf("audio fixture: %v %s", err, out)
	}
	in, seg := trimRange(0, 1, 3)
	dur := clipDurationWithVoice(seg, 2)
	if dur < 2.4 || dur > 2.5 {
		t.Fatalf("dur = %.2f, want ≈2.45", dur)
	}
	pixelAt := func(path string, at string) []byte {
		px, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-ss", at, "-i", path, "-frames:v", "1", "-vf", "scale=1:1", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1").Output()
		if err != nil || len(px) < 3 {
			t.Fatalf("read frame at %s: %v", at, err)
		}
		return px[:3]
	}
	for name, r := range map[string]clipRender{
		"voice":   {in: in, seg: seg, dur: dur, voice: voice, voiceVol: 1},
		"novoice": {in: in, seg: seg, dur: dur, origVol: 1},
	} {
		dst := filepath.Join(dir, name+".mp4")
		if err := normalizeClip(ctx, src, dst, 128, 128, r); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		gotDur, hasAudio := ProbeMedia(ctx, dst)
		if !hasAudio || gotDur < dur-0.2 || gotDur > dur+0.2 {
			t.Fatalf("%s: duration=%.2f audio=%v, want ≈%.2f with audio", name, gotDur, hasAudio, dur)
		}
		for _, at := range []string{"0.5", "1.5", "2.3"} {
			px := pixelAt(dst, at)
			if px[2] > px[0] {
				t.Fatalf("%s: frame at %ss RGB=%v，被裁掉的蓝色尾部重新出现；应是冻结的红色末帧", name, at, px)
			}
		}
	}
	// 入点不为 0 时同样不能越过出点：选 1.5~2 秒（蓝），延长到 3 秒后仍全蓝，不会跑到素材末尾之外
	in, seg = trimRange(1.5, 2, 3)
	dst := filepath.Join(dir, "mid.mp4")
	if err := normalizeClip(ctx, src, dst, 128, 128, clipRender{in: in, seg: seg, dur: 3, origVol: 1}); err != nil {
		t.Fatal(err)
	}
	if px := pixelAt(dst, "0.2"); px[0] > px[2] {
		t.Fatalf("入点 1.5 秒处应是蓝色: %v", px)
	}
}

// TestReviewShortSubtitle D06：0.3 秒片段的字幕不能结束早于开始；内缩量取 min(0.2, 时长 10%)。
func TestReviewShortSubtitle(t *testing.T) {
	srt := BuildSRT([]model.ComposeClip{{Text: "停！", Duration: 0.3}})
	if strings.Contains(srt, "00:00:00,200 --> 00:00:00,100") {
		t.Fatalf("反向字幕:\n%s", srt)
	}
	if !strings.Contains(srt, "00:00:00,030 --> 00:00:00,270") {
		t.Fatalf("0.3 秒片段应内缩 0.03 秒:\n%s", srt)
	}
	// 长片段仍是 0.2 秒内缩；每条字幕都满足 start < end
	srt = BuildSRT([]model.ComposeClip{{Text: "一", Duration: 0.3}, {Text: "二", Duration: 5}, {Text: "三", Duration: 1}, {Text: "四", Duration: 0.05}})
	if !strings.Contains(srt, "00:00:00,500 --> 00:00:05,100") || !strings.Contains(srt, "00:00:05,400 --> 00:00:06,200") {
		t.Fatalf("srt:\n%s", srt)
	}
	for _, line := range strings.Split(srt, "\n") {
		if !strings.Contains(line, " --> ") {
			continue
		}
		parts := strings.Split(line, " --> ")
		if parts[0] >= parts[1] {
			t.Fatalf("start >= end: %s", line)
		}
	}
	for _, c := range []struct{ dur, wantStart, wantEnd float64 }{{0.3, 0.03, 0.27}, {4, 0.2, 3.8}, {0, 0, 0}} {
		if s, e := srtCueRange(0, c.dur); (c.dur > 0 && s >= e) || s < c.wantStart-1e-9 || s > c.wantStart+1e-9 || e < c.wantEnd-1e-9 || e > c.wantEnd+1e-9 {
			t.Fatalf("dur %.2f → %.3f~%.3f", c.dur, s, e)
		}
	}
}

// TestReviewBgmZero D07：BGM 音量未设置回落 0.2；明确为 0 时不混 BGM（地址清空）。
func TestReviewBgmZero(t *testing.T) {
	if url, vol := resolveBgm("/upload/bgm.mp3", model.TimelineVolumeUnset); url == "" || vol != 0.2 {
		t.Fatalf("未设置应回落 0.2: %q %v", url, vol)
	}
	if url, vol := resolveBgm("/upload/bgm.mp3", 0); url != "" || vol != 0 {
		t.Fatalf("音量 0 应不混 BGM: %q %v", url, vol)
	}
	if url, vol := resolveBgm("/upload/bgm.mp3", 0.5); url == "" || vol != 0.5 {
		t.Fatalf("正常音量应保留: %q %v", url, vol)
	}
	// 合成请求里 timeline 缺 bgm_volume → 0.2；传 0 → 0（走 Timeline.UnmarshalJSON）
	var req ComposeRequest
	if err := json.Unmarshal([]byte(`{"timeline":{"bgm_asset_id":3,"clips":[]}}`), &req); err != nil {
		t.Fatal(err)
	}
	req.Timeline.Normalize()
	if req.Timeline.BgmVolume != 0.2 {
		t.Fatalf("缺省应为 0.2: %v", req.Timeline.BgmVolume)
	}
	if err := json.Unmarshal([]byte(`{"timeline":{"bgm_asset_id":3,"bgm_volume":0,"clips":[]}}`), &req); err != nil {
		t.Fatal(err)
	}
	req.Timeline.Normalize()
	if req.Timeline.BgmVolume != 0 {
		t.Fatalf("明确 0 应保持 0: %v", req.Timeline.BgmVolume)
	}
}

// TestReviewSplitTake D04：A / B / A 三条片段经「保存 → 与画布对齐 → 合成」全程保留三条，各带独立 clip_id 与入出点。
func TestReviewSplitTake(t *testing.T) {
	shots := []Shot{{No: 1, Video: ShotMedia{NodeId: "video-1", AssetId: 11}}, {No: 2, Video: ShotMedia{NodeId: "video-2", AssetId: 12}}}
	saved := &model.Timeline{Clips: []model.TimelineClip{
		{Id: "c_aaaaaaaa", NodeId: "video-1", Enabled: true, In: 0, Out: 1},
		{Id: "c_bbbbbbbb", NodeId: "video-2", Enabled: true, In: 0, Out: 1},
		{Id: "c_cccccccc", NodeId: "video-1", Enabled: true, In: 2, Out: 3},
		{Id: "c_dddddddd", NodeId: "video-9", Enabled: true},
	}}
	merged := MergeTimeline(saved, shots)
	if len(merged.Clips) != 3 || merged.Clips[0].NodeId != "video-1" || merged.Clips[1].NodeId != "video-2" || merged.Clips[2].NodeId != "video-1" ||
		merged.Clips[2].In != 2 || merged.Clips[2].Id != "c_cccccccc" {
		t.Fatalf("merged = %+v", merged.Clips)
	}

	cv := videoCanvas(2)
	mock := mockDB(t)
	for _, id := range []int{1, 2, 1} {
		mock.ExpectQuery("SELECT .* FROM `asset`").WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "type", "url", "duration"}).
			AddRow(id, 8, model.AssetTypeVideo, "/upload/v.mp4", 4))
	}
	tl := &model.Timeline{Clips: []model.TimelineClip{
		{NodeId: "video-1", Enabled: true, In: 0, Out: 1},
		{NodeId: "video-2", Enabled: true, In: 0, Out: 1},
		{NodeId: "video-1", Enabled: true, In: 2, Out: 3},
	}}
	tl.Normalize()
	clips, err := clipsFromTimeline(cv, tl)
	if err != nil {
		t.Fatal(err)
	}
	if len(clips) != 3 || clips[0].NodeId != "video-1" || clips[2].NodeId != "video-1" || clips[2].In != 2 || clips[2].Out != 3 ||
		clips[0].ClipId == "" || clips[0].ClipId == clips[2].ClipId {
		t.Fatalf("clips = %+v", clips)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

const reviewPlanJSON = `{"title":"真相","style":"电影感","aspect":"16:9",
"characters":[{"key":"c1","name":"男主","look":"黑衣"}],
"scenes":[{"key":"s1","name":"客厅","look":"暖光"}],
"shots":[{"no":1,"scene":"s1","characters":["c1"],"prompt":"男主看着照片","motion":"推近","line":"我早就知道了","seconds":5},
{"no":2,"scene":"s1","characters":["c1"],"prompt":"男主放下照片","motion":"","line":"","seconds":5}]}`

func graphNodesById(t *testing.T, graph string) (map[string]map[string]any, []map[string]any) {
	t.Helper()
	var g struct {
		Nodes []map[string]any `json:"nodes"`
		Edges []map[string]any `json:"edges"`
	}
	if err := json.Unmarshal([]byte(graph), &g); err != nil {
		t.Fatal(err)
	}
	byId := map[string]map[string]any{}
	for _, n := range g.Nodes {
		byId[n["id"].(string)] = n
	}
	return byId, g.Edges
}

// TestReviewSyncPlanGraph D02：改台词 / 删镜 / 加镜后同步到既有画布：既有节点只改提示词与台词、素材保留；新镜补建节点；删掉的镜只列出不删节点。
func TestReviewSyncPlanGraph(t *testing.T) {
	plan, err := ParsePlanText(reviewPlanJSON)
	if err != nil || plan.Normalize(30) != nil {
		t.Fatalf("plan: %v", err)
	}
	m := BuildModels{Image: "img", Video: "vid", Audio: "aud"}
	graph := BuildPlanGraph(plan, m)
	// 模拟已生成：镜头 1 视频与配音都有产物
	byId, _ := graphNodesById(t, graph)
	byId["video-1"]["data"].(map[string]any)["assetId"] = 11
	byId["video-1"]["data"].(map[string]any)["taskStatus"] = "succeeded"
	byId["audio-1"]["data"].(map[string]any)["assetId"] = 21
	var g map[string]any
	_ = json.Unmarshal([]byte(graph), &g)
	nodes := []any{}
	for _, id := range []string{"text-1", "image-1", "image-2", "video-1", "audio-1", "image-3", "video-2"} {
		nodes = append(nodes, byId[id])
	}
	g["nodes"] = nodes
	raw, _ := json.Marshal(g)
	graph = string(raw)

	// 改稿：镜头 1 台词反转 + 英文译文；镜头 2 删掉；新增镜头 3（有台词）
	edited, _ := ParsePlanText(reviewPlanJSON)
	edited.Shots[0].Line = "他还被蒙在鼓里"
	edited.Shots[0].Lines = map[string]string{"en": "He still has no idea"}
	edited.Shots = []model.PlanShot{edited.Shots[0], {Scene: "s1", Characters: []string{"c1"}, Prompt: "男主转身离开", Line: "走吧", Seconds: 4}}
	if err := edited.Normalize(30); err != nil {
		t.Fatal(err)
	}
	// Normalize 会重新编号：新镜头成为第 2 镜，原第 2 镜（无台词、无产物）会被按镜号覆盖内容 —— 这是「按镜号同步」的已知取舍
	out, res, err := syncPlanGraph(edited, graph, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SyncedShots) != 2 || res.SyncedShots[0] != 1 || res.SyncedShots[1] != 2 || len(res.AddedShots) != 0 || len(res.RemovedShots) != 0 {
		t.Fatalf("res = %+v", res)
	}
	byId, edges := graphNodesById(t, out)
	v1 := byId["video-1"]["data"].(map[string]any)
	if v1["line"] != "他还被蒙在鼓里" || toInt(v1["assetId"]) != 11 || v1["taskStatus"] != "succeeded" || v1["lines"].(map[string]any)["en"] != "He still has no idea" {
		t.Fatalf("video-1 = %+v", v1)
	}
	a1 := byId["audio-1"]["data"].(map[string]any)
	if a1["text"] != "他还被蒙在鼓里" || a1["prompt"] != "他还被蒙在鼓里" || toInt(a1["assetId"]) != 21 {
		t.Fatalf("audio-1 = %+v", a1)
	}
	v2 := byId["video-2"]["data"].(map[string]any)
	if v2["line"] != "走吧" || !strings.Contains(v2["prompt"].(string), "男主转身离开") {
		t.Fatalf("video-2 = %+v", v2)
	}
	// 第 2 镜原来没有配音节点，现在有台词 → 补建 audio-2 并连线
	a2, ok := byId["audio-2"]
	if !ok || a2["data"].(map[string]any)["text"] != "走吧" {
		t.Fatalf("audio-2 = %+v", a2)
	}
	hasEdge := func(s, d string) bool {
		for _, e := range edges {
			if e["source"] == s && e["target"] == d {
				return true
			}
		}
		return false
	}
	if !hasEdge("video-2", "audio-2") || !hasEdge("image-2", "video-1") {
		t.Fatalf("edges = %+v", edges)
	}
	if !strings.Contains(byId["text-1"]["data"].(map[string]any)["content"].(string), "他还被蒙在鼓里") {
		t.Fatal("策划案文本节点应更新")
	}

	// 再加第 3 镜（新增），并去掉第 2 镜 → 第 3 镜变成第 2 镜（同步），画布里的 2 仍在；新增只有在 plan 镜数超过画布时出现
	more, _ := ParsePlanText(reviewPlanJSON)
	more.Shots = append(more.Shots, model.PlanShot{Scene: "s1", Characters: []string{"c1"}, Prompt: "片尾", Line: "完", Seconds: 3})
	_ = more.Normalize(30)
	out2, res2, err := syncPlanGraph(more, out, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.SyncedShots) != 2 || len(res2.AddedShots) != 1 || res2.AddedShots[0] != 3 || len(res2.RemovedShots) != 0 {
		t.Fatalf("res2 = %+v", res2)
	}
	byId, edges = graphNodesById(t, out2)
	v3, ok := byId["video-3"]
	if !ok || v3["data"].(map[string]any)["line"] != "完" {
		t.Fatalf("video-3 = %+v", v3)
	}
	img3 := v3["data"].(map[string]any)["refNodeId"].(string)
	if img3 != "image-4" || !hasEdge(img3, "video-3") || !hasEdge("image-1", img3) || !hasEdge("video-3", "audio-3") {
		t.Fatalf("新镜头连线不对: ref=%s edges=%+v", img3, edges)
	}

	// 只剩第 1 镜：画布里的 2、3 只列出为 removed，节点仍在
	less, _ := ParsePlanText(reviewPlanJSON)
	less.Shots = less.Shots[:1]
	_ = less.Normalize(30)
	out3, res3, err := syncPlanGraph(less, out2, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(res3.RemovedShots) != 2 || res3.RemovedShots[0] != 2 || res3.RemovedShots[1] != 3 || len(res3.SyncedShots) != 1 {
		t.Fatalf("res3 = %+v", res3)
	}
	byId, _ = graphNodesById(t, out3)
	if _, ok := byId["video-3"]; !ok {
		t.Fatal("删掉的镜头节点不应被删除")
	}
	if len(byId) != 11 {
		t.Fatalf("节点数 = %d，want 11（7 原有 + 补建的 audio-2 + 新镜的 image-4/video-3/audio-3）", len(byId))
	}
}

// TestReviewSyncScreenplayCanvas D02：同步落库顺序 —— 保存画布（乐观锁）→ 记快照 → 项目 touch → built_revision 推到 revision。
func TestReviewSyncScreenplayCanvas(t *testing.T) {
	plan, _ := ParsePlanText(reviewPlanJSON)
	_ = plan.Normalize(30)
	m := BuildModels{Image: "img", Video: "vid", Audio: "aud"}
	cv := &model.Canvas{Id: 29, UserId: 8, ProjectId: 5, Version: 3, Name: "真相", Graph: BuildPlanGraph(plan, m)}
	edited, _ := ParsePlanText(reviewPlanJSON)
	edited.Shots[0].Line = "改过的台词"
	_ = edited.Normalize(30)
	b, _ := json.Marshal(edited)
	sp := &model.Screenplay{Id: 7, UserId: 8, Status: model.ScreenplayStatusBuilt, CanvasId: 29, Plan: string(b), Revision: 4, BuiltRevision: 1}

	mock := mockDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `canvas` SET").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), 29, 8, 3).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `canvas_version`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT `id` FROM `canvas_version`").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `project` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `screenplay` SET").WithArgs(4, sqlmock.AnyArg(), 7).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	res, err := SyncScreenplayCanvas(8, sp, cv, m)
	if err != nil {
		t.Fatal(err)
	}
	if res.CanvasId != 29 || res.ProjectId != 5 || res.Revision != 4 || len(res.SyncedShots) != 2 {
		t.Fatalf("res = %+v", res)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
