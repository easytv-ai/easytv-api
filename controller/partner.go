package controller

import (
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 渠道伙伴计划（API.md 9.3，M12）=====

// ApplyPartner POST /user/partner/apply
func ApplyPartner(c *gin.Context) {
	var req struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Contact string `json:"contact"`
		Intro   string `json:"intro"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	p, err := service.ApplyPartner(c.GetInt("id"), req.Name, strings.TrimSpace(req.Kind), req.Contact, req.Intro)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, p)
}

// GetMyPartner GET /user/partner/me（未申请返回 null）
func GetMyPartner(c *gin.Context) {
	common.ApiSuccess(c, service.GetPartnerDashboard(c.GetInt("id")))
}

// GetPartnerLanding GET /partner/:code 落地页数据
func GetPartnerLanding(c *gin.Context) {
	p := model.GetPartnerByCode(c.Param("code"))
	if p == nil {
		common.ApiErrorMsg(c, "推广链接无效或已停用")
		return
	}
	common.ApiSuccess(c, gin.H{"name": p.Name, "intro": p.Intro, "kind": p.Kind, "code": p.Code, "valid": true})
}

// AdminGetPartners GET /admin/partner?status=&keyword=
func AdminGetPartners(c *gin.Context) {
	f := model.PartnerFilter{Keyword: c.Query("keyword")}
	if v := strings.TrimSpace(c.Query("status")); v != "" {
		s := queryInt(c, "status", 0)
		f.Status = &s
	}
	list, total, err := model.GetPartners(f, parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// AdminReviewPartner PUT /admin/partner/:id/review
func AdminReviewPartner(c *gin.Context) {
	var req struct {
		Action     string   `json:"action"`
		RebateRate *float64 `json:"rebate_rate"`
		Remark     string   `json:"remark"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	p, err := service.ReviewPartner(paramInt(c, "id"), c.GetInt("id"), req.Action, req.RebateRate, req.Remark)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, p)
}

// AdminGetPartnerCustomers GET /admin/partner/:id/customers
func AdminGetPartnerCustomers(c *gin.Context) {
	list, total, err := model.GetPartnerCustomers(paramInt(c, "id"), parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}
