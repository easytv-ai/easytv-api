package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// GitHubProvider GitHub OAuth 登录示例实现。
// 配置存 setting 表：GitHubOAuthClientId / GitHubOAuthClientSecret / GitHubOAuthRedirectUri。
type GitHubProvider struct{}

const (
	settingGitHubClientId     = "GitHubOAuthClientId"
	settingGitHubClientSecret = "GitHubOAuthClientSecret"
	settingGitHubRedirectUri  = "GitHubOAuthRedirectUri"
)

func (p *GitHubProvider) GetName() string { return "GitHub" }
func (p *GitHubProvider) GetSlug() string { return "github" }

func (p *GitHubProvider) clientId() string { return model.GetSetting(settingGitHubClientId) }
func (p *GitHubProvider) clientSecret() string {
	return model.GetSetting(settingGitHubClientSecret)
}
func (p *GitHubProvider) redirectUri() string {
	return model.GetSetting(settingGitHubRedirectUri)
}

func (p *GitHubProvider) IsEnabled() bool {
	return p.clientId() != "" && p.clientSecret() != ""
}

func (p *GitHubProvider) GetAuthorizeURL(state string, c *gin.Context) (string, error) {
	u, err := url.Parse("https://github.com/login/oauth/authorize")
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("client_id", p.clientId())
	q.Set("redirect_uri", p.redirectUri())
	q.Set("state", state)
	q.Set("scope", "read:user user:email")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (p *GitHubProvider) ExchangeToken(ctx context.Context, code string, c *gin.Context) (*OAuthToken, error) {
	form := url.Values{}
	form.Set("client_id", p.clientId())
	form.Set("client_secret", p.clientSecret())
	form.Set("code", code)
	form.Set("redirect_uri", p.redirectUri())

	resp, err := http.PostForm("https://github.com/login/oauth/access_token", form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// GitHub 默认返回表单格式，支持 Accept: application/json
	if resp.Header.Get("Content-Type") == "application/json" {
		var tokenResp struct {
			AccessToken string `json:"access_token"`
			TokenType   string `json:"token_type"`
			Scope       string `json:"scope"`
		}
		if err := json.Unmarshal(body, &tokenResp); err != nil {
			return nil, err
		}
		if tokenResp.AccessToken == "" {
			return nil, errors.New("github exchange token failed")
		}
		return &OAuthToken{AccessToken: tokenResp.AccessToken, TokenType: tokenResp.TokenType}, nil
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, err
	}
	token := values.Get("access_token")
	if token == "" {
		return nil, errors.New("github exchange token failed")
	}
	return &OAuthToken{AccessToken: token}, nil
}

func (p *GitHubProvider) GetUserInfo(ctx context.Context, token *OAuthToken) (*OAuthUser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github userinfo failed: %d", resp.StatusCode)
	}
	var user struct {
		ID     int    `json:"id"`
		Login  string `json:"login"`
		Name   string `json:"name"`
		Email  string `json:"email"`
		Avatar string `json:"avatar_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}
	return &OAuthUser{
		ProviderUserID: fmt.Sprintf("%d", user.ID),
		Username:       user.Login,
		Nickname:       user.Name,
		Email:          user.Email,
		Avatar:         user.Avatar,
	}, nil
}

func (p *GitHubProvider) GetProviderUserID(user *OAuthUser) string {
	return user.ProviderUserID
}

// init 自注册：新增登录方式 = 实现 Provider 接口 + 在此注册一行。
func init() {
	Register(&GitHubProvider{})
}
