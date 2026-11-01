package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/setting"

	"github.com/qiniu/go-sdk/v7/auth/qbox"
	"github.com/qiniu/go-sdk/v7/storage"
)

// 七牛云存储封装，兼容 New API service/qiniu.go。
// 配置在 DB setting 表（setting/qiniu_setting.go），管理端 /admin/gateway/config 可热更新。

// GenerateQiniuKey 生成七牛云存储 key：<项目码>/<日期>/<毫秒时间戳><后缀>。
func GenerateQiniuKey(suffix string) string {
	dateStr := time.Now().Format("2006/01/02")
	return fmt.Sprintf("%s/%s/%d%s", setting.GetQiniuProjectCode(), dateStr, time.Now().UnixMilli(), suffix)
}

// GenerateQiniuUploadToken 获取上传 token；key 非空时 Scope 限定为单文件（bucket:key）。
func GenerateQiniuUploadToken(bucket, key string) string {
	mac := qbox.NewMac(setting.GetQiniuAccessKey(), setting.GetQiniuSecretKey())
	putPolicy := storage.PutPolicy{
		Scope: bucket,
	}
	if key != "" {
		// <bucket>:<key>，只允许上传指定 key 的文件；默认允许覆盖同名资源。
		putPolicy.Scope = fmt.Sprintf("%s:%s", bucket, key)
	}
	putPolicy.Expires = 3600 // 1 小时有效
	return putPolicy.UploadToken(mac)
}

// UploadDataToQiniu 上传文件数据到七牛云，上传绑定 ctx（可限制整体超时）；失败返回空串。
func UploadDataToQiniu(ctx context.Context, bucket string, data []byte, key string) string {
	mac := qbox.NewMac(setting.GetQiniuAccessKey(), setting.GetQiniuSecretKey())
	cfg := storage.Config{UseHTTPS: true}
	formUploader := storage.NewFormUploader(&cfg)
	ret := storage.PutRet{}
	putPolicy := storage.PutPolicy{Scope: fmt.Sprintf("%s:%s", bucket, key)}
	upToken := putPolicy.UploadToken(mac)

	dataLen := int64(len(data))
	err := formUploader.Put(ctx, &ret, upToken, key, bytes.NewReader(data), dataLen, nil)
	if err != nil {
		common.SysError(fmt.Sprintf("UploadDataToQiniu error: %v", err))
		return ""
	}
	return ret.Key
}

// BuildQiniuURL 根据 key 构建完整 CDN URL。
func BuildQiniuURL(key string) string {
	return strings.TrimRight(setting.GetQiniuDomain(), "/") + "/" + key
}

// DeleteQiniuFile 删除七牛文件（不存在不报错）。
func DeleteQiniuFile(bucket, key string) error {
	mac := qbox.NewMac(setting.GetQiniuAccessKey(), setting.GetQiniuSecretKey())
	cfg := storage.Config{UseHTTPS: true}
	bucketManager := storage.NewBucketManager(mac, &cfg)
	if err := bucketManager.Delete(bucket, key); err != nil {
		common.SysError(fmt.Sprintf("DeleteQiniuFile error: %v", err))
		return err
	}
	return nil
}

// uploadBytes 封装 UploadDataToQiniu 的字节流转存（storage.Save 需要 io.Reader）。
func uploadBytesToQiniu(ctx context.Context, key string, r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	bucket := setting.GetQiniuBucketName()
	retKey := UploadDataToQiniu(ctx, bucket, data, key)
	if retKey == "" {
		return "", fmt.Errorf("qiniu upload failed: %s", key)
	}
	return BuildQiniuURL(key), nil
}
