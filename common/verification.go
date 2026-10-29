package common

import (
	"crypto/rand"
	"strconv"
	"sync"
	"time"
)

// 校验码用途前缀。
const (
	EmailVerificationPurpose = "v" // 注册/邮箱验证
	PasswordResetPurpose     = "r" // 重置密码
	WechatBindEmailPurpose   = "b" // 微信登录绑定邮箱
	SmsLoginPurpose          = "s" // 手机号验证码登录
)

// verificationRedisPrefix 校验码在 Redis 中的 key 前缀。
const verificationRedisPrefix = "easytv:verify:"

// verificationMap Redis 不可用时的内存兜底（与 easyapi 实现一致）。
var (
	verificationMutex  sync.Mutex
	verificationMap    map[string]verificationValue
	verificationMapMax = 1000
)

type verificationValue struct {
	code string
	time time.Time
}

func init() {
	verificationMap = make(map[string]verificationValue)
}

// GenerateNumericVerificationCode 生成指定长度的纯数字校验码。
func GenerateNumericVerificationCode(length int) string {
	if length <= 0 {
		length = 6
	}
	buf := make([]byte, length)
	_, _ = rand.Read(buf)
	result := make([]byte, length)
	for i := 0; i < length; i++ {
		result[i] = byte('0' + buf[i]%10)
	}
	return string(result)
}

// GenerateAlphanumericVerificationCode 生成指定长度的字母数字校验码（用于 state/evidence 等）。
func GenerateAlphanumericVerificationCode(length int) string {
	return GetRandomString(length)
}

// RegisterVerificationCodeWithKey 将校验码写入 Redis；Redis 不可用时降级到内存。
func RegisterVerificationCodeWithKey(key string, code string, purpose string) {
	if RedisClient != nil {
		_ = RedisSetEX(verificationRedisPrefix+purpose+":"+key, code, time.Duration(VerificationValidMinutes)*time.Minute)
		return
	}
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	verificationMap[purpose+key] = verificationValue{code: code, time: time.Now()}
	if len(verificationMap) > verificationMapMax {
		removeExpiredVerifications()
	}
}

// VerifyCodeWithKey 校验校验码，正确且未过期返回 true（一次性使用）。
func VerifyCodeWithKey(key string, code string, purpose string) bool {
	if RedisClient != nil {
		redisKey := verificationRedisPrefix + purpose + ":" + key
		stored := RedisGet(redisKey)
		if stored == "" || stored != code {
			return false
		}
		_ = RedisDel(redisKey)
		return true
	}
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	value, ok := verificationMap[purpose+key]
	now := time.Now()
	if !ok || now.Sub(value.time) >= time.Duration(VerificationValidMinutes)*time.Minute {
		return false
	}
	if code != value.code {
		return false
	}
	delete(verificationMap, purpose+key)
	return true
}

// DeleteKey 删除指定用途的校验码。
func DeleteKey(key string, purpose string) {
	if RedisClient != nil {
		_ = RedisDel(verificationRedisPrefix + purpose + ":" + key)
		return
	}
	verificationMutex.Lock()
	defer verificationMutex.Unlock()
	delete(verificationMap, purpose+key)
}

// removeExpiredVerifications 清理过期校验码（调用方需持有锁）。
func removeExpiredVerifications() {
	now := time.Now()
	for key := range verificationMap {
		if now.Sub(verificationMap[key].time) >= time.Duration(VerificationValidMinutes)*time.Minute {
			delete(verificationMap, key)
		}
	}
}

// VerificationCodeInt 生成纯数字校验码并返回字符串（兼容旧调用）。
func VerificationCodeInt() string {
	return GenerateNumericVerificationCode(6)
}

// ParseVerifyCodeToInt 兼容层：解析校验码为整数（保留以兼容参考代码风格）。
func ParseVerifyCodeToInt(code string) int {
	n, _ := strconv.Atoi(code)
	return n
}
