package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/middleware"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 注册 =====

// RegisterRequest 注册请求：邮箱 + 密码 + 验证码（EmailVerificationEnabled 时必填）。
type RegisterRequest struct {
	Username         string `json:"username"`
	Password         string `json:"password"`
	VerificationCode string `json:"verification_code"`
	Nickname         string `json:"nickname"`
	// Attr 归因（API.md 7.2）：utm 三项、邀请码 ref、口令 code、匿名 anon_id；缺省为空。
	Attr model.Attribution `json:"attr"`
}

// Register 邮箱注册。
func Register(c *gin.Context) {
	var req RegisterRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	email := model.NormalizeEmail(req.Username)
	if email == "" || !model.IsEmailFormat(email) {
		common.ApiErrorMsg(c, "邮箱格式不正确")
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 32 {
		common.ApiErrorMsg(c, "密码长度需为 8-32 位")
		return
	}
	// 邮箱验证码校验
	if common.EmailVerificationEnabled {
		if req.VerificationCode == "" {
			common.ApiErrorMsg(c, "请填写邮箱验证码")
			return
		}
		if !common.VerifyCodeWithKey(email, req.VerificationCode, common.EmailVerificationPurpose) {
			common.ApiErrorMsg(c, "验证码错误或已过期")
			return
		}
	}
	if model.IsEmailAlreadyTaken(email) {
		common.ApiErrorMsg(c, "该邮箱已被注册")
		return
	}

	hashedPassword, err := common.Password2Hash(req.Password)
	if err != nil {
		common.ApiErrorMsg(c, "密码加密失败")
		return
	}
	nickname := strings.TrimSpace(req.Nickname)
	if nickname == "" {
		nickname = strings.Split(email, "@")[0]
	}
	attr := req.Attr.Normalize()
	user := &model.User{
		Username:  email,
		Password:  hashedPassword,
		Nickname:  nickname,
		Role:      constant.RoleCommonUser,
		Status:    constant.UserStatusEnabled,
		Group:     "default",
		Source:    attr.Source,
		Medium:    attr.Medium,
		Campaign:  attr.Campaign,
		BonusCode: attr.BonusCode,
	}
	if err := user.Insert(); err != nil {
		common.SysError("register insert failed: " + err.Error())
		common.ApiErrorMsg(c, "注册失败，请稍后重试")
		return
	}
	service.AfterRegister(user, attr, c.ClientIP(), c.GetHeader("EasyTV-Device"))
	model.RecordOperationAudit(c, user.Id, user.Username, user.Role,
		model.AuditCategoryLogin, "user.register",
		"用户注册", map[string]interface{}{"email": model.MaskEmail(email)})
	setupLogin(user, c)
}

// ===== 登录 =====

// LoginRequest 登录请求：邮箱 + 密码。
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login 邮箱密码登录。
func Login(c *gin.Context) {
	var req LoginRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	username := strings.TrimSpace(req.Username)
	password := req.Password
	if username == "" || password == "" {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	user := model.User{
		Username: username,
		Password: password,
	}
	err := user.ValidateAndFill()
	if err != nil {
		common.SysLog(fmt.Sprintf("login failed for %s: %v", username, err))
		// 数据库错误不是登录失败，不记安全事件
		if errors.Is(err, common.ErrInvalidCredentials) {
			recordLoginFailed(c, model.AuditActionLoginFailed, "邮箱密码登录失败", username, "invalid_credentials")
		}
		common.ApiErrorMsg(c, "用户名或密码错误")
		return
	}
	// 令牌签发成功才算登录成功
	if setupLogin(&user, c) {
		model.RecordOperationAudit(c, user.Id, user.Username, user.Role,
			model.AuditCategoryLogin, "user.login",
			"邮箱密码登录", nil)
	}
}

// recordLoginFailed 登录失败安全事件：账号来自请求体，只存脱敏值，不关联用户 id。
func recordLoginFailed(c *gin.Context, action, content, account, reason string) {
	masked := model.MaskAccount(account)
	model.RecordAuditEvent(c, model.AuditEvent{
		Username: masked,
		Category: model.AuditCategorySecurity,
		Action:   action,
		Content:  content,
		Failed:   true,
		Params:   map[string]any{"account": masked, "reason": reason},
	})
}

// setupLogin 签发 access_token 并返回用户信息（与 easyapi 响应结构一致，前端可直接消费）。
// 返回是否签发成功（失败时已写错误响应），调用方据此决定是否记登录成功审计。
func setupLogin(user *model.User, c *gin.Context) bool {
	model.UpdateUserLastLoginAt(user.Id)
	accessToken, err := model.EnsureUserAccessToken(user.Id)
	if err != nil {
		common.ApiError(c, err)
		return false
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "",
		"success": true,
		"data": map[string]any{
			"id":           user.Id,
			"username":     user.Username,
			"nickname":     user.Nickname,
			"role":         user.Role,
			"status":       user.Status,
			"group":        user.Group,
			"access_token": accessToken,
		},
	})
	return true
}

// Logout 登出：清除 access_token。
func Logout(c *gin.Context) {
	if token := middleware.ParseUserAccessToken(c); token != "" {
		if user, _ := model.ValidateAccessToken(token); user != nil {
			_ = model.InvalidateUserAccessToken(user.Id)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "",
		"success": true,
	})
}

// ===== 邮箱验证码 =====

// SendEmailVerification 发送邮箱验证码（GET /verification?email=xxx&action=xxx）。
// action: v=注册验证 r=重置密码 b=微信绑定邮箱。
func SendEmailVerification(c *gin.Context) {
	email := model.NormalizeEmail(c.Query("email"))
	if email == "" || !model.IsEmailFormat(email) {
		common.ApiErrorMsg(c, "邮箱格式不正确")
		return
	}
	action := c.Query("action")
	if action == "" {
		action = common.EmailVerificationPurpose
	}
	switch action {
	case common.EmailVerificationPurpose, common.PasswordResetPurpose, common.WechatBindEmailPurpose:
	default:
		common.ApiErrorMsg(c, "无效的验证码用途")
		return
	}
	// 注册/重置场景校验邮箱是否已注册；绑定场景允许新邮箱
	if action == common.EmailVerificationPurpose {
		if model.IsEmailAlreadyTaken(email) {
			common.ApiErrorMsg(c, "该邮箱已被注册")
			return
		}
	}
	code := common.GenerateNumericVerificationCode(6)
	common.RegisterVerificationCodeWithKey(email, code, action)
	subject := "EasyTV 邮箱验证码"
	body := fmt.Sprintf("您好，您的验证码：%s，%d 分钟内有效。", code, common.VerificationValidMinutes)
	if err := common.SendEmail(email, subject, body); err != nil {
		common.SysError("send verification email failed: " + err.Error())
		common.ApiErrorMsg(c, "验证码发送失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// SendPasswordResetEmail 发送重置密码邮件（GET /reset_password?email=xxx）。
func SendPasswordResetEmail(c *gin.Context) {
	email := model.NormalizeEmail(c.Query("email"))
	if email == "" || !model.IsEmailFormat(email) {
		common.ApiErrorMsg(c, "邮箱格式不正确")
		return
	}
	if !model.IsEmailAlreadyTaken(email) {
		common.ApiErrorMsg(c, "该邮箱未注册")
		return
	}
	code := common.GenerateNumericVerificationCode(6)
	common.RegisterVerificationCodeWithKey(email, code, common.PasswordResetPurpose)
	subject := "EasyTV 重置密码"
	body := fmt.Sprintf("您的 EasyTV 重置密码验证码是：%s，%d 分钟内有效。", code, common.VerificationValidMinutes)
	if err := common.SendEmail(email, subject, body); err != nil {
		common.SysError("send reset password email failed: " + err.Error())
		common.ApiErrorMsg(c, "邮件发送失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// ResetPassword 重置密码（POST /user/reset）：邮箱 + 验证码 + 新密码。
func ResetPassword(c *gin.Context) {
	var req struct {
		Email            string `json:"email"`
		VerificationCode string `json:"verification_code"`
		Password         string `json:"password"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	email := model.NormalizeEmail(req.Email)
	if email == "" || !model.IsEmailFormat(email) {
		common.ApiErrorMsg(c, "邮箱格式不正确")
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 32 {
		common.ApiErrorMsg(c, "密码长度需为 8-32 位")
		return
	}
	if !common.VerifyCodeWithKey(email, req.VerificationCode, common.PasswordResetPurpose) {
		common.ApiErrorMsg(c, "验证码错误或已过期")
		return
	}
	if err := model.ResetUserPasswordByEmail(email, req.Password); err != nil {
		common.SysError("reset password failed: " + err.Error())
		common.ApiErrorMsg(c, "重置密码失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// ===== 短信验证码登录 =====

// smsResendIntervalKey 短信重发间隔 key 前缀。
const smsResendIntervalKey = "easytv:sms:interval:"

// smsResendIntervalSeconds 同一手机号重发间隔（秒）。
const smsResendIntervalSeconds = 60

// SendSmsVerification 发送短信验证码（GET /sms/verification?mobile=xxx）。
func SendSmsVerification(c *gin.Context) {
	mobile := strings.TrimSpace(c.Query("mobile"))
	if !model.IsMobileFormat(mobile) {
		common.ApiErrorMsg(c, "手机号格式不正确")
		return
	}
	// 重发间隔控制（Redis 不可用时放行，与限流中间件行为一致）
	if common.RedisClient != nil && common.RedisGet(smsResendIntervalKey+mobile) != "" {
		common.ApiErrorMsg(c, "发送过于频繁，请稍后再试")
		return
	}
	// 测试手机号（SMS_TEST_ACCOUNTS，仅本地联调）：不发短信，登录时认固定验证码
	if _, ok := common.SmsTestAccounts[mobile]; ok {
		common.ApiSuccess(c, nil)
		return
	}
	code := common.GenerateNumericVerificationCode(6)
	common.RegisterVerificationCodeWithKey(mobile, code, common.SmsLoginPurpose)
	if err := common.SendSMS(mobile, map[string]string{"code": code}); err != nil {
		common.SysError("send sms verification failed: " + err.Error())
		common.ApiErrorMsg(c, "验证码发送失败")
		return
	}
	_ = common.RedisSetEX(smsResendIntervalKey+mobile, 1, smsResendIntervalSeconds*time.Second)
	common.ApiSuccess(c, nil)
}

// SmsLoginRequest 手机号验证码登录请求。
type SmsLoginRequest struct {
	Mobile string `json:"mobile"`
	Code   string `json:"code"`
}

// SmsLogin 手机号验证码登录：校验验证码，用户不存在则自动注册（参考 ic-conference-api captcha-login）。
func SmsLogin(c *gin.Context) {
	var req SmsLoginRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	mobile := strings.TrimSpace(req.Mobile)
	if !model.IsMobileFormat(mobile) {
		common.ApiErrorMsg(c, "手机号格式不正确")
		return
	}
	testCode, isTestMobile := common.SmsTestAccounts[mobile]
	if (isTestMobile && req.Code != testCode) ||
		(!isTestMobile && !common.VerifyCodeWithKey(mobile, req.Code, common.SmsLoginPurpose)) {
		recordLoginFailed(c, model.AuditActionSmsLoginFailed, "手机验证码登录失败", mobile, "wrong_code")
		common.ApiErrorMsg(c, "验证码错误或已过期")
		return
	}
	user := &model.User{Username: mobile}
	if model.IsMobileAlreadyTaken(mobile) {
		if err := user.FillUserByMobile(); err != nil {
			common.SysError("sms login load user failed: " + err.Error())
			common.ApiErrorMsg(c, "登录失败，请稍后重试")
			return
		}
	} else {
		// 自动注册：随机密码（仅支持短信登录），昵称脱敏
		randomPassword := common.GetRandomString(16)
		hashedPassword, err := common.Password2Hash(randomPassword)
		if err != nil {
			common.ApiErrorMsg(c, "登录失败，请稍后重试")
			return
		}
		user = &model.User{
			Username: mobile,
			Password: hashedPassword,
			Nickname: maskMobile(mobile),
			Role:     constant.RoleCommonUser,
			Status:   constant.UserStatusEnabled,
			Group:    "default",
		}
		if err := user.Insert(); err != nil {
			// 并发注册兜底：唯一索引冲突时重查已有账号
			if retryErr := user.FillUserByMobile(); retryErr != nil {
				common.SysError("sms login auto register failed: " + err.Error())
				common.ApiErrorMsg(c, "登录失败，请稍后重试")
				return
			}
		}
	}
	// 令牌签发成功才算登录成功
	if setupLogin(user, c) {
		model.RecordOperationAudit(c, user.Id, user.Username, user.Role,
			model.AuditCategoryLogin, "user.sms_login",
			"手机验证码登录", map[string]interface{}{"mobile": model.MaskMobile(mobile)})
	}
}

// maskMobile 手机号脱敏（13812341234 → 138****1234）。
func maskMobile(mobile string) string {
	return model.MaskMobile(mobile)
}

// ===== 用户资料 =====

// GetSelf 获取当前用户信息（需登录）。
func GetSelf(c *gin.Context) {
	userId := c.GetInt("id")
	user, err := model.GetUserById(userId)
	if err != nil {
		common.ApiErrorMsg(c, "用户不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "",
		"success": true,
		"data": map[string]any{
			"id":               user.Id,
			"username":         user.Username,
			"nickname":         user.Nickname,
			"role":             user.Role,
			"status":           user.Status,
			"group":            user.Group,
			"quota":            user.Quota,
			"used_quota":       user.UsedQuota,
			"request_count":    user.RequestCount,
			"setting":          user.Setting,
			"remark":           user.Remark,
			"wechat_id":        user.WeChatId,
			"wechat_nickname":  user.WeChatNickname,
			"github_id":        user.GitHubId,
			"discord_id":       user.DiscordId,
			"oidc_id":          user.OidcId,
			"telegram_id":      user.TelegramId,
			"linux_do_id":      user.LinuxDOId,
			"aff_code":         user.AffCode,
			"aff_count":        user.AffCount,
			"aff_quota":        user.AffQuota,
			"aff_history":      user.AffHistoryQuota,
			"inviter_id":       user.InviterId,
			"partner_id":       user.PartnerId,
			"billing_discount": user.BillingDiscount,
			"created_at":       user.CreatedAt,
			"last_login_at":    user.LastLoginAt,
		},
	})
}

// UpdateSelf 更新用户资料（昵称/设置）。
func UpdateSelf(c *gin.Context) {
	userId := c.GetInt("id")
	user, err := model.GetUserById(userId)
	if err != nil {
		common.ApiErrorMsg(c, "用户不存在")
		return
	}
	var req struct {
		Nickname string `json:"nickname"`
		Setting  string `json:"setting"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Nickname != "" {
		if len(req.Nickname) > 20 {
			common.ApiErrorMsg(c, "昵称过长")
			return
		}
		user.Nickname = req.Nickname
	}
	if req.Setting != "" {
		user.Setting = req.Setting
	}
	if err := user.Update(); err != nil {
		common.ApiErrorMsg(c, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// GetUserGroups 获取用户分组列表（无需登录，前端 /user/groups 使用）。
func GetUserGroups(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		"groups": []gin.H{
			{"name": "default", "display_name": "默认分组"},
		},
		"default": "default",
	})
}

// GetSelfGroup 获取当前用户分组（需登录）。
func GetSelfGroup(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		"group": c.GetString("group"),
	})
}
