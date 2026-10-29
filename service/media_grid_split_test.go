package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easytv-api/model"
)

// ===== 宫格切分（L4，API.md 23.3 grid_split）=====

func TestParseGridCells(t *testing.T) {
	cell := func(x, y, w, h float64) map[string]any { return map[string]any{"x": x, "y": y, "w": w, "h": h} }
	if _, err := parseGridCells(nil); err == nil {
		t.Error("没给参数应报错")
	}
	if _, err := parseGridCells(map[string]any{"cells": []any{}}); err == nil {
		t.Error("空数组应报错")
	}
	too := make([]any, 26)
	for i := range too {
		too[i] = cell(0, 0, 0.1, 0.1)
	}
	if _, err := parseGridCells(map[string]any{"cells": too}); err == nil || !strings.Contains(err.Error(), "最多") {
		t.Errorf("26 片应被拒：%v", err)
	}
	if _, err := parseGridCells(map[string]any{"cells": []any{cell(0, 0, 0, 0.5)}}); err == nil {
		t.Error("零宽区域应报错")
	}
	if _, err := parseGridCells(map[string]any{"cells": []any{"x"}}); err == nil {
		t.Error("不是对象应报错")
	}
	cells, err := parseGridCells(map[string]any{"cells": []any{cell(0, 0, 0.5, 0.5), cell(0.5, 0.5, 0.5, 0.5)}})
	if err != nil || len(cells) != 2 || cells[1].X != 0.5 {
		t.Fatalf("正常输入解析失败：%v %+v", err, cells)
	}
}

// TestMediaGridSplitFFmpeg 真跑 ffmpeg：320x240 切 2x2 → 四片 160x120，编号从 1 起。
func TestMediaGridSplitFFmpeg(t *testing.T) {
	if !FFmpegAvailable() {
		t.Skip("no ffmpeg")
	}
	ctx := context.Background()
	work := t.TempDir()
	srcPath := filepath.Join(work, "grid.jpg")
	if out, err := RunFFmpeg(ctx, "-y", "-f", "lavfi", "-i", "color=c=blue:s=320x240:d=1", "-frames:v", "1", "-q:v", "2", srcPath); err != nil {
		t.Fatalf("造素材失败：%v %s", err, out)
	}
	src := &model.Asset{Id: 3, Type: model.AssetTypeImage, Name: "九宫格.jpg"}
	cells := []any{}
	for _, r := range [][4]float64{{0, 0, 0.5, 0.5}, {0.5, 0, 0.5, 0.5}, {0, 0.5, 0.5, 0.5}, {0.5, 0.5, 0.5, 0.5}} {
		cells = append(cells, map[string]any{"x": r[0], "y": r[1], "w": r[2], "h": r[3]})
	}
	dir, _ := os.MkdirTemp(work, "out-")
	products, err := runMediaAction(ctx, dir, mustAction(t, "grid_split"), src, srcPath, map[string]any{"cells": cells})
	if err != nil {
		t.Fatalf("grid_split: %v", err)
	}
	if len(products) != 4 {
		t.Fatalf("产物数 = %d，want 4", len(products))
	}
	for i, p := range products {
		if p.Type != model.AssetTypeImage || p.Label != "切片" || !strings.HasPrefix(p.Name, "切片 0") {
			t.Errorf("产物 %d 不对：%+v", i+1, p)
		}
		if _, w, h, _ := ProbeMediaInfo(ctx, p.Path); w != 160 || h != 120 {
			t.Errorf("第 %d 片尺寸 = %dx%d，want 160x120", i+1, w, h)
		}
	}
}
