package setting

import "strings"

// 七牛云存储配置（DB setting 表，PascalCase 键，管理端 /admin/gateway/config 可热更新）。
// 兼容 New API setting/system_setting/qiniu_setting.go。
const (
	QiniuAccessKey   = "QiniuAccessKey"
	QiniuSecretKey   = "QiniuSecretKey"
	QiniuBucketName  = "QiniuBucketName"
	QiniuProjectCode = "QiniuProjectCode"
	QiniuDomain      = "QiniuDomain"
)

// GetQiniuAccessKey 七牛 AK。
func GetQiniuAccessKey() string { return GetSettingString(QiniuAccessKey) }

// GetQiniuSecretKey 七牛 SK。
func GetQiniuSecretKey() string { return GetSettingString(QiniuSecretKey) }

// GetQiniuBucketName 七牛存储空间名，缺省 easyapi。
func GetQiniuBucketName() string {
	if v := GetSettingString(QiniuBucketName); v != "" {
		return v
	}
	return "easyapi"
}

// GetQiniuProjectCode 七牛 key 前缀（项目码），缺省 easytv。
func GetQiniuProjectCode() string {
	if v := GetSettingString(QiniuProjectCode); v != "" {
		return v
	}
	return "easytv"
}

// GetQiniuDomain 七牛 CDN 对外域名（setting QiniuDomain），启用七牛时必须配置。
func GetQiniuDomain() string {
	return strings.TrimRight(GetSettingString(QiniuDomain), "/")
}

// IsQiniuEnabled 七牛是否启用：AK/SK 都非空即启用（与 New API 一致）。
func IsQiniuEnabled() bool {
	return GetQiniuAccessKey() != "" && GetQiniuSecretKey() != ""
}
