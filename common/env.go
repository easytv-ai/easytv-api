package common

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// GetEnvOrDefault 读取整数环境变量，缺失或解析失败时返回默认值。
func GetEnvOrDefault(env string, defaultValue int) int {
	if env == "" || os.Getenv(env) == "" {
		return defaultValue
	}
	num, err := strconv.Atoi(os.Getenv(env))
	if err != nil {
		SysError(fmt.Sprintf("failed to parse %s: %s, using default value: %d", env, err.Error(), defaultValue))
		return defaultValue
	}
	return num
}

// GetEnvOrDefaultString 读取字符串环境变量，缺失时返回默认值。
func GetEnvOrDefaultString(env string, defaultValue string) string {
	if env == "" || os.Getenv(env) == "" {
		return defaultValue
	}
	return os.Getenv(env)
}

// GetEnvOrDefaultBool 读取布尔环境变量，缺失或解析失败时返回默认值。
func GetEnvOrDefaultBool(env string, defaultValue bool) bool {
	if env == "" || os.Getenv(env) == "" {
		return defaultValue
	}
	b, err := strconv.ParseBool(os.Getenv(env))
	if err != nil {
		SysError(fmt.Sprintf("failed to parse %s: %s, using default value: %t", env, err.Error(), defaultValue))
		return defaultValue
	}
	return b
}

// WorkerTag 联调实例标签（环境变量 WORKER_TAG，≤ 32 字符）。
// 本地联调常常直连生产库，和线上进程共用同一张作业表：不打标签时两边会互相抢媒体 / 合成 / 批量作业
// （线上没装 ffmpeg 就把本地提交的作业跑失败，本地又会领走线上用户的作业）。
// 非空时：本进程建的作业带上这个标签，worker 只领同标签的作业，并且不跑全局维护任务（账单 / 超时退分 / 触达 / 清理）。
// 生产不设，空串 = 只处理未打标签的作业。
func WorkerTag() string {
	v := strings.TrimSpace(os.Getenv("WORKER_TAG"))
	if len(v) > 32 {
		v = v[:32]
	}
	return v
}

// IsDevInstance 是否联调实例（设了 WORKER_TAG）。
func IsDevInstance() bool {
	return WorkerTag() != ""
}
