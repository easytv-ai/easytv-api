package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"easytv-api/model"
)

const mixBgmVolumeDefault = 0.2

// validateMixBgm 创建作业时确认配乐属于当前用户或团队，并且是音频。
func validateMixBgm(userId, teamId int, src *model.Asset, params map[string]any) error {
	audioId := mixBgmAudioId(params)
	if audioId <= 0 {
		return errors.New("请指定配乐")
	}
	if audioId == src.Id {
		return errors.New("配乐不能是这段视频本身")
	}
	audio, err := ResolveOwnedAsset(userId, teamId, audioId)
	if err != nil {
		return errors.New("配乐不存在或不属于你")
	}
	if audio.Type != model.AssetTypeAudio {
		return errors.New("配乐必须是音频")
	}
	return nil
}

func mixBgmAudioId(params map[string]any) int {
	return int(mediaParamFloat(params, "audio_asset_id", 0))
}

func mixBgmVolume(params map[string]any) float64 {
	if params == nil || params["volume"] == nil {
		return mixBgmVolumeDefault
	}
	v := mediaParamFloat(params, "volume", mixBgmVolumeDefault)
	if v < 0.05 {
		return 0.05
	}
	if v > 1 {
		return 1
	}
	return v
}

// mixBgmFilter 配乐先压低、尾部淡出。原片有声时再和原声混在一起，没有音轨就只留配乐。
func mixBgmFilter(hasAudio bool, volume, fadeStart, fadeDur float64) string {
	bg := fmt.Sprintf("[1:a]volume=%.2f,afade=t=out:st=%.1f:d=%.1f[bg]", volume, fadeStart, fadeDur)
	if !hasAudio {
		return bg
	}
	return bg + ";[0:a][bg]amix=inputs=2:duration=first:dropout_transition=2:normalize=0[a]"
}

func mixBgmFade(duration float64) (start, length float64) {
	length = 2
	if duration < 4 {
		length = duration / 2
	}
	if length < 0.2 {
		length = 0.2
	}
	start = duration - length
	if start < 0 {
		start = 0
	}
	return start, length
}

// mediaMixBgm 把一条已有音频混进视频。配乐短了就循环，长了按原片时长截掉。
func mediaMixBgm(ctx context.Context, dir string, src *model.Asset, srcPath string, params map[string]any) (mediaProduct, error) {
	audioId := mixBgmAudioId(params)
	if audioId <= 0 {
		return mediaProduct{}, errors.New("请指定配乐")
	}
	audio, err := model.GetAssetByIdUnscoped(audioId)
	if err != nil || audio.DeletedAt.Valid {
		return mediaProduct{}, errors.New("配乐已不存在")
	}
	if audio.Type != model.AssetTypeAudio {
		return mediaProduct{}, errors.New("配乐必须是音频")
	}
	audioPath, err := mediaSourceFileAs(ctx, dir, audio, "bgm")
	if err != nil {
		return mediaProduct{}, fmt.Errorf("读取配乐失败：%v", err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	duration, _, _, hasAudio := ProbeMediaInfo(probeCtx, srcPath)
	cancel()
	if duration <= 0.3 {
		return mediaProduct{}, errors.New("读不到视频时长")
	}
	fadeStart, fadeDur := mixBgmFade(duration)
	filter := mixBgmFilter(hasAudio, mixBgmVolume(params), fadeStart, fadeDur)
	audioMap := "[bg]"
	if hasAudio {
		audioMap = "[a]"
	}
	out := filepath.Join(dir, "mix-bgm.mp4")
	if _, err := runMediaFFmpeg(ctx, out, mixBgmArgs(srcPath, audioPath, filter, audioMap, out, duration, true)...); err != nil {
		if _, err2 := runMediaFFmpeg(ctx, out, mixBgmArgs(srcPath, audioPath, filter, audioMap, out, duration, false)...); err2 != nil {
			return mediaProduct{}, fmt.Errorf("混入配乐失败：%v", err2)
		}
	}
	return videoProduct(ctx, out, src, "配乐"), nil
}

func mixBgmArgs(srcPath, audioPath, filter, audioMap, out string, duration float64, copyVideo bool) []string {
	args := []string{
		"-y", "-i", srcPath, "-stream_loop", "-1", "-i", audioPath,
		"-filter_complex", filter,
		"-map", "0:v:0", "-map", audioMap,
		"-t", fmt.Sprintf("%.3f", duration),
	}
	if copyVideo {
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "20")
	}
	return append(args, "-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart", out)
}
