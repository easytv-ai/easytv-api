package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/logger"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

const (
	wechatQRCodeRedisPrefix = "wechat:qrcode:state:"
	wechatQRCodeTTL         = 5 * time.Minute
	wechatBindingTTL        = 30 * time.Minute
	wechatOAuthBaseURL      = "https://api.weixin.qq.com"
	wechatQRConnectURL      = "https://open.weixin.qq.com/connect/qrconnect"
)

// 微信扫码配置（setting 表 key）。
const (
	settingWechatOAuthEnabled   = "WechatOAuthEnabled" // 显式开关：false/0 关闭；未配置或 true 时有 AppId 即开启
	settingWechatOAuthAppId     = "WechatOAuthAppId"
	settingWechatOAuthAppSecret = "WechatOAuthAppSecret"
	settingWechatOAuthScope     = "WechatOAuthScope"
	settingWechatOAuthRedirect  = "WechatOAuthRedirectUri"
	settingServerAddress        = "ServerAddress"
)

type wechatQRCodeState struct {
	Status      string `json:"status"`
	Action      string `json:"action,omitempty"` // "login"（默认）
	AccessToken string `json:"access_token,omitempty"`
	UserID      int    `json:"user_id,omitempty"`
	OpenID      string `json:"openid,omitempty"`
	UnionID     string `json:"unionid,omitempty"`
	Nickname    string `json:"nickname,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
	RedirectURL string `json:"redirect_url,omitempty"`
	// ErrorCode：status=error 时的失败原因，供父页面轮询 login-status 展示
	// （WxLogin 使用 self_redirect，?error 只落在 iframe 内，顶层页面看不到）
	ErrorCode string `json:"error_code,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

type wechatOAuthPreAuthResponse struct {
	AppID       string `json:"app_id"`
	State       string `json:"state"`
	RedirectURI string `json:"redirect_uri"`
	Scope       string `json:"scope"`
	AuthURL     string `json:"auth_url"`
}

type wechatOAuthLoginStatusResponse struct {
	Status      string `json:"status"`
	Action      string `json:"action,omitempty"`
	AccessToken string `json:"access_token,omitempty"`
	User        *gin.H `json:"user,omitempty"`
	// status=error 时的失败原因（user_banned / token_failed / ...），前端映射为具体提示
	ErrorCode string `json:"error_code,omitempty"`
}

type wechatAccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	OpenID      string `json:"openid"`
	UnionID     string `json:"unionid"`
}

type wechatUserInfoResponse struct {
	OpenID     string `json:"openid"`
	Nickname   string `json:"nickname"`
	HeadimgURL string `json:"headimgurl"`
	UnionID    string `json:"unionid"`
}

// ===== 配置读取 =====

func weChatOAuthConfig() (appId string, appSecret string, scope string, redirectURI string) {
	appId = model.GetSetting(settingWechatOAuthAppId)
	appSecret = model.GetSetting(settingWechatOAuthAppSecret)
	scope = model.GetSetting(settingWechatOAuthScope)
	if scope == "" {
		scope = "snsapi_login"
	}
	redirectURI = model.GetSetting(settingWechatOAuthRedirect)
	if redirectURI == "" {
		redirectURI = strings.TrimSuffix(model.GetSetting(settingServerAddress), "/") + "/wechat/oauth/callback"
	}
	return appId, appSecret, scope, redirectURI
}

func weChatOAuthEnabled() bool {
	if enabled := strings.TrimSpace(model.GetSetting(settingWechatOAuthEnabled)); enabled == "false" || enabled == "0" {
		return false
	}
	appId, _, _, _ := weChatOAuthConfig()
	return appId != ""
}

func weChatOAuthConfigReady() bool {
	appId, appSecret, _, _ := weChatOAuthConfig()
	return appId != "" && appSecret != ""
}

// ===== Redis state =====

func saveWeChatQRCodeState(state string, value *wechatQRCodeState, ttl time.Duration) error {
	if state == "" {
		return errors.New("state is empty")
	}
	data, err := common.Marshal(value)
	if err != nil {
		return err
	}
	return common.RedisSetEX(wechatQRCodeRedisPrefix+state, string(data), ttl)
}

func getWeChatQRCodeState(state string) (*wechatQRCodeState, error) {
	if state == "" {
		return nil, nil
	}
	raw := common.RedisGet(wechatQRCodeRedisPrefix + state)
	if raw == "" {
		return nil, nil
	}
	var value wechatQRCodeState
	if err := common.UnmarshalJsonStr(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func delWeChatQRCodeState(state string) {
	if state == "" {
		return
	}
	_ = common.RedisDel(wechatQRCodeRedisPrefix + state)
}

// ===== 回调跳转工具 =====

func stripQueryParams(rawURL string, keys ...string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	for _, k := range keys {
		q.Del(k)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func appendQueryParam(redirectURL, key, value string) string {
	sep := "?"
	if strings.Contains(redirectURL, "?") {
		sep = "&"
	}
	return redirectURL + sep + key + "=" + url.QueryEscape(value)
}

func weChatOAuthCallbackRedirect(c *gin.Context, redirectURL string, errorCode string) {
	if redirectURL == "" {
		redirectURL = model.GetSetting(settingServerAddress)
	}
	if errorCode != "" {
		redirectURL = stripQueryParams(redirectURL, "evidence", "error")
		redirectURL = appendQueryParam(redirectURL, "error", errorCode)
	}
	c.Redirect(http.StatusFound, redirectURL)
}

func weChatOAuthCallbackSuccess(c *gin.Context, redirectURL, state string) {
	if redirectURL == "" {
		redirectURL = model.GetSetting(settingServerAddress)
	}
	redirectURL = stripQueryParams(redirectURL, "evidence", "error")
	c.Redirect(http.StatusFound, appendQueryParam(redirectURL, "evidence", state))
}

// weChatOAuthCallbackError 把失败原因写回同一个 state，再 302 到前端。
// WxLogin 挂载时 self_redirect=true，错误跳转只发生在 iframe 内，顶层页面看不到 ?error，
// 只能靠父页面轮询 login-status 拿到终态；不写回 state 的话轮询端只会一直看到 expired。
func weChatOAuthCallbackError(c *gin.Context, redirectURL, state, errorCode string) {
	if state != "" {
		if err := saveWeChatQRCodeState(state, &wechatQRCodeState{
			Status:    "error",
			ErrorCode: errorCode,
			CreatedAt: time.Now().Unix(),
		}, wechatQRCodeTTL); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("wechat oauth save error state failed: %s", err.Error()))
		}
	}
	weChatOAuthCallbackRedirect(c, redirectURL, errorCode)
}

// ===== 微信 API =====

func weChatGetAccessToken(code string) (*wechatAccessTokenResponse, error) {
	appId, appSecret, _, _ := weChatOAuthConfig()
	u, err := url.Parse(wechatOAuthBaseURL + "/sns/oauth2/access_token")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("appid", appId)
	q.Set("secret", appSecret)
	q.Set("code", code)
	q.Set("grant_type", "authorization_code")
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var tokenResp wechatAccessTokenResponse
	if err := common.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}
	if tokenResp.AccessToken == "" || tokenResp.OpenID == "" {
		return nil, errors.New("invalid access_token response from wechat")
	}
	return &tokenResp, nil
}

func weChatGetUserInfo(accessToken, openID string) (*wechatUserInfoResponse, error) {
	u, err := url.Parse(wechatOAuthBaseURL + "/sns/userinfo")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("access_token", accessToken)
	q.Set("openid", openID)
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var userInfo wechatUserInfoResponse
	if err := common.Unmarshal(body, &userInfo); err != nil {
		return nil, err
	}
	if userInfo.OpenID == "" {
		return nil, errors.New("invalid userinfo response from wechat")
	}
	return &userInfo, nil
}

// ===== Handler =====

// WeChatOAuthPreAuth 生成扫码登录 state 与授权 URL（POST /wechat/oauth/preauth）。
func WeChatOAuthPreAuth(c *gin.Context) {
	if !weChatOAuthEnabled() {
		common.ApiErrorMsg(c, "微信扫码登录未开启")
		return
	}
	if !weChatOAuthConfigReady() {
		common.ApiErrorMsg(c, "微信扫码登录配置缺失")
		return
	}

	appId, _, scope, redirectURI := weChatOAuthConfig()
	state := common.GetRandomString(16)

	// 前端落地页 redirect_url：优先 query，其次 JSON body
	redirectURL := c.Query("redirect_url")
	if redirectURL == "" {
		var preAuthReq struct {
			RedirectURL string `json:"redirect_url"`
		}
		if err := common.DecodeJson(c.Request.Body, &preAuthReq); err == nil {
			redirectURL = preAuthReq.RedirectURL
		}
	}

	// 已登录用户暂不开放绑定流程（预留 Action=bind 扩展位）
	stateValue := &wechatQRCodeState{
		Status:      "pending",
		RedirectURL: redirectURL,
		CreatedAt:   time.Now().Unix(),
	}

	authURL, err := url.Parse(wechatQRConnectURL)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	q := authURL.Query()
	q.Set("appid", appId)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", scope)
	q.Set("state", state)
	authURL.RawQuery = q.Encode()
	authURL.Fragment = "wechat_redirect"

	if err := saveWeChatQRCodeState(state, stateValue, wechatQRCodeTTL); err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": wechatOAuthPreAuthResponse{
			AppID:       appId,
			State:       state,
			RedirectURI: redirectURI,
			Scope:       scope,
			AuthURL:     authURL.String(),
		},
	})
}

// WeChatOAuthCallback 微信授权回调（GET /wechat/oauth/callback?code=&state=）。
func WeChatOAuthCallback(c *gin.Context) {
	redirectURL := c.Query("redirect_url")
	if redirectURL == "" {
		redirectURL = model.GetSetting(settingServerAddress)
	}

	if !weChatOAuthConfigReady() {
		weChatOAuthCallbackRedirect(c, redirectURL, "missing_config")
		return
	}

	state := c.Query("state")
	stateValue, err := getWeChatQRCodeState(state)
	if err != nil || stateValue == nil {
		weChatOAuthCallbackRedirect(c, redirectURL, "invalid_state")
		return
	}
	delWeChatQRCodeState(state)
	if stateValue.RedirectURL != "" {
		redirectURL = stateValue.RedirectURL
	}

	if c.Query("error") == "access_denied" {
		weChatOAuthCallbackError(c, redirectURL, state, "access_denied")
		return
	}

	code := c.Query("code")
	if code == "" {
		weChatOAuthCallbackError(c, redirectURL, state, "token_failed")
		return
	}

	tokenResp, err := weChatGetAccessToken(code)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("wechat oauth access_token failed: %s", err.Error()))
		weChatOAuthCallbackError(c, redirectURL, state, "token_failed")
		return
	}

	userInfo, err := weChatGetUserInfo(tokenResp.AccessToken, tokenResp.OpenID)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("wechat oauth userinfo failed: %s", err.Error()))
		weChatOAuthCallbackError(c, redirectURL, state, "userinfo_failed")
		return
	}

	// 已绑定用户：直接登录；未绑定：自动注册占位账号后直接登录（微信无 email，无需绑定即可完成登录）
	user, err := model.GetUserByWeChatId(userInfo.UnionID)
	if err != nil {
		// 按 openid 再试一次（兼容未开放 unionid 的账号）
		user, err = model.GetUserByWeChatId(userInfo.OpenID)
	}
	if err != nil {
		user, err = autoRegisterWeChatUser(userInfo)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("wechat oauth auto register failed: %s", err.Error()))
			weChatOAuthCallbackError(c, redirectURL, state, "token_failed")
			return
		}
	}
	if user.Status != constant.UserStatusEnabled {
		weChatOAuthCallbackError(c, redirectURL, state, "user_banned")
		return
	}
	model.UpdateUserLastLoginAt(user.Id)
	accessToken, err := model.EnsureUserAccessToken(user.Id)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("wechat oauth ensure access token failed: %s", err.Error()))
		weChatOAuthCallbackError(c, redirectURL, state, "token_failed")
		return
	}
	if err := saveWeChatQRCodeState(state, &wechatQRCodeState{
		Status:      "success",
		AccessToken: accessToken,
		UserID:      user.Id,
		OpenID:      userInfo.OpenID,
		UnionID:     userInfo.UnionID,
		Nickname:    userInfo.Nickname,
		Avatar:      userInfo.HeadimgURL,
		CreatedAt:   time.Now().Unix(),
	}, wechatQRCodeTTL); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("wechat oauth save state failed: %s", err.Error()))
		weChatOAuthCallbackError(c, redirectURL, state, "token_failed")
		return
	}
	weChatOAuthCallbackSuccess(c, redirectURL, state)
}

// autoRegisterWeChatUser 首次扫码自动注册占位账号并返回。
// 用户名用 wx_ 占位符（与邮箱/手机号格式区分），后续通过绑定手机号（/user/self/mobile）或绑定邮箱（/oauth/email/bind）补全。
func autoRegisterWeChatUser(userInfo *wechatUserInfoResponse) (*model.User, error) {
	identifier := userInfo.UnionID
	if identifier == "" {
		identifier = userInfo.OpenID
	}
	// 并发重复扫码兜底：已存在则直接返回已有账号
	if model.IsWeChatIdAlreadyTaken(identifier) {
		return model.GetUserByWeChatId(identifier)
	}
	nickname := userInfo.Nickname
	if nickname == "" {
		nickname = "wechat_" + common.GetRandomString(6)
	}
	user := &model.User{
		Username: "wx_" + common.GetRandomString(16), // 占位用户名，不与邮箱/手机号格式冲突
		Password: common.GetRandomString(16),         // 无密码，用户后续通过微信扫码/绑定手机号登录
		Nickname: nickname,
		Role:     constant.RoleCommonUser,
		Status:   constant.UserStatusEnabled,
		Group:    "default",
		WeChatId: identifier,
	}
	if err := user.Insert(); err != nil {
		// 并发兜底：插入冲突时回查已有账号
		existing, getErr := model.GetUserByWeChatId(identifier)
		if getErr != nil {
			return nil, err
		}
		return existing, nil
	}
	return user, nil
}

// WeChatOAuthLoginStatus 前端轮询登录状态（GET /wechat/oauth/login-status?evidence=xxx）。
func WeChatOAuthLoginStatus(c *gin.Context) {
	evidence := c.Query("evidence")
	state, err := getWeChatQRCodeState(evidence)
	if err != nil || state == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    wechatOAuthLoginStatusResponse{Status: "expired"},
		})
		return
	}

	if state.Status == "pending_binding" {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data": wechatOAuthLoginStatusResponse{
				Status: "pending_binding",
				User: &gin.H{
					"nickname": state.Nickname,
					"avatar":   state.Avatar,
				},
			},
		})
		return
	}

	if state.Status != "success" {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data": wechatOAuthLoginStatusResponse{
				Status:    state.Status,
				ErrorCode: state.ErrorCode,
			},
		})
		return
	}

	user := model.User{Id: state.UserID}
	if err := user.FillUserById(); err != nil || user.Id == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "",
			"data":    wechatOAuthLoginStatusResponse{Status: "expired"},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": wechatOAuthLoginStatusResponse{
			Status:      "success",
			AccessToken: state.AccessToken,
			User: &gin.H{
				"id":       user.Id,
				"username": user.Username,
				"nickname": user.Nickname,
				"role":     user.Role,
				"status":   user.Status,
				"group":    user.Group,
				"mobile":   user.Mobile, // 空串=未绑定手机号，前端可引导去个人中心绑定
			},
		},
	})
}

// WeChatOAuthRegister 微信扫码后绑定邮箱完成注册（POST /wechat/oauth/register）。
func WeChatOAuthRegister(c *gin.Context) {
	var req struct {
		Evidence         string `json:"evidence"`
		Email            string `json:"email"`
		VerificationCode string `json:"code"`
		// Attr 归因（API.md 7.2），与邮箱注册一致
		Attr model.Attribution `json:"attr"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Evidence == "" {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	state, err := getWeChatQRCodeState(req.Evidence)
	if err != nil || state == nil || state.Status != "pending_binding" {
		common.ApiErrorMsg(c, "登录凭证已过期，请重新扫码")
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
	if !common.VerifyCodeWithKey(email, req.VerificationCode, common.WechatBindEmailPurpose) {
		common.ApiErrorMsg(c, "验证码错误或已过期")
		return
	}

	// 绑定唯一性检查
	identifier := state.UnionID
	if identifier == "" {
		identifier = state.OpenID
	}
	if model.IsWeChatIdAlreadyTaken(identifier) {
		common.ApiErrorMsg(c, "该微信账号已绑定其他邮箱")
		return
	}

	// 创建用户并绑定微信
	nickname := state.Nickname
	if nickname == "" {
		nickname = "wechat_" + common.GetRandomString(6)
	}
	attr := req.Attr.Normalize()
	user := &model.User{
		Username:  email,
		Password:  common.GetRandomString(16), // 微信注册无密码，用户后续可通过邮箱重置
		Nickname:  nickname,
		Role:      constant.RoleCommonUser,
		Status:    constant.UserStatusEnabled,
		Group:     "default",
		WeChatId:  identifier,
		Source:    attr.Source,
		Medium:    attr.Medium,
		Campaign:  attr.Campaign,
		BonusCode: attr.BonusCode,
	}
	if err := user.Insert(); err != nil {
		common.SysError("wechat register insert failed: " + err.Error())
		common.ApiErrorMsg(c, "注册失败，请稍后重试")
		return
	}
	delWeChatQRCodeState(req.Evidence)
	service.AfterRegister(user, attr, c.ClientIP(), c.GetHeader("EasyTV-Device"))
	setupLogin(user, c)
}

// EmailBind 已登录用户绑定邮箱（POST /oauth/email/bind，微信登录后无邮箱场景）。
func EmailBind(c *gin.Context) {
	userId := c.GetInt("id")
	if userId <= 0 {
		common.ApiErrorMsg(c, "未登录")
		return
	}
	user, err := model.GetUserById(userId)
	if err != nil {
		common.ApiErrorMsg(c, "用户不存在")
		return
	}
	var req struct {
		Email            string `json:"email"`
		VerificationCode string `json:"code"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	email := model.NormalizeEmail(req.Email)
	if email == "" || !model.IsEmailFormat(email) {
		common.ApiErrorMsg(c, "邮箱格式不正确")
		return
	}
	if model.IsEmailAlreadyTaken(email) && model.NormalizeEmail(user.Username) != email {
		common.ApiErrorMsg(c, "该邮箱已被其他账号绑定")
		return
	}
	if !common.VerifyCodeWithKey(email, req.VerificationCode, common.WechatBindEmailPurpose) {
		common.ApiErrorMsg(c, "验证码错误或已过期")
		return
	}
	user.Username = email
	if err := user.Update(); err != nil {
		common.ApiErrorMsg(c, "绑定失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// WeChatOAuthBind 已登录用户绑定微信（GET /oauth/wechat/bind，预留）。
func WeChatOAuthBind(c *gin.Context) {
	// 绑定流程与登录流程共用 preauth/callback，本期仅实现登录，绑定预留。
	common.ApiErrorMsg(c, "微信绑定功能暂未开放")
}

// TelegramLogin 预留：Telegram 登录占位（与前端 oauth.ts 对齐）。
func TelegramLogin(c *gin.Context) {
	common.ApiErrorMsg(c, "Telegram 登录暂未开放")
}

// TelegramBind 预留：Telegram 绑定占位。
func TelegramBind(c *gin.Context) {
	common.ApiErrorMsg(c, "Telegram 绑定暂未开放")
}
