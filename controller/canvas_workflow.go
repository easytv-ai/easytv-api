package controller

import (
	"encoding/json"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// ===== 工作流模板（API.md 31，L3）=====

// workflowGraph 只校验外形：节点非空、连线可缺省，其余字段原样存回，前端版本变了也不用改后端。
type workflowGraph struct {
	Nodes []json.RawMessage `json:"nodes"`
	Edges []json.RawMessage `json:"edges"`
}

type workflowPayload struct {
	Name     *string        `json:"name"`
	CoverUrl *string        `json:"cover_url"`
	Graph    *workflowGraph `json:"graph"`
}

// GetMyWorkflows GET /user/workflow?keyword=&page
func GetMyWorkflows(c *gin.Context) {
	userId := c.GetInt("id")
	list, total, err := model.GetCanvasWorkflows(model.CanvasWorkflowFilter{
		UserId: userId, TeamId: teamIdOf(c), Keyword: c.Query("keyword"),
	}, parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// GetMyWorkflow GET /user/workflow/:id —— 带 graph，发送到画布时调
func GetMyWorkflow(c *gin.Context) {
	userId := c.GetInt("id")
	w, err := model.GetCanvasWorkflow(userId, teamIdOf(c), paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	model.BumpCanvasWorkflowUses(w.Id)
	w.FillView(userId, true)
	common.ApiSuccess(c, w)
}

// CreateMyWorkflow POST /user/workflow
func CreateMyWorkflow(c *gin.Context) {
	userId := c.GetInt("id")
	var req workflowPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	name := ""
	if req.Name != nil {
		name = trimLimit(*req.Name, 50)
	}
	if name == "" {
		common.ApiErrorMsg(c, "请填写工作流名称")
		return
	}
	if req.Graph == nil || len(req.Graph.Nodes) == 0 {
		common.ApiErrorMsg(c, "工作流里没有节点")
		return
	}
	raw, err := json.Marshal(req.Graph)
	if err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if len(raw) > model.CanvasWorkflowGraphMax {
		common.ApiErrorMsg(c, "工作流太大，请减少节点后再保存")
		return
	}
	w := &model.CanvasWorkflow{
		UserId:    userId,
		TeamId:    teamIdOf(c),
		Name:      name,
		NodeCount: len(req.Graph.Nodes),
		EdgeCount: len(req.Graph.Edges),
		Graph:     string(raw),
	}
	if req.CoverUrl != nil {
		w.CoverUrl = trimLimit(*req.CoverUrl, 1000)
	}
	if err := w.Insert(); err != nil {
		respondModelError(c, err, "保存失败")
		return
	}
	w.FillView(userId, false)
	common.ApiSuccess(c, w)
}

// UpdateMyWorkflow PUT /user/workflow/:id —— 只改名字和封面，图内容不在这里覆盖
func UpdateMyWorkflow(c *gin.Context) {
	userId := c.GetInt("id")
	w, err := model.GetOwnCanvasWorkflow(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	var req workflowPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	fields := map[string]any{}
	if req.Name != nil {
		name := trimLimit(*req.Name, 50)
		if name == "" {
			common.ApiErrorMsg(c, "请填写工作流名称")
			return
		}
		fields["name"] = name
		w.Name = name
	}
	if req.CoverUrl != nil {
		w.CoverUrl = trimLimit(*req.CoverUrl, 1000)
		fields["cover_url"] = w.CoverUrl
	}
	if len(fields) > 0 {
		if err := w.Update(fields); err != nil {
			respondModelError(c, err, "保存失败")
			return
		}
	}
	w.FillView(userId, false)
	common.ApiSuccess(c, w)
}

// DeleteMyWorkflow DELETE /user/workflow/:id
func DeleteMyWorkflow(c *gin.Context) {
	userId := c.GetInt("id")
	w, err := model.GetOwnCanvasWorkflow(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if err := w.Delete(); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}
