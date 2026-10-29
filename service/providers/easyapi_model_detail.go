package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"easytv-api/common"
	"easytv-api/setting"
)

// ===== 网关模型详情（GET /pricing/detail，前台 GET /generation/models/detail 用）=====
//
// 网关的 /pricing（列表）只给选择器要的字段，详情页要的说明文案、供应商、参数属性表、
// 端点定义走 /pricing/detail。这里只取展示元数据：上游的成本口径（model_ratio / model_price /
// 各项 ratio / quota_type / enable_groups / billing_expr）不下发给前台，见 API.md 28。
//
// 可见性由网关按令牌分组判定（网关侧模型不存在 / 已禁用 / 分组不可见统一回 404），
// 本服务在此之前已按 model_price 过滤过一次。

// GatewayEndpoint 端点定义（网关 supported_endpoint / endpoints 的条目）。
type GatewayEndpoint struct {
	Path   string `json:"path"`
	Method string `json:"method"`
}

// GatewayVendor 模型供应商。
type GatewayVendor struct {
	Id          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
}

// GatewayModelDetail 网关模型详情里的展示元数据（字段名沿用网关）。
//
// Endpoints 原样透传：网关那边它就是 model.endpoints 列的原始 JSON，形态由上游数据决定——
// 2026-09-17 实测线上都是端点类型数组（["openai"]、["anthropic","openai"]，与
// supported_endpoint_types 同值），上游迁移文档里给的是运营配了自定义端点时的
// {"image-edit":{"path":…,"method":…}} 形态。两种都要能过，所以不做类型化。
type GatewayModelDetail struct {
	Model                  string                     `json:"model_name"`
	Description            string                     `json:"description"`
	Icon                   string                     `json:"icon"`
	Tags                   string                     `json:"tags"`
	Category               string                     `json:"category"`
	Vendor                 *GatewayVendor             `json:"vendor"`
	Endpoints              json.RawMessage            `json:"endpoints,omitempty"`
	SupportedEndpoint      map[string]GatewayEndpoint `json:"supported_endpoint"`
	SupportedEndpointTypes []string                   `json:"supported_endpoint_types"`
	Params                 json.RawMessage            `json:"params"`
	PricingVersion         string                     `json:"pricing_version"`
}

// gatewayPricingDetailData 网关 data 字段（Pricing 结构平铺，这里只取展示元数据）。
type gatewayPricingDetailData struct {
	ModelName              string          `json:"model_name"`
	Description            string          `json:"description"`
	Icon                   string          `json:"icon"`
	Tags                   string          `json:"tags"`
	Category               string          `json:"category"`
	Vendor                 *GatewayVendor  `json:"vendor"`
	Endpoints              json.RawMessage `json:"endpoints"`
	SupportedEndpointTypes []string        `json:"supported_endpoint_types"`
	Params                 json.RawMessage `json:"params"`
}

// gatewayPricingDetailResp 网关 /pricing/detail 响应：supported_endpoint 与 pricing_version 在顶层。
type gatewayPricingDetailResp struct {
	Success           bool                       `json:"success"`
	Message           string                     `json:"message"`
	Data              gatewayPricingDetailData   `json:"data"`
	SupportedEndpoint map[string]GatewayEndpoint `json:"supported_endpoint"`
	PricingVersion    string                     `json:"pricing_version"`
}

// fetchModelDetail 拉取单个模型的详情。path 由调用方给出，测试可直接指向假网关。
func fetchModelDetail(baseUrl, path, apiKey, modelName, lang string) (*GatewayModelDetail, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("网关令牌未配置（EasyAPIGatewayKey 为空）")
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	data, err := gatewayGetLang(baseUrl, path+sep+"model_name="+url.QueryEscape(modelName), apiKey, lang)
	if err != nil {
		return nil, err
	}
	var body gatewayPricingDetailResp
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	if !body.Success && body.Message != "" {
		return nil, fmt.Errorf("网关返回：%s", body.Message)
	}
	d := body.Data
	return &GatewayModelDetail{
		Model:                  d.ModelName,
		Description:            d.Description,
		Icon:                   d.Icon,
		Tags:                   d.Tags,
		Category:               d.Category,
		Vendor:                 d.Vendor,
		Endpoints:              d.Endpoints,
		SupportedEndpoint:      body.SupportedEndpoint,
		SupportedEndpointTypes: d.SupportedEndpointTypes,
		Params:                 d.Params,
		PricingVersion:         body.PricingVersion,
	}, nil
}

// gatewayDetailCacheTtl 详情缓存时长：与网关自己的定价缓存同量级（1 分钟），
// 免得每次打开详情页都放大成一次网关请求。
const gatewayDetailCacheTtl = 60 * time.Second

// gatewayDetailCacheErrTtl 失败结果的缓存时长。前台是匿名接口，网关挂的时候
// 不能每个请求都去敲一次网关、写一行日志；失败也记一小会儿。
const gatewayDetailCacheErrTtl = 30 * time.Second

// gatewayDetailCacheMax 缓存条目上限；超了先清过期项（键是「模型 + 语言」，量级很小）。
const gatewayDetailCacheMax = 256

type gatewayDetailCacheItem struct {
	detail  *GatewayModelDetail
	err     error
	expires time.Time
}

var gatewayDetailCache = struct {
	mu    sync.Mutex
	items map[string]gatewayDetailCacheItem
}{items: map[string]gatewayDetailCacheItem{}}

// FetchGatewayModelDetail 拉取网关模型详情（展示元数据），按「模型 + 语言」缓存：
// 成功 60 秒、失败 30 秒。网关未配置或调用失败返回 error，调用方降级（本地定价照常下发）。
// 返回的 detail 由缓存共享，调用方只读。
func FetchGatewayModelDetail(modelName, lang string) (*GatewayModelDetail, error) {
	lang = strings.TrimSpace(lang)
	key := modelName + "|" + lang
	if detail, err, hit := gatewayDetailCacheGet(key); hit {
		return detail, err
	}
	detail, err := fetchGatewayModelDetailUncached(modelName, lang)
	gatewayDetailCachePut(key, detail, err)
	return detail, err
}

func fetchGatewayModelDetailUncached(modelName, lang string) (*GatewayModelDetail, error) {
	if !setting.IsGatewayEnabled() {
		return nil, errors.New("网关未开启（EasyAPIGatewayKey 为空或 EasyAPIGatewayEnabled=false）")
	}
	detail, err := fetchModelDetail(
		setting.GetGatewayPricingBaseUrl(),
		setting.GetGatewayPricingDetailPath(),
		setting.GetGatewayKey(),
		modelName,
		lang,
	)
	if err != nil {
		// 日志写在这里：命中失败缓存的请求不会再打网关，也就不再刷日志
		common.SysLog("fetch gateway model detail failed: " + modelName + ": " + err.Error())
		return nil, err
	}
	return detail, nil
}

func gatewayDetailCacheGet(key string) (*GatewayModelDetail, error, bool) {
	gatewayDetailCache.mu.Lock()
	defer gatewayDetailCache.mu.Unlock()
	item, ok := gatewayDetailCache.items[key]
	if !ok || time.Now().After(item.expires) {
		return nil, nil, false
	}
	return item.detail, item.err, true
}

func gatewayDetailCachePut(key string, detail *GatewayModelDetail, err error) {
	ttl := gatewayDetailCacheTtl
	if err != nil {
		ttl = gatewayDetailCacheErrTtl
	}
	gatewayDetailCache.mu.Lock()
	defer gatewayDetailCache.mu.Unlock()
	if len(gatewayDetailCache.items) >= gatewayDetailCacheMax {
		now := time.Now()
		for k, v := range gatewayDetailCache.items {
			if now.After(v.expires) {
				delete(gatewayDetailCache.items, k)
			}
		}
	}
	gatewayDetailCache.items[key] = gatewayDetailCacheItem{detail: detail, err: err, expires: time.Now().Add(ttl)}
}
