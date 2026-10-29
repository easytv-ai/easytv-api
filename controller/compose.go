package controller

import (
	"errors"
	"fmt"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 成片合成（API.md 8.2，T24）=====

// CreateCompose POST /user/canvas/:id/compose
func CreateCompose(c *gin.Context) {
	userId := c.GetInt("id")
	cv, err := model.GetUserCanvas(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询画布失败")
		return
	}
	var req service.ComposeRequest
	_ = common.DecodeJson(c.Request.Body, &req)
	if len(req.ClipOrder) > service.ComposeMaxClips {
		common.ApiErrorMsg(c, fmt.Sprintf("一次最多合成 %d 个镜头", service.ComposeMaxClips))
		return
	}
	teamId, err := service.ResolveTaskTeam(userId, teamIdOf(c), cv.ProjectId)
	if err != nil {
		respondModelError(c, err, "查询项目失败")
		return
	}
	job, err := service.CreateComposeJob(userId, teamId, cv, req)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, job)
}

// GetMyCompose GET /user/compose/:id
func GetMyCompose(c *gin.Context) {
	job, err := model.GetUserComposeJob(c.GetInt("id"), paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, job)
}

// GetMyComposeList GET /user/compose?canvas_id=
func GetMyComposeList(c *gin.Context) {
	canvasId := queryInt(c, "canvas_id", 0)
	if canvasId <= 0 {
		common.ApiErrorMsg(c, "请指定画布")
		return
	}
	list, err := model.GetComposeJobsByCanvas(c.GetInt("id"), canvasId, 10)
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	common.ApiSuccess(c, list)
}

// ===== 镜头视图与成片时间线（API.md 12.1 / 12.2）=====

// GetMyCanvasShots GET /user/canvas/:id/shots
func GetMyCanvasShots(c *gin.Context) {
	cv, err := model.GetUserCanvas(c.GetInt("id"), paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询画布失败")
		return
	}
	shots, err := service.BuildShots(cv)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{"shots": shots, "characters": service.BuildCharacterMedia(cv)})
}

// GetMyCanvasTimeline GET /user/canvas/:id/timeline：时间线与镜头视图一起返回；没保存过给缺省时间线（version=0）。
func GetMyCanvasTimeline(c *gin.Context) {
	cv, err := model.GetUserCanvas(c.GetInt("id"), paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询画布失败")
		return
	}
	shots, err := service.BuildShots(cv)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	row, saved, err := model.GetCanvasTimeline(cv.Id)
	if err != nil || saved == nil {
		tl := service.DefaultTimeline(cv, shots)
		common.ApiSuccess(c, gin.H{"version": 0, "timeline": tl, "shots": shots, "characters": service.BuildCharacterMedia(cv), "estimate": service.EstimateTimeline(cv, tl), "locked": false})
		return
	}
	tl := service.MergeTimeline(saved, shots)
	common.ApiSuccess(c, gin.H{"version": row.Version, "timeline": tl, "shots": shots, "characters": service.BuildCharacterMedia(cv), "updated_at": row.UpdatedAt,
		"estimate": service.EstimateTimeline(cv, tl), "locked": row.Locked, "locked_at": row.LockedAt, "locked_by": row.LockedBy})
}

// SaveMyCanvasTimeline PUT /user/canvas/:id/timeline { version, timeline }
func SaveMyCanvasTimeline(c *gin.Context) {
	userId := c.GetInt("id")
	cv, err := model.GetUserCanvas(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询画布失败")
		return
	}
	var req struct {
		Version  int             `json:"version"`
		Timeline *model.Timeline `json:"timeline"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || req.Timeline == nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	req.Timeline.Normalize()
	req.Timeline.Lang = service.NormalizeLang(req.Timeline.Lang)
	version, err := model.SaveCanvasTimeline(userId, cv.Id, req.Version, req.Timeline)
	if err != nil {
		if errors.Is(err, model.ErrTimelineVersionConflict) || errors.Is(err, model.ErrTimelineLocked) {
			common.ApiErrorMsg(c, err.Error())
			return
		}
		respondModelError(c, err, "保存失败")
		return
	}
	common.ApiSuccess(c, gin.H{"version": version, "timeline": req.Timeline, "estimate": service.EstimateTimeline(cv, req.Timeline)})
}
