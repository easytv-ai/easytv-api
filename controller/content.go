package controller

import (
	"strings"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// ===== 内容运营：Banner / 公告（公开 + 管理侧）=====

// GetPublicBanners 首页轮播（GET /banner）。
func GetPublicBanners(c *gin.Context) {
	list, err := model.GetActiveBanners()
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, list)
}

// GetPublicNotices 已发布公告（GET /notice?page=&page_size=）。
func GetPublicNotices(c *gin.Context) {
	on := model.ContentStatusOn
	list, total, err := model.GetNotices("", &on, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// GetPublicNotice 公告详情（GET /notice/:id）。
func GetPublicNotice(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的公告 ID")
		return
	}
	n, err := model.GetNoticeById(id)
	if err != nil || n.Status != model.ContentStatusOn {
		common.ApiErrorMsg(c, model.ErrNotFound.Error())
		return
	}
	common.ApiSuccess(c, n)
}

func statusFilter(c *gin.Context) (*int, bool) {
	raw := strings.TrimSpace(c.Query("status"))
	if raw == "" {
		return nil, true
	}
	s := queryInt(c, "status", 0)
	if s != model.ContentStatusOn && s != model.ContentStatusOff {
		return nil, false
	}
	return &s, true
}

// ----- Banner 管理 -----

type bannerPayload struct {
	Title     *string `json:"title"`
	Subtitle  *string `json:"subtitle"`
	Tag       *string `json:"tag"`
	CoverUrl  *string `json:"cover_url"`
	VideoUrl  *string `json:"video_url"`
	LinkUrl   *string `json:"link_url"`
	Cta       *string `json:"cta"`
	SortOrder *int    `json:"sort_order"`
	Status    *int    `json:"status"`
	StartsAt  *int64  `json:"starts_at"`
	EndsAt    *int64  `json:"ends_at"`
}

func (p *bannerPayload) fields() (map[string]any, string) {
	f := map[string]any{}
	if p.Title != nil {
		t := trimLimit(*p.Title, 100)
		if t == "" {
			return nil, "请填写标题"
		}
		f["title"] = t
	}
	if p.Subtitle != nil {
		f["subtitle"] = trimLimit(*p.Subtitle, 200)
	}
	if p.Tag != nil {
		f["tag"] = trimLimit(*p.Tag, 50)
	}
	if p.CoverUrl != nil {
		f["cover_url"] = trimLimit(*p.CoverUrl, 1000)
	}
	if p.VideoUrl != nil {
		f["video_url"] = trimLimit(*p.VideoUrl, 1000)
	}
	if p.LinkUrl != nil {
		f["link_url"] = trimLimit(*p.LinkUrl, 1000)
	}
	if p.Cta != nil {
		f["cta"] = trimLimit(*p.Cta, 50)
	}
	if p.SortOrder != nil {
		f["sort_order"] = *p.SortOrder
	}
	if p.Status != nil {
		if *p.Status != model.ContentStatusOn && *p.Status != model.ContentStatusOff {
			return nil, "无效的状态"
		}
		f["status"] = *p.Status
	}
	if p.StartsAt != nil {
		f["starts_at"] = *p.StartsAt
	}
	if p.EndsAt != nil {
		f["ends_at"] = *p.EndsAt
	}
	return f, ""
}

func AdminGetBanners(c *gin.Context) {
	status, ok := statusFilter(c)
	if !ok {
		common.ApiErrorMsg(c, "无效的状态")
		return
	}
	list, total, err := model.GetBanners(strings.TrimSpace(c.Query("keyword")), status, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

func AdminCreateBanner(c *gin.Context) {
	var req bannerPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Title == nil {
		common.ApiErrorMsg(c, "请填写标题")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	b := &model.Banner{}
	str := func(k string) string {
		v, _ := f[k].(string)
		return v
	}
	b.Title, b.Subtitle, b.Tag = str("title"), str("subtitle"), str("tag")
	b.CoverUrl, b.VideoUrl, b.LinkUrl, b.Cta = str("cover_url"), str("video_url"), str("link_url"), str("cta")
	if v, ok := f["sort_order"].(int); ok {
		b.SortOrder = v
	}
	if v, ok := f["status"].(int); ok {
		b.Status = v
	}
	if v, ok := f["starts_at"].(int64); ok {
		b.StartsAt = v
	}
	if v, ok := f["ends_at"].(int64); ok {
		b.EndsAt = v
	}
	if err := b.Insert(); err != nil {
		respondModelError(c, err, "创建失败")
		return
	}
	common.ApiSuccess(c, b)
}

func AdminUpdateBanner(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的 Banner ID")
		return
	}
	var req bannerPayload
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
	if err := model.UpdateBannerFields(id, f); err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

func AdminDeleteBanner(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的 Banner ID")
		return
	}
	if err := model.DeleteBanner(id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// ----- 公告管理 -----

type noticePayload struct {
	Title   *string `json:"title"`
	Content *string `json:"content"`
	Type    *string `json:"type"`
	Pinned  *bool   `json:"pinned"`
	Status  *int    `json:"status"`
}

func (p *noticePayload) fields() (map[string]any, string) {
	f := map[string]any{}
	if p.Title != nil {
		t := trimLimit(*p.Title, 100)
		if t == "" {
			return nil, "请填写公告标题"
		}
		f["title"] = t
	}
	if p.Content != nil {
		content := strings.TrimSpace(*p.Content)
		if content == "" {
			return nil, "请填写公告内容"
		}
		f["content"] = content
	}
	if p.Type != nil {
		if *p.Type != model.NoticeTypeSystem && *p.Type != model.NoticeTypeActivity {
			return nil, "无效的公告类型"
		}
		f["type"] = *p.Type
	}
	if p.Pinned != nil {
		f["pinned"] = *p.Pinned
	}
	if p.Status != nil {
		if *p.Status != model.ContentStatusOn && *p.Status != model.ContentStatusOff {
			return nil, "无效的状态"
		}
		f["status"] = *p.Status
	}
	return f, ""
}

func AdminGetNotices(c *gin.Context) {
	status, ok := statusFilter(c)
	if !ok {
		common.ApiErrorMsg(c, "无效的状态")
		return
	}
	list, total, err := model.GetNotices(strings.TrimSpace(c.Query("keyword")), status, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

func AdminCreateNotice(c *gin.Context) {
	var req noticePayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Title == nil || req.Content == nil {
		common.ApiErrorMsg(c, "请填写标题与内容")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	n := &model.Notice{}
	if v, ok := f["title"].(string); ok {
		n.Title = v
	}
	if v, ok := f["content"].(string); ok {
		n.Content = v
	}
	if v, ok := f["type"].(string); ok {
		n.Type = v
	}
	if v, ok := f["pinned"].(bool); ok {
		n.Pinned = v
	}
	if v, ok := f["status"].(int); ok {
		n.Status = v
	}
	if err := n.Insert(); err != nil {
		respondModelError(c, err, "创建失败")
		return
	}
	common.ApiSuccess(c, n)
}

func AdminUpdateNotice(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的公告 ID")
		return
	}
	var req noticePayload
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
	if err := model.UpdateNoticeFields(id, f); err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

func AdminDeleteNotice(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的公告 ID")
		return
	}
	if err := model.DeleteNotice(id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}
