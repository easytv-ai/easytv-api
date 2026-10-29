package middleware

import (
	"bytes"
	"strings"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// auditResponseWriter 包装 gin.ResponseWriter：捕获状态码，并把响应体复制到有限缓冲区
// 用于解析业务 success 字段；超出上限即停止缓存，此时只按 HTTP 状态码判成败。
type auditResponseWriter struct {
	gin.ResponseWriter
	body    *bytes.Buffer
	maxSize int
}

func (w *auditResponseWriter) Write(b []byte) (int, error) {
	if w.body.Len() < w.maxSize {
		remain := w.maxSize - w.body.Len()
		if remain >= len(b) {
			w.body.Write(b)
		} else {
			w.body.Write(b[:remain])
		}
	}
	return w.ResponseWriter.Write(b)
}

func (w *auditResponseWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// auditRouteActions 把「METHOD + 路由模板」映射为操作标识。
// 这些是未在 handler 内手动埋点的写操作，由兜底记录；未命中回退 action="generic"。
var auditRouteActions = map[string]string{
	// ---- 用户管理 ----
	"PUT /admin/user/:id/status":          "user.status_change",
	"PUT /admin/user/:id/identity":        "user.identity_change",
	"PUT /admin/user/:id/bind":            "user.rebind",
	"POST /admin/user/:id/console_access": "user.console_access",
	"DELETE /admin/user/:id":              "user.delete",
	"PUT /admin/user/:id/invite-block":    "user.invite_block",
	"POST /admin/user/:id/quota":          "user.quota_adjust",

	// ---- 创作域：项目 ----
	"PUT /admin/project/:id/status": "project.status_change",
	"DELETE /admin/project/:id":     "project.delete",

	// ---- 创作域：画布 ----
	"PUT /admin/canvas/:id/status": "canvas.status_change",
	"DELETE /admin/canvas/:id":     "canvas.delete",

	// ---- 创作域：资产 ----
	"PUT /admin/asset/:id/status": "asset.status_change",
	"DELETE /admin/asset/:id":     "asset.delete",

	// ---- 创作域：作品 ----
	"PUT /admin/work/:id":        "work.update",
	"PUT /admin/work/:id/status": "work.status_change",
	"PUT /admin/work/:id/review": "work.review",
	"DELETE /admin/work/:id":     "work.delete",

	// ---- 社区审核 ----
	"PUT /admin/work-report/:id/status": "work_report.status_change",
	"PUT /admin/comment/:id/status":     "comment.status_change",
	"DELETE /admin/comment/:id":         "comment.delete",

	// ---- 内容运营：分类 ----
	"POST /admin/category":       "category.create",
	"PUT /admin/category/:id":    "category.update",
	"DELETE /admin/category/:id": "category.delete",

	// ---- 内容运营：技能 ----
	"PUT /admin/skill/:id/review": "skill.review",
	"POST /admin/skill":           "skill.create",
	"PUT /admin/skill/:id":        "skill.update",
	"DELETE /admin/skill/:id":     "skill.delete",

	// ---- 内容运营：提示词 ----
	"POST /admin/prompt":                 "prompt.create",
	"PUT /admin/prompt/:id":              "prompt.update",
	"DELETE /admin/prompt/:id":           "prompt.delete",
	"POST /admin/prompt-source":          "prompt_source.create",
	"PUT /admin/prompt-source/:id":       "prompt_source.update",
	"DELETE /admin/prompt-source/:id":    "prompt_source.delete",
	"POST /admin/prompt-source/:id/sync": "prompt_source.sync",

	// ---- 内容运营：Banner ----
	"POST /admin/banner":       "banner.create",
	"PUT /admin/banner/:id":    "banner.update",
	"DELETE /admin/banner/:id": "banner.delete",

	// ---- 内容运营：公告 ----
	"POST /admin/notice":       "notice.create",
	"PUT /admin/notice/:id":    "notice.update",
	"DELETE /admin/notice/:id": "notice.delete",

	// ---- 内容运营：站内通知 ----
	"DELETE /admin/notification/:id": "notification.delete",
	"POST /admin/notification/send":  "notification.send",

	// ---- UGC / 活动 / 伙伴 ----
	"PUT /admin/ugc/:id/review":            "ugc.review",
	"PUT /admin/campaign/entry/:id/review": "campaign.entry_review",
	"POST /admin/campaign":                 "campaign.create",
	"PUT /admin/campaign/:id":              "campaign.update",
	"DELETE /admin/campaign/:id":           "campaign.delete",
	"POST /admin/campaign/:id/settle":      "campaign.settle",
	"PUT /admin/partner/:id/review":        "partner.review",
	"DELETE /admin/subject/:id":            "subject.delete",
	"PUT /admin/team/member/:id/limit":     "team.member_limit",

	// ---- 口令红包 ----
	"POST /admin/bonus-code":       "bonus_code.create",
	"POST /admin/bonus-code/batch": "bonus_code.batch",
	"PUT /admin/bonus-code/:id":    "bonus_code.update",
	"DELETE /admin/bonus-code/:id": "bonus_code.delete",

	// ---- 提现打款 ----
	"POST /admin/withdraw/:id/audit":  "withdraw.audit",
	"POST /admin/withdraw/:id/paid":   "withdraw.mark_paid",
	"POST /admin/withdraw/:id/failed": "withdraw.mark_failed",

	// ---- 系统设置 ----
	"PUT /admin/setting/:key": "setting.update",

	// ---- 付费域：订单 ----
	"POST /admin/order/:id/close":     "order.close",
	"POST /admin/order/:id/mark-paid": "order.mark_paid",
	"POST /admin/order/:id/refund":    "order.refund",

	// ---- 付费域：钱包 ----
	"POST /admin/wallet/:user_id/adjust": "wallet.adjust",

	// ---- 付费域：会员/积分/定价 ----
	"POST /admin/plan":              "plan.create",
	"PUT /admin/plan/:id":           "plan.update",
	"DELETE /admin/plan/:id":        "plan.delete",
	"POST /admin/points-pack":       "points_pack.create",
	"PUT /admin/points-pack/:id":    "points_pack.update",
	"DELETE /admin/points-pack/:id": "points_pack.delete",
	"PUT /admin/billing/promo":      "billing.promo_update",
	"POST /admin/model-price":       "model_price.create",
	"PUT /admin/model-price/:id":    "model_price.update",
	"DELETE /admin/model-price/:id": "model_price.delete",

	// ---- 生成网关 ----
	"PUT /admin/gateway/config":         "gateway.config_update",
	"POST /admin/gateway/models/import": "gateway.models_import",

	// ---- AI 对话 ----
	"PUT /admin/ai-chat/config": "ai_chat.config_update",

	// ---- 风格库 ----
	"POST /admin/style":            "style.create",
	"PUT /admin/style/:id":         "style.update",
	"DELETE /admin/style/:id":      "style.delete",
	"POST /admin/style/:id/review": "style.review",
}

// auditResponseSuccess 依据状态码与响应体推断是否成功：
// 优先响应 JSON 的 success 字段，无法解析时退回状态码。
func auditResponseSuccess(status int, body []byte) bool {
	if status >= 400 {
		return false
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var resp struct {
			Success *bool `json:"success"`
		}
		if err := common.Unmarshal(trimmed, &resp); err == nil && resp.Success != nil {
			return *resp.Success
		}
	}
	return status < 400
}

// beginAdminAudit 在管理写操作进入 handler 前包装 ResponseWriter。
// 只对写方法生效；只读请求返回 nil。
func beginAdminAudit(c *gin.Context) *auditResponseWriter {
	method := c.Request.Method
	if method != "POST" && method != "PUT" && method != "PATCH" && method != "DELETE" {
		return nil
	}
	writer := &auditResponseWriter{
		ResponseWriter: c.Writer,
		body:           bytes.NewBuffer(nil),
		maxSize:        64 * 1024,
	}
	c.Writer = writer
	return writer
}

// finishAdminAudit 在 c.Next() 之后对管理写操作做兜底记录；
// handler 已手动埋点（ContextKeyAuditLogged）则跳过，避免重复。
func finishAdminAudit(c *gin.Context, writer *auditResponseWriter) {
	if writer == nil {
		return
	}
	// 如果 handler 已手动记录审计，跳过兜底
	if logged, _ := c.Get(string(constant.ContextKeyAuditLogged)); logged == true {
		return
	}
	method := c.Request.Method
	operatorId := c.GetInt("id")
	operatorName := c.GetString("username")
	operatorRole := c.GetInt("role")
	if operatorId == 0 {
		return
	}
	status := writer.Status()
	success := auditResponseSuccess(status, writer.body.Bytes())

	route := c.FullPath()
	action := auditRouteActions[method+" "+route]
	if action == "" {
		action = "generic"
	}
	routeParams := map[string]string{}
	for _, p := range c.Params {
		routeParams[p.Key] = p.Value
	}
	opParams := map[string]interface{}{}
	if action == "generic" {
		opParams["method"] = method
		opParams["route"] = route
	}
	for k, v := range routeParams {
		opParams[k] = v
	}

	content := method + " " + route
	if action != "generic" {
		content = action
	}

	other := model.AuditOther{
		Op: &model.AuditOperation{Action: action, Params: opParams},
	}
	entry := model.AuditLog{
		UserId: operatorId, Username: operatorName, ActorRole: operatorRole,
		Category: model.AuditCategoryOperation, Action: action, Content: content,
		Status: status, Success: success, Other: other,
	}
	model.RecordAuditLog(c, entry)
}

// AdminAudit 管理写操作审计中间件。
// 应挂载在 AdminAuth / RootAuth 之后，自动为写操作兜底记录审计日志。
func AdminAudit() gin.HandlerFunc {
	return func(c *gin.Context) {
		writer := beginAdminAudit(c)
		c.Next()
		finishAdminAudit(c, writer)
	}
}

// adminDeniedReasons 管理接口鉴权拒绝原因 → 审计内容文案。
var adminDeniedReasons = map[string]string{
	"not_logged_in":       "未登录",
	"invalid_token":       "登录凭证无效",
	"user_header_missing": "缺少 EasyTV-User 头",
	"user_header_invalid": "EasyTV-User 头格式错误",
	"user_mismatch":       "EasyTV-User 头与登录凭证不匹配",
	"user_disabled":       "账号已被禁用",
	"insufficient_role":   "权限不足",
}

// adminDeniedThrottleKey 去重键：同 IP + 方法 + 路由模板每分钟只记一条，扫描器反复探测不会刷爆审计表。
func adminDeniedThrottleKey(ip, method, route string) string {
	return "admin_denied|" + ip + "|" + method + "|" + route
}

// recordAdminAccessDenied 管理接口（/admin，AdminAuth / RootAuth）鉴权拒绝时记一条安全事件。
// auth 为已解析出的登录用户（未登录 / 凭证无效时为 nil，记为匿名事件）。
func recordAdminAccessDenied(c *gin.Context, minRole int, reason string, auth *resolvedUserAuth) {
	if minRole < constant.RoleAdminUser || c.Request == nil {
		return
	}
	route := c.FullPath()
	if !strings.HasPrefix(route, "/admin") {
		return
	}
	if !model.AllowSecurityAudit(adminDeniedThrottleKey(c.ClientIP(), c.Request.Method, route)) {
		return
	}
	requiredRole := "admin"
	if minRole >= constant.RoleRootUser {
		requiredRole = "root"
	}
	ev := model.AuditEvent{
		Category: model.AuditCategorySecurity,
		Action:   model.AuditActionAdminAccessDenied,
		Content:  "管理接口鉴权拒绝：" + adminDeniedReasons[reason],
		Failed:   true,
		Params:   map[string]any{"reason": reason, "required_role": requiredRole},
	}
	if auth != nil {
		ev.UserId, ev.Username, ev.ActorRole = auth.id, auth.username, auth.role
	}
	model.RecordAuditEvent(c, ev)
}
