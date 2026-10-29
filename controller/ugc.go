package controller

import (
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 站外投稿（API.md 8.5，T31）=====

// SubmitMyUgc POST /user/ugc
func SubmitMyUgc(c *gin.Context) {
	var req struct {
		Platform string `json:"platform"`
		Url      string `json:"url"`
		WorkId   int    `json:"work_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	u, err := service.SubmitUgc(c.GetInt("id"), req.Platform, req.Url, req.WorkId)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, u)
}

// GetMyUgc GET /user/ugc
func GetMyUgc(c *gin.Context) {
	userId := c.GetInt("id")
	list, total, err := model.GetUgcSubmissions(model.UgcFilter{UserId: userId}, &common.PageInfo{Page: 1, PageSize: 100})
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	out := service.UgcMonthView(userId)
	out["items"], out["total"] = list, total
	common.ApiSuccess(c, out)
}

// AdminGetUgc GET /admin/ugc?status=&platform=&keyword=&page
func AdminGetUgc(c *gin.Context) {
	f := model.UgcFilter{Platform: strings.TrimSpace(c.Query("platform")), Keyword: c.Query("keyword")}
	if v := strings.TrimSpace(c.Query("status")); v != "" {
		s := queryInt(c, "status", 0)
		f.Status = &s
	}
	list, total, err := model.GetUgcSubmissions(f, parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// AdminReviewUgc PUT /admin/ugc/:id/review
func AdminReviewUgc(c *gin.Context) {
	var req service.UgcReview
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	u, err := service.ReviewUgc(paramInt(c, "id"), c.GetInt("id"), req)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	model.AttachUgcRefs([]*model.UgcSubmission{u})
	common.ApiSuccess(c, u)
}
