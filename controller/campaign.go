package controller

import (
	"encoding/json"
	"regexp"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 活动（API.md 8.6，T33）=====

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,58}$`)

// GetCampaigns GET /campaign
func GetCampaigns(c *gin.Context) {
	list, err := model.GetPublicCampaigns()
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	now := common.GetTimestamp()
	for _, x := range list {
		x.Status = x.EffectiveStatus(now)
	}
	common.ApiSuccess(c, list)
}

// GetCampaign GET /campaign/:slug（TryUserAuth，带我的投稿）
func GetCampaign(c *gin.Context) {
	x, err := model.GetCampaignBySlug(strings.TrimSpace(c.Param("slug")))
	if err != nil || x.Status == model.CampaignStatusDraft {
		common.ApiErrorMsg(c, "活动不存在")
		return
	}
	x.Status = x.EffectiveStatus(common.GetTimestamp())
	if uid := c.GetInt("id"); uid > 0 {
		x.MyEntry = model.GetUserCampaignEntry(x.Id, uid)
	}
	common.ApiSuccess(c, x)
}

// GetCampaignEntries GET /campaign/:slug/entries?sort=likes|new|rank
func GetCampaignEntries(c *gin.Context) {
	x, err := model.GetCampaignBySlug(strings.TrimSpace(c.Param("slug")))
	if err != nil || x.Status == model.CampaignStatusDraft {
		common.ApiErrorMsg(c, "活动不存在")
		return
	}
	sort := c.Query("sort")
	if sort == "" {
		if x.Status == model.CampaignStatusSettled {
			sort = "rank"
		} else {
			sort = "likes"
		}
	}
	if sort == "likes" && x.Status != model.CampaignStatusSettled {
		model.RefreshCampaignLikes(x.Id)
	}
	approved := model.CampaignEntryApproved
	list, total, err := model.GetCampaignEntries(x.Id, &approved, sort, parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// EnterCampaign POST /user/work/:id/enter { campaign_id }
func EnterCampaign(c *gin.Context) {
	var req struct {
		CampaignId int `json:"campaign_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || req.CampaignId <= 0 {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	e, err := service.EnterCampaign(c.GetInt("id"), paramInt(c, "id"), req.CampaignId)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, e)
}

// ---- 管理端 ----

type campaignPayload struct {
	Title      *string          `json:"title"`
	Slug       *string          `json:"slug"`
	BannerUrl  *string          `json:"banner_url"`
	Rules      *string          `json:"rules"`
	Tag        *string          `json:"tag"`
	StartsAt   *int64           `json:"starts_at"`
	EndsAt     *int64           `json:"ends_at"`
	Status     *int             `json:"status"`
	Prizes     json.RawMessage  `json:"prizes"`
	ModelPromo *json.RawMessage `json:"model_promo"`
}

func (p *campaignPayload) fields() (map[string]any, string) {
	f := map[string]any{}
	if p.Title != nil {
		t := trimLimit(*p.Title, 100)
		if t == "" {
			return nil, "请填写活动标题"
		}
		f["title"] = t
	}
	if p.Slug != nil {
		s := strings.ToLower(strings.TrimSpace(*p.Slug))
		if !slugRe.MatchString(s) {
			return nil, "slug 只能是小写字母、数字和连字符，2~59 位"
		}
		f["slug"] = s
	}
	if p.BannerUrl != nil {
		f["banner_url"] = trimLimit(*p.BannerUrl, 1000)
	}
	if p.Rules != nil {
		f["rules"] = *p.Rules
	}
	if p.Tag != nil {
		f["tag"] = trimLimit(*p.Tag, 50)
	}
	if p.StartsAt != nil {
		f["starts_at"] = *p.StartsAt
	}
	if p.EndsAt != nil {
		f["ends_at"] = *p.EndsAt
	}
	if p.StartsAt != nil && p.EndsAt != nil && *p.EndsAt > 0 && *p.EndsAt <= *p.StartsAt {
		return nil, "结束时间需晚于开始时间"
	}
	if p.Status != nil {
		if *p.Status < 0 || *p.Status > 2 {
			return nil, "无效的状态"
		}
		f["status"] = *p.Status
	}
	if len(p.Prizes) > 0 {
		var raw any
		if err := json.Unmarshal(p.Prizes, &raw); err != nil {
			return nil, "奖项格式不正确"
		}
		var list []model.CampaignPrize
		if s, ok := raw.(string); ok {
			_ = json.Unmarshal([]byte(s), &list)
		} else {
			_ = json.Unmarshal(p.Prizes, &list)
		}
		for _, x := range list {
			if x.RankFrom < 1 || x.RankTo < x.RankFrom || x.Points < 0 || x.MemberDays < 0 {
				return nil, "奖项名次或数值不正确"
			}
		}
		b, _ := json.Marshal(list)
		f["prizes"] = string(b)
	}
	if p.ModelPromo != nil {
		s := strings.TrimSpace(string(*p.ModelPromo))
		if s == "" || s == "null" || s == `""` {
			f["model_promo"] = ""
		} else if norm, msg := model.NormalizeCampaignModelPromo(s); msg != "" {
			return nil, msg
		} else {
			f["model_promo"] = norm
		}
	}
	return f, ""
}

// AdminGetCampaigns GET /admin/campaign
func AdminGetCampaigns(c *gin.Context) {
	var status *int
	if v := strings.TrimSpace(c.Query("status")); v != "" {
		s := queryInt(c, "status", 0)
		status = &s
	}
	list, total, err := model.GetCampaigns(c.Query("keyword"), status, parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// AdminCreateCampaign POST /admin/campaign
func AdminCreateCampaign(c *gin.Context) {
	var req campaignPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Title == nil || req.Slug == nil {
		common.ApiErrorMsg(c, "请填写标题与 slug")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	x := &model.Campaign{Title: f["title"].(string), Slug: f["slug"].(string), Status: model.CampaignStatusDraft, Prizes: "[]"}
	str := func(k string) string {
		if v, ok := f[k].(string); ok {
			return v
		}
		return ""
	}
	x.BannerUrl, x.Rules, x.Tag, x.ModelPromo = str("banner_url"), str("rules"), str("tag"), str("model_promo")
	if v, ok := f["prizes"].(string); ok {
		x.Prizes = v
	}
	if v, ok := f["starts_at"].(int64); ok {
		x.StartsAt = v
	}
	if v, ok := f["ends_at"].(int64); ok {
		x.EndsAt = v
	}
	if v, ok := f["status"].(int); ok {
		x.Status = v
	}
	if err := x.Insert(); err != nil {
		respondModelError(c, err, "创建失败（slug 可能重复）")
		return
	}
	model.ResetCampaignPromoCache()
	x.FillView()
	common.ApiSuccess(c, x)
}

// AdminUpdateCampaign PUT /admin/campaign/:id
func AdminUpdateCampaign(c *gin.Context) {
	id := paramInt(c, "id")
	var req campaignPayload
	if id == 0 || common.DecodeJson(c.Request.Body, &req) != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	x, err := model.GetCampaignById(id)
	if err != nil {
		common.ApiErrorMsg(c, "活动不存在")
		return
	}
	if x.Status == model.CampaignStatusSettled {
		common.ApiErrorMsg(c, "已发奖的活动不能修改")
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
	if err := model.UpdateCampaignFields(id, f); err != nil {
		respondModelError(c, err, "保存失败")
		return
	}
	model.ResetCampaignPromoCache()
	x, _ = model.GetCampaignById(id)
	common.ApiSuccess(c, x)
}

// AdminDeleteCampaign DELETE /admin/campaign/:id
func AdminDeleteCampaign(c *gin.Context) {
	if err := model.DeleteCampaign(paramInt(c, "id")); err != nil {
		common.ApiErrorMsg(c, "删除失败")
		return
	}
	model.ResetCampaignPromoCache()
	common.ApiSuccess(c, nil)
}

// AdminGetCampaignEntries GET /admin/campaign/:id/entries?status=
func AdminGetCampaignEntries(c *gin.Context) {
	var status *int
	if v := strings.TrimSpace(c.Query("status")); v != "" {
		s := queryInt(c, "status", 0)
		status = &s
	}
	list, total, err := model.GetCampaignEntries(paramInt(c, "id"), status, c.Query("sort"), parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// AdminReviewCampaignEntry PUT /admin/campaign/entry/:id/review { action }
func AdminReviewCampaignEntry(c *gin.Context) {
	var req struct {
		Action string `json:"action"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	status := 0
	switch req.Action {
	case "approve":
		status = model.CampaignEntryApproved
	case "reject":
		status = model.CampaignEntryRejected
	default:
		common.ApiErrorMsg(c, "无效的操作")
		return
	}
	if err := model.UpdateCampaignEntryFields(paramInt(c, "id"), map[string]any{"status": status}); err != nil {
		respondModelError(c, err, "保存失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminSettleCampaign POST /admin/campaign/:id/settle
func AdminSettleCampaign(c *gin.Context) {
	n, err := service.SettleCampaign(paramInt(c, "id"))
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{"awarded": n})
}
