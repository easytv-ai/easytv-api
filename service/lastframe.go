package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
)

// 末帧提取（F4 续写）：取视频最后一帧存成 JPG 资产，作为下一段的首帧参考图。

// ExtractLastFrame 返回新建的图片资产；无 ffmpeg 或失败时返回错误。
func ExtractLastFrame(userId int, video *model.Asset) (*model.Asset, error) {
	if video == nil || video.Type != model.AssetTypeVideo {
		return nil, errors.New("只能对视频取末帧")
	}
	if DefaultStorage == nil {
		return nil, errors.New("存储未配置")
	}
	if !FFmpegAvailable() {
		return nil, errors.New("服务器未安装 ffmpeg，暂不能续写")
	}
	bin := FFmpegBin()
	input := localVideoPath(video.StorageKey)
	if input == "" {
		if !strings.HasPrefix(video.Url, "http") {
			return nil, errors.New("视频地址不可用")
		}
		dir, err := os.MkdirTemp("", "easytv-lastframe-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		tmp := filepath.Join(dir, "src.mp4")
		ctx, cancel := context.WithTimeout(context.Background(), composeJobTimeout)
		defer cancel()
		if err := DownloadTo(ctx, video.Url, tmp, composeClipMaxByte); err != nil {
			return nil, fmt.Errorf("下载视频失败：%v", err)
		}
		input = tmp
	}
	ctx, cancel := context.WithTimeout(context.Background(), coverFFmpegTimeout)
	defer cancel()
	// sseof 从结尾回退取帧；部分容器不支持时退回 -ss 0
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "-hide_banner", "-loglevel", "error", "-sseof", "-0.5", "-i", input,
		"-update", "1", "-frames:v", "1", "-q:v", "3", "-f", "image2", "-vcodec", "mjpeg", "pipe:1")
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil || out.Len() == 0 {
		out.Reset()
		stderr.Reset()
		cmd2 := exec.CommandContext(ctx, bin, "-hide_banner", "-loglevel", "error", "-i", input,
			"-vf", "select=eq(n\\,0)", "-frames:v", "1", "-q:v", "3", "-f", "image2", "-vcodec", "mjpeg", "pipe:1")
		cmd2.Stdout, cmd2.Stderr = &out, &stderr
		if err2 := cmd2.Run(); err2 != nil || out.Len() == 0 {
			return nil, fmt.Errorf("取末帧失败：%s", truncate(strings.TrimSpace(stderr.String()), 120))
		}
	}
	key := NewStorageKey(fmt.Sprintf("lastframe-%d.jpg", video.Id))
	url, err := DefaultStorage.Save(key, bytes.NewReader(out.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("保存末帧失败：%v", err)
	}
	asset := &model.Asset{
		UserId: userId, TeamId: video.TeamId, ProjectId: video.ProjectId, Type: model.AssetTypeImage,
		Name: fmt.Sprintf("末帧 · %s", truncate(video.Name, 40)), Url: url, StorageKey: key,
		Mime: "image/jpeg", Size: int64(out.Len()), Source: model.AssetSourceGenerated,
	}
	if err := asset.Insert(); err != nil {
		_ = DefaultStorage.Remove(key)
		return nil, err
	}
	common.SysLog(fmt.Sprintf("lastframe extracted asset=%d from video=%d", asset.Id, video.Id))
	return asset, nil
}
