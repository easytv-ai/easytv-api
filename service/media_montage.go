package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"easytv-api/common"
	"easytv-api/model"
)

// 分镜组拼接（L4，API.md 23.3 montage）：把多张图按行列拼成一张宫格大图，本地 ffmpeg，免费。
//
// 作业壳只有一个 source_asset_id，多图走 params.asset_ids（有序，含源素材本身）；
// 归属在创建时逐张校验，worker 里只按 id 取。拼法：每张先缩放并补边到同一格子尺寸
// → concat 成 N 帧 → tile 铺成 C×R（最后一行不满自动补背景色）。
// 用 tile 而不是 xstack：xstack 要求每个位置都有输入，行不满时得自己造空图；tile 顺手就处理了。

const (
	montageMin     = 2
	montageMax     = 25
	montageColsMax = 10
	montageGapMax  = 64
	// 单格宽：默认 512，够看清也不至于 25 格拼出一张几十兆的图
	montageCellDefault = 512
	montageCellMin     = 256
	montageCellMax     = 1024
	montageBgDefault   = "white"
)

// montageRatios 单格画幅比白名单；original 按第一张图的比例
var montageRatios = []string{"original", "1:1", "16:9", "9:16", "4:3", "3:4"}

type montageSpec struct {
	AssetIds []int
	Cols     int
	Ratio    string
	Gap      int
	Number   bool
	Cell     int
	Bg       string
}

// parseMontageParams 只管参数形状与范围，不查库。
func parseMontageParams(params map[string]any) (montageSpec, error) {
	spec := montageSpec{
		Cols:   0,
		Ratio:  "original",
		Gap:    8,
		Number: false,
		Cell:   montageCellDefault,
		Bg:     montageBgDefault,
	}
	if params == nil {
		return spec, errors.New("请指定要拼接的素材")
	}
	ids := mediaParamInts(params, "asset_ids")
	if len(ids) < montageMin {
		return spec, fmt.Errorf("至少选 %d 张图", montageMin)
	}
	if len(ids) > montageMax {
		return spec, fmt.Errorf("一次最多拼 %d 张", montageMax)
	}
	seen := map[int]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return spec, errors.New("素材列表里有重复或非法的 id")
		}
		seen[id] = true
	}
	spec.AssetIds = ids

	if cols := int(mediaParamFloat(params, "cols", 0)); cols > 0 {
		if cols > montageColsMax {
			return spec, fmt.Errorf("每行最多 %d 张", montageColsMax)
		}
		spec.Cols = cols
	}
	if spec.Cols == 0 {
		spec.Cols = montageDefaultCols(len(ids))
	}
	if ratio := strings.ToLower(mediaParamStr(params, "ratio")); ratio != "" {
		if !containsString(montageRatios, ratio) {
			return spec, errors.New("画幅比只能是 " + strings.Join(montageRatios, " / "))
		}
		spec.Ratio = ratio
	}
	if raw, ok := params["gap"]; ok && raw != nil {
		gap := int(mediaParamFloat(params, "gap", 8))
		if gap < 0 || gap > montageGapMax {
			return spec, fmt.Errorf("间距要在 0~%d 像素之间", montageGapMax)
		}
		spec.Gap = gap
	}
	spec.Number = mediaParamBool(params, "number", false)
	if cell := int(mediaParamFloat(params, "cell", 0)); cell > 0 {
		if cell < montageCellMin || cell > montageCellMax {
			return spec, fmt.Errorf("单格宽度要在 %d~%d 像素之间", montageCellMin, montageCellMax)
		}
		spec.Cell = cell
	}
	if bg := mediaParamStr(params, "bg"); bg != "" {
		if !montageColorOK(bg) {
			return spec, errors.New("背景色只认 white / black 或 #rrggbb")
		}
		spec.Bg = bg
	}
	return spec, nil
}

// montageDefaultCols 没指定列数时取接近正方形的排法：4 张 2 列、9 张 3 列、10 张 4 列。
func montageDefaultCols(n int) int {
	cols := 1
	for cols*cols < n {
		cols++
	}
	if cols > montageColsMax {
		cols = montageColsMax
	}
	return cols
}

func montageRows(n, cols int) int {
	if cols <= 0 {
		return n
	}
	return (n + cols - 1) / cols
}

// montageColorOK ffmpeg 颜色写法只放行最常见的两种，别让任意串进 filter 表达式。
func montageColorOK(c string) bool {
	c = strings.ToLower(c)
	if c == "white" || c == "black" {
		return true
	}
	if len(c) != 7 || c[0] != '#' {
		return false
	}
	_, err := strconv.ParseUint(c[1:], 16, 32)
	return err == nil
}

// montageCellSize 单格尺寸：宽取 spec.Cell，高按画幅比算，都截到偶数（yuv420p 不吃奇数）。
func montageCellSize(spec montageSpec, firstW, firstH int) (int, int) {
	w := spec.Cell
	rw, rh := 1, 1
	if spec.Ratio == "original" {
		if firstW > 0 && firstH > 0 {
			rw, rh = firstW, firstH
		}
	} else {
		parts := strings.SplitN(spec.Ratio, ":", 2)
		if len(parts) == 2 {
			a, errA := strconv.Atoi(parts[0])
			b, errB := strconv.Atoi(parts[1])
			if errA == nil && errB == nil && a > 0 && b > 0 {
				rw, rh = a, b
			}
		}
	}
	h := w * rh / rw
	if h < 2 {
		h = 2
	}
	return w &^ 1, h &^ 1
}

// montageFilter 拼 filter_complex。fontFile 为空时不加序号（drawtext 不可用或没找到字体）。
func montageFilter(n, cellW, cellH int, spec montageSpec, fontFile string) string {
	var b strings.Builder
	labels := make([]string, 0, n)
	fontSize := cellH / 9
	if fontSize < 14 {
		fontSize = 14
	}
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease:flags=lanczos,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=%s,setsar=1,format=yuv420p",
			i, cellW, cellH, cellW, cellH, spec.Bg)
		if spec.Number && fontFile != "" {
			fmt.Fprintf(&b, ",drawtext=fontfile='%s':text='%d':x=%d:y=%d:fontsize=%d:fontcolor=white:box=1:boxcolor=black@0.55:boxborderw=%d",
				montageEscape(fontFile), i+1, cellH/24+4, cellH/24+4, fontSize, fontSize/3)
		}
		fmt.Fprintf(&b, "[c%d];", i)
		labels = append(labels, fmt.Sprintf("[c%d]", i))
	}
	rows := montageRows(n, spec.Cols)
	fmt.Fprintf(&b, "%sconcat=n=%d:v=1:a=0[s];[s]tile=%dx%d:padding=%d:color=%s[out]",
		strings.Join(labels, ""), n, spec.Cols, rows, spec.Gap, spec.Bg)
	return b.String()
}

// montageEscape 字体路径进 filter 表达式：反斜杠、冒号、单引号都要转义。
func montageEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `:`, `\:`, `'`, `\'`)
	return r.Replace(s)
}

// montageOutputSize 拼出来的整图尺寸（给资产记宽高）。
func montageOutputSize(n, cellW, cellH int, spec montageSpec) (int, int) {
	rows := montageRows(n, spec.Cols)
	return spec.Cols*cellW + (spec.Cols-1)*spec.Gap, rows*cellH + (rows-1)*spec.Gap
}

// validateMontageAssets 创建时逐张校验归属与类型，返回按 params 顺序排好的资产。
func validateMontageAssets(userId, teamId int, src *model.Asset, spec montageSpec) ([]*model.Asset, error) {
	out := make([]*model.Asset, 0, len(spec.AssetIds))
	hasSrc := false
	for _, id := range spec.AssetIds {
		a, err := ResolveOwnedAsset(userId, teamId, id)
		if err != nil {
			return nil, fmt.Errorf("素材 %d 不存在或不属于你", id)
		}
		if a.Type != model.AssetTypeImage {
			return nil, fmt.Errorf("「%s」不是图片，分镜组只能拼图片", a.Name)
		}
		if a.Id == src.Id {
			hasSrc = true
		}
		out = append(out, a)
	}
	if !hasSrc {
		return nil, errors.New("asset_id 必须是 asset_ids 里的一张")
	}
	return out, nil
}

// mediaMontage worker 里真跑：逐张落地 → 拼滤镜 → 一帧 jpg。
func mediaMontage(ctx context.Context, dir string, src *model.Asset, srcPath string, params map[string]any) ([]mediaProduct, error) {
	spec, err := parseMontageParams(params)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(spec.AssetIds))
	var first *model.Asset
	for i, id := range spec.AssetIds {
		a, err := model.GetAssetByIdUnscoped(id)
		if err != nil || a.DeletedAt.Valid {
			return nil, fmt.Errorf("第 %d 张素材已不存在", i+1)
		}
		if first == nil {
			first = a
		}
		p := srcPath
		if a.Id != src.Id {
			p, err = mediaSourceFileAs(ctx, dir, a, fmt.Sprintf("in-%d", i))
			if err != nil {
				return nil, fmt.Errorf("读取第 %d 张素材失败：%v", i+1, err)
			}
		}
		paths = append(paths, p)
	}
	return mediaMontageFiles(ctx, dir, src, paths, spec, first.Width, first.Height)
}

// mediaMontageFiles 拿到本地文件之后的部分：不查库，真跑 ffmpeg 的单测直接进这里。
func mediaMontageFiles(ctx context.Context, dir string, src *model.Asset, paths []string, spec montageSpec, firstW, firstH int) ([]mediaProduct, error) {
	if len(paths) < montageMin {
		return nil, fmt.Errorf("至少选 %d 张图", montageMin)
	}
	if spec.Ratio == "original" && (firstW <= 0 || firstH <= 0) {
		_, firstW, firstH, _ = ProbeMediaInfo(ctx, paths[0])
	}
	cellW, cellH := montageCellSize(spec, firstW, firstH)

	fontFile := ""
	if spec.Number {
		fontFile = montageFontFile()
		if fontFile == "" {
			common.SysLog("media montage: 没找到可用字体或 drawtext 滤镜，序号已跳过")
		}
	}
	args := []string{"-y"}
	for _, p := range paths {
		args = append(args, "-i", p)
	}
	out := filepath.Join(dir, "montage.jpg")
	args = append(args, "-filter_complex", montageFilter(len(paths), cellW, cellH, spec, fontFile),
		"-map", "[out]", "-frames:v", "1", "-q:v", "2", out)
	if _, err := runMediaFFmpeg(ctx, out, args...); err != nil {
		return nil, fmt.Errorf("拼接失败：%v", err)
	}
	product := imageProduct(out, src, "分镜组")
	product.Width, product.Height = montageOutputSize(len(paths), cellW, cellH, spec)
	return []mediaProduct{product}, nil
}

// mediaSourceFileAs 与 mediaSourceFile 一样把素材落到本地，但文件名可指定——多图作业不能都叫 source。
func mediaSourceFileAs(ctx context.Context, dir string, src *model.Asset, name string) (string, error) {
	if local := localVideoPath(src.StorageKey); local != "" {
		return local, nil
	}
	if !isFetchableUrl(src.Url) {
		return "", errors.New("素材地址不可用")
	}
	dst := filepath.Join(dir, name+extOf(src.Url, ".jpg"))
	if err := DownloadTo(ctx, src.Url, dst, MediaSourceMaxByte); err != nil {
		return "", err
	}
	return dst, nil
}

// mediaParamInts 读整数数组参数：JSON 数字、字符串数字、逗号分隔串都认。
func mediaParamInts(params map[string]any, key string) []int {
	if params == nil {
		return nil
	}
	out := []int{}
	push := func(v any) {
		switch x := v.(type) {
		case float64:
			out = append(out, int(x))
		case int:
			out = append(out, x)
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
				out = append(out, n)
			}
		}
	}
	switch v := params[key].(type) {
	case []any:
		for _, item := range v {
			push(item)
		}
	case []int:
		out = append(out, v...)
	case string:
		for _, part := range strings.Split(v, ",") {
			push(part)
		}
	}
	return out
}

// ===== 序号用的字体 =====

var (
	montageFontOnce sync.Once
	montageFontPath string
)

// montageFontCandidates 常见系统字体；只画数字，任何一款都行。
var montageFontCandidates = []string{
	"/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
	"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	"/usr/share/fonts/opentype/noto/NotoSansCJK-Bold.ttc",
	"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	"/usr/share/fonts/truetype/liberation/LiberationSans-Bold.ttf",
	"/usr/share/fonts/wqy-microhei/wqy-microhei.ttc",
	"/System/Library/Fonts/Helvetica.ttc",
	"/System/Library/Fonts/Supplemental/Arial Bold.ttf",
	"/Library/Fonts/Arial.ttf",
	"C:/Windows/Fonts/arialbd.ttf",
	"C:/Windows/Fonts/arial.ttf",
}

// montageFontFile 找一款能用的字体；ffmpeg 没编 drawtext 时返回空串。只探测一次。
func montageFontFile() string {
	montageFontOnce.Do(func() {
		if !ffmpegHasFilter("drawtext") {
			return
		}
		for _, p := range montageFontCandidates {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				montageFontPath = p
				return
			}
		}
	})
	return montageFontPath
}

func ffmpegHasFilter(name string) bool {
	out, err := RunFFmpeg(context.Background(), "-hide_banner", "-filters")
	if err != nil && out == "" {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == name {
			return true
		}
	}
	return false
}
