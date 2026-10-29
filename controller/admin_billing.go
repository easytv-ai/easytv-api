package controller

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/service/providers"

	"github.com/gin-gonic/gin"
)

// ===== 付费域：管理侧（API.md 6.2）=====

// ----- 会员计划 -----

type planPayload struct {
	Code          *string  `json:"code"`
	Name          *string  `json:"name"`
	Featured      *bool    `json:"featured"`
	MonthlyOrigin *float64 `json:"monthly_origin"`
	MonthlyPrice  *float64 `json:"monthly_price"`
	MonthlyPoints *int     `json:"monthly_points"`
	AddOnPoints   *int     `json:"add_on_points"`
	AddOnPrice    *float64 `json:"add_on_price"`
	Concurrent    *int     `json:"concurrent"`
	StorageGb     *int     `json:"storage_gb"`
	SortOrder     *int     `json:"sort_order"`
	Status        *int     `json:"status"`
}

func (p *planPayload) fields() (map[string]any, string) {
	f := map[string]any{}
	if p.Code != nil {
		code := strings.ToLower(trimLimit(*p.Code, 20))
		if code == "" {
			return nil, "请填写计划编码"
		}
		f["code"] = code
	}
	if p.Name != nil {
		name := trimLimit(*p.Name, 50)
		if name == "" {
			return nil, "请填写计划名称"
		}
		f["name"] = name
	}
	if p.Featured != nil {
		f["featured"] = *p.Featured
	}
	if p.MonthlyOrigin != nil {
		f["monthly_origin"] = *p.MonthlyOrigin
	}
	if p.MonthlyPrice != nil {
		f["monthly_price"] = *p.MonthlyPrice
	}
	if p.MonthlyPoints != nil {
		f["monthly_points"] = *p.MonthlyPoints
	}
	if p.AddOnPoints != nil {
		f["add_on_points"] = *p.AddOnPoints
	}
	if p.AddOnPrice != nil {
		f["add_on_price"] = *p.AddOnPrice
	}
	if p.Concurrent != nil {
		f["concurrent"] = *p.Concurrent
	}
	if p.StorageGb != nil {
		f["storage_gb"] = *p.StorageGb
	}
	if p.SortOrder != nil {
		f["sort_order"] = *p.SortOrder
	}
	if p.Status != nil {
		if *p.Status != model.BillingStatusOn && *p.Status != model.BillingStatusOff {
			return nil, "无效的状态"
		}
		f["status"] = *p.Status
	}
	return f, ""
}

func AdminGetPlans(c *gin.Context) {
	list, err := model.GetMembershipPlans(false)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, list)
}

func AdminCreatePlan(c *gin.Context) {
	var req planPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Code == nil || req.Name == nil {
		common.ApiErrorMsg(c, "请填写计划编码与名称")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	p := &model.MembershipPlan{Concurrent: 1}
	applyPlanFields(p, f)
	if err := p.Insert(); err != nil {
		respondModelError(c, err, "创建失败（编码可能重复）")
		return
	}
	common.ApiSuccess(c, p)
}

func applyPlanFields(p *model.MembershipPlan, f map[string]any) {
	if v, ok := f["code"].(string); ok {
		p.Code = v
	}
	if v, ok := f["name"].(string); ok {
		p.Name = v
	}
	if v, ok := f["featured"].(bool); ok {
		p.Featured = v
	}
	if v, ok := f["monthly_origin"].(float64); ok {
		p.MonthlyOrigin = v
	}
	if v, ok := f["monthly_price"].(float64); ok {
		p.MonthlyPrice = v
	}
	if v, ok := f["monthly_points"].(int); ok {
		p.MonthlyPoints = v
	}
	if v, ok := f["add_on_points"].(int); ok {
		p.AddOnPoints = v
	}
	if v, ok := f["add_on_price"].(float64); ok {
		p.AddOnPrice = v
	}
	if v, ok := f["concurrent"].(int); ok {
		p.Concurrent = v
	}
	if v, ok := f["storage_gb"].(int); ok {
		p.StorageGb = v
	}
	if v, ok := f["sort_order"].(int); ok {
		p.SortOrder = v
	}
	if v, ok := f["status"].(int); ok {
		p.Status = v
	}
}

func AdminUpdatePlan(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的计划 ID")
		return
	}
	var req planPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if len(f) == 0 {
		common.ApiErrorMsg(c, "没有需要更新的字段")
		return
	}
	if err := model.UpdateMembershipPlanFields(id, f); err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

func AdminDeletePlan(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的计划 ID")
		return
	}
	if err := model.DeleteMembershipPlan(id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// ----- 积分包 -----

type packPayload struct {
	Code        *string  `json:"code"`
	Points      *int     `json:"points"`
	ValidDays   *int     `json:"valid_days"`
	Origin      *float64 `json:"origin"`
	MemberPrice *float64 `json:"member_price"`
	Featured    *bool    `json:"featured"`
	SortOrder   *int     `json:"sort_order"`
	Status      *int     `json:"status"`
}

func (p *packPayload) fields() (map[string]any, string) {
	f := map[string]any{}
	if p.Code != nil {
		code := strings.ToLower(trimLimit(*p.Code, 20))
		if code == "" {
			return nil, "请填写积分包编码"
		}
		f["code"] = code
	}
	if p.Points != nil {
		if *p.Points <= 0 {
			return nil, "积分数必须大于 0"
		}
		f["points"] = *p.Points
	}
	if p.ValidDays != nil {
		if *p.ValidDays <= 0 {
			return nil, "有效天数必须大于 0"
		}
		f["valid_days"] = *p.ValidDays
	}
	if p.Origin != nil {
		f["origin"] = *p.Origin
	}
	if p.MemberPrice != nil {
		f["member_price"] = *p.MemberPrice
	}
	if p.Featured != nil {
		f["featured"] = *p.Featured
	}
	if p.SortOrder != nil {
		f["sort_order"] = *p.SortOrder
	}
	if p.Status != nil {
		if *p.Status != model.BillingStatusOn && *p.Status != model.BillingStatusOff {
			return nil, "无效的状态"
		}
		f["status"] = *p.Status
	}
	return f, ""
}

func AdminGetPacks(c *gin.Context) {
	list, err := model.GetPointPacks(false)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, list)
}

func AdminCreatePack(c *gin.Context) {
	var req packPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Code == nil || req.Points == nil {
		common.ApiErrorMsg(c, "请填写编码与积分数")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	p := &model.PointPack{ValidDays: 30}
	if v, ok := f["code"].(string); ok {
		p.Code = v
	}
	if v, ok := f["points"].(int); ok {
		p.Points = v
	}
	if v, ok := f["valid_days"].(int); ok {
		p.ValidDays = v
	}
	if v, ok := f["origin"].(float64); ok {
		p.Origin = v
	}
	if v, ok := f["member_price"].(float64); ok {
		p.MemberPrice = v
	}
	if v, ok := f["featured"].(bool); ok {
		p.Featured = v
	}
	if v, ok := f["sort_order"].(int); ok {
		p.SortOrder = v
	}
	if v, ok := f["status"].(int); ok {
		p.Status = v
	}
	if err := p.Insert(); err != nil {
		respondModelError(c, err, "创建失败（编码可能重复）")
		return
	}
	common.ApiSuccess(c, p)
}

func AdminUpdatePack(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的积分包 ID")
		return
	}
	var req packPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if len(f) == 0 {
		common.ApiErrorMsg(c, "没有需要更新的字段")
		return
	}
	if err := model.UpdatePointPackFields(id, f); err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

func AdminDeletePack(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的积分包 ID")
		return
	}
	if err := model.DeletePointPack(id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// ----- 促销配置 -----

func AdminGetBillingPromo(c *gin.Context) {
	common.ApiSuccess(c, model.GetBillingPromo())
}

func AdminUpdateBillingPromo(c *gin.Context) {
	var promo model.BillingPromo
	if err := common.DecodeJson(c.Request.Body, &promo); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if promo.TeamSeatMin <= 0 || promo.TeamSeatMax < promo.TeamSeatMin {
		common.ApiErrorMsg(c, "团队席位范围不正确")
		return
	}
	if promo.TeamSeatRate <= 0 || promo.TeamSeatRate > 1 {
		common.ApiErrorMsg(c, "团队席位折扣需在 0 到 1 之间")
		return
	}
	for _, k := range []string{model.CycleMonth, model.CycleQuarter, model.CycleYear} {
		if r, ok := promo.CycleRate[k]; !ok || r <= 0 || r > 1 {
			common.ApiErrorMsg(c, fmt.Sprintf("周期折扣 %s 需在 0 到 1 之间", k))
			return
		}
	}
	if err := model.SaveBillingPromo(promo); err != nil {
		respondModelError(c, err, "保存失败")
		return
	}
	common.ApiSuccess(c, model.GetBillingPromo())
}

// ----- 模型定价 -----

type modelPricePayload struct {
	Model               *string `json:"model"`
	Type                *string `json:"type"`
	Label               *string `json:"label"`
	Unit                *string `json:"unit"`
	PointsPerUnit       *int    `json:"points_per_unit"`
	MemberPointsPerUnit *int    `json:"member_points_per_unit"`
	Status              *int    `json:"status"`
	// 促销价（T18）：promo_points_per_unit 置 0 即取消促销
	PromoPointsPerUnit *int    `json:"promo_points_per_unit"`
	PromoLabel         *string `json:"promo_label"`
	PromoStartsAt      *int64  `json:"promo_starts_at"`
	PromoEndsAt        *int64  `json:"promo_ends_at"`
	// Durations 支持的时长秒数，逗号分隔（如 "4,8,12"）；空串=不限
	Durations *string `json:"durations"`
	// Resolutions 支持的分辨率，逗号分隔（如 "512P,768P,1080P"）；空串=不限
	Resolutions *string `json:"resolutions"`
	// FrameMatch 1=图生视频的首帧图必须与输出尺寸一致（服务端会自动裁）
	FrameMatch *int `json:"frame_match"`
	// ImageInput 图片模型参考图输入方式：none / edits / url，空串=按模型名推断
	ImageInput *string `json:"image_input"`
	// Abilities 媒体后处理能力（API.md 23）：upscale / remove_subtitle / vocal_separation / …
	// 逗号分隔，空串=按模型名推断
	Abilities *string `json:"abilities"`
}

func (p *modelPricePayload) fields() (map[string]any, string) {
	f := map[string]any{}
	if p.Model != nil {
		m := trimLimit(*p.Model, 100)
		if m == "" {
			return nil, "请填写模型标识"
		}
		f["model"] = m
	}
	if p.Type != nil {
		if !model.IsValidTaskType(*p.Type) {
			return nil, "无效的任务类型"
		}
		f["type"] = *p.Type
	}
	if p.Label != nil {
		f["label"] = trimLimit(*p.Label, 100)
	}
	if p.Unit != nil {
		switch *p.Unit {
		case model.PriceUnitSecond, model.PriceUnitImage, model.PriceUnitCall:
			f["unit"] = *p.Unit
		default:
			return nil, "无效的计价单位"
		}
	}
	if p.PointsPerUnit != nil {
		if *p.PointsPerUnit < 0 {
			return nil, "单价不能为负"
		}
		f["points_per_unit"] = *p.PointsPerUnit
	}
	if p.MemberPointsPerUnit != nil {
		if *p.MemberPointsPerUnit < 0 {
			return nil, "会员单价不能为负"
		}
		f["member_points_per_unit"] = *p.MemberPointsPerUnit
	}
	if p.Status != nil {
		if *p.Status != model.BillingStatusOn && *p.Status != model.BillingStatusOff {
			return nil, "无效的状态"
		}
		f["status"] = *p.Status
	}
	if p.PromoPointsPerUnit != nil {
		if *p.PromoPointsPerUnit < 0 {
			return nil, "促销价不能为负"
		}
		f["promo_points_per_unit"] = *p.PromoPointsPerUnit
	}
	if p.PromoLabel != nil {
		f["promo_label"] = trimLimit(*p.PromoLabel, 20)
	}
	if p.Durations != nil {
		f["durations"] = model.NormalizeDurations(*p.Durations)
	}
	if p.Resolutions != nil {
		f["resolutions"] = model.NormalizeResolutions(*p.Resolutions)
	}
	if p.FrameMatch != nil {
		if *p.FrameMatch != 0 && *p.FrameMatch != 1 {
			return nil, "首帧尺寸开关只能是 0 或 1"
		}
		f["frame_match"] = *p.FrameMatch
	}
	if p.ImageInput != nil {
		v := strings.TrimSpace(*p.ImageInput)
		if !model.IsValidImageInput(v) {
			return nil, "参考图输入方式只能是 none / edits / url 或留空"
		}
		f["image_input"] = v
	}
	if p.Abilities != nil {
		// 非法值直接丢掉（网关导入的模型清单可能带着我们不认识的能力名），只保留白名单里的
		f["abilities"] = model.NormalizeAbilities(*p.Abilities)
	}
	if p.PromoStartsAt != nil {
		if *p.PromoStartsAt < 0 {
			return nil, "促销开始时间无效"
		}
		f["promo_starts_at"] = *p.PromoStartsAt
	}
	if p.PromoEndsAt != nil {
		if *p.PromoEndsAt < 0 {
			return nil, "促销结束时间无效"
		}
		f["promo_ends_at"] = *p.PromoEndsAt
	}
	if p.PromoStartsAt != nil && p.PromoEndsAt != nil && *p.PromoStartsAt > 0 && *p.PromoEndsAt > 0 && *p.PromoEndsAt <= *p.PromoStartsAt {
		return nil, "促销结束时间需晚于开始时间"
	}
	return f, ""
}

func AdminGetModelPrices(c *gin.Context) {
	list, total, err := model.GetModelPricesPage(
		parsePageInfo(c),
		strings.TrimSpace(c.Query("sort_by")),
		strings.TrimSpace(c.Query("order")),
	)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	model.FillModelPricesEffective(list)
	pagedResponse(c, list, total)
}

// AdminGetModelPrice 单条定价（GET /admin/model-price/:id），附带 promo_active / effective_* 展示字段。
func AdminGetModelPrice(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的定价 ID")
		return
	}
	p, err := model.GetModelPriceById(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	p.FillEffective(common.GetTimestamp())
	common.ApiSuccess(c, p)
}

func AdminCreateModelPrice(c *gin.Context) {
	var req modelPricePayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Model == nil || req.Type == nil || req.PointsPerUnit == nil {
		common.ApiErrorMsg(c, "请填写模型、类型与单价")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	p := &model.ModelPrice{}
	if v, ok := f["model"].(string); ok {
		p.Model = v
	}
	if v, ok := f["type"].(string); ok {
		p.Type = v
	}
	if v, ok := f["label"].(string); ok {
		p.Label = v
	}
	if v, ok := f["unit"].(string); ok {
		p.Unit = v
	}
	if v, ok := f["points_per_unit"].(int); ok {
		p.PointsPerUnit = v
	}
	if v, ok := f["member_points_per_unit"].(int); ok {
		p.MemberPointsPerUnit = v
	}
	if v, ok := f["status"].(int); ok {
		p.Status = v
	}
	if v, ok := f["promo_points_per_unit"].(int); ok {
		p.PromoPointsPerUnit = v
	}
	if v, ok := f["promo_label"].(string); ok {
		p.PromoLabel = v
	}
	if v, ok := f["promo_starts_at"].(int64); ok {
		p.PromoStartsAt = v
	}
	if v, ok := f["promo_ends_at"].(int64); ok {
		p.PromoEndsAt = v
	}
	// 能力标签 / 时长档 / 分辨率 / 首帧适配 / 图片输入：新建时原来被丢掉，运营勾了标签保存后仍是空（2026-09-24 实测发现）
	if v, ok := f["abilities"].(string); ok {
		p.Abilities = v
	}
	if v, ok := f["durations"].(string); ok {
		p.Durations = v
	}
	if v, ok := f["resolutions"].(string); ok {
		p.Resolutions = v
	}
	if v, ok := f["frame_match"].(int); ok {
		p.FrameMatch = v
	}
	if v, ok := f["image_input"].(string); ok {
		p.ImageInput = v
	}
	if err := p.Insert(); err != nil {
		respondModelError(c, err, "创建失败（模型可能已定价）")
		return
	}
	p.FillEffective(common.GetTimestamp())
	common.ApiSuccess(c, p)
}

func AdminUpdateModelPrice(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的定价 ID")
		return
	}
	var req modelPricePayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if len(f) == 0 {
		common.ApiErrorMsg(c, "没有需要更新的字段")
		return
	}
	if err := model.UpdateModelPriceFields(id, f); err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	if p, err := model.GetModelPriceById(id); err == nil {
		p.FillEffective(common.GetTimestamp())
		common.ApiSuccess(c, p)
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminSyncModelPriceGateway 单个模型从网关详情同步 durations / resolutions / abilities（API.md 28.3）。
// 默认只填空字段，`force=1` 覆盖运营手填的值；abilities 只增不减。
func AdminSyncModelPriceGateway(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的定价 ID")
		return
	}
	p, err := model.GetModelPriceById(id)
	if err != nil {
		respondModelError(c, err, "查询定价失败")
		return
	}
	res := providers.SyncModelPriceFromGateway(p, c.Query("force") == "1")
	if res.Error != "" {
		common.ApiErrorMsg(c, res.Error)
		return
	}
	latest, err := model.GetModelPriceById(id)
	if err != nil {
		respondModelError(c, err, "查询定价失败")
		return
	}
	latest.FillEffective(common.GetTimestamp())
	common.ApiSuccess(c, gin.H{"price": latest, "result": res})
}

// AdminSyncAllModelPricesGateway 全部定价行从网关同步，返回逐个结果与汇总。
func AdminSyncAllModelPricesGateway(c *gin.Context) {
	results, err := providers.SyncAllModelPricesFromGateway(c.Query("force") == "1")
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	updated, skipped, failed := 0, 0, 0
	for _, r := range results {
		switch {
		case r.Error != "":
			failed++
		case len(r.Changes) > 0:
			updated++
		default:
			skipped++
		}
	}
	common.ApiSuccess(c, gin.H{"results": results, "updated": updated, "skipped": skipped, "failed": failed})
}

func AdminDeleteModelPrice(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的定价 ID")
		return
	}
	if err := model.DeleteModelPrice(id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// ----- 订单 -----

func AdminGetOrders(c *gin.Context) {
	list, total, err := model.GetOrders(model.OrderFilter{
		UserId:  queryInt(c, "user_id", 0),
		Kind:    strings.TrimSpace(c.Query("kind")),
		Status:  strings.TrimSpace(c.Query("status")),
		Keyword: strings.TrimSpace(c.Query("keyword")),
	}, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

func AdminCloseOrder(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的订单 ID")
		return
	}
	if err := model.CloseOrder(0, id, fmt.Sprintf("管理员 %d 关闭", c.GetInt("id"))); err != nil {
		if errors.Is(err, model.ErrOrderStatus) {
			common.ApiErrorMsg(c, "订单不是待支付状态")
			return
		}
		respondModelError(c, err, "关闭失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminMarkOrderPaid 线下收款 / 联调：手动标记已支付并履约（POST /admin/order/:id/mark-paid）。
func AdminMarkOrderPaid(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的订单 ID")
		return
	}
	if err := service.FulfillOrder(id, "manual"); err != nil {
		if errors.Is(err, model.ErrOrderStatus) {
			common.ApiErrorMsg(c, "订单不是待支付状态")
			return
		}
		respondModelError(c, err, "履约失败")
		return
	}
	common.SysLog(fmt.Sprintf("admin %d marked order %d paid", c.GetInt("id"), id))
	common.ApiSuccess(c, nil)
}

func AdminRefundOrder(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的订单 ID")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = common.DecodeJson(c.Request.Body, &req)
	reason := trimLimit(req.Reason, 255)
	o, err := model.GetOrderById(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if o.Status != model.OrderStatusPaid {
		common.ApiErrorMsg(c, "只有已支付订单可以退款")
		return
	}
	// 1. 渠道退款（微信 / 支付宝），失败则不改本地状态
	refundNo := "RF" + o.OrderNo
	channelMsg, err := refundViaChannel(c.Request.Context(), o, refundNo, reason)
	if err != nil {
		common.SysError(fmt.Sprintf("refund order %s via channel failed: %v", o.OrderNo, err))
		common.ApiErrorMsg(c, err.Error())
		return
	}
	// 2. 本地：标记退款、回收未消耗积分、终止会员
	revoked, err := service.RefundOrder(id, reason, c.GetInt("id"))
	if err != nil {
		if errors.Is(err, model.ErrOrderStatus) {
			common.ApiErrorMsg(c, "只有已支付订单可以退款")
			return
		}
		respondModelError(c, err, "退款标记失败")
		return
	}
	_ = model.SetOrderRefundInfo(id, refundNo, channelMsg)
	// 3. 支付单同步为已退款
	_ = model.DB.Model(&model.Recharge{}).Where("trade_no = ?", o.OrderNo).Updates(map[string]any{
		"status":             common.RechargeStatusRefunded,
		"refunded_amount":    o.Price,
		"invoiceable_amount": 0,
		"out_request_no":     refundNo,
		"refunded_time":      common.GetTimestamp(),
		"refund_reason":      reason,
	}).Error
	common.SysLog(fmt.Sprintf("admin %d refunded order %d (%s), revoked %d points", c.GetInt("id"), id, channelMsg, revoked))
	common.ApiSuccess(c, gin.H{"revoked_points": revoked, "refund_no": refundNo, "channel": channelMsg})
}

// ----- 钱包 / 流水 -----

// AdminGetWallets 每用户一行（GET /admin/wallet?keyword=）。
func AdminGetWallets(c *gin.Context) {
	page := parsePageInfo(c)
	keyword := strings.TrimSpace(c.Query("keyword"))
	var users []*model.User
	var total int64
	query := model.DB.Model(&model.User{})
	if keyword != "" {
		query = query.Where("username LIKE ? OR nickname LIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if err := query.Count(&total).Error; err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	if err := query.Order("id desc").Limit(page.GetPageSize()).Offset(page.GetStartIdx()).Find(&users).Error; err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	ids := make([]int, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.Id)
	}
	balances := model.GetPointsBalances(ids)
	memberships := model.GetMembershipsByUsers(ids)
	items := make([]gin.H, 0, len(users))
	for _, u := range users {
		row := gin.H{
			"user_id":    u.Id,
			"user_name":  u.Nickname,
			"username":   u.Username,
			"balance":    balances[u.Id],
			"plan_name":  "",
			"expires_at": 0,
		}
		if m := memberships[u.Id]; m != nil && m.IsActive() {
			row["plan_name"] = m.PlanName
			row["expires_at"] = m.ExpiresAt
		}
		items = append(items, row)
	}
	pagedResponse(c, items, total)
}

// AdminAdjustWallet 后台调整积分（POST /admin/wallet/:user_id/adjust）。
func AdminAdjustWallet(c *gin.Context) {
	userId := paramInt(c, "user_id")
	if userId == 0 {
		common.ApiErrorMsg(c, "无效的用户 ID")
		return
	}
	var req struct {
		Delta     int    `json:"delta"`
		Reason    string `json:"reason"`
		ValidDays int    `json:"valid_days"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Delta == 0 {
		common.ApiErrorMsg(c, "调整数量不能为 0")
		return
	}
	if _, err := model.GetUserById(userId); err != nil {
		common.ApiErrorMsg(c, "用户不存在")
		return
	}
	reason := trimLimit(req.Reason, 255)
	if reason == "" {
		reason = "后台调整"
	}
	if err := service.AdjustPoints(userId, req.Delta, reason, req.ValidDays, c.GetInt("id")); err != nil {
		var insufficient model.ErrInsufficientPoints
		if errors.As(err, &insufficient) {
			common.ApiErrorMsg(c, err.Error())
			return
		}
		respondModelError(c, err, "调整失败")
		return
	}
	common.SysLog(fmt.Sprintf("admin %d adjusted user %d points by %d", c.GetInt("id"), userId, req.Delta))
	common.ApiSuccess(c, gin.H{"balance": model.GetPointsBalance(nil, userId)})
}

// AdminGetPointLogs 全站流水（GET /admin/points/log?user_id=&type=&biz_type=）。
func AdminGetPointLogs(c *gin.Context) {
	list, total, err := model.GetPointLogs(model.PointLogFilter{
		UserId:  queryInt(c, "user_id", 0),
		Type:    strings.TrimSpace(c.Query("type")),
		BizType: strings.TrimSpace(c.Query("biz_type")),
	}, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// AdminModelPriceHealth GET /admin/model-price/health?days=7：每个模型最近 N 天的成功率与失败原因（API.md 37）。
func AdminModelPriceHealth(c *gin.Context) {
	days := queryInt(c, "days", service.ModelHealthDays)
	if days < 1 || days > 90 {
		days = service.ModelHealthDays
	}
	stats, err := service.BuildModelHealth(days, time.Now())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"days": days, "min_tasks": service.ModelHealthMinTasks, "warn_rate": service.ModelHealthWarnRate, "models": stats})
}
