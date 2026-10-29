package setting

// 七牛云存储配置（DB setting 表，PascalCase 键，管理端 /admin/gateway/config 可热更新）。
// 镜像母版 easyapi-ai-api setting/system_setting/qiniu_setting.go。
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

// GetQiniuDomain 七牛 CDN 对外域名，缺省 https://qiniu.easyapi.com。
func GetQiniuDomain() string {
	if v := GetSettingString(QiniuDomain); v != "" {
		return v
	}
	return "https://qiniu.easyapi.com"
}

// IsQiniuEnabled 七牛是否启用：AK/SK 都非空即启用（与母版 IsQiniuEnabled 一致）。
func IsQiniuEnabled() bool {
	return GetQiniuAccessKey() != "" && GetQiniuSecretKey() != ""
}
