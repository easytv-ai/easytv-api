package controller

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// 对话式工作流生成（API.md 32，agent-contract 2.9 / 第 8 节）。
// 三个接口与 /user/canvas/:id/assist 同形：画布权限走 writableCanvas，任务提交走 launchTask。

// CreateCanvasAgent POST /user/canvas/:id/agent：带画布上下文多轮生成 / 改图，返回任务，前端轮询取计划。
func CreateCanvasAgent(c *gin.Context) {
	userId := c.GetInt("id")
	cv, ok := writableCanvas(c, userId, paramInt(c, "id"))
	if !ok {
		return
	}
	var req service.AgentRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	task, provider, err := service.NewAgentTask(userId, cv, req)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if !resolveTaskTeam(c, task) {
		return
	}
	model.RecordSimpleAudit(c, model.AuditCategoryUsage, "agent.plan.create",
		fmt.Sprintf("提交对话式工作流任务 [%s]", task.Model),
		map[string]interface{}{"canvas_id": cv.Id, "model": task.Model, "task_id": task.Id})
	launchTask(c, task, provider)
}

// GetCanvasAgentPlan GET /user/canvas/:id/agent/:task_id：读计划（方案 + 校验报告 + 步骤）。
func GetCanvasAgentPlan(c *gin.Context) {
	userId := c.GetInt("id")
	cv, ok := writableCanvas(c, userId, paramInt(c, "id"))
	if !ok {
		return
	}
	plan, err := service.GetAgentPlan(userId, cv, paramInt(c, "task_id"))
	if err != nil {
		respondModelError(c, err, "查询方案失败")
		return
	}
	common.ApiSuccess(c, plan)
}

// RetryCanvasAgent POST /user/canvas/:id/agent/:task_id/retry：带着上一轮校验报告再跑一次。
func RetryCanvasAgent(c *gin.Context) {
	userId := c.GetInt("id")
	cv, ok := writableCanvas(c, userId, paramInt(c, "id"))
	if !ok {
		return
	}
	var req struct {
		Note      string `json:"note"`
		MaxRounds int    `json:"max_rounds"`
	}
	// 重试可以不传 body（note / max_rounds 都可省），空 body 时用默认值
	if raw, _ := io.ReadAll(c.Request.Body); len(bytes.TrimSpace(raw)) > 0 {
		if err := common.DecodeJson(bytes.NewReader(raw), &req); err != nil {
			common.ApiErrorMsg(c, "无效的参数")
			return
		}
	}
	src, err := model.GetTaskById(paramInt(c, "task_id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	task, provider, err := service.NewAgentRetryTask(userId, cv, src, req.Note, req.MaxRounds)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			respondModelError(c, err, "查询失败")
			return
		}
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if !resolveTaskTeam(c, task) {
		return
	}
	model.RecordSimpleAudit(c, model.AuditCategoryUsage, "agent.plan.retry",
		fmt.Sprintf("重试对话式工作流任务（来源 #%d）", src.Id),
		map[string]interface{}{"canvas_id": cv.Id, "model": task.Model, "retry_of": src.Id})
	launchTask(c, task, provider)
}
