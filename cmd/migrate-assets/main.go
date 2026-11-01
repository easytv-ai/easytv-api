// migrate-assets 把存量本地素材（upload 目录 + asset 表中 url = /upload/... 的行）迁移到阿里云 OSS，
// 并回填 asset.url / storage_key。
//
// 用法：
//
//	go run ./cmd/migrate-assets            # 使用 .env（须已配置 OSS_BUCKET 等）
//	go run ./cmd/migrate-assets --env-file .env.cn
//
// 幂等：url 已为 http(s) 的行跳过；上传失败不中断，最后以退出码与统计给出结论。
// 注意：先确认 OSS_BUCKET/OSS_BASE_URL 可访问（私有桶走 CDN 回源鉴权），再执行。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
)

const batchSize = 500

func main() {
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

	oss := service.NewOSSStorageFromEnv(common.GetEnvOrDefaultString, common.GetEnvOrDefaultBool)
	if oss == nil {
		fmt.Println("OSS 未配置（OSS_BUCKET 为空），无法执行迁移")
		os.Exit(1)
	}
	if oss.KeyId == "" || oss.KeySecret == "" {
		fmt.Println("OSS_ACCESS_KEY_ID / OSS_ACCESS_KEY_SECRET 未配置")
		os.Exit(1)
	}
	uploadDir := common.GetEnvOrDefaultString("UPLOAD_DIR", "./upload")

	start := time.Now()
	var migrated, skipped, failed int
	offset := 0
	for {
		var rows []*model.Asset
		if err := model.DB.Where("url LIKE ?", "/upload/%").Order("id asc").Limit(batchSize).Offset(offset).Find(&rows).Error; err != nil {
			fmt.Printf("查询 asset 失败: %v\n", err)
			os.Exit(1)
		}
		if len(rows) == 0 {
			break
		}
		for _, a := range rows {
			key := strings.TrimSpace(a.StorageKey)
			if key == "" {
				key = strings.TrimPrefix(a.Url, "/upload/")
			}
			if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") || strings.Contains(key, "\\") {
				failed++
				fmt.Printf("[跳过-异常key] asset#%d url=%s\n", a.Id, a.Url)
				continue
			}
			full := filepath.Join(uploadDir, filepath.FromSlash(key))
			f, err := os.Open(full)
			if err != nil {
				// 本地文件已不存在（可能已清理）：仅提示，不中断
				skipped++
				fmt.Printf("[跳过-无文件] asset#%d key=%s\n", a.Id, key)
				continue
			}
			url, err := oss.Save(key, f)
			f.Close()
			if err != nil {
				failed++
				fmt.Printf("[失败] asset#%d key=%s: %v\n", a.Id, key, err)
				continue
			}
			if err := model.DB.Model(a).Updates(map[string]any{
				"url":         url,
				"storage_key": key,
			}).Error; err != nil {
				failed++
				fmt.Printf("[失败] asset#%d 回填失败: %v\n", a.Id, err)
				continue
			}
			migrated++
		}
		offset += len(rows)
		if len(rows) < batchSize {
			break
		}
	}

	fmt.Printf("migrate done: migrated=%d skipped=%d failed=%d elapsed=%s\n", migrated, skipped, failed, time.Since(start).Round(time.Millisecond))
	if failed > 0 {
		os.Exit(1)
	}
}
