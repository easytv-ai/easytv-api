package common

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

// 使用中文字符串常量，保持与业务语义一致。

const (
	RoleGuestUser  = 0
	RoleCommonUser = 1
	RoleAdminUser  = 10
	RoleRootUser   = 100
)

// IsValidateRole 判断角色是否合法。
func IsValidateRole(role int) bool {
	return role == RoleGuestUser || role == RoleCommonUser || role == RoleAdminUser || role == RoleRootUser
}

// 充值订单状态。
const (
	RechargeStatusPending  = "pending"
	RechargeStatusSuccess  = "success"
	RechargeStatusFailed   = "failed"
	RechargeStatusExpired  = "expired"
	RechargeStatusRefunded = "refunded"
)

// GenerateRandomKey 生成指定长度的随机字节串（16 进制），长度通常为偶数。
func GenerateRandomKey(length int) (string, error) {
	k := make([]byte, length/2)
	if _, err := rand.Read(k); err != nil {
		return "", err
	}
	return toHex(k), nil
}

// GetTimestamp 返回当前 Unix 秒。
func GetTimestamp() int64 {
	return time.Now().Unix()
}

// CheckGinError 简化错误检查，返回 true 表示有错误。占位以兼容未来扩展。
func CheckGinError(err error) bool {
	return err != nil
}

var ErrNoData = errors.New("no data")

// GetTimeString 返回精确到纳秒级的时间字符串（复刻 easyapi）。
func GetTimeString() string {
	now := time.Now().UTC()
	return fmt.Sprintf("%s%d", now.Format("20060102150405"), now.UnixNano()%1e9)
}

var requestIdPrefix = func() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Path != "" {
		h := sha256.Sum256([]byte(bi.Main.Path))
		return hex.EncodeToString(h[:4])
	}
	return GetRandomString(8)
}()

// NewRequestId 生成全局唯一的请求 id（复刻 easyapi）。
func NewRequestId() string {
	return GetTimeString() + requestIdPrefix + GetRandomString(8)
}

// GetNetworkIps 获取常见的私有网段 IP，用于启动横幅展示（复刻 easyapi）。
func GetNetworkIps() []string {
	var networkIps []string
	ips, err := net.InterfaceAddrs()
	if err != nil {
		log.Println(err)
		return networkIps
	}

	for _, a := range ips {
		if ipNet, ok := a.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				ip := ipNet.IP.String()
				// Include common private network ranges
				if strings.HasPrefix(ip, "10.") ||
					strings.HasPrefix(ip, "172.") ||
					strings.HasPrefix(ip, "192.168.") {
					networkIps = append(networkIps, ip)
				}
			}
		}
	}
	return networkIps
}

// IsRunningInContainer detects if the application is running inside a container
func IsRunningInContainer() bool {
	// Method 1: Check common container marker files
	if _, err := os.Stat("/.containerenv"); err == nil {
		return true
	}

	// Method 2: Check cgroup for container indicators
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		content := string(data)
		if strings.Contains(content, "containerd") ||
			strings.Contains(content, "kubepods") ||
			strings.Contains(content, "/lxc/") {
			return true
		}
	}

	// Method 3: Check environment variables commonly set by container runtimes
	containerEnvVars := []string{
		"KUBERNETES_SERVICE_HOST",
		"container",
	}

	for _, envVar := range containerEnvVars {
		if os.Getenv(envVar) != "" {
			return true
		}
	}

	return false
}
