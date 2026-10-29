package controller

import (
	"strconv"
	"strings"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// auditMaxPage 审计日志列表最大页码：深分页 OFFSET 代价高，更早的记录请缩小时间范围或加筛选。
const auditMaxPage = 500

// AdminGetAuditLogs 审计日志列表（GET /admin/audit）。
// 权限：AdminAuth（role ≥ 10）。非 root 看不到 root 操作者的事件。
// 筛选：category / action / user_id / username（精确）/ operator（用户名前缀）/ ip / success /
// start_timestamp / end_timestamp（秒，闭区间）/ keyword（模糊匹配 route、content、action）。
func AdminGetAuditLogs(c *gin.Context) {
	page := parsePageInfo(c)
	if page.Page < 1 || page.PageSize < 1 {
		common.ApiErrorMsg(c, "无效的分页参数")
		return
	}
	if page.Page > auditMaxPage {
		common.ApiErrorMsg(c, "页码过大，请缩小时间范围或增加筛选条件")
		return
	}

	filter := model.AuditLogFilter{
		Username: strings.TrimSpace(c.Query("username")),
		Operator: strings.TrimSpace(c.Query("operator")),
		Category: strings.TrimSpace(c.Query("category")),
		Action:   strings.TrimSpace(c.Query("action")),
		Ip:       strings.TrimSpace(c.Query("ip")),
		Keyword:  strings.TrimSpace(c.Query("keyword")),
	}
	if len([]rune(filter.Keyword)) > 100 || len([]rune(filter.Operator)) > 64 {
		common.ApiErrorMsg(c, "筛选关键词过长")
		return
	}

	if raw := c.Query("user_id"); raw != "" {
		uid, err := strconv.Atoi(raw)
		if err != nil || uid < 0 {
			common.ApiErrorMsg(c, "无效的用户 ID")
			return
		}
		filter.UserId = uid
	}

	// 分类校验
	if !model.ValidAuditCategory(filter.Category) {
		common.ApiErrorMsg(c, "无效的日志分类")
		return
	}

	// 时间范围
	for name, target := range map[string]*int64{
		"start_timestamp": &filter.StartTimestamp,
		"end_timestamp":   &filter.EndTimestamp,
	} {
		if raw := c.Query(name); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed < 0 {
				common.ApiErrorMsg(c, "无效的时间范围")
				return
			}
			*target = parsed
		}
	}
	if filter.EndTimestamp > 0 && filter.EndTimestamp < filter.StartTimestamp {
		common.ApiErrorMsg(c, "无效的时间范围（结束时间早于开始时间）")
		return
	}

	// 成功状态过滤
	if raw := c.Query("success"); raw != "" {
		if raw != "true" && raw != "false" {
			common.ApiErrorMsg(c, "无效的筛选条件")
			return
		}
		success := raw == "true"
		filter.Success = &success
	}

	viewerRole := c.GetInt("role")
	logs, total, err := model.GetAuditLogs(filter, page.GetStartIdx(), page.GetPageSize(), viewerRole)
	if err != nil {
		common.SysError("audit log query failed: " + err.Error())
		common.ApiErrorMsg(c, "查询失败")
		return
	}

	common.ApiSuccess(c, gin.H{
		"items":     logs,
		"total":     total,
		"page":      page.Page,
		"page_size": page.GetPageSize(),
	})
}
