package controller

import (
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 媒体工具（API.md 23）=====

// GetMediaActions GET /user/media/actions 动作清单与可用性。
// 前端整条工具条据此驱动：available=false 的按钮显示原因，而不是假装「即将上线」。
func GetMediaActions(c *gin.Context) {
	common.ApiSuccess(c, gin.H{"actions": service.MediaActions()})
}

// AdminGetMediaActions GET /admin/media/actions 画布媒体工具盘点（AdminAuth）。
// 比用户清单多 priced_models / gap / ffmpeg，方便运营对「暂未接入」。
func AdminGetMediaActions(c *gin.Context) {
	actions, ffmpeg := service.AdminMediaActions()
	common.ApiSuccess(c, gin.H{"actions": actions, "ffmpeg": ffmpeg})
}

// CreateMediaJob POST /user/media/jobs 创建媒体处理作业。
func CreateMediaJob(c *gin.Context) {
	userId := c.GetInt("id")
	var req service.MediaJobRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的请求参数")
		return
	}
	job, err := service.CreateMediaJob(userId, teamIdOf(c), req)
	if err != nil {
		if respondSpaceError(c, err) {
			return
		}
		common.ApiErrorMsg(c, err.Error())
		return
	}
	model.RecordSimpleAudit(c, model.AuditCategoryUsage, "media.process.create",
		"提交媒体处理 "+job.Action, map[string]interface{}{
			"job_id": job.Id, "media_action": job.Action, "asset_id": job.SourceAssetId,
			"engine": job.Engine, "task_id": job.TaskId, "canvas_id": job.CanvasId,
		})
	common.ApiSuccess(c, gin.H{"job": job})
}

// GetMyMediaJobs GET /user/media/jobs?canvas_id=&asset_id=&active=1 作业列表（刷新后恢复处理中状态）。
func GetMyMediaJobs(c *gin.Context) {
	list, total, err := model.GetMediaJobs(model.MediaJobFilter{
		UserId:     c.GetInt("id"),
		CanvasId:   queryInt(c, "canvas_id", 0),
		AssetId:    queryInt(c, "asset_id", 0),
		ProjectId:  queryInt(c, "project_id", 0),
		Action:     strings.TrimSpace(c.Query("action")),
		OnlyActive: c.Query("active") == "1",
	}, parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	// 模型动作的状态由生成任务承载，列表里也要映射回来，否则刷新后会看到已完成的还在排队
	service.MergeMediaJobTasks(list)
	pagedResponse(c, list, total)
}

// GetMyMediaJob GET /user/media/jobs/:id 作业详情（模型动作的状态由生成任务实时映射）。
func GetMyMediaJob(c *gin.Context) {
	job, err := service.MediaJobDetail(c.GetInt("id"), paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, gin.H{"job": job})
}
