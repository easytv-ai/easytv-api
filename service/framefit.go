package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
)

// 首帧尺寸适配：部分模型（sora 系）要求图生视频的首帧图与输出尺寸完全一致，
// 否则网关回 400「Inpaint image must match the requested width and height」。
// 我们的分镜图是按比例生成的（16:9 → 1792x1024），和 720P 的 1280x720 对不上，
// 所以在提交前先裁一张同尺寸的副本。只对 model_price.frame_match=1 的模型做，
// 避免给本来就正常的模型白加一次 ffmpeg 与一次上传。

const frameFitMaxByte = 30 << 20

// TargetVideoSize 按 params 推导视频输出尺寸。
// 优先取显式 size（如 "1280x720"），否则用 resolution 的数字做短边、ratio 定方向。
// 返回 0,0 表示推不出来（调用方应跳过适配，让网关自己决定）。
func TargetVideoSize(ratio, resolution, size string) (int, int) {
	if w, h, ok := parseSize(size); ok {
		return w, h
	}
	short := resolutionShortSide(resolution)
	if short == 0 {
		return 0, 0
	}
	rw, rh, ok := parseRatio(ratio)
	if !ok {
		rw, rh = 16, 9
	}
	if rw >= rh {
		// 横屏：高为短边
		return even(short * rw / rh), even(short)
	}
	// 竖屏：宽为短边
	return even(short), even(short * rh / rw)
}

func parseSize(size string) (int, int, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

func parseRatio(ratio string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(ratio), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// resolutionShortSide "720P" → 720；取不出数字返回 0。
func resolutionShortSide(res string) int {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, res)
	n, err := strconv.Atoi(digits)
	if err != nil || n < 120 || n > 4320 {
		return 0
	}
	return n
}

// even 编码器普遍要求偶数边长。
func even(n int) int {
	if n%2 == 1 {
		return n - 1
	}
	return n
}

// FitFirstFrame 返回一张恰好 w×h 的图片资产；原图已是该尺寸时直接返回原图。
// 缩放策略是「铺满后居中裁切」：首帧加黑边会直接出现在成片第一帧里。
func FitFirstFrame(userId int, img *model.Asset, w, h int) (*model.Asset, error) {
	if img == nil || img.Type != model.AssetTypeImage {
		return nil, errors.New("只能适配图片资产")
	}
	if w <= 0 || h <= 0 {
		return nil, errors.New("目标尺寸无效")
	}
	if DefaultStorage == nil {
		return nil, errors.New("存储未配置")
	}
	if !FFmpegAvailable() {
		return nil, errors.New("服务器未安装 ffmpeg，暂不能适配首帧尺寸")
	}
	bin := FFmpegBin()
	input := localVideoPath(img.StorageKey)
	if input == "" {
		if !strings.HasPrefix(img.Url, "http") {
			return nil, errors.New("图片地址不可用")
		}
		dir, err := os.MkdirTemp("", "easytv-framefit-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		tmp := filepath.Join(dir, "src")
		ctx, cancel := context.WithTimeout(context.Background(), coverFFmpegTimeout)
		defer cancel()
		if err := DownloadTo(ctx, img.Url, tmp, frameFitMaxByte); err != nil {
			return nil, fmt.Errorf("下载图片失败：%v", err)
		}
		input = tmp
	}
	ctx, cancel := context.WithTimeout(context.Background(), coverFFmpegTimeout)
	defer cancel()
	if iw, ih := probeImageSize(ctx, input); iw == w && ih == h {
		// 尺寸已经对得上，不必多造一份资产
		return img, nil
	}
	var out, stderr bytes.Buffer
	vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d", w, h, w, h)
	cmd := exec.CommandContext(ctx, bin, "-hide_banner", "-loglevel", "error", "-i", input,
		"-vf", vf, "-frames:v", "1", "-q:v", "2", "-f", "image2", "-vcodec", "mjpeg", "pipe:1")
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil || out.Len() == 0 {
		return nil, fmt.Errorf("适配首帧尺寸失败：%s", truncate(strings.TrimSpace(stderr.String()), 120))
	}
	key := NewStorageKey(fmt.Sprintf("framefit-%d-%dx%d.jpg", img.Id, w, h))
	url, err := DefaultStorage.Save(key, bytes.NewReader(out.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("保存首帧失败：%v", err)
	}
	asset := &model.Asset{
		UserId: userId, TeamId: img.TeamId, ProjectId: img.ProjectId, Type: model.AssetTypeImage,
		Name: fmt.Sprintf("首帧 %dx%d · %s", w, h, truncate(img.Name, 30)), Url: url, StorageKey: key,
		Mime: "image/jpeg", Size: int64(out.Len()), Source: model.AssetSourceGenerated,
	}
	if err := asset.Insert(); err != nil {
		_ = DefaultStorage.Remove(key)
		return nil, err
	}
	common.SysLog(fmt.Sprintf("first frame fitted asset=%d from=%d size=%dx%d", asset.Id, img.Id, w, h))
	return asset, nil
}

// probeImageSize 取图片宽高；失败返回 0,0。
func probeImageSize(ctx context.Context, path string) (int, int) {
	_, w, h, _ := ProbeMediaInfo(ctx, path)
	return w, h
}

// FitTaskFirstFrame 按模型能力适配任务的首帧素材，返回改写后的 inputs JSON。
// 只在 price.FrameMatch=1 且任务带 first_frame / image 素材时动手；
// 推不出目标尺寸、或适配失败时原样返回，让网关自己判断（失败照样全额退分）。
func FitTaskFirstFrame(userId int, price *model.ModelPrice, inputsJSON, paramsJSON string) string {
	if price == nil || price.FrameMatch != 1 || strings.TrimSpace(inputsJSON) == "" {
		return inputsJSON
	}
	var params struct {
		Ratio       string `json:"ratio"`
		AspectRatio string `json:"aspect_ratio"`
		Resolution  string `json:"resolution"`
		Size        string `json:"size"`
	}
	_ = json.Unmarshal([]byte(paramsJSON), &params)
	// provider 优先发 aspect_ratio，这里按同样的优先级推尺寸，否则裁出来的首帧和输出方向对不上
	ratio := params.AspectRatio
	if ratio == "" {
		ratio = params.Ratio
	}
	w, h := TargetVideoSize(ratio, params.Resolution, params.Size)
	if w == 0 || h == 0 {
		return inputsJSON
	}
	var inputs []map[string]any
	if err := json.Unmarshal([]byte(inputsJSON), &inputs); err != nil {
		return inputsJSON
	}
	// 只有作为首帧的素材需要与输出同尺寸；reference 是外观参考图，不裁
	changed := false
	for _, in := range inputs {
		role, _ := in["role"].(string)
		if role != "first_frame" && role != "image" {
			continue
		}
		assetId := 0
		switch v := in["asset_id"].(type) {
		case float64:
			assetId = int(v)
		case int:
			assetId = v
		}
		if assetId <= 0 {
			continue
		}
		img, err := model.GetUserAsset(userId, assetId)
		if err != nil {
			continue
		}
		fitted, err := FitFirstFrame(userId, img, w, h)
		if err != nil {
			common.SysLog(fmt.Sprintf("first frame fit skipped asset=%d: %v", assetId, err))
			continue
		}
		if fitted.Id != assetId {
			in["asset_id"] = fitted.Id
			changed = true
		}
	}
	if !changed {
		return inputsJSON
	}
	out, err := json.Marshal(inputs)
	if err != nil {
		return inputsJSON
	}
	return string(out)
}
