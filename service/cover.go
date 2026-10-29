package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
)

// ===== 视频封面抽帧 =====
//
// 优先用阿里云 OSS 的视频截帧：视频存在 OSS 时给地址加 x-oss-process 参数即可拿到 JPEG，服务器不需要装 ffmpeg；
// 其次本机 ffmpeg（本地存储或非 OSS 地址）；都不可用则封面留空，前端用视频首帧兜底。
// 抽到的帧存回存储成为独立 JPG 文件（分享卡片、OG 图需要稳定的静态地址）。

const (
	coverSnapshotParam = "x-oss-process=video/snapshot,t_1000,f_jpg,w_1280,h_0,m_fast"
	coverMaxBytes      = 8 << 20
	coverHTTPTimeout   = 20 * time.Second
	coverFFmpegTimeout = 60 * time.Second
)

// CoverCapability 当前部署能用的抽帧手段（管理端展示）。
type CoverCapability struct {
	OSSSnapshot bool `json:"oss_snapshot"` // 存储为阿里云 OSS，可用截帧参数
	FFmpeg      bool `json:"ffmpeg"`       // 本机有 ffmpeg
}

// CoverCapabilities 探测抽帧能力。
func CoverCapabilities() CoverCapability {
	_, ossOK := DefaultStorage.(*OSSStorage)
	return CoverCapability{OSSSnapshot: ossOK, FFmpeg: FFmpegAvailable()}
}

// GenerateVideoCover 从视频抽一帧存为 JPG，返回封面地址。抽不到（相对地址、无可用手段）返回空串且不报错。
func GenerateVideoCover(videoUrl, videoKey string) (string, error) {
	if DefaultStorage == nil {
		return "", errors.New("存储未初始化")
	}
	var data []byte
	if strings.HasPrefix(videoUrl, "http") {
		data = snapshotViaOSS(videoUrl)
	}
	if len(data) == 0 {
		// ffmpeg 输入：本地存储的文件优先，其次公网地址；相对地址（前端静态文件）没法处理
		input := ""
		if strings.HasPrefix(videoUrl, "http") {
			input = videoUrl
		}
		if local := localVideoPath(videoKey); local != "" {
			input = local
		}
		if input != "" {
			var err error
			data, err = snapshotViaFFmpeg(input)
			if err != nil {
				return "", err
			}
		}
	}
	if len(data) == 0 {
		return "", nil
	}
	return DefaultStorage.Save(coverKeyFor(videoKey), bytes.NewReader(data))
}

// EnsureAssetCover 视频资产没有封面时抽帧并写回（异步调用，失败只记日志）。
func EnsureAssetCover(asset *model.Asset) string {
	if asset == nil || asset.Type != model.AssetTypeVideo {
		return ""
	}
	if asset.CoverUrl != "" {
		return asset.CoverUrl
	}
	cover, err := GenerateVideoCover(asset.Url, asset.StorageKey)
	if err != nil {
		common.SysError(fmt.Sprintf("cover: asset #%d snapshot failed: %v", asset.Id, err))
		return ""
	}
	if cover == "" {
		return ""
	}
	if err := model.SetAssetCover(asset.Id, cover); err != nil {
		common.SysError(fmt.Sprintf("cover: asset #%d save failed: %v", asset.Id, err))
		return ""
	}
	asset.CoverUrl = cover
	return cover
}

// EnsureAssetMediaInfo 生成的音视频入库时没有时长 / 尺寸（流式存储，拿不到文件），入库后用 ffprobe 读地址补上。
// 时间线按时长算入出点与总长，缺了只能回落到策划秒数。没有 ffprobe 或读取失败时静默跳过。
func EnsureAssetMediaInfo(asset *model.Asset) {
	if asset == nil || (asset.Type != model.AssetTypeVideo && asset.Type != model.AssetTypeAudio) || asset.Duration > 0 {
		return
	}
	src := asset.Url
	if strings.HasPrefix(src, "/upload/") {
		src = filepath.Join(UploadDir, filepath.FromSlash(strings.TrimPrefix(src, "/upload/")))
	} else if !strings.HasPrefix(src, "http") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dur, w, h, _ := ProbeMediaInfo(ctx, src)
	if dur <= 0 {
		return
	}
	dur = math.Round(dur*100) / 100
	if err := model.SetAssetMediaInfo(asset.Id, dur, w, h); err == nil {
		asset.Duration, asset.Width, asset.Height = dur, w, h
	}
}

// snapshotViaOSS 用 OSS 截帧参数请求视频地址；不是 OSS（返回的不是图片）或出错时返回 nil。
func snapshotViaOSS(videoUrl string) []byte {
	sep := "?"
	if strings.Contains(videoUrl, "?") {
		sep = "&"
	}
	req, err := http.NewRequest(http.MethodGet, videoUrl+sep+coverSnapshotParam, nil)
	if err != nil {
		return nil
	}
	res, err := (&http.Client{Timeout: coverHTTPTimeout}).Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "image/") {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, coverMaxBytes))
	if err != nil || len(data) == 0 {
		return nil
	}
	return data
}

// snapshotViaFFmpeg 本机 ffmpeg 抽第 1 秒的帧；没有 ffmpeg 返回 nil, nil。
func snapshotViaFFmpeg(input string) ([]byte, error) {
	if !FFmpegAvailable() {
		return nil, nil
	}
	bin := FFmpegBin()
	for _, seek := range []string{"1", "0"} {
		ctx, cancel := context.WithTimeout(context.Background(), coverFFmpegTimeout)
		cmd := exec.CommandContext(ctx, bin, "-hide_banner", "-loglevel", "error", "-ss", seek, "-i", input,
			"-frames:v", "1", "-vf", "scale='min(1280,iw)':-2", "-q:v", "3", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1")
		var out, stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		cancel()
		if runErr == nil && out.Len() > 0 {
			return out.Bytes(), nil
		}
		if runErr != nil && seek == "0" {
			return nil, fmt.Errorf("ffmpeg: %s", strings.TrimSpace(firstNonEmptyString(stderr.String(), runErr.Error())))
		}
	}
	return nil, nil
}

// localVideoPath 本地存储时返回视频文件的绝对路径（ffmpeg 直接读文件）。
func localVideoPath(videoKey string) string {
	local, ok := DefaultStorage.(*LocalStorage)
	if !ok || videoKey == "" || !isSafeKey(videoKey) {
		return ""
	}
	p := filepath.Join(local.Dir, filepath.FromSlash(videoKey))
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	return abs
}

// coverKeyFor 封面存储路径：与视频同目录同名加 _cover.jpg；没有视频 key 时按日期生成。
func coverKeyFor(videoKey string) string {
	if videoKey != "" && isSafeKey(videoKey) {
		return strings.TrimSuffix(videoKey, path.Ext(videoKey)) + "_cover.jpg"
	}
	return NewStorageKey("cover.jpg")
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
