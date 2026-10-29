package common

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// 全局配置项（初始化后由 InitEnv 赋值）。
var (
	Port                     = 8383
	DebugEnabled             = false
	CryptoSecret             string
	VerificationValidMinutes = 10

	// SMTP 配置
	EmailVerificationEnabled = false
	SmtpHost                 string
	SmtpPort                 int
	SmtpUser                 string
	SmtpPass                 string
	SmtpFrom                 string

	// SubMail 短信配置（env 基线，启动后不再改动；实际生效值见 CurrentSmsConfig，DB setting 叠加在基线上）
	SmsEnabled        = false
	SubmailUrl        = "https://api-v4.mysubmail.com"
	SubmailAppId      string
	SubmailAppKey     string
	SubmailSignType   = "md5" // md5 / sha1 / normal
	SubmailSmsProject string  // sms/xsend 短信模板 ID

	// 微信开放平台扫码登录回调域名
	WechatOAuthRedirectDomain string
)

// EnvFile 环境变量文件路径，可通过 --env-file 覆盖（默认 .env）。
var EnvFile = ".env"

// LogDir 日志目录，可通过 --log-dir 覆盖（默认 ./logs）。
var LogDir = "./logs"

// InitEnv 解析环境变量到全局配置。
func InitEnv() {
	Port = GetEnvOrDefault("PORT", 8383)
	DebugEnabled = GetEnvOrDefaultBool("DEBUG", false)
	CryptoSecret = GetEnvOrDefaultString("CRYPTO_SECRET", CryptoSecret)
	if !DebugEnabled && len(CryptoSecret) < 16 {
		log.Fatal("CRYPTO_SECRET 未配置或长度不足 16 位：生产环境必须显式设置随机密钥（生成示例：openssl rand -hex 32）")
	} else if DebugEnabled && len(CryptoSecret) < 16 {
		SysLog("warning: CRYPTO_SECRET 未配置，开发模式降级使用弱默认值；生产环境必须显式设置")
	}
	VerificationValidMinutes = GetEnvOrDefault("VERIFICATION_VALID_MINUTES", 10)
	Version = GetEnvOrDefaultString("VERSION", Version)

	EmailVerificationEnabled = GetEnvOrDefaultBool("EMAIL_VERIFICATION_ENABLED", false)
	SmtpHost = GetEnvOrDefaultString("SMTP_HOST", "")
	SmtpPort = GetEnvOrDefault("SMTP_PORT", 465)
	SmtpUser = GetEnvOrDefaultString("SMTP_USER", "")
	SmtpPass = GetEnvOrDefaultString("SMTP_PASS", "")
	SmtpFrom = GetEnvOrDefaultString("SMTP_FROM", "EasyTV <noreply@example.com>")

	SmsEnabled = GetEnvOrDefaultBool("SMS_ENABLED", false)
	SmsTestAccounts = ParseSmsTestAccounts(GetEnvOrDefaultString("SMS_TEST_ACCOUNTS", ""))
	if len(SmsTestAccounts) > 0 {
		SysLog(fmt.Sprintf("[SMS] 已启用 %d 个测试手机号（固定验证码，不发短信）；线上服务器不要配置 SMS_TEST_ACCOUNTS", len(SmsTestAccounts)))
	}
	SubmailUrl = GetEnvOrDefaultString("SUBMAIL_URL", SubmailUrl)
	SubmailAppId = GetEnvOrDefaultString("SUBMAIL_APP_ID", "")
	SubmailAppKey = GetEnvOrDefaultString("SUBMAIL_APP_KEY", "")
	SubmailSignType = GetEnvOrDefaultString("SUBMAIL_SIGN_TYPE", "md5")
	SubmailSmsProject = GetEnvOrDefaultString("SUBMAIL_SMS_PROJECT", "")

	WechatOAuthRedirectDomain = GetEnvOrDefaultString("WECHAT_OAUTH_REDIRECT_DOMAIN", "")

	// 日志目录：绝对化并创建（复刻 easyapi InitEnv 逻辑）
	if LogDir != "" {
		var err error
		LogDir, err = filepath.Abs(LogDir)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := os.Stat(LogDir); os.IsNotExist(err) {
			err = os.Mkdir(LogDir, 0777)
			if err != nil {
				log.Fatal(err)
			}
		}
	}
}

// LoadEnvFile 加载环境变量文件（godotenv，已存在变量不覆盖）。
func LoadEnvFile() error {
	if err := godotenv.Load(EnvFile); err != nil {
		// 文件不存在时允许继续（从系统环境变量读取）
		SysLog(fmt.Sprintf("env file %s not loaded: %v", EnvFile, err))
	}
	return nil
}
