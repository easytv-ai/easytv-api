package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// ===== 会员计划 / 积分包 / 模型定价 / 促销配置（API.md 6.1）=====

// MembershipPlan 会员计划（运营维护）。
type MembershipPlan struct {
	Id            int     `json:"id" gorm:"comment:计划ID"`
	Code          string  `json:"code" gorm:"type:varchar(20);uniqueIndex;comment:starter/creator/pro/flagship"`
	Name          string  `json:"name" gorm:"type:varchar(50);comment:名称"`
	Featured      bool    `json:"featured" gorm:"default:false;comment:推荐"`
	MonthlyOrigin float64 `json:"monthly_origin" gorm:"type:decimal(10,2);default:0;comment:月付原价"`
	MonthlyPrice  float64 `json:"monthly_price" gorm:"type:decimal(10,2);default:0;comment:月付现价"`
	MonthlyPoints int     `json:"monthly_points" gorm:"default:0;comment:每月积分（单席）"`
	AddOnPoints   int     `json:"add_on_points" gorm:"default:0;comment:加购积分（单席）"`
	AddOnPrice    float64 `json:"add_on_price" gorm:"type:decimal(10,2);default:0;comment:加购价格（单席）"`
	Concurrent    int     `json:"concurrent" gorm:"default:1;comment:并发路数"`
	StorageGb     int     `json:"storage_gb" gorm:"default:0;comment:云存储 GB"`
	SortOrder     int     `json:"sort_order" gorm:"default:0;index"`
	Status        int     `json:"status" gorm:"index;default:1;comment:1 上架 2 下架"`
	CreatedAt     int64   `json:"created_at" gorm:"bigint"`
	UpdatedAt     int64   `json:"updated_at" gorm:"bigint"`
}

func (MembershipPlan) TableName() string { return "membership_plan" }

// PointPack 积分包（运营维护）。
type PointPack struct {
	Id          int     `json:"id" gorm:"comment:积分包ID"`
	Code        string  `json:"code" gorm:"type:varchar(20);uniqueIndex;comment:p30/p90/p365/p730"`
	Points      int     `json:"points" gorm:"default:0"`
	ValidDays   int     `json:"valid_days" gorm:"default:30;comment:有效天数"`
	Origin      float64 `json:"origin" gorm:"type:decimal(10,2);default:0;comment:原价"`
	MemberPrice float64 `json:"member_price" gorm:"type:decimal(10,2);default:0;comment:会员价"`
	Featured    bool    `json:"featured" gorm:"default:false"`
	SortOrder   int     `json:"sort_order" gorm:"default:0;index"`
	Status      int     `json:"status" gorm:"index;default:1;comment:1 上架 2 下架"`
	CreatedAt   int64   `json:"created_at" gorm:"bigint"`
	UpdatedAt   int64   `json:"updated_at" gorm:"bigint"`
}

func (PointPack) TableName() string { return "point_pack" }

// ModelPrice 模型积分定价。
type ModelPrice struct {
	Id                  int    `json:"id" gorm:"comment:定价ID"`
	Model               string `json:"model" gorm:"type:varchar(100);uniqueIndex;comment:模型标识（与 EASYAPI_*_MODELS 一致）"`
	Type                string `json:"type" gorm:"type:varchar(20);index;comment:text/image/video/audio"`
	Label               string `json:"label" gorm:"type:varchar(100);default:'';comment:展示名"`
	Unit                string `json:"unit" gorm:"type:varchar(20);default:'call';comment:second/image/call"`
	PointsPerUnit       int    `json:"points_per_unit" gorm:"default:0;comment:标准价"`
	MemberPointsPerUnit int    `json:"member_points_per_unit" gorm:"default:0;comment:会员价，0 同标准价"`
	PromoPointsPerUnit  int    `json:"promo_points_per_unit" gorm:"default:0;comment:促销价（T18），0 无促销；有效期内取 min(标准价/会员价, 促销价)"`
	PromoLabel          string `json:"promo_label" gorm:"type:varchar(20);default:'';comment:促销标签，如「首发 5 折」"`
	PromoStartsAt       int64  `json:"promo_starts_at" gorm:"bigint;default:0;comment:促销开始（Unix 秒），0 立即"`
	PromoEndsAt         int64  `json:"promo_ends_at" gorm:"bigint;default:0;comment:促销结束（Unix 秒），0 不限"`
	// Durations 该模型支持的时长（秒，逗号分隔，如 "4,8,12"）；空表示不限。
	// 各家模型允许的秒数不一样，网关只会在提交时回 400 unsupported duration，
	// 所以这里声明一次，前端限制选项、后端提交前拦下，避免白跑一趟。
	Durations string `json:"durations" gorm:"type:varchar(60);default:'';comment:支持的时长秒数，逗号分隔，空=不限"`
	// Resolutions 该模型支持的分辨率（逗号分隔，如 "512P,768P,1080P"）；空表示不限。
	// 和 Durations 同一个问题：创作台给的 480P/720P/1080P 三档不是每个模型都收。
	Resolutions string `json:"resolutions" gorm:"type:varchar(120);default:'';comment:支持的分辨率，逗号分隔，空=不限"`
	// FrameMatch 图生视频时首帧图必须与输出尺寸完全一致（sora 系）。
	// 置 1 后服务端会在提交前把首帧裁成输出尺寸，否则网关回
	// 400「Inpaint image must match the requested width and height」。
	FrameMatch int `json:"frame_match" gorm:"default:0;comment:1=首帧需与输出同尺寸"`
	// ImageInput 图片模型怎么接收参考图 / 原图（F01）：none 不支持；edits 走 /v1/images/edits 表单上传（gpt-image 系）；
	// url 在 /v1/images/generations 里带 image 地址（seedream / qwen-image / gemini image 等）。空串按模型名推断，见 EffectiveImageInput。
	ImageInput string `json:"image_input" gorm:"type:varchar(10);default:'';comment:参考图输入方式 none/edits/url，空=按模型名推断"`
	// Abilities 模型能干的媒体后处理能力（API.md 23），逗号分隔，如 "upscale,remove_subtitle"。
	// 媒体工具条按能力挑模型：打了标签的模型对应按钮即从「暂未接入」变可用，不用发版。
	// 空串按模型名推断，见 EffectiveAbilities / InferAbilities。
	Abilities string `json:"abilities" gorm:"type:varchar(255);default:'';comment:媒体能力标签，逗号分隔，空=按模型名推断"`
	Status    int    `json:"status" gorm:"index;default:1;comment:1 生效 2 停用"`
	CreatedAt  int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt  int64  `json:"updated_at" gorm:"bigint"`

	// 以下为列表接口填充的展示字段（非表字段），见 FillEffective
	PromoActive                  bool   `json:"promo_active" gorm:"-"`
	EffectivePointsPerUnit       int    `json:"effective_points_per_unit" gorm:"-"`
	EffectiveMemberPointsPerUnit int    `json:"effective_member_points_per_unit" gorm:"-"`
	CampaignPromoLabel           string `json:"campaign_promo_label,omitempty" gorm:"-"`
}

func (ModelPrice) TableName() string { return "model_price" }

// 图片模型接收参考图的方式（model_price.image_input）。
const (
	ImageInputNone  = "none"
	ImageInputEdits = "edits"
	ImageInputUrl   = "url"
)

// IsValidImageInput 管理端可写的值（空串表示按模型名推断）。
func IsValidImageInput(v string) bool {
	return v == "" || v == ImageInputNone || v == ImageInputEdits || v == ImageInputUrl
}

// EffectiveImageInput 声明了就用声明的，否则按模型名推断。
func (p *ModelPrice) EffectiveImageInput() string {
	if p.ImageInput != "" {
		return p.ImageInput
	}
	return InferImageInput(p.Model)
}

// InferImageInput 按网关 /pricing 的模型说明推断（2026-09-13）：OpenAI 图像模型走 edits 表单上传，
// 标明「生成与编辑」「参考图」的其他家模型在 generations 里带图片地址；dall-e-3 等纯文生图不支持。
func InferImageInput(modelName string) string {
	m := strings.ToLower(modelName)
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	switch {
	case strings.HasPrefix(m, "gpt-image"), strings.HasPrefix(m, "chatgpt-image"), m == "dall-e-2":
		return ImageInputEdits
	case strings.HasPrefix(m, "seedream"), strings.HasPrefix(m, "seededit"), strings.HasPrefix(m, "qwen-image"),
		strings.HasPrefix(m, "gemini") && strings.HasSuffix(m, "-image"), strings.HasPrefix(m, "nano-banana"),
		strings.Contains(m, "kontext"):
		return ImageInputUrl
	}
	return ImageInputNone
}

// AllowedDurations 解析 Durations；空或全非法返回 nil（表示不限）。
func (p *ModelPrice) AllowedDurations() []int {
	if strings.TrimSpace(p.Durations) == "" {
		return nil
	}
	out := []int{}
	for _, part := range strings.Split(p.Durations, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && n > 0 {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// DurationAllowed 时长是否被该模型接受；未声明时一律放过。
func (p *ModelPrice) DurationAllowed(seconds int) bool {
	allowed := p.AllowedDurations()
	if allowed == nil || seconds <= 0 {
		return true
	}
	for _, n := range allowed {
		if n == seconds {
			return true
		}
	}
	return false
}

// NormalizeDurations 清洗管理端传来的时长串：去空白、去非法、去重、升序。
func NormalizeDurations(raw string) string {
	seen := map[int]bool{}
	nums := []int{}
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n <= 0 || n > 600 || seen[n] {
			continue
		}
		seen[n] = true
		nums = append(nums, n)
	}
	sort.Ints(nums)
	parts := make([]string, 0, len(nums))
	for _, n := range nums {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ",")
}

// AllowedResolutions 解析 Resolutions（统一成大写去空白）；空返回 nil（不限）。
func (p *ModelPrice) AllowedResolutions() []string {
	if strings.TrimSpace(p.Resolutions) == "" {
		return nil
	}
	out := []string{}
	for _, part := range strings.Split(p.Resolutions, ",") {
		v := strings.ToUpper(strings.TrimSpace(part))
		if v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ResolutionAllowed 分辨率是否被该模型接受；未声明或空值一律放过（大小写不敏感）。
func (p *ModelPrice) ResolutionAllowed(res string) bool {
	allowed := p.AllowedResolutions()
	res = strings.ToUpper(strings.TrimSpace(res))
	if allowed == nil || res == "" {
		return true
	}
	for _, v := range allowed {
		if v == res {
			return true
		}
	}
	return false
}

// NormalizeResolutions 清洗管理端输入：去空白、大写、去重、保序。
func NormalizeResolutions(raw string) string {
	seen := map[string]bool{}
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		v := strings.ToUpper(strings.TrimSpace(part))
		if v == "" || len(v) > 12 || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return strings.Join(out, ",")
}

// IsPromoActive 促销价是否在有效期内（促销价必须大于 0）。
func (p *ModelPrice) IsPromoActive(now int64) bool {
	if p.PromoPointsPerUnit <= 0 {
		return false
	}
	if p.PromoStartsAt > 0 && now < p.PromoStartsAt {
		return false
	}
	if p.PromoEndsAt > 0 && now >= p.PromoEndsAt {
		return false
	}
	return true
}

// EffectiveUnitPoints 实际结算单价：会员取会员价（0 同标准价）；促销有效期内再取 min(该价, 促销价)；
// 进行中的活动价（campaign.model_promo）再取更低的那档。
func (p *ModelPrice) EffectiveUnitPoints(isMember bool, now int64) int {
	unit := p.PointsPerUnit
	if isMember && p.MemberPointsPerUnit > 0 {
		unit = p.MemberPointsPerUnit
	}
	if p.IsPromoActive(now) && p.PromoPointsPerUnit < unit {
		unit = p.PromoPointsPerUnit
	}
	if _, pts, ok := CampaignPromoFor(p.Model, p.PointsPerUnit, now); ok && pts > 0 && pts < unit {
		unit = pts
	}
	return unit
}

// FillEffective 填充 promo_active / effective_* 展示字段。不改表上的促销四字段，避免管理端把活动价写回模型定价。
func (p *ModelPrice) FillEffective(now int64) {
	p.EffectivePointsPerUnit = p.EffectiveUnitPoints(false, now)
	p.EffectiveMemberPointsPerUnit = p.EffectiveUnitPoints(true, now)
	listed := p.PointsPerUnit
	p.PromoActive = listed > 0 && p.EffectivePointsPerUnit > 0 && p.EffectivePointsPerUnit < listed
	p.CampaignPromoLabel = ""
	if offer, pts, ok := CampaignPromoFor(p.Model, listed, now); ok && pts > 0 && pts == p.EffectivePointsPerUnit {
		p.CampaignPromoLabel = offer.Label
	}
}

// FillModelPricesEffective 批量填充展示字段。
func FillModelPricesEffective(list []*ModelPrice) {
	now := common.GetTimestamp()
	for _, p := range list {
		p.FillEffective(now)
	}
}

const (
	PriceUnitSecond = "second"
	PriceUnitImage  = "image"
	PriceUnitCall   = "call"

	BillingStatusOn  = 1
	BillingStatusOff = 2

	SettingKeyBillingPromo = "BillingPromo"

	// 顶部倒计时促销条（T18）：与 setting.GrowthPromoBanner* 同键，BillingPromo 视图里以 banner_* 读写
	SettingKeyPromoBannerEnabled = "GrowthPromoBannerEnabled"
	SettingKeyPromoBannerLink    = "GrowthPromoBannerLink"
	DefaultPromoBannerLink       = "/pricing"
)

// BillingPromo 促销与团队配置（setting 表 BillingPromo，JSON）。
type BillingPromo struct {
	Enabled       bool               `json:"enabled"`
	Title         string             `json:"title"`
	Subtitle      string             `json:"subtitle"`
	EndsAt        int64              `json:"ends_at"`
	CycleRate     map[string]float64 `json:"cycle_rate"`
	CycleOffLabel map[string]string  `json:"cycle_off_label"`
	TeamSeatMin   int                `json:"team_seat_min"`
	TeamSeatMax   int                `json:"team_seat_max"`
	TeamSeatRate  float64            `json:"team_seat_rate"`

	// 顶部倒计时条（T18）：不存进 BillingPromo JSON，读写 GrowthPromoBanner* 两个 setting 键
	BannerEnabled bool   `json:"banner_enabled"`
	BannerLink    string `json:"banner_link"`
}

// DefaultBillingPromo 与 easytv-web pricing-plans.ts 一致的默认值。
func DefaultBillingPromo() BillingPromo {
	return BillingPromo{
		Enabled:       true,
		Title:         "年会员最低 6.2 折",
		Subtitle:      "创作会员限时优惠，视频模型会员价结算",
		EndsAt:        0,
		CycleRate:     map[string]float64{"month": 1, "quarter": 0.85, "year": 0.62},
		CycleOffLabel: map[string]string{"quarter": "8.5 折", "year": "6.2 折"},
		TeamSeatMin:   2,
		TeamSeatMax:   10,
		TeamSeatRate:  0.9,
	}
}

// GetBillingPromo 读取促销配置；未配置或解析失败用默认值。
func GetBillingPromo() BillingPromo {
	raw := GetSetting(SettingKeyBillingPromo)
	promo := DefaultBillingPromo()
	if raw == "" {
		return promo
	}
	var parsed BillingPromo
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return promo
	}
	if parsed.CycleRate == nil {
		parsed.CycleRate = promo.CycleRate
	}
	if parsed.CycleOffLabel == nil {
		parsed.CycleOffLabel = promo.CycleOffLabel
	}
	if parsed.TeamSeatMin <= 0 {
		parsed.TeamSeatMin = promo.TeamSeatMin
	}
	if parsed.TeamSeatMax <= 0 {
		parsed.TeamSeatMax = promo.TeamSeatMax
	}
	if parsed.TeamSeatRate <= 0 {
		parsed.TeamSeatRate = promo.TeamSeatRate
	}
	parsed.BannerEnabled, parsed.BannerLink = promoBannerSettings()
	return parsed
}

// promoBannerSettings 顶部促销条开关与链接（与 setting.GetGrowthConfig 同源）。
func promoBannerSettings() (bool, string) {
	enabled := GetSetting(SettingKeyPromoBannerEnabled)
	link := GetSetting(SettingKeyPromoBannerLink)
	if link == "" {
		link = DefaultPromoBannerLink
	}
	return enabled == "true" || enabled == "1", link
}

// SaveBillingPromo 写入促销配置；banner_* 写到 GrowthPromoBanner* 键，不进 JSON。
func SaveBillingPromo(promo BillingPromo) error {
	bannerEnabled, bannerLink := promo.BannerEnabled, promo.BannerLink
	promo.BannerEnabled, promo.BannerLink = false, ""
	raw, err := json.Marshal(promo)
	if err != nil {
		return err
	}
	if err := UpdateSetting(SettingKeyBillingPromo, string(raw)); err != nil {
		return err
	}
	val := "false"
	if bannerEnabled {
		val = "true"
	}
	if err := UpdateSetting(SettingKeyPromoBannerEnabled, val); err != nil {
		return err
	}
	if bannerLink == "" {
		bannerLink = DefaultPromoBannerLink
	}
	return UpdateSetting(SettingKeyPromoBannerLink, bannerLink)
}

// EffectiveCycleRate 促销过期后周期系数按 1 计。
func (p BillingPromo) EffectiveCycleRate(cycle string) float64 {
	if !p.Enabled || (p.EndsAt > 0 && p.EndsAt <= common.GetTimestamp()) {
		return 1
	}
	if r, ok := p.CycleRate[cycle]; ok && r > 0 {
		return r
	}
	return 1
}

// ----- 会员计划 -----

func (p *MembershipPlan) Insert() error {
	now := common.GetTimestamp()
	p.CreatedAt, p.UpdatedAt = now, now
	if p.Status == 0 {
		p.Status = BillingStatusOn
	}
	return DB.Create(p).Error
}

func GetMembershipPlanById(id int) (*MembershipPlan, error) {
	p := &MembershipPlan{}
	if err := DB.Where("id = ?", id).First(p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

func GetMembershipPlanByCode(code string) (*MembershipPlan, error) {
	p := &MembershipPlan{}
	if err := DB.Where("code = ?", code).First(p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// GetMembershipPlans onlyOn=true 仅上架。
func GetMembershipPlans(onlyOn bool) (list []*MembershipPlan, err error) {
	query := DB.Model(&MembershipPlan{})
	if onlyOn {
		query = query.Where("status = ?", BillingStatusOn)
	}
	err = query.Order("sort_order asc, id asc").Find(&list).Error
	return list, err
}

func UpdateMembershipPlanFields(id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	return updateOne(DB.Model(&MembershipPlan{}).Where("id = ?", id), fields)
}

func DeleteMembershipPlan(id int) error {
	return deleteOne(DB.Where("id = ?", id), &MembershipPlan{})
}

// ----- 积分包 -----

func (p *PointPack) Insert() error {
	now := common.GetTimestamp()
	p.CreatedAt, p.UpdatedAt = now, now
	if p.Status == 0 {
		p.Status = BillingStatusOn
	}
	return DB.Create(p).Error
}

func GetPointPackById(id int) (*PointPack, error) {
	p := &PointPack{}
	if err := DB.Where("id = ?", id).First(p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

func GetPointPackByCode(code string) (*PointPack, error) {
	p := &PointPack{}
	if err := DB.Where("code = ?", code).First(p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

func GetPointPacks(onlyOn bool) (list []*PointPack, err error) {
	query := DB.Model(&PointPack{})
	if onlyOn {
		query = query.Where("status = ?", BillingStatusOn)
	}
	err = query.Order("sort_order asc, id asc").Find(&list).Error
	return list, err
}

func UpdatePointPackFields(id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	return updateOne(DB.Model(&PointPack{}).Where("id = ?", id), fields)
}

func DeletePointPack(id int) error {
	return deleteOne(DB.Where("id = ?", id), &PointPack{})
}

// ----- 模型定价 -----

func (p *ModelPrice) Insert() error {
	now := common.GetTimestamp()
	p.CreatedAt, p.UpdatedAt = now, now
	if p.Status == 0 {
		p.Status = BillingStatusOn
	}
	if p.Unit == "" {
		p.Unit = PriceUnitCall
	}
	return DB.Create(p).Error
}

// GetModelPrice 生效中的定价；未定价返回 ErrNotFound。
func GetModelPrice(modelName string) (*ModelPrice, error) {
	p := &ModelPrice{}
	if err := DB.Where("model = ? AND status = ?", modelName, BillingStatusOn).First(p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

func GetModelPrices(onlyOn bool) (list []*ModelPrice, err error) {
	query := DB.Model(&ModelPrice{})
	if onlyOn {
		query = query.Where("status = ?", BillingStatusOn)
	}
	err = query.Order("type asc, id asc").Find(&list).Error
	return list, err
}

// modelPriceSortFields 管理端可分页排序字段白名单（防注入）。
var modelPriceSortFields = map[string]string{
	"model":                 "model",
	"type":                  "type",
	"unit":                  "unit",
	"points_per_unit":       "points_per_unit",       // 标准价
	"promo_points_per_unit": "promo_points_per_unit", // 促销价
	"promo_ends_at":         "promo_ends_at",         // 促销结束
}

// GetModelPricesPage 管理端分页查询模型定价。
// sortBy/sortOrder 命中白名单时按该列排序（id 兜底保证分页稳定），否则默认 type asc, id asc。
func GetModelPricesPage(pageInfo *common.PageInfo, sortBy, sortOrder string) (list []*ModelPrice, total int64, err error) {
	query := DB.Model(&ModelPrice{})
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	order, ok := modelPriceSortFields[sortBy]
	if !ok {
		order = "type"
		sortOrder = "asc"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		sortOrder = "asc"
	}
	err = query.Order(order + " " + sortOrder + ", id asc").
		Limit(pageInfo.GetPageSize()).
		Offset(pageInfo.GetStartIdx()).
		Find(&list).Error
	return list, total, err
}

// GetModelPriceById 管理端单条（任意状态）。
func GetModelPriceById(id int) (*ModelPrice, error) {
	p := &ModelPrice{}
	if err := DB.Where("id = ?", id).First(p).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return p, nil
}

func UpdateModelPriceFields(id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	return updateOne(DB.Model(&ModelPrice{}).Where("id = ?", id), fields)
}

func DeleteModelPrice(id int) error {
	return deleteOne(DB.Where("id = ?", id), &ModelPrice{})
}

// SeedModelsFromEnvIfEmpty 存量迁移：model_price 表为空且存在旧环境变量 EASYAPI_*_MODELS 时，
// 按其清单导入（积分单价默认 1，运营后续经管理端调整）。env 仅作首次迁移，不再参与运行期。
func SeedModelsFromEnvIfEmpty() {
	var count int64
	DB.Model(&ModelPrice{}).Count(&count)
	if count > 0 {
		return
	}
	envLists := []struct {
		taskType string
		envKey   string
	}{
		{TaskTypeText, "EASYAPI_TEXT_MODELS"},
		{TaskTypeImage, "EASYAPI_IMAGE_MODELS"},
		{TaskTypeVideo, "EASYAPI_VIDEO_MODELS"},
		{TaskTypeAudio, "EASYAPI_AUDIO_MODELS"},
	}
	inserted := 0
	for _, el := range envLists {
		raw := common.GetEnvOrDefaultString(el.envKey, "")
		if raw == "" {
			continue
		}
		for _, item := range strings.Split(raw, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			name, label := item, item
			if idx := strings.Index(item, ":"); idx > 0 {
				name = strings.TrimSpace(item[:idx])
				label = strings.TrimSpace(item[idx+1:])
				if label == "" {
					label = name
				}
			}
			p := &ModelPrice{Model: name, Type: el.taskType, Label: label, Unit: PriceUnitCall, PointsPerUnit: 1}
			if err := p.Insert(); err != nil {
				common.SysError(fmt.Sprintf("seed model price from env failed: %s: %v", name, err))
				continue
			}
			inserted++
		}
	}
	if inserted > 0 {
		common.SysLog(fmt.Sprintf("seeded %d model prices from legacy env EASYAPI_*_MODELS", inserted))
	}
}

// ----- 通用小工具 -----

func updateOne(query *gorm.DB, fields map[string]any) error {
	result := query.Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func deleteOne(query *gorm.DB, target any) error {
	result := query.Delete(target)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
