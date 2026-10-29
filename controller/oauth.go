package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/middleware"
	"easytv-api/model"
	"easytv-api/oauth"

	"github.com/gin-gonic/gin"
)

const (
	oauthStateRedisPrefix = "oauth:state:"
	oauthStateTTL         = 10 * time.Minute
)

// oauthStatePayload OAuth state 在 Redis 中的载荷（携带发起人身份，用于绑定流程）。
type oauthStatePayload struct {
	UserId    int    `json:"user_id"`
	Token     string `json:"token,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// GenerateOAuthCode 生成 OAuth state（CSRF 防护），携带发起人身份（GET /oauth/state）。
func GenerateOAuthCode(c *gin.Context) {
	state := common.GetRandomString(12)
	var userId int
	token := middleware.ParseUserAccessToken(c)
	if token != "" {
		if user, err := model.ValidateAccessToken(token); err == nil && user != nil {
			userId = user.Id
		}
	}
	payload := oauthStatePayload{
		UserId:    userId,
		Token:     token,
		CreatedAt: time.Now().Unix(),
	}
	data, err := common.Marshal(payload)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := common.RedisSetEX(oauthStateRedisPrefix+state, string(data), oauthStateTTL); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    state,
	})
}

// consumeOAuthState 读取并删除 OAuth state（一次性）。不存在返回 (nil, nil)。
func consumeOAuthState(state string) (*oauthStatePayload, error) {
	if state == "" {
		return nil, nil
	}
	raw := common.RedisGet(oauthStateRedisPrefix + state)
	if raw == "" {
		return nil, nil
	}
	_ = common.RedisDel(oauthStateRedisPrefix + state)
	var p oauthStatePayload
	if err := common.UnmarshalJsonStr(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// HandleOAuth 统一 OAuth 回调（GET /oauth/:provider?code=&state=）。
// 流程：校验 state → 解析绑定用户 → 换 token → 取用户信息 → 查已有用户（登录）/ 新用户（注册）。
func HandleOAuth(c *gin.Context) {
	providerName := c.Param("provider")
	provider := oauth.GetProvider(providerName)
	if provider == nil {
		common.ApiErrorMsg(c, "未知的登录方式")
		return
	}

	// 1. 校验 state（CSRF）
	state := c.Query("state")
	stateInfo, stateErr := consumeOAuthState(state)
	if stateErr != nil || stateInfo == nil {
		common.ApiErrorMsg(c, "OAuth state 无效或已过期")
		return
	}

	// 2. 解析绑定用户：优先 state 载荷，其次当前请求 token
	var bindUserId int
	if stateInfo.UserId > 0 {
		bindUserId = stateInfo.UserId
	} else if token := middleware.ParseUserAccessToken(c); token != "" {
		if user, err := model.ValidateAccessToken(token); err == nil && user != nil {
			bindUserId = user.Id
		}
	}
	if bindUserId > 0 {
		handleOAuthBindByUserId(c, provider, bindUserId)
		return
	}

	// 3. 检查登录方式开关
	if !provider.IsEnabled() {
		common.ApiErrorMsg(c, "该登录方式未开启")
		return
	}

	// 4. provider 错误
	if errorCode := c.Query("error"); errorCode != "" {
		errorDescription := c.Query("error_description")
		if errorDescription == "" {
			errorDescription = "用户拒绝了授权"
		}
		common.ApiErrorMsg(c, errorDescription)
		return
	}

	// 5. 换 token
	code := c.Query("code")
	token, err := provider.ExchangeToken(c.Request.Context(), code, c)
	if err != nil {
		common.SysLog(fmt.Sprintf("oauth %s exchange token failed: %v", providerName, err))
		common.ApiErrorMsg(c, "授权失败，请重试")
		return
	}

	// 6. 取用户信息
	oauthUser, err := provider.GetUserInfo(c.Request.Context(), token)
	if err != nil {
		common.SysLog(fmt.Sprintf("oauth %s userinfo failed: %v", providerName, err))
		common.ApiErrorMsg(c, "获取用户信息失败")
		return
	}

	// 7. 查已有用户
	user, err := findOAuthUser(provider, oauthUser)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// 8. 已有用户：检查状态后直接登录
	if user != nil {
		if user.Status != constant.UserStatusEnabled {
			common.ApiErrorMsg(c, "该账号已被禁用")
			return
		}
		setupLogin(user, c)
		return
	}

	// 9. 新用户：有邮箱直接创建并登录，否则返回 pending_binding
	if oauthUser.Email != "" {
		user, err := createOAuthUser(provider, oauthUser)
		if err != nil {
			if errors.Is(err, model.ErrEmailAlreadyTaken) {
				common.ApiErrorMsg(c, "该邮箱已被注册")
				return
			}
			common.ApiError(c, err)
			return
		}
		setupLogin(user, c)
		return
	}

	// 无邮箱：返回 pending_binding（由前端跳转绑定邮箱页）
	bindingToken := common.GetRandomString(24)
	pending := map[string]any{
		"provider_name":    provider.GetName(),
		"provider_user_id": oauthUser.ProviderUserID,
		"username":         oauthUser.Username,
		"nickname":         oauthUser.Nickname,
		"created_at":       time.Now().Unix(),
	}
	data, _ := common.Marshal(pending)
	_ = common.RedisSetEX("oauth:pending:"+bindingToken, string(data), 30*time.Minute)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"status":        "pending_binding",
			"binding_token": bindingToken,
			"provider":      provider.GetName(),
			"nickname":      oauthUser.Nickname,
			"username":      oauthUser.Username,
		},
	})
}

// handleOAuthBindByUserId 将 OAuth 账号绑定到指定用户。
func handleOAuthBindByUserId(c *gin.Context, provider oauth.Provider, userId int) {
	if userId <= 0 {
		common.ApiErrorMsg(c, "OAuth state 无效或已过期")
		return
	}
	if !provider.IsEnabled() {
		common.ApiErrorMsg(c, "该登录方式未开启")
		return
	}
	code := c.Query("code")
	if code == "" {
		common.ApiErrorMsg(c, "授权失败")
		return
	}
	token, err := provider.ExchangeToken(c.Request.Context(), code, c)
	if err != nil {
		common.ApiErrorMsg(c, "授权失败，请重试")
		return
	}
	oauthUser, err := provider.GetUserInfo(c.Request.Context(), token)
	if err != nil {
		common.ApiErrorMsg(c, "获取用户信息失败")
		return
	}
	// 该 OAuth 账号已被绑定
	if _, err := model.GetUserOAuthByProvider(provider.GetSlug(), oauthUser.ProviderUserID); err == nil {
		common.ApiErrorMsg(c, "该第三方账号已绑定其他用户")
		return
	}
	uo := &model.UserOAuth{
		UserId:         userId,
		Provider:       provider.GetSlug(),
		ProviderUserId: oauthUser.ProviderUserID,
	}
	if err := uo.Insert(); err != nil {
		common.ApiErrorMsg(c, "绑定失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// findOAuthUser 按 provider + provider_user_id 查找已绑定用户。
func findOAuthUser(provider oauth.Provider, oauthUser *oauth.OAuthUser) (*model.User, error) {
	uo, err := model.GetUserOAuthByProvider(provider.GetSlug(), oauthUser.ProviderUserID)
	if err != nil {
		return nil, nil // 未绑定
	}
	user, err := model.GetUserById(uo.UserId)
	if err != nil {
		return nil, common.ErrUserNotFound
	}
	return user, nil
}

// createOAuthUser 创建新用户并绑定 OAuth 账号。
func createOAuthUser(provider oauth.Provider, oauthUser *oauth.OAuthUser) (*model.User, error) {
	if model.IsEmailAlreadyTaken(oauthUser.Email) {
		return nil, model.ErrEmailAlreadyTaken
	}
	nickname := oauthUser.Nickname
	if nickname == "" {
		nickname = oauthUser.Username
	}
	if nickname == "" {
		nickname = provider.GetSlug() + "_" + common.GetRandomString(6)
	}
	user := &model.User{
		Username: model.NormalizeEmail(oauthUser.Email),
		Password: common.GetRandomString(16), // 无密码，可通过邮箱重置
		Nickname: nickname,
		Role:     constant.RoleCommonUser,
		Status:   constant.UserStatusEnabled,
		Group:    "default",
	}
	err := model.CreateUserWithOAuth(user, provider.GetSlug(), oauthUser.ProviderUserID)
	if err != nil {
		return nil, err
	}
	return user, nil
}

// OAuthRegister 预留：OAuth pending_binding 后绑定邮箱完成注册（POST /oauth/register）。
func OAuthRegister(c *gin.Context) {
	var req struct {
		BindingToken     string `json:"binding_token"`
		Email            string `json:"email"`
		VerificationCode string `json:"verification_code"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.BindingToken == "" {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	raw := common.RedisGet("oauth:pending:" + req.BindingToken)
	if raw == "" {
		common.ApiErrorMsg(c, "绑定凭证已过期，请重新授权")
		return
	}
	var pending map[string]any
	if err := common.UnmarshalJsonStr(raw, &pending); err != nil {
		common.ApiErrorMsg(c, "绑定凭证无效")
		return
	}
	email := model.NormalizeEmail(req.Email)
	if email == "" || !model.IsEmailFormat(email) {
		common.ApiErrorMsg(c, "邮箱格式不正确")
		return
	}
	if model.IsEmailAlreadyTaken(email) {
		common.ApiErrorMsg(c, "该邮箱已被注册")
		return
	}
	if !common.VerifyCodeWithKey(email, req.VerificationCode, common.EmailVerificationPurpose) {
		common.ApiErrorMsg(c, "验证码错误或已过期")
		return
	}
	providerName, _ := pending["provider_name"].(string)
	providerUserID, _ := pending["provider_user_id"].(string)
	nickname, _ := pending["nickname"].(string)
	provider := oauth.GetProvider(providerName)
	if provider == nil {
		common.ApiErrorMsg(c, "未知的登录方式")
		return
	}
	user := &model.User{
		Username: email,
		Password: common.GetRandomString(16),
		Nickname: nickname,
		Role:     constant.RoleCommonUser,
		Status:   constant.UserStatusEnabled,
		Group:    "default",
	}
	if err := model.CreateUserWithOAuth(user, provider.GetSlug(), providerUserID); err != nil {
		common.ApiErrorMsg(c, "注册失败")
		return
	}
	_ = common.RedisDel("oauth:pending:" + req.BindingToken)
	setupLogin(user, c)
}

// GetOAuthProviders 返回已启用的登录方式列表。
func GetOAuthProviders(c *gin.Context) {
	var list []gin.H
	for _, p := range oauth.GetEnabledProviders() {
		list = append(list, gin.H{
			"name": p.GetName(),
			"slug": p.GetSlug(),
		})
	}
	common.ApiSuccess(c, gin.H{"providers": list})
}
