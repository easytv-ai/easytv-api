package service

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/setting"
)

// Storage 文件存储抽象：本地磁盘先行，换 OSS/R2 时只需新增实现并在 InitStorage 中切换。
type Storage interface {
	// Save 写入文件，返回可访问的 URL。key 形如 "2026/09/08/xxx.png"。
	Save(key string, r io.Reader) (url string, err error)
	// Remove 删除文件（不存在不报错）。
	Remove(key string) error
}

var (
	// DefaultStorage 当前生效的存储实现。
	DefaultStorage Storage
	// UploadDir 本地上传目录（供 main.go 挂静态路由）。
	UploadDir = "./upload"
	// UploadBaseUrl 上传文件对外前缀；为空表示由本服务 /upload/ 提供。
	UploadBaseUrl = ""
	// UploadMaxBytes 单文件上限。
	UploadMaxBytes int64 = 200 * 1024 * 1024
)

// InitStorage 初始化存储（在 common.InitEnv 之后、setting 加载之后调用）。
func InitStorage() error {
	UploadDir = common.GetEnvOrDefaultString("UPLOAD_DIR", UploadDir)
	UploadMaxBytes = int64(common.GetEnvOrDefault("UPLOAD_MAX_MB", 200)) * 1024 * 1024
	if err := os.MkdirAll(UploadDir, 0o755); err != nil {
		return fmt.Errorf("create upload dir failed: %w", err)
	}
	ApplyStorageConfig()
	return nil
}

// ApplyStorageConfig 按当前配置重建 DefaultStorage（启动时与配置热更新后调用）。
// 优先级：OSS（env）> 七牛（DB setting）> 本地磁盘；UploadBaseUrl 来自 setting（回退 env）。
func ApplyStorageConfig() {
	UploadBaseUrl = strings.TrimRight(setting.GetUploadBaseUrl(), "/")
	if oss := NewOSSStorageFromEnv(common.GetEnvOrDefaultString, common.GetEnvOrDefaultBool); oss != nil {
		DefaultStorage = oss
		common.SysLog(fmt.Sprintf("storage: aliyun oss bucket=%s base=%s", oss.Bucket, oss.BaseUrl))
		return
	}
	if setting.IsQiniuEnabled() {
		DefaultStorage = NewQiniuStorage()
		common.SysLog("storage: qiniu cloud " + setting.GetQiniuDomain())
		return
	}
	DefaultStorage = &LocalStorage{Dir: UploadDir, BaseUrl: UploadBaseUrl}
	common.SysLog("storage: local disk " + UploadDir)
}

// LocalStorage 本地磁盘存储。
type LocalStorage struct {
	Dir     string
	BaseUrl string
}

func (s *LocalStorage) Save(key string, r io.Reader) (string, error) {
	if !isSafeKey(key) {
		return "", errors.New("invalid storage key")
	}
	full := filepath.Join(s.Dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(full)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		_ = os.Remove(full)
		return "", err
	}
	if s.BaseUrl != "" {
		return s.BaseUrl + "/" + key, nil
	}
	return "/upload/" + key, nil
}

func (s *LocalStorage) Remove(key string) error {
	if !isSafeKey(key) {
		return errors.New("invalid storage key")
	}
	err := os.Remove(filepath.Join(s.Dir, filepath.FromSlash(key)))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// NewStorageKey 生成按日期分目录的存储 key，保留原扩展名。
func NewStorageKey(filename string) string {
	ext := strings.ToLower(path.Ext(filename))
	if len(ext) > 10 {
		ext = ""
	}
	now := time.Now()
	return fmt.Sprintf("%s/%d%s%s", now.Format("2006/01/02"), now.UnixNano()/1e6, common.GetRandomString(6), ext)
}

// isSafeKey 禁止路径穿越与绝对路径。
func isSafeKey(key string) bool {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") || strings.Contains(key, "\\") {
		return false
	}
	return true
}
