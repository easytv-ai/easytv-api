package controller

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// trackRequest 前端批量上报（API.md 7.2）：匿名可调，单次最多 20 条。
type trackRequest struct {
	AnonId string            `json:"anon_id"`
	Device string            `json:"device"` // sendBeacon 无法带自定义头时的设备指纹兜底
	Attr   model.Attribution `json:"attr"`
	Events []struct {
		Name  string         `json:"name"`
		Props map[string]any `json:"props"`
	} `json:"events"`
}

// TrackEvents POST /track
func TrackEvents(c *gin.Context) {
	var req trackRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if len(req.Events) == 0 || len(req.Events) > 20 {
		common.ApiErrorMsg(c, "事件数量需在 1~20 之间")
		return
	}
	attr := req.Attr.Normalize()
	if attr.AnonId == "" {
		attr.AnonId = strings.TrimSpace(req.AnonId)
		if len(attr.AnonId) > 32 {
			attr.AnonId = attr.AnonId[:32]
		}
	}
	userId := c.GetInt("id")
	device := strings.TrimSpace(c.GetHeader("EasyTV-Device"))
	if device == "" {
		device = strings.TrimSpace(req.Device)
	}
	if len(device) > 64 {
		device = device[:64]
	}
	now := common.GetTimestamp()
	list := make([]*model.TrackEvent, 0, len(req.Events))
	for _, e := range req.Events {
		if !model.TrackEventNameValid(e.Name) {
			continue
		}
		if e.Name == "share_open" {
			// 分享回流奖励（API.md 8.4）：落表前用已有事件做当日去重
			ref, _ := e.Props["ref"].(string)
			var workId int
			if v, ok := e.Props["work_id"].(float64); ok {
				workId = int(v)
			}
			service.RewardShareOpen(workId, strings.TrimSpace(ref), attr.AnonId, userId)
		}
		list = append(list, &model.TrackEvent{
			UserId:    userId,
			AnonId:    attr.AnonId,
			Name:      e.Name,
			Props:     model.PropsJSON(e.Props),
			Source:    attr.Source,
			Medium:    attr.Medium,
			Campaign:  attr.Campaign,
			AffCode:   attr.AffCode,
			BonusCode: attr.BonusCode,
			Ip:        c.ClientIP(),
			Device:    device,
			CreatedAt: now,
		})
	}
	if err := model.InsertTrackEvents(list); err != nil {
		common.SysError("track insert failed: " + err.Error())
		common.ApiErrorMsg(c, "上报失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// GetMyGrowthTasks GET /user/growth/tasks 新手任务与进度（T7）
func GetMyGrowthTasks(c *gin.Context) {
	summary, err := service.GetUserTasksSummary(c.GetInt("id"))
	if err != nil {
		common.ApiErrorMsg(c, "读取任务失败")
		return
	}
	common.ApiSuccess(c, summary)
}

// Checkin POST /user/checkin 每日签到（T10）
func Checkin(c *gin.Context) {
	res, err := service.Checkin(c.GetInt("id"))
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	model.RecordSimpleAudit(c, model.AuditCategoryUsage, "user.checkin",
		"每日签到", map[string]interface{}{
			"points_earned": res,
		})
	common.ApiSuccess(c, res)
}

// GetCheckinStatus GET /user/checkin/status
func GetCheckinStatus(c *gin.Context) {
	st, err := service.GetCheckinStatus(c.GetInt("id"))
	if err != nil {
		common.ApiErrorMsg(c, "读取签到状态失败")
		return
	}
	common.ApiSuccess(c, st)
}

// AdminFunnel GET /admin/stats/funnel?from&to&source（Unix 秒；缺省最近 14 天）
func AdminFunnel(c *gin.Context) {
	from := common.GetTimestamp() - 14*86400
	to := common.GetTimestamp()
	if v := c.Query("from"); v != "" {
		if n, ok := parseInt64(v); ok && n > 0 {
			from = n
		}
	}
	if v := c.Query("to"); v != "" {
		if n, ok := parseInt64(v); ok && n > 0 {
			to = n
		}
	}
	if to < from || to-from > 366*86400 {
		common.ApiErrorMsg(c, "时间范围无效（最长 366 天）")
		return
	}
	days, bySource, err := model.GetFunnel(from, to, strings.TrimSpace(c.Query("source")))
	if err != nil {
		common.SysError("funnel query failed: " + err.Error())
		common.ApiErrorMsg(c, "统计失败")
		return
	}
	common.ApiSuccess(c, gin.H{
		"from":      from,
		"to":        to,
		"days":      days,
		"by_source": bySource,
	})
}

func parseInt64(s string) (int64, bool) {
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int64(r-'0')
		if n > 1<<53 {
			return 0, false
		}
	}
	return n, s != ""
}

// AdminCreationMetrics GET /admin/stats/creation?days=7&idle_minutes=10：创作指标（API.md 17）
func AdminCreationMetrics(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	idle, _ := strconv.Atoi(c.DefaultQuery("idle_minutes", "10"))
	if days < 1 || days > service.CreationMetricsMaxDays {
		common.ApiErrorMsg(c, fmt.Sprintf("days 需在 1-%d 之间", service.CreationMetricsMaxDays))
		return
	}
	if idle < 1 || idle > 24*60 {
		common.ApiErrorMsg(c, "idle_minutes 需在 1-1440 之间")
		return
	}
	m, err := service.BuildCreationMetrics(days, idle, time.Now())
	if err != nil {
		common.SysError("creation metrics query failed: " + err.Error())
		common.ApiErrorMsg(c, "统计失败")
		return
	}
	common.ApiSuccess(c, m)
}
