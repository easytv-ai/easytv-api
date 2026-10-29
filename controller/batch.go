package controller

import (
	"errors"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 服务端批量生成（API.md 13）=====

// CreateMyCanvasBatch POST /user/canvas/:id/batch
func CreateMyCanvasBatch(c *gin.Context) {
	userId := c.GetInt("id")
	cv, err := model.GetUserCanvas(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询画布失败")
		return
	}
	var req service.BatchRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	teamId, err := service.ResolveTaskTeam(userId, teamIdOf(c), cv.ProjectId)
	if err != nil {
		respondModelError(c, err, "查询项目失败")
		return
	}
	b, err := service.CreateBatch(userId, teamId, cv, req)
	if err != nil {
		if respondSpaceError(c, err) {
			return
		}
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, b)
}

// GetMyCanvasBatch GET /user/canvas/:id/batch：画布最近一个批次（含条目）；没有返回 data=null。
func GetMyCanvasBatch(c *gin.Context) {
	userId := c.GetInt("id")
	cv, err := model.GetUserCanvas(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询画布失败")
		return
	}
	b, err := model.GetLatestBatchByCanvas(userId, cv.Id)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			common.ApiSuccess(c, nil)
			return
		}
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, service.BatchView(b))
}

// BatchAction POST /user/batch/:id/:action（pause / resume / cancel / retry-failed）
func BatchAction(c *gin.Context) {
	userId := c.GetInt("id")
	b, err := model.GetUserBatch(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	switch c.Param("action") {
	case "pause":
		err = service.PauseBatch(b)
	case "resume":
		err = service.ResumeBatch(b)
	case "cancel":
		err = service.CancelBatch(b)
	case "retry-failed":
		_, err = service.RetryFailedBatch(b)
	default:
		common.ApiErrorMsg(c, "不支持的操作")
		return
	}
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	fresh, _ := model.GetUserBatch(userId, b.Id)
	common.ApiSuccess(c, service.BatchView(fresh))
}
