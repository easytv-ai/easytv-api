package controller

import (
	"errors"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 代理商后台（easytv-partner，API.md 9.3.1）=====
// 复用 UserAuth（伙伴用自己的 EasyTV 账号登录），数据只限本人名下的归属客户聚合。

// PartnerConsoleOverview GET /user/partner/console/overview
// 未申请返回 success=false + code "not_applied"，前端进入申请页；待审 / 驳回 / 停用照常返回 partner 供前端展示状态。
func PartnerConsoleOverview(c *gin.Context) {
	data, err := service.GetPartnerConsoleOverview(c.GetInt("id"))
	if err != nil {
		respondPartnerConsoleError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}

// PartnerConsoleStats GET /user/partner/console/stats?range=7d|30d|90d
func PartnerConsoleStats(c *gin.Context) {
	days := 30
	switch strings.TrimSpace(c.Query("range")) {
	case "7d":
		days = 7
	case "90d":
		days = 90
	}
	data, err := service.GetPartnerConsoleStats(c.GetInt("id"), days)
	if err != nil {
		respondPartnerConsoleError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}

// PartnerConsoleCustomers GET /user/partner/console/customers?paid=1|0&sort=recent|amount&page&page_size
func PartnerConsoleCustomers(c *gin.Context) {
	f := model.PartnerCustomerFilter{Sort: strings.TrimSpace(c.Query("sort"))}
	switch strings.TrimSpace(c.Query("paid")) {
	case "1":
		v := true
		f.Paid = &v
	case "0":
		v := false
		f.Paid = &v
	}
	items, total, err := service.GetPartnerConsoleCustomers(c.GetInt("id"), f, parsePageInfo(c))
	if err != nil {
		respondPartnerConsoleError(c, err)
		return
	}
	pagedResponse(c, items, total)
}

// PartnerConsoleBonusCodes GET /user/partner/console/bonus-codes?page&page_size
func PartnerConsoleBonusCodes(c *gin.Context) {
	items, total, err := service.GetPartnerConsoleBonusCodes(c.GetInt("id"), parsePageInfo(c))
	if err != nil {
		respondPartnerConsoleError(c, err)
		return
	}
	pagedResponse(c, items, total)
}

// PartnerConsoleUpdateProfile PUT /user/partner/console/profile  { name?, kind?, contact, intro }
func PartnerConsoleUpdateProfile(c *gin.Context) {
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
	p, err := service.UpdatePartnerProfile(c.GetInt("id"), req.Name, strings.TrimSpace(req.Kind), req.Contact, req.Intro)
	if err != nil {
		respondPartnerConsoleError(c, err)
		return
	}
	common.ApiSuccess(c, p)
}

// respondPartnerConsoleError 未申请 / 未通过带机器可读的 code，其余按文案返回。
func respondPartnerConsoleError(c *gin.Context, err error) {
	code := ""
	switch {
	case errors.Is(err, service.ErrPartnerNotApplied):
		code = "not_applied"
	case errors.Is(err, service.ErrPartnerNotApproved):
		code = "not_approved"
	}
	if code == "" {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	c.JSON(200, gin.H{"success": false, "message": err.Error(), "code": code})
}
