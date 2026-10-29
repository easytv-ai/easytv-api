package controller

import (
	"easytv-api/common"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// CreateCanvasAssist POST /user/canvas/:id/assist：带画布上下文问助手（API.md 16）。
// 按一次文本任务计费，返回任务；前端轮询 GET /user/task/:id，完成后取方案。
func CreateCanvasAssist(c *gin.Context) {
	userId := c.GetInt("id")
	cv, ok := writableCanvas(c, userId, paramInt(c, "id"))
	if !ok {
		return
	}
	var req service.AssistRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	task, provider, err := service.NewAssistTask(userId, cv, req)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if !resolveTaskTeam(c, task) {
		return
	}
	launchTask(c, task, provider)
}

// GetCanvasAssistPlan GET /user/canvas/:id/assist/:task_id：助手任务的修改方案（校验后的修改 + 预计积分）。
func GetCanvasAssistPlan(c *gin.Context) {
	userId := c.GetInt("id")
	cv, ok := writableCanvas(c, userId, paramInt(c, "id"))
	if !ok {
		return
	}
	plan, err := service.GetAssistPlan(userId, cv, paramInt(c, "task_id"))
	if err != nil {
		respondModelError(c, err, "查询方案失败")
		return
	}
	common.ApiSuccess(c, plan)
}
