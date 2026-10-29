package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easytv-api/model"
)

// ===== 分镜组拼接（L4，API.md 23.3 montage）=====

func TestParseMontageParams(t *testing.T) {
	ids := func(n int) []any {
		out := make([]any, 0, n)
		for i := 1; i <= n; i++ {
			out = append(out, float64(i))
		}
		return out
	}
	t.Run("缺省值", func(t *testing.T) {
		spec, err := parseMontageParams(map[string]any{"asset_ids": ids(9)})
		if err != nil {
			t.Fatal(err)
		}
		if spec.Cols != 3 || spec.Ratio != "original" || spec.Gap != 8 || spec.Number || spec.Cell != montageCellDefault || spec.Bg != "white" {
			t.Fatalf("缺省值不对：%+v", spec)
		}
	})
	t.Run("张数与重复", func(t *testing.T) {
		if _, err := parseMontageParams(nil); err == nil {
			t.Error("没给参数应报错")
		}
		if _, err := parseMontageParams(map[string]any{"asset_ids": ids(1)}); err == nil || !strings.Contains(err.Error(), "至少") {
			t.Errorf("1 张应被拒：%v", err)
		}
		if _, err := parseMontageParams(map[string]any{"asset_ids": ids(26)}); err == nil || !strings.Contains(err.Error(), "最多") {
			t.Errorf("26 张应被拒：%v", err)
		}
		if _, err := parseMontageParams(map[string]any{"asset_ids": []any{1.0, 1.0}}); err == nil {
			t.Error("重复 id 应被拒")
		}
	})
	t.Run("范围校验", func(t *testing.T) {
		base := func() map[string]any { return map[string]any{"asset_ids": ids(4)} }
		bad := []map[string]any{
			{"cols": 11.0}, {"ratio": "2:1"}, {"gap": 65.0}, {"gap": -1.0}, {"cell": 100.0}, {"cell": 4096.0}, {"bg": "red; rm -rf"},
		}
		for _, extra := range bad {
			p := base()
			for k, v := range extra {
				p[k] = v
			}
			if _, err := parseMontageParams(p); err == nil {
				t.Errorf("%v 应被拒", extra)
			}
		}
		p := base()
		p["cols"], p["ratio"], p["gap"], p["number"], p["cell"], p["bg"] = 4.0, "16:9", 0.0, true, 800.0, "#112233"
		spec, err := parseMontageParams(p)
		if err != nil {
			t.Fatal(err)
		}
		if spec.Cols != 4 || spec.Ratio != "16:9" || spec.Gap != 0 || !spec.Number || spec.Cell != 800 || spec.Bg != "#112233" {
			t.Fatalf("参数没读进来：%+v", spec)
		}
	})
	t.Run("字符串形式的 id 列表", func(t *testing.T) {
		spec, err := parseMontageParams(map[string]any{"asset_ids": "3, 5,7"})
		if err != nil || len(spec.AssetIds) != 3 || spec.AssetIds[2] != 7 {
			t.Fatalf("逗号串没解析：%+v %v", spec.AssetIds, err)
		}
	})
}

func TestMontageGeometry(t *testing.T) {
	for n, want := range map[int]int{2: 2, 4: 2, 5: 3, 9: 3, 10: 4, 16: 4, 25: 5} {
		if got := montageDefaultCols(n); got != want {
			t.Errorf("%d 张默认列数 = %d，want %d", n, got, want)
		}
	}
	if montageRows(5, 3) != 2 || montageRows(9, 3) != 3 || montageRows(10, 4) != 3 {
		t.Error("行数算错")
	}
	// original 按第一张的比例；显式比例按比例；都截到偶数
	if w, h := montageCellSize(montageSpec{Cell: 512, Ratio: "original"}, 1920, 1080); w != 512 || h != 288 {
		t.Errorf("original 单格 = %dx%d", w, h)
	}
	if w, h := montageCellSize(montageSpec{Cell: 511, Ratio: "9:16"}, 0, 0); w != 510 || h != 908 {
		t.Errorf("9:16 单格 = %dx%d", w, h)
	}
	if w, h := montageCellSize(montageSpec{Cell: 512, Ratio: "original"}, 0, 0); w != 512 || h != 512 {
		t.Errorf("没有尺寸信息时应退成方形：%dx%d", w, h)
	}
	spec := montageSpec{Cols: 3, Gap: 4}
	if w, h := montageOutputSize(5, 256, 144, spec); w != 3*256+8 || h != 2*144+4 {
		t.Errorf("整图尺寸 = %dx%d", w, h)
	}
}

func TestMontageFilter(t *testing.T) {
	spec := montageSpec{Cols: 3, Gap: 6, Bg: "white", Number: true}
	f := montageFilter(5, 256, 144, spec, "")
	if strings.Count(f, "scale=256:144:force_original_aspect_ratio=decrease") != 5 {
		t.Errorf("每张都要缩放补边：%s", f)
	}
	if !strings.Contains(f, "concat=n=5:v=1:a=0[s];[s]tile=3x2:padding=6:color=white[out]") {
		t.Errorf("concat/tile 不对：%s", f)
	}
	if strings.Contains(f, "drawtext") {
		t.Error("没有字体时不该加序号")
	}
	f = montageFilter(2, 256, 256, spec, `/fonts/My Font:v1.ttf`)
	if strings.Count(f, "drawtext=fontfile='/fonts/My Font\\:v1.ttf':text='") != 2 {
		t.Errorf("序号与字体路径转义不对：%s", f)
	}
	if !strings.Contains(f, "text='1'") || !strings.Contains(f, "text='2'") {
		t.Errorf("序号要从 1 起：%s", f)
	}
}

func TestMediaParamInts(t *testing.T) {
	if got := mediaParamInts(map[string]any{"k": []any{1.0, "2", " 3 ", "x"}}, "k"); fmt.Sprint(got) != "[1 2 3]" {
		t.Errorf("混合数组 = %v", got)
	}
	if got := mediaParamInts(map[string]any{"k": "4,5"}, "k"); fmt.Sprint(got) != "[4 5]" {
		t.Errorf("逗号串 = %v", got)
	}
	if got := mediaParamInts(nil, "k"); len(got) != 0 {
		t.Errorf("nil = %v", got)
	}
}

// TestMediaMontageFFmpeg 真跑 ffmpeg：5 张 320x240 的纯色图拼 3 列，最后一行不满由背景补齐。
func TestMediaMontageFFmpeg(t *testing.T) {
	if !FFmpegAvailable() {
		t.Skip("no ffmpeg")
	}
	ctx := context.Background()
	work := t.TempDir()
	paths := []string{}
	for i, c := range []string{"red", "green", "blue", "yellow", "gray"} {
		p := filepath.Join(work, fmt.Sprintf("in-%d.jpg", i))
		if out, err := RunFFmpeg(ctx, "-y", "-f", "lavfi", "-i", "color=c="+c+":s=320x240:d=1", "-frames:v", "1", "-q:v", "2", p); err != nil {
			t.Fatalf("造素材失败：%v %s", err, out)
		}
		paths = append(paths, p)
	}
	src := &model.Asset{Id: 1, Type: model.AssetTypeImage, Name: "分镜 1.jpg"}
	spec := montageSpec{AssetIds: []int{1, 2, 3, 4, 5}, Cols: 3, Ratio: "1:1", Gap: 4, Cell: 256, Bg: "white", Number: true}
	dir, _ := os.MkdirTemp(work, "out-")
	products, err := mediaMontageFiles(ctx, dir, src, paths, spec, 320, 240)
	if err != nil {
		t.Fatalf("montage: %v", err)
	}
	if len(products) != 1 || products[0].Type != model.AssetTypeImage || products[0].Label != "分镜组" {
		t.Fatalf("产物不对：%+v", products)
	}
	wantW, wantH := 3*256+2*4, 2*256+4
	if products[0].Width != wantW || products[0].Height != wantH {
		t.Errorf("记录的尺寸 = %dx%d，want %dx%d", products[0].Width, products[0].Height, wantW, wantH)
	}
	if _, w, h, _ := ProbeMediaInfo(ctx, products[0].Path); w != wantW || h != wantH {
		t.Errorf("实际拼出 = %dx%d，want %dx%d", w, h, wantW, wantH)
	}
	// original 比例按第一张（4:3）：单格 256x192，两张一行
	spec.Ratio, spec.Number, spec.Cols = "original", false, 2
	products, err = mediaMontageFiles(ctx, os.TempDir(), src, paths[:2], spec, 320, 240)
	if err != nil {
		t.Fatalf("montage original: %v", err)
	}
	if _, w, h, _ := ProbeMediaInfo(ctx, products[0].Path); w != 2*256+4 || h != 192 {
		t.Errorf("original 两张 = %dx%d，want %dx192", w, h, 2*256+4)
	}
	_ = os.Remove(products[0].Path)
}
