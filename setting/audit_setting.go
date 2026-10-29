package setting

// 审计日志配置（DB setting 表，管理端「系统设置 → 基础」可热更新）。
const (
	// AuditLogRetentionDays 审计日志保留天数，默认 180；0 表示永久保留（不清理）。
	AuditLogRetentionDays = "AuditLogRetentionDays"
)

// defaultAuditLogRetentionDays 未配置或配置非法（负数、非数字）时的保留天数。
const defaultAuditLogRetentionDays = 180

// GetAuditLogRetentionDays 读取审计日志保留天数：空 / 非法回退 180，显式 0 表示永久保留。
func GetAuditLogRetentionDays() int {
	return getIntSetting(AuditLogRetentionDays, defaultAuditLogRetentionDays)
}
