package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"easytv-api/model"
)

// 去停顿（API.md 35，本地 ffmpeg，免费）：把口播里的长停顿剪掉，音画保持同步。
//
// 两步：先 silencedetect 找出静音区间，再用 select / aselect 只保留有声段并重排时间戳。
// 不直接用 silenceremove 滤镜：它只处理音频，视频轨不会跟着缩，音画会错位。
//
//	pass 1: ffmpeg -i in -af silencedetect=noise=-35dB:d=0.6 -f null -
//	pass 2: ffmpeg -i in -vf "select='between(t,a,b)+between(t,c,d)',setpts=N/FRAME_RATE/TB"
//	                  -af "aselect='between(t,a,b)+between(t,c,d)',asetpts=N/SR/TB" -c:v libx264 -c:a aac out.mp4

const (
	silenceThresholdDefault = -35.0
	silenceMinDefault       = 0.6
	// silencePad 每段停顿两头各留这么多秒，语句之间不至于贴得喘不过气。
	silencePad = 0.15
	// silenceMaxSegments select 表达式里最多保留多少段：再多说明阈值调得不对，合并成整段保留。
	silenceMaxSegments = 400
)

// timeRange 一段时间区间（秒）。
type timeRange struct {
	Start float64
	End   float64
}

var (
	silenceStartRe = regexp.MustCompile(`silence_start:\s*(-?[0-9.]+)`)
	silenceEndRe   = regexp.MustCompile(`silence_end:\s*(-?[0-9.]+)`)
)

// parseSilenceIntervals 从 silencedetect 的输出里读静音区间；末尾没有 silence_end 的（静音持续到结尾）用 total 收口。
func parseSilenceIntervals(out string, total float64) []timeRange {
	var list []timeRange
	start := -1.0
	for _, line := range strings.Split(out, "\n") {
		if m := silenceStartRe.FindStringSubmatch(line); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				start = math.Max(0, v)
			}
			continue
		}
		if m := silenceEndRe.FindStringSubmatch(line); m != nil && start >= 0 {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil && v > start {
				list = append(list, timeRange{Start: start, End: v})
			}
			start = -1
		}
	}
	if start >= 0 && total > start {
		list = append(list, timeRange{Start: start, End: total})
	}
	sort.Slice(list, func(i, k int) bool { return list[i].Start < list[k].Start })
	return list
}

// keepRanges 静音区间的补集：每段静音两头各留 pad 秒，短于 2*pad 的静音不剪。
// 返回要保留的区间（按时间排序、互不重叠）；没有可剪的停顿时返回 nil。
func keepRanges(silences []timeRange, total, pad float64) []timeRange {
	if total <= 0 {
		return nil
	}
	var cuts []timeRange
	for _, s := range silences {
		a, b := s.Start+pad, s.End-pad
		if b-a <= 0.05 {
			continue
		}
		cuts = append(cuts, timeRange{Start: math.Max(0, a), End: math.Min(total, b)})
	}
	if len(cuts) == 0 {
		return nil
	}
	var keep []timeRange
	cursor := 0.0
	for _, c := range cuts {
		if c.Start > cursor+0.02 {
			keep = append(keep, timeRange{Start: cursor, End: c.Start})
		}
		if c.End > cursor {
			cursor = c.End
		}
	}
	if total > cursor+0.02 {
		keep = append(keep, timeRange{Start: cursor, End: total})
	}
	return keep
}

// selectExpr 保留区间 → ffmpeg select 表达式：between(t,a,b)+between(t,c,d)。
func selectExpr(keep []timeRange) string {
	parts := make([]string, 0, len(keep))
	for _, r := range keep {
		parts = append(parts, fmt.Sprintf("between(t,%s,%s)", ffNum(r.Start), ffNum(r.End)))
	}
	return strings.Join(parts, "+")
}

func ffNum(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }

func rangesTotal(list []timeRange) float64 {
	sum := 0.0
	for _, r := range list {
		sum += r.End - r.Start
	}
	return sum
}

// mediaRemoveSilence 去停顿：两趟 ffmpeg，产物与源同类型；没检测到停顿时原样转封装并带 warning。
func mediaRemoveSilence(ctx context.Context, dir string, src *model.Asset, srcPath string, params map[string]any) ([]mediaProduct, error) {
	threshold := mediaParamFloat(params, "threshold", silenceThresholdDefault)
	if threshold > -10 || threshold < -80 {
		threshold = silenceThresholdDefault
	}
	minSilence := mediaParamFloat(params, "min_silence", silenceMinDefault)
	if minSilence < 0.2 || minSilence > 10 {
		minSilence = silenceMinDefault
	}
	isAudio := src.Type == model.AssetTypeAudio
	total, _, _, hasAudio := ProbeMediaInfo(ctx, srcPath)
	if !hasAudio {
		return nil, errors.New("这段素材没有音轨，没法找停顿")
	}
	if total <= 0 {
		return nil, errors.New("读不到素材时长")
	}
	// pass 1：silencedetect 写在 info 级别日志里，RunFFmpeg 默认 error 级别，这里再压回 info
	detect := fmt.Sprintf("silencedetect=noise=%sdB:d=%s", ffNum(threshold), ffNum(minSilence))
	out, err := RunFFmpeg(ctx, "-loglevel", "info", "-i", srcPath, "-vn", "-af", detect, "-f", "null", "-")
	if err != nil {
		return nil, fmt.Errorf("检测停顿失败：%v", FFmpegError(out, err))
	}
	keep := keepRanges(parseSilenceIntervals(out, total), total, silencePad)
	label := "去停顿"
	ext := mediaExt(src.Type)
	outPath := filepath.Join(dir, "desilence"+ext)
	if len(keep) == 0 || len(keep) > silenceMaxSegments || rangesTotal(keep) >= total-0.05 {
		// 没有可剪的停顿：原样转封装交回去，让用户知道不是没跑
		args := []string{"-y", "-i", srcPath, "-c", "copy", "-movflags", "+faststart", outPath}
		if isAudio {
			args = []string{"-y", "-i", srcPath, "-vn", "-c:a", "aac", "-b:a", "192k", outPath}
		}
		if _, err := runMediaFFmpeg(ctx, outPath, args...); err != nil {
			return nil, fmt.Errorf("去停顿失败：%v", err)
		}
		p := silenceProduct(ctx, outPath, src, label, isAudio)
		p.Warning = "没有检测到可去掉的停顿"
		p.WarningCode = model.MediaWarningNoSilence
		return []mediaProduct{p}, nil
	}
	expr := selectExpr(keep)
	// pass 2：视频与音频各自 select 同一组区间再重排时间戳，两轨才对得上
	args := []string{"-y", "-i", srcPath}
	if isAudio {
		args = append(args, "-vn", "-af", fmt.Sprintf("aselect='%s',asetpts=N/SR/TB", expr), "-c:a", "aac", "-b:a", "192k")
	} else {
		args = append(args,
			"-vf", fmt.Sprintf("select='%s',setpts=N/FRAME_RATE/TB", expr),
			"-af", fmt.Sprintf("aselect='%s',asetpts=N/SR/TB", expr),
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart")
	}
	args = append(args, outPath)
	if _, err := runMediaFFmpeg(ctx, outPath, args...); err != nil {
		return nil, fmt.Errorf("去停顿失败：%v", err)
	}
	return []mediaProduct{silenceProduct(ctx, outPath, src, label, isAudio)}, nil
}

func silenceProduct(ctx context.Context, path string, src *model.Asset, label string, isAudio bool) mediaProduct {
	if isAudio {
		return audioProduct(ctx, path, src, label)
	}
	return videoProduct(ctx, path, src, label)
}
