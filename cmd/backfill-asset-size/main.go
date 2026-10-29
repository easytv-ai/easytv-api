// backfill-asset-size 给 size=0 的资产补字节数（幂等，可重复跑）。
// 网关生成的图片 / 视频 / 音频早期入库没写 Size，云存储配额按 size 求和，会一直偏小。
// 本地存储读文件大小，绝对地址发 HEAD 取 Content-Length。
//
//	go run ./cmd/backfill-asset-size            # 使用 .env
//	go run ./cmd/backfill-asset-size --dry-run  # 只统计
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "只统计不写入")
	limit := flag.Int("limit", 5000, "最多处理条数")
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

	client := &http.Client{Timeout: 20 * time.Second}
	lastId, seen, done := 0, 0, 0
	for seen < *limit {
		assets, err := model.GetAssetsWithoutSize(lastId, 200)
		if err != nil {
			panic(err)
		}
		if len(assets) == 0 {
			break
		}
		for _, a := range assets {
			lastId = a.Id
			seen++
			size, err := sizeOf(client, a)
			if err != nil {
				fmt.Printf("  asset #%d skipped: %v\n", a.Id, err)
				continue
			}
			if *dryRun {
				fmt.Printf("  asset #%d %d bytes\n", a.Id, size)
				continue
			}
			if err := model.SetAssetSize(a.Id, size); err != nil {
				fmt.Printf("  asset #%d save failed: %v\n", a.Id, err)
				continue
			}
			done++
		}
	}
	fmt.Printf("scanned %d, updated %d\n", seen, done)
}

func sizeOf(client *http.Client, a *model.Asset) (int64, error) {
	if strings.HasPrefix(a.Url, "/upload/") {
		fi, err := os.Stat(filepath.Join(service.UploadDir, filepath.FromSlash(strings.TrimPrefix(a.Url, "/upload/"))))
		if err != nil {
			return 0, err
		}
		return fi.Size(), nil
	}
	if !strings.HasPrefix(a.Url, "http") {
		return 0, fmt.Errorf("不支持的地址 %s", a.Url)
	}
	res, err := client.Head(a.Url)
	if err != nil {
		return 0, err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || res.ContentLength <= 0 {
		return 0, fmt.Errorf("HEAD %d, Content-Length %d", res.StatusCode, res.ContentLength)
	}
	return res.ContentLength, nil
}
