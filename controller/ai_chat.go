package controller

import (
	"bytes"
	"encoding/json"
	"io"

	"easytv-api/common"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== AI 对话每日次数（用户侧）=====

// GetMyChatQuota 今日 AI 对话配额（GET /user/ai-chat/quota）。
// 空间口径与 GET /user/usage 一致：TeamSpace 中间件解析出的团队 id，个人空间为 0。
func GetMyChatQuota(c *gin.Context) {
	q, err := service.GetChatQuota(c.GetInt("id"), teamIdOf(c))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, q)
}

// GuardClientChatFree 拦截客户端自带的 params.agent.free。
//
// 免积分对话的 free 标记只能由服务端写：画布对话入口（POST /user/canvas/:id/agent → NewAgentTask /
// NewAgentRetryTask）在开关开启时落 free=true。而 POST /user/task 的 params 是客户端原样透传的，
// 若放行 free=true，任何登录用户都能建出免积分对话任务（跳过估价与预扣），开关关闭回滚时也一样白嫖。
// 挂在 POST /user/task 上：发现客户端指定 free 就按无效参数拒绝；请求体原样留给后面的 handler。
func GuardClientChatFree() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request == nil || c.Request.Body == nil {
			c.Next()
			return
		}
		raw, err := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewReader(raw)) // 后面的 CreateTask 还要读，先放回去
		if err != nil || len(raw) == 0 {
			c.Next()
			return
		}
		var body struct {
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(raw, &body) != nil || len(body.Params) == 0 {
			c.Next()
			return
		}
		var params struct {
			Agent *struct {
				Free json.RawMessage `json:"free"`
			} `json:"agent"`
			Chat *struct {
				Free json.RawMessage `json:"free"`
			} `json:"chat"`
		}
		if json.Unmarshal(body.Params, &params) != nil {
			c.Next()
			return
		}
		agentFree := params.Agent != nil && len(params.Agent.Free) > 0
		chatFree := params.Chat != nil && len(params.Chat.Free) > 0
		if !agentFree && !chatFree {
			c.Next()
			return
		}
		if chatFree && !agentFree {
			common.ApiErrorMsg(c, "params.chat.free 由服务端管理，请从对话入口发起")
		} else {
			common.ApiErrorMsg(c, "params.agent.free 由服务端管理，请从画布对话入口发起")
		}
		c.Abort()
	}
}

// CreateDirectorChat POST /user/ai-chat：导演台助手单轮文本任务，和画布对话共用每日次数。
// 不接受客户端 params：free 由 NewDirectorChatTask 按当前开关写入。
func CreateDirectorChat(c *gin.Context) {
	userId := c.GetInt("id")
	var req struct {
		Model    string `json:"model"`
		Prompt   string `json:"prompt"`
		CanvasId int    `json:"canvas_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	task, provider, err := service.NewDirectorChatTask(userId, req.Model, req.Prompt)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if req.CanvasId > 0 {
		cv, perm, err := service.CanvasAccess(userId, req.CanvasId)
		if err != nil {
			respondModelError(c, err, "查询画布失败")
			return
		}
		if !service.CanvasWritable(perm) {
			common.ApiErrorMsg(c, "你在团队里是查看者，不能在队友的画布上生成")
			return
		}
		task.CanvasId = cv.Id
		task.ProjectId = cv.ProjectId
	}
	if !resolveTaskTeam(c, task) {
		return
	}
	launchTask(c, task, provider)
}
