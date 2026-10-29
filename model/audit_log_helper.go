package model

import (
	"strings"
	"sync"
	"time"
)

// ===== 脱敏 =====

// MaskMobile 手机号脱敏（13812341234 → 138****1234）；非 11 位原样返回。
func MaskMobile(mobile string) string {
	if len(mobile) != 11 {
		return mobile
	}
	return mobile[:3] + "****" + mobile[7:]
}

// MaskEmail 邮箱脱敏（alice@example.com → a***@example.com）；无 @ 时按普通账号脱敏。
func MaskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return maskPlain(email)
	}
	local, domain := []rune(email[:at]), email[at+1:]
	if len(local) == 0 {
		return "***@" + domain
	}
	return string(local[:1]) + "***@" + domain
}

// MaskAccount 登录账号脱敏：手机号 / 邮箱 / 其他用户名分别处理，超长输入先截断（登录失败时账号来自请求体，不可信）。
func MaskAccount(account string) string {
	account = strings.TrimSpace(account)
	if r := []rune(account); len(r) > 64 {
		account = string(r[:64])
	}
	switch {
	case account == "":
		return ""
	case IsMobileFormat(account):
		return MaskMobile(account)
	case strings.Contains(account, "@"):
		return MaskEmail(account)
	default:
		return maskPlain(account)
	}
}

// maskPlain 普通用户名只留首尾各一个字符（admin → a***n）；两个字符以内全部打码。
func maskPlain(s string) string {
	r := []rune(s)
	if len(r) <= 2 {
		return "***"
	}
	return string(r[:1]) + "***" + string(r[len(r)-1:])
}

// ===== 安全事件去重 =====

// auditThrottle 进程内按 key 限频：同一 key 在 window 内只放行一次。
// 用于匿名探测类安全事件（同 IP + 路由），避免扫描器把审计表刷爆；多实例部署时各实例各自计数。
type auditThrottle struct {
	mu      sync.Mutex
	window  time.Duration
	maxKeys int
	last    map[string]time.Time
}

func newAuditThrottle(window time.Duration, maxKeys int) *auditThrottle {
	return &auditThrottle{window: window, maxKeys: maxKeys, last: make(map[string]time.Time)}
}

func (t *auditThrottle) allow(key string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if ts, ok := t.last[key]; ok && now.Sub(ts) < t.window {
		return false
	}
	if len(t.last) >= t.maxKeys {
		for k, ts := range t.last {
			if now.Sub(ts) >= t.window {
				delete(t.last, k)
			}
		}
		// 窗口内 key 仍然太多（大规模扫描）：整表重置，内存有界优先于精确去重
		if len(t.last) >= t.maxKeys {
			t.last = make(map[string]time.Time)
		}
	}
	t.last[key] = now
	return true
}

var securityAuditThrottle = newAuditThrottle(time.Minute, 10000)

// AllowSecurityAudit 同一 key 每分钟只允许记录一次安全事件。
func AllowSecurityAudit(key string) bool {
	return securityAuditThrottle.allow(key, time.Now())
}
