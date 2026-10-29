package oauth

import (
	"context"

	"github.com/gin-gonic/gin"
)

// Provider OAuth 登录方式接口。新增登录方式 = 实现该接口 + oauth.Register 注册。
type Provider interface {
	// GetName 返回登录方式显示名（如 "GitHub"）。
	GetName() string

	// GetSlug 返回路由标识（如 "github"，与 /oauth/:provider 对齐）。
	GetSlug() string

	// IsEnabled 该登录方式是否开启。
	IsEnabled() bool

	// GetAuthorizeURL 构建授权跳转 URL（redirect_uri 由控制器拼接）。
	GetAuthorizeURL(state string, c *gin.Context) (string, error)

	// ExchangeToken 用授权码换取 access token。
	ExchangeToken(ctx context.Context, code string, c *gin.Context) (*OAuthToken, error)

	// GetUserInfo 用 access token 获取用户信息。
	GetUserInfo(ctx context.Context, token *OAuthToken) (*OAuthUser, error)

	// GetProviderUserID 返回该登录方式下的用户唯一标识。
	GetProviderUserID(user *OAuthUser) string
}

// OAuthToken 第三方 access token。
type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// OAuthUser 第三方用户信息。
type OAuthUser struct {
	ProviderUserID string `json:"provider_user_id"`
	Username       string `json:"username"`
	Nickname       string `json:"nickname"`
	Email          string `json:"email"`
	Avatar         string `json:"avatar"`
}
