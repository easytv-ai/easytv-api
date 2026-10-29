// backfill-covers 给没有封面的视频资产与作品补抽帧封面（幂等，可重复跑）。
//
//	go run ./cmd/backfill-covers            # 使用 .env
//	go run ./cmd/backfill-covers --dry-run  # 只统计
package main

import (
	"flag"
	"fmt"
	"os"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "只统计不写入")
	limit := flag.Int("limit", 500, "每类最多处理条数")
	envFile := flag.String("env-file", "", "环境变量文件路径（默认 .env）")
	flag.Parse()
	if *envFile != "" {
		common.EnvFile = *envFile
	}
	if err := common.LoadEnvFile(); err != nil {
		fmt.Println("load env failed:", err)
	}
	common.InitEnv()
	if err := model.InitDB(); err != nil {
		panic(err)
	}
	_ = model.SyncSettings()
	if err := service.InitStorage(); err != nil {
		fmt.Println("init storage failed:", err)
		os.Exit(1)
	}
	fmt.Printf("capabilities: %+v\n", service.CoverCapabilities())

	assets, err := model.GetVideoAssetsWithoutCover(*limit)
	if err != nil {
		panic(err)
	}
	fmt.Printf("video assets without cover: %d\n", len(assets))
	done := 0
	for _, a := range assets {
		if *dryRun {
			fmt.Printf("  asset #%d %s\n", a.Id, a.Url)
			continue
		}
		if cover := service.EnsureAssetCover(a); cover != "" {
			done++
			fmt.Printf("  asset #%d -> %s\n", a.Id, cover)
		} else {
			fmt.Printf("  asset #%d skipped\n", a.Id)
		}
	}

	works, err := model.GetWorksWithoutCover(*limit)
	if err != nil {
		panic(err)
	}
	fmt.Printf("works without cover: %d\n", len(works))
	wdone := 0
	for _, w := range works {
		cover := ""
		if w.AssetId > 0 {
			if a, err := model.GetAssetByIdUnscoped(w.AssetId); err == nil && a != nil {
				cover = a.CoverUrl
				if cover == "" && !*dryRun {
					cover = service.EnsureAssetCover(a)
				}
			}
		}
		if cover == "" && !*dryRun {
			c, err := service.GenerateVideoCover(w.VideoUrl, "")
			if err != nil {
				fmt.Printf("  work #%d failed: %v\n", w.Id, err)
				continue
			}
			cover = c
		}
		if *dryRun {
			fmt.Printf("  work #%d %s (asset cover: %q)\n", w.Id, w.VideoUrl, cover)
			continue
		}
		if cover == "" {
			fmt.Printf("  work #%d skipped\n", w.Id)
			continue
		}
		if err := model.SetWorkCover(w.Id, cover); err != nil {
			fmt.Printf("  work #%d save failed: %v\n", w.Id, err)
			continue
		}
		wdone++
		fmt.Printf("  work #%d -> %s\n", w.Id, cover)
	}
	fmt.Printf("done: assets %d, works %d\n", done, wdone)
}
