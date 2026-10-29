package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"easytv-api/service"
	"easytv-api/setting"
)

// 网关余额（API.md 38）。网关是 one-api 一系，两条路：
//   - 账户：GET {控制台}/api/user/self，头 Authorization: Bearer <系统访问令牌> + EasyAPI-User: <用户 id>，
//     data.quota = 剩余、data.used_quota = 累计用量（quota 单位，500000 = $1）。这是会欠费的那个数。
//   - 令牌：GET /v1/dashboard/billing/subscription（hard_limit_usd）+ /billing/usage（total_usage 美分）。
//     sk 令牌不限额时 hard_limit_usd 固定回 100000000，看不到账户余额，只能报 unlimited。
// 配了访问令牌走账户，否则走令牌。

const gatewayQuotaPerUSD = 500000

// gatewayUnlimitedSentinel one-api 对不限额令牌回的占位额度
const gatewayUnlimitedSentinel = 1e8

func init() {
	service.GatewayBalanceFetcher = func() (service.GatewayBalance, error) {
		if !setting.IsGatewayEnabled() {
			return service.GatewayBalance{}, errors.New("网关未启用")
		}
		if tok, uid := setting.GetGatewayAccessToken(), setting.GetGatewayUserId(); tok != "" && uid != "" {
			return FetchGatewayAccountBalance(setting.GetGatewayPricingBaseUrl(), tok, uid)
		}
		return FetchGatewayBalance(setting.GetGatewayBaseUrl(), setting.GetGatewayKey())
	}
}

// FetchGatewayAccountBalance 用控制台系统访问令牌查网关账户余额（/api/user/self）。
func FetchGatewayAccountBalance(consoleUrl, accessToken, userId string) (service.GatewayBalance, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(consoleUrl, "/")+"/api/user/self", nil)
	if err != nil {
		return service.GatewayBalance{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("EasyAPI-User", userId)
	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return service.GatewayBalance{}, fmt.Errorf("请求网关失败：%w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return service.GatewayBalance{}, err
	}
	if res.StatusCode >= 400 {
		return service.GatewayBalance{}, gatewayCallError(res.StatusCode, "/api/user/self", gatewayErrorMessage(raw))
	}
	var body struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Quota     float64 `json:"quota"`
			UsedQuota float64 `json:"used_quota"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return service.GatewayBalance{}, fmt.Errorf("网关响应解析失败：%s", truncateForLog(strings.TrimSpace(string(raw))))
	}
	if !body.Success {
		msg := body.Message
		if msg == "" {
			msg = "访问令牌或用户 ID 不对"
		}
		return service.GatewayBalance{}, errors.New("网关账户查询失败：" + msg)
	}
	return service.GatewayBalance{
		RemainUSD: body.Data.Quota / gatewayQuotaPerUSD,
		UsedUSD:   body.Data.UsedQuota / gatewayQuotaPerUSD,
		Source:    "account",
	}, nil
}

// FetchGatewayBalance 用 sk 令牌查令牌额度与累计用量（账单接口）。
func FetchGatewayBalance(baseUrl, apiKey string) (service.GatewayBalance, error) {
	p := NewEasyAPIProvider(baseUrl, apiKey, nil)
	p.client = &http.Client{Timeout: 15 * time.Second}
	var sub struct {
		HardLimitUSD float64 `json:"hard_limit_usd"`
	}
	raw, err := p.doRaw(http.MethodGet, "/v1/dashboard/billing/subscription", nil, "")
	if err != nil {
		return service.GatewayBalance{}, err
	}
	if err := json.Unmarshal(raw, &sub); err != nil {
		return service.GatewayBalance{}, fmt.Errorf("网关响应解析失败：%s", truncateForLog(strings.TrimSpace(string(raw))))
	}
	out := service.GatewayBalance{Source: "token"}
	var usage struct {
		TotalUsage float64 `json:"total_usage"`
	}
	end := time.Now()
	start := end.AddDate(0, 0, -30)
	raw, err = p.doRaw(http.MethodGet, fmt.Sprintf("/v1/dashboard/billing/usage?start_date=%s&end_date=%s", start.Format("2006-01-02"), end.Format("2006-01-02")), nil, "")
	if err == nil && json.Unmarshal(raw, &usage) == nil {
		out.UsedUSD = usage.TotalUsage / 100
	}
	if sub.HardLimitUSD >= gatewayUnlimitedSentinel {
		out.Unlimited = true
		return out, nil
	}
	// 账单接口的 hard_limit 是「剩余 + 已用」的总额度，剩余要减掉已用
	out.RemainUSD = math.Round((sub.HardLimitUSD-out.UsedUSD)*100) / 100
	return out, nil
}
