package controller

import (
	"fmt"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"
	"easytv-api/setting"

	"github.com/gin-gonic/gin"
)

// ===== 发票（API.md 21，参照 EasyAPI）=====
// 发票按账户开（不分团队空间），开具与存档都在 EasyAPI 发票平台，本地不建表。

// GetInvoiceSummary 发票额度总览（GET /user/invoice/summary）。
// 可开票 = max(0, 套餐订单实付 + 纯充值实付 − 平台占用)；平台不可达时可开票为 0 并附 invoice_error。
func GetInvoiceSummary(c *gin.Context) {
	cfg := setting.GetInvoiceConfig()
	if !cfg.Enabled {
		common.ApiSuccess(c, gin.H{"enabled": false})
		return
	}
	q, err := service.GetInvoiceQuota(service.NewInvoiceClient(cfg), c.GetInt("id"))
	if err != nil {
		common.SysError("invoice summary paid total failed: " + err.Error())
		common.ApiErrorMsg(c, "查询可开票金额失败，请稍后重试")
		return
	}
	common.ApiSuccess(c, gin.H{
		"enabled":       true,
		"paid":          q.Paid,
		"invoiced":      q.Invoiced,
		"available":     q.Available,
		"invoice_error": q.Error,
		"tax_rate":      cfg.TaxRate,
		"item_name":     cfg.ItemName,
		"category":      cfg.Category,
		"service_url":   cfg.ServiceUrl,
	})
}

// GetInvoiceAccessToken 开票用户令牌（GET /user/invoice/access-token）。
// 前端持令牌直调发票平台查询发票列表，appKey/appSecret 只在服务端。
func GetInvoiceAccessToken(c *gin.Context) {
	cfg := setting.GetInvoiceConfig()
	if !cfg.Enabled {
		common.ApiErrorMsg(c, service.InvoiceMsgDisabled)
		return
	}
	userId := c.GetInt("id")
	account := ""
	if u, err := model.GetUserById(userId); err == nil && u != nil {
		account = u.Username
	}
	username := service.InvoiceUsernameForUser(userId, account)
	tok, err := service.NewInvoiceClient(cfg).AccessToken(username)
	if err != nil {
		common.SysError(fmt.Sprintf("invoice access token failed: username=%s err=%v", username, err))
		common.ApiErrorMsg(c, service.InvoiceErrorMessage(err))
		return
	}
	common.ApiSuccess(c, gin.H{
		"access_token": tok.AccessToken,
		"username":     tok.Username,
		"service_url":  cfg.ServiceUrl,
	})
}

// ApplyInvoice 申请开票（POST /user/invoice/apply）。
// 与 EasyAPI 前端直调平台不同，这里服务端加用户锁、重算可开票金额并校验后才代为提交，防止并发 / 篡改超额开票。
func ApplyInvoice(c *gin.Context) {
	cfg := setting.GetInvoiceConfig()
	if !cfg.Enabled {
		common.ApiErrorMsg(c, service.InvoiceMsgDisabled)
		return
	}
	if !cfg.HasKeys() {
		common.ApiErrorMsg(c, service.InvoiceMsgConfigMissing)
		return
	}
	var req service.InvoiceApplyRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if err := service.NormalizeInvoiceApply(&req, cfg.Category); err != nil {
		common.ApiError(c, err)
		return
	}

	userId := c.GetInt("id")
	// 审计：表单校验通过后的每次申请记一条成败（只记金额与商户单号，不记抬头 / 税号 / 邮箱等开票资料）
	// failReason 缺省非空：以后新增的提前返回忘了赋值也不会被记成成功
	outOrderNo, failReason := "", "unknown"
	defer func() {
		recordInvoiceApply(c, req.Amount, outOrderNo, failReason)
	}()
	unlock, ok := service.LockInvoiceApply(userId)
	if !ok {
		failReason = "busy"
		common.ApiErrorMsg(c, service.InvoiceMsgApplyBusy)
		return
	}
	defer unlock()

	client := service.NewInvoiceClient(cfg)
	q, err := service.GetInvoiceQuota(client, userId)
	if err != nil {
		common.SysError("invoice apply paid total failed: " + err.Error())
		failReason = "quota_query_failed"
		common.ApiErrorMsg(c, "查询可开票金额失败，请稍后重试")
		return
	}
	if q.Error != "" {
		failReason = "platform_unavailable"
		common.ApiErrorMsg(c, q.Error)
		return
	}
	if err := service.CheckInvoiceAmount(req.Amount, q.Available); err != nil {
		failReason = "amount_exceeded"
		common.ApiError(c, err)
		return
	}

	account := ""
	if u, err := model.GetUserById(userId); err == nil && u != nil {
		account = u.Username
	}
	username := service.InvoiceUsernameForUser(userId, account)
	tok, err := client.AccessToken(username)
	if err != nil {
		common.SysError(fmt.Sprintf("invoice apply access token failed: user=%d err=%v", userId, err))
		failReason = "access_token_failed"
		common.ApiErrorMsg(c, service.InvoiceErrorMessage(err))
		return
	}
	outOrderNo = service.NewInvoiceOutOrderNo(userId, time.Now())
	content, err := client.MakeInvoice(service.BuildInvoiceMakeRequest(cfg, req, tok.AccessToken, username, outOrderNo))
	if err != nil {
		common.SysError(fmt.Sprintf("invoice apply make failed: user=%d amount=%.2f out_order_no=%s err=%v", userId, req.Amount, outOrderNo, err))
		failReason = "make_failed"
		common.ApiErrorMsg(c, service.InvoiceErrorMessage(err))
		return
	}
	service.RecordInvoicePending(userId, outOrderNo, req.Amount)
	if err := service.AllocateInvoiceToRecharges(userId, service.InvoiceRound2(q.Invoiced+req.Amount)); err != nil {
		common.SysError(fmt.Sprintf("invoice apply allocate recharge failed: user=%d amount=%.2f err=%v", userId, req.Amount, err))
	}
	failReason = ""
	common.SysLog(fmt.Sprintf("invoice apply: user=%d amount=%.2f type=%s category=%s out_order_no=%s", userId, req.Amount, req.Type, req.Category, outOrderNo))
	common.ApiSuccess(c, gin.H{
		"out_order_no": outOrderNo,
		"content":      content,
	})
}

// recordInvoiceApply 开票申请审计（usage 分类，与充值等用户功能使用同类）。failReason 为空表示成功。
func recordInvoiceApply(c *gin.Context, amount float64, outOrderNo, failReason string) {
	params := map[string]any{"amount": amount, "out_order_no": outOrderNo}
	content := fmt.Sprintf("申请开票 ¥%.2f", amount)
	if failReason != "" {
		params["reason"] = failReason
		content += "（失败）"
	} else {
		content += " " + outOrderNo
	}
	model.RecordAuditEvent(c, model.AuditEvent{
		UserId:    c.GetInt("id"),
		Username:  c.GetString("username"),
		ActorRole: c.GetInt("role"),
		Category:  model.AuditCategoryUsage,
		Action:    model.AuditActionInvoiceApply,
		Content:   content,
		Failed:    failReason != "",
		Params:    params,
	})
}
