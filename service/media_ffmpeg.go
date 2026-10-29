package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/setting"
)

// ===== 媒体处理能力层 =====
//
// 全服务就这一处直连 ffmpeg / ffprobe：成片合成、封面抽帧、末帧、首帧适配、媒体工具
// （API.md 23）都走这里的入口，可执行文件路径由 setting 配置，不再各写各的 "ffmpeg"。

const (
	// MediaSourceMaxByte 单个源素材下载上限（1GB）：抽帧 / 分离要下载整段视频，
	// 不设上限时一个超大文件就能把磁盘写满。
	MediaSourceMaxByte = int64(1) << 30

	mediaHTTPTimeout = 3 * time.Minute

	mediaDefaultFFmpeg  = "ffmpeg"
	mediaDefaultFFprobe = "ffprobe"
)

var (
	mediaBinsMu sync.RWMutex
	ffmpegBin   = mediaDefaultFFmpeg
	ffprobeBin  = mediaDefaultFFprobe
)

// FFmpegBin ffmpeg 可执行文件（启动时与配置热更新后由 ApplyMediaConfig 解析）。
func FFmpegBin() string {
	mediaBinsMu.RLock()
	defer mediaBinsMu.RUnlock()
	return ffmpegBin
}

// FFprobeBin ffprobe 可执行文件。
func FFprobeBin() string {
	mediaBinsMu.RLock()
	defer mediaBinsMu.RUnlock()
	return ffprobeBin
}

// ApplyMediaConfig 解析 ffmpeg / ffprobe 路径（启动时与配置热更新后调用）。
// 优先级：setting（FFmpegPath / FFprobePath）> 环境变量（FFMPEG_PATH / FFPROBE_PATH）> PATH 里同名程序。
// 解析结果缓存在进程里，不在 ffmpeg 热路径上查配置表。
func ApplyMediaConfig() {
	mediaBinsMu.Lock()
	defer mediaBinsMu.Unlock()
	if v := resolveMediaBin(setting.FFmpegPath, "FFMPEG_PATH", mediaDefaultFFmpeg); v != "" {
		ffmpegBin = v
	}
	if v := resolveMediaBin(setting.FFprobePath, "FFPROBE_PATH", mediaDefaultFFprobe); v != "" {
		ffprobeBin = v
	}
}

func resolveMediaBin(settingKey, envKey, def string) string {
	if v := strings.TrimSpace(setting.GetSettingString(settingKey)); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return v
	}
	return def
}

// FFmpegAvailable 服务器是否有可用的 ffmpeg。
func FFmpegAvailable() bool {
	_, err := exec.LookPath(FFmpegBin())
	return err == nil
}

// FFprobeAvailable 服务器是否有可用的 ffprobe。
func FFprobeAvailable() bool {
	_, err := exec.LookPath(FFprobeBin())
	return err == nil
}

// RunFFmpeg 跑一次 ffmpeg，返回合并后的输出。
func RunFFmpeg(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, FFmpegBin(), append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, args...)...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

// FFmpegError ffmpeg 失败原因：优先最后一行输出；没有输出（被信号杀掉、找不到可执行文件）时用进程错误。
func FFmpegError(out string, err error) error {
	if line := LastLine(out); line != "" {
		return errors.New(line)
	}
	return err
}

// LastLine 取输出的最后一行（ffmpeg 把失败原因写在最后一行）。
func LastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 {
		return ""
	}
	return truncate(strings.TrimSpace(lines[len(lines)-1]), 200)
}

// ProbeMedia 用 ffprobe 取时长与是否有音轨。
func ProbeMedia(ctx context.Context, path string) (float64, bool) {
	dur, _, _, hasAudio := ProbeMediaInfo(ctx, path)
	return dur, hasAudio
}

// ProbeMediaInfo 取时长、首条视频流宽高与是否有音轨。src 可以是本地路径或 ffprobe 能读的地址。
func ProbeMediaInfo(ctx context.Context, src string) (dur float64, width, height int, hasAudio bool) {
	cmd := exec.CommandContext(ctx, FFprobeBin(), "-v", "error",
		"-show_entries", "format=duration:stream=codec_type,width,height", "-of", "json", src)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, 0, false
	}
	var r struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(out, &r) != nil {
		return 0, 0, 0, false
	}
	dur, _ = strconv.ParseFloat(r.Format.Duration, 64)
	for _, s := range r.Streams {
		switch s.CodecType {
		case "video":
			if width == 0 {
				width, height = s.Width, s.Height
			}
		case "audio":
			hasAudio = true
		}
	}
	return dur, width, height, hasAudio
}

var mediaHTTPClient = &http.Client{Timeout: mediaHTTPTimeout}

// DownloadTo 把素材地址拉到本地文件；limit 是字节上限，超了返回「文件过大」。
// 本地存储（没配 UploadBaseUrl）的资产地址是 /upload/... 相对路径，直接读盘。
func DownloadTo(ctx context.Context, url, path string, limit int64) error {
	if strings.HasPrefix(url, "/upload/") {
		return copyLocalUpload(strings.TrimPrefix(url, "/upload/"), path, limit)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := mediaHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return errors.New("文件过大")
	}
	return nil
}

func copyLocalUpload(key, path string, limit int64) error {
	if !isSafeKey(key) {
		return errors.New("无效的本地文件地址")
	}
	src, err := os.Open(filepath.Join(UploadDir, filepath.FromSlash(key)))
	if err != nil {
		return err
	}
	defer src.Close()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(src, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return errors.New("文件过大")
	}
	return nil
}

// AssetDraft 落资产需要的元信息。
type AssetDraft struct {
	UserId    int
	TeamId    int
	ProjectId int
	TaskId    int
	Type      string
	Name      string
	Mime      string
	Source    string
	Label     string
	// Duration / Width / Height 本地处理直接就知道，省一次 ffprobe；0 表示入库后异步补
	Duration float64
	Width    int
	Height   int
}

// SaveAssetFromFile 把本地文件上传存储并写入资产库。上传成功但入库失败时删掉文件，不留孤儿。
func SaveAssetFromFile(path string, d AssetDraft) (*model.Asset, error) {
	if DefaultStorage == nil {
		return nil, errors.New("存储未配置")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, _ := f.Stat()
	key := NewStorageKey(d.Name)
	url, err := DefaultStorage.Save(key, f)
	if err != nil {
		return nil, err
	}
	asset := &model.Asset{
		UserId: d.UserId, TeamId: d.TeamId, ProjectId: d.ProjectId, TaskId: d.TaskId,
		Type: d.Type, Name: d.Name, Url: url, StorageKey: key, Mime: d.Mime,
		Source: d.Source, Duration: d.Duration, Width: d.Width, Height: d.Height,
	}
	if st != nil {
		asset.Size = st.Size()
	}
	if err := asset.Insert(); err != nil {
		_ = DefaultStorage.Remove(key)
		return nil, err
	}
	if asset.Type == model.AssetTypeVideo {
		go EnsureAssetCover(asset)
	}
	// 本地处理没带时长（少数动作路径）时才需要探测补齐
	if asset.Duration <= 0 && (asset.Type == model.AssetTypeVideo || asset.Type == model.AssetTypeAudio) {
		go EnsureAssetMediaInfo(asset)
	}
	common.SysLog(fmt.Sprintf("media asset saved id=%d type=%s source=%s", asset.Id, asset.Type, asset.Source))
	return asset, nil
}
