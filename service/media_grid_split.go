package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"easytv-api/model"
)

// 宫格切分（L4，API.md 23.3 grid_split）：一张宫格图按前端拖好的分割线切成 N 片，一次作业出 N 个产物。
// 不在浏览器里裁：OSS 图片没有 CORS 头，canvas 读不到像素；也不拆成 N 个 crop 作业：
// 单用户排队上限 3 个，九宫格就会被挡。

const (
	gridSplitMax = 25
)

// parseGridCells 读 params.cells：归一化矩形数组，1~25 个，每个都要合法。
func parseGridCells(params map[string]any) ([]mediaRect, error) {
	if params == nil {
		return nil, errors.New("请先划好分割线")
	}
	raw, ok := params["cells"].([]any)
	if !ok || len(raw) == 0 {
		return nil, errors.New("请先划好分割线")
	}
	if len(raw) > gridSplitMax {
		return nil, fmt.Errorf("一次最多切 %d 片", gridSplitMax)
	}
	cells := make([]mediaRect, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("第 %d 片的区域不是对象", i+1)
		}
		r := mediaRect{X: mediaParamFloat(m, "x", 0), Y: mediaParamFloat(m, "y", 0), W: mediaParamFloat(m, "w", 0), H: mediaParamFloat(m, "h", 0)}
		if !r.Valid() {
			return nil, fmt.Errorf("第 %d 片的区域不合法", i+1)
		}
		cells = append(cells, r)
	}
	return cells, nil
}

// mediaGridSplit 逐片裁：与画面裁切同一条 crop 滤镜，宽高截到偶数。
func mediaGridSplit(ctx context.Context, dir string, src *model.Asset, srcPath string, params map[string]any) ([]mediaProduct, error) {
	cells, err := parseGridCells(params)
	if err != nil {
		return nil, err
	}
	scale, err := mediaScaleFilter(mediaParamStr(params, "size"))
	if err != nil {
		return nil, err
	}
	products := make([]mediaProduct, 0, len(cells))
	for i, cell := range cells {
		vf := fmt.Sprintf("crop='trunc(iw*%.6f/2)*2':'trunc(ih*%.6f/2)*2':'trunc(iw*%.6f/2)*2':'trunc(ih*%.6f/2)*2'",
			cell.W, cell.H, cell.X, cell.Y)
		if scale != "" {
			vf += "," + scale
		}
		out := filepath.Join(dir, fmt.Sprintf("slice-%02d.jpg", i+1))
		if _, err := runMediaFFmpeg(ctx, out, "-y", "-i", srcPath, "-vf", vf, "-frames:v", "1", "-q:v", "2", out); err != nil {
			return nil, fmt.Errorf("第 %d 片裁切失败：%v", i+1, err)
		}
		p := imageProduct(out, src, "切片")
		p.Name = fmt.Sprintf("切片 %02d · %s", i+1, truncate(src.Name, 30))
		// 切片真实尺寸随产物下发，前端按它定节点比例（不然落组后只能按默认框摆）
		_, p.Width, p.Height, _ = ProbeMediaInfo(ctx, out)
		products = append(products, p)
	}
	return products, nil
}
