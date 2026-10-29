package model

import "strings"

const (
	IdentityOfficial = "easytv_official"
	IdentityEnterprise = "enterprise"
	IdentityTeacher = "easytv_teacher"
	IdentityAssistant = "easytv_teaching_assistant"
)

// UserIdentities 管理端可选运营身份。
func UserIdentities() []string {
	return []string{IdentityOfficial, IdentityEnterprise, IdentityTeacher, IdentityAssistant}
}

// NormalizeUserIdentity 清洗身份；空串表示去掉身份。非法值返回 false。
func NormalizeUserIdentity(raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" || v == "__none__" || v == "none" {
		return "", true
	}
	for _, item := range UserIdentities() {
		if item == v {
			return v, true
		}
	}
	return "", false
}

// UserIdentityTags 给管理端列表用：有身份就放进 tags[0]，兼容旧前端。
func UserIdentityTags(identity string) []string {
	if v := strings.TrimSpace(identity); v != "" {
		return []string{v}
	}
	return []string{}
}

// IsMobileTakenByOther 手机号是否被别人占用（含软删，username 或 mobile）。
func IsMobileTakenByOther(mobile string, excludeId int) bool {
	if !IsMobileFormat(mobile) || DB == nil {
		return false
	}
	var n int64
	q := DB.Unscoped().Model(&User{}).Where("username = ? OR mobile = ?", mobile, mobile)
	if excludeId > 0 {
		q = q.Where("id <> ?", excludeId)
	}
	_ = q.Count(&n).Error
	return n > 0
}
