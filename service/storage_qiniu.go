package service

import (
	"context"
	"errors"
	"io"
	"time"

	"easytv-api/setting"
)

// QiniuStorage 七牛云存储（Storage 接口实现）：配置中 AK/SK 非空即启用（setting.IsQiniuEnabled）。
type QiniuStorage struct{}

// NewQiniuStorage 构造七牛存储（配置从 setting 实时读取）。
func NewQiniuStorage() *QiniuStorage { return &QiniuStorage{} }

// Save 上传到七牛，返回 CDN URL（Domain + key）。
func (s *QiniuStorage) Save(key string, r io.Reader) (string, error) {
	if !isSafeKey(key) {
		return "", errors.New("invalid storage key")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	return uploadBytesToQiniu(ctx, key, r)
}

// Remove 删除七牛文件（不存在视为成功）。
func (s *QiniuStorage) Remove(key string) error {
	if !isSafeKey(key) {
		return errors.New("invalid storage key")
	}
	return DeleteQiniuFile(setting.GetQiniuBucketName(), key)
}