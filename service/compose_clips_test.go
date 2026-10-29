package service

import (
	"context"
	"encoding/json"
	"fmt"
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

func videoCanvas(n int) *model.Canvas {
	nodes := make([]map[string]any, 0, n)
	for i := 1; i <= n; i++ {
		nodes = append(nodes, map[string]any{
			"id":       fmt.Sprintf("video-%d", i),
			"position": map[string]any{"x": 0, "y": i * 100},
			"data":     map[string]any{"kind": "video", "assetId": i},
		})
	}
	raw, _ := json.Marshal(map[string]any{"nodes": nodes})
	return &model.Canvas{UserId: 8, Graph: string(raw)}
}

// TestCollectVideoClipsTooMany 超过上限要明确报错，不能截掉后面的镜头还报成功。
func TestCollectVideoClipsTooMany(t *testing.T) {
	_, err := collectVideoClips(videoCanvas(ComposeMaxClips+1), nil)
	if err == nil || !strings.Contains(err.Error(), "最多合成") {
		t.Fatalf("err = %v", err)
	}
}

// TestCollectVideoClipsBroken 本地存储的 /upload/ 地址可用；资产不存在的镜头要逐个列出来。
func TestCollectVideoClipsBroken(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.DB = db
	mock.ExpectQuery("SELECT .* FROM `asset`").WillReturnRows(
		sqlmock.NewRows([]string{"id", "user_id", "type", "url"}).AddRow(1, 8, model.AssetTypeVideo, "/upload/2026/09/13/a.mp4"))
	mock.ExpectQuery("SELECT .* FROM `asset`").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, err = collectVideoClips(videoCanvas(2), nil)
	if err == nil || !strings.Contains(err.Error(), "video-2") || strings.Contains(err.Error(), "video-1") {
		t.Fatalf("err = %v, want only video-2 listed", err)
	}
}

// TestDownloadToLocalUpload 本地存储相对地址直接读盘，并守住大小上限。
func TestDownloadToLocalUpload(t *testing.T) {
	dir := t.TempDir()
	old := UploadDir
	UploadDir = dir
	defer func() { UploadDir = old }()
	if err := os.MkdirAll(filepath.Join(dir, "2026"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026", "a.mp4"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out.mp4")
	if err := DownloadTo(context.Background(), "/upload/2026/a.mp4", dst, 10); err != nil {
		t.Fatalf("local copy: %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "0123456789" {
		t.Fatalf("copied = %q", b)
	}
	if err := DownloadTo(context.Background(), "/upload/2026/a.mp4", dst, 5); err == nil {
		t.Fatal("超限应报错")
	}
	if err := DownloadTo(context.Background(), "/upload/../secret", dst, 10); err == nil {
		t.Fatal("路径穿越应拒绝")
	}
}

// TestNormalizeClipWithVoice 真跑 ffmpeg：2 秒无声画面 + 3 秒配音 → 段落延长到配音结束且带音轨（本机没有 ffmpeg 就跳过）。
func TestNormalizeClipWithVoice(t *testing.T) {
	if exec.Command("ffmpeg", "-version").Run() != nil {
		t.Skip("no ffmpeg")
	}
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	voice := filepath.Join(dir, "voice.mp3")
	if out, err := RunFFmpeg(ctx, "-y", "-f", "lavfi", "-i", "color=c=blue:s=320x240:d=2", "-r", "30", "-pix_fmt", "yuv420p", src); err != nil {
		t.Fatalf("make video: %v %s", err, out)
	}
	if out, err := RunFFmpeg(ctx, "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=3", voice); err != nil {
		t.Fatalf("make voice: %v %s", err, out)
	}
	srcDur, hasAudio := ProbeMedia(ctx, src)
	voiceDur, _ := ProbeMedia(ctx, voice)
	dur := clipDurationWithVoice(srcDur, voiceDur)
	if dur < 3.3 || dur > 3.6 {
		t.Fatalf("clip duration = %.2f, want ≈3.45", dur)
	}
	dst := filepath.Join(dir, "clip.mp4")
	if err := normalizeClip(ctx, src, dst, 640, 360, clipRender{dur: dur, hasAudio: hasAudio, voice: voice, origVol: 0.35, voiceVol: 1}); err != nil {
		t.Fatalf("normalize with voice: %v", err)
	}
	gotDur, gotAudio := ProbeMedia(ctx, dst)
	if !gotAudio || gotDur < dur-0.15 || gotDur > dur+0.15 {
		t.Fatalf("output duration=%.2f audio=%v, want ≈%.2f with audio", gotDur, gotAudio, dur)
	}
	// 配音比画面短：段落保持原长
	if d := clipDurationWithVoice(5, 2); d != 5 {
		t.Fatalf("short voice changed duration: %.2f", d)
	}
	if d := clipDurationWithVoice(5, 30); d != 15 {
		t.Fatalf("long voice should cap at +10s: %.2f", d)
	}
}

// TestVoiceAssetsByVideo 按连线找配音，没连线按镜号兜底；未生成的配音不算。
func TestVoiceAssetsByVideo(t *testing.T) {
	var g struct {
		Nodes []composeGraphNode `json:"nodes"`
		Edges []composeGraphEdge `json:"edges"`
	}
	_ = json.Unmarshal([]byte(`{"nodes":[
		{"id":"video-1","data":{"kind":"video","assetId":1,"sp":{"layer":"shot_video","shot":1}}},
		{"id":"audio-1","data":{"kind":"audio","assetId":11,"sp":{"layer":"voice","shot":1}}},
		{"id":"video-2","data":{"kind":"video","assetId":2,"sp":{"layer":"shot_video","shot":2}}},
		{"id":"audio-2","data":{"kind":"audio","assetId":12,"sp":{"layer":"voice","shot":2}}},
		{"id":"video-3","data":{"kind":"video","assetId":3,"sp":{"layer":"shot_video","shot":3}}},
		{"id":"audio-3","data":{"kind":"audio","sp":{"layer":"voice","shot":3}}}
	],"edges":[{"source":"video-1","target":"audio-1"},{"source":"video-3","target":"audio-3"}]}`), &g)
	got := voiceAssetsByVideo(g.Nodes, g.Edges)
	if got["video-1"] != 11 || got["video-2"] != 12 || got["video-3"] != 0 {
		t.Fatalf("voice map = %v", got)
	}
}
