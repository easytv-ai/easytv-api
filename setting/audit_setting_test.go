package setting

import (
	"testing"

	"easytv-api/model"
)

// TestGetAuditLogRetentionDays 缺省 180；显式 0 永久保留；负数 / 非数字回退缺省。
func TestGetAuditLogRetentionDays(t *testing.T) {
	defer model.SetSettingCache(AuditLogRetentionDays, "")
	for raw, want := range map[string]int{"30": 30, "0": 0, "-1": 180, "abc": 180, " 90 ": 90} {
		model.SetSettingCache(AuditLogRetentionDays, raw)
		if got := GetAuditLogRetentionDays(); got != want {
			t.Errorf("%q → %d, want %d", raw, got, want)
		}
	}
}
