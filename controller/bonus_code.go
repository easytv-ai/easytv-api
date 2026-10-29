package controller

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 公开 / 用户侧（与 easytv-web app/api/bonus-code.ts 对齐）=====

// PreviewBonusCode GET /bonus-code/preview?code=
func PreviewBonusCode(c *gin.Context) {
	p, err := service.PreviewBonusCode(c.Query("code"))
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, p)
}

// ClaimBonusCode POST /user/bonus-code/claim {"code"}
func ClaimBonusCode(c *gin.Context) {
	var req struct {
		Code string `json:"code"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || strings.TrimSpace(req.Code) == "" {
		common.ApiErrorMsg(c, "请输入口令")
		return
	}
	res, err := service.ClaimBonusCode(c.GetInt("id"), req.Code)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, res)
}

// GetMyBonusRecord GET /user/bonus-code/record（未领取过返回 null；附带 partner_name）
func GetMyBonusRecord(c *gin.Context) {
	claim := model.GetBonusClaimByUser(model.DB, c.GetInt("id"))
	if claim == nil {
		common.ApiSuccess(c, nil)
		return
	}
	claim.AttachPartnerName(model.DB)
	common.ApiSuccess(c, claim)
}

// ===== 管理侧 =====

type bonusCodePayload struct {
	Code                   string  `json:"code"`
	Kind                   int     `json:"kind"`
	PartnerName            string  `json:"partner_name"`
	PartnerUserId          int     `json:"partner_user_id"`
	BonusPoints            int     `json:"bonus_points"`
	BonusValidDays         int     `json:"bonus_valid_days"`
	FirstRechargeRate      float64 `json:"first_recharge_rate"`
	FirstRechargeCapPoints int     `json:"first_recharge_cap_points"`
	RewardValidDays        int     `json:"reward_valid_days"`
	MemberDays             int     `json:"member_days"`
	PlanCode               string  `json:"plan_code"`
	MaxClaims              int     `json:"max_claims"`
	StartsAt               int64   `json:"starts_at"`
	ExpiresAt              int64   `json:"expires_at"`
	Status                 *int    `json:"status"`
	Remark                 string  `json:"remark"`
}

func (p bonusCodePayload) validate() string {
	if p.Kind < 1 || p.Kind > 3 {
		return "类型无效"
	}
	if p.BonusPoints < 0 || p.MemberDays < 0 || p.MaxClaims < 0 || p.FirstRechargeCapPoints < 0 {
		return "数值不能为负"
	}
	if p.FirstRechargeRate < 0 || p.FirstRechargeRate > 5 {
		return "首充加赠比例需在 0~5 之间"
	}
	if p.ExpiresAt > 0 && p.StartsAt > 0 && p.ExpiresAt <= p.StartsAt {
		return "结束时间需晚于开始时间"
	}
	if p.BonusPoints == 0 && p.FirstRechargeRate == 0 && p.MemberDays == 0 {
		return "红包积分、首充加赠、会员天数至少填一项"
	}
	return ""
}

func (p bonusCodePayload) apply(b *model.BonusCode) {
	b.Kind, b.PartnerName, b.PartnerUserId = p.Kind, strings.TrimSpace(p.PartnerName), p.PartnerUserId
	b.BonusPoints, b.BonusValidDays = p.BonusPoints, p.BonusValidDays
	b.FirstRechargeRate, b.FirstRechargeCapPoints, b.RewardValidDays = p.FirstRechargeRate, p.FirstRechargeCapPoints, p.RewardValidDays
	b.MemberDays, b.PlanCode = p.MemberDays, strings.TrimSpace(p.PlanCode)
	b.MaxClaims, b.StartsAt, b.ExpiresAt, b.Remark = p.MaxClaims, p.StartsAt, p.ExpiresAt, strings.TrimSpace(p.Remark)
	if p.Status != nil {
		b.Status = *p.Status
	}
	if b.BonusValidDays <= 0 {
		b.BonusValidDays = 30
	}
}

// AdminGetBonusCodes GET /admin/bonus-code?kind&status&partner&partner_user_id&keyword&page&page_size
func AdminGetBonusCodes(c *gin.Context) {
	f := model.BonusCodeFilter{Kind: queryInt(c, "kind", 0), Status: -1, PartnerName: c.Query("partner"),
		PartnerUserId: queryInt(c, "partner_user_id", 0), Keyword: c.Query("keyword")}
	if v := c.Query("status"); v != "" {
		f.Status = queryInt(c, "status", -1)
	}
	list, total, err := model.GetBonusCodes(f, parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "读取失败")
		return
	}
	common.ApiSuccess(c, gin.H{"items": list, "total": total})
}

// AdminCreateBonusCode POST /admin/bonus-code
func AdminCreateBonusCode(c *gin.Context) {
	var req bonusCodePayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	code := model.NormalizeBonusCode(req.Code)
	if len(code) < 3 || len(code) > 32 {
		common.ApiErrorMsg(c, "口令长度需为 3~32 位")
		return
	}
	if msg := req.validate(); msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	now := common.GetTimestamp()
	b := &model.BonusCode{Code: code, Status: 1, CreatedAt: now, UpdatedAt: now}
	req.apply(b)
	if err := model.DB.Create(b).Error; err != nil {
		respondModelError(c, err, "创建失败（口令可能重复）")
		return
	}
	common.ApiSuccess(c, b)
}

// AdminUpdateBonusCode PUT /admin/bonus-code/:id（口令本身不可改）
func AdminUpdateBonusCode(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var req bonusCodePayload
	if id <= 0 || common.DecodeJson(c.Request.Body, &req) != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if msg := req.validate(); msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	var b model.BonusCode
	if err := model.DB.Where("id = ?", id).First(&b).Error; err != nil {
		common.ApiErrorMsg(c, "口令不存在")
		return
	}
	req.apply(&b)
	b.UpdatedAt = common.GetTimestamp()
	if err := model.DB.Save(&b).Error; err != nil {
		common.ApiErrorMsg(c, "保存失败")
		return
	}
	common.ApiSuccess(c, b)
}

// AdminDeleteBonusCode DELETE /admin/bonus-code/:id（有领取记录的只停用）
func AdminDeleteBonusCode(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	var claimed int64
	model.DB.Model(&model.BonusCodeClaim{}).Where("bonus_code_id = ?", id).Count(&claimed)
	if claimed > 0 {
		if err := model.DB.Model(&model.BonusCode{}).Where("id = ?", id).Updates(map[string]any{"status": 0, "updated_at": common.GetTimestamp()}).Error; err != nil {
			common.ApiErrorMsg(c, "停用失败")
			return
		}
		common.ApiSuccessMsg(c, "已有领取记录，已改为停用", nil)
		return
	}
	if err := model.DB.Where("id = ?", id).Delete(&model.BonusCode{}).Error; err != nil {
		common.ApiErrorMsg(c, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminBatchBonusCodes POST /admin/bonus-code/batch {"prefix","count",...同创建字段}
func AdminBatchBonusCodes(c *gin.Context) {
	var req struct {
		bonusCodePayload
		Prefix string `json:"prefix"`
		Count  int    `json:"count"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if msg := req.validate(); msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	tpl := model.BonusCode{Status: 1}
	req.apply(&tpl)
	list, err := service.GenerateBonusCodes(req.Prefix, req.Count, tpl)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	codes := make([]string, 0, len(list))
	for _, b := range list {
		codes = append(codes, b.Code)
	}
	common.ApiSuccess(c, gin.H{"count": len(list), "codes": codes})
}

// AdminBonusCodeStats GET /admin/bonus-code/:id/stats
func AdminBonusCodeStats(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var b model.BonusCode
	if id <= 0 || model.DB.Where("id = ?", id).First(&b).Error != nil {
		common.ApiErrorMsg(c, "口令不存在")
		return
	}
	common.ApiSuccess(c, model.GetBonusCodeStats(&b))
}

// AdminExportBonusCodes GET /admin/bonus-code/export?ids=1,2,3 → CSV
func AdminExportBonusCodes(c *gin.Context) {
	var ids []int
	for _, s := range strings.Split(c.Query("ids"), ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
			ids = append(ids, n)
		}
	}
	if len(ids) == 0 || len(ids) > 2000 {
		common.ApiErrorMsg(c, "请选择 1~2000 个口令")
		return
	}
	list, err := model.GetBonusCodesByIds(ids)
	if err != nil {
		common.ApiErrorMsg(c, "读取失败")
		return
	}
	buf := &bytes.Buffer{}
	buf.WriteString("\xEF\xBB\xBF") // BOM，Excel 直接识别 UTF-8
	w := csv.NewWriter(buf)
	_ = w.Write([]string{"口令", "类型", "渠道", "红包积分", "首充加赠比例", "加赠封顶积分", "会员天数", "可领次数", "已领", "开始", "结束", "状态", "备注"})
	kinds := map[int]string{1: "首充加赠", 2: "定额积分", 3: "会员天数"}
	for _, b := range list {
		_ = w.Write([]string{
			b.Code, kinds[b.Kind], b.PartnerName, strconv.Itoa(b.BonusPoints),
			fmt.Sprintf("%.2f", b.FirstRechargeRate), strconv.Itoa(b.FirstRechargeCapPoints), strconv.Itoa(b.MemberDays),
			strconv.Itoa(b.MaxClaims), strconv.Itoa(b.ClaimedCount), fmtUnix(b.StartsAt), fmtUnix(b.ExpiresAt),
			map[int]string{1: "启用", 0: "停用"}[b.Status], b.Remark,
		})
	}
	w.Flush()
	c.Header("Content-Disposition", "attachment; filename=bonus-codes.csv")
	c.Data(200, "text/csv; charset=utf-8", buf.Bytes())
}

func fmtUnix(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).In(model.CheckinLocation).Format("2006-01-02 15:04")
}
