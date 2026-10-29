package controller

import (
	"encoding/json"
	"errors"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// 画布右侧对话（API.md 2.1）：会话和历史都挂在当前用户的这张画布上。

type canvasChatBody struct {
	Title    string          `json:"title"`
	Messages json.RawMessage `json:"messages"`
}

func loadOwnCanvas(c *gin.Context) (*model.Canvas, bool) {
	cv, err := model.GetUserCanvas(c.GetInt("id"), paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询画布失败")
		return nil, false
	}
	return cv, true
}

func readCanvasChatBody(c *gin.Context) (string, string, int, bool) {
	var req canvasChatBody
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return "", "", 0, false
	}
	title := model.NormalizeCanvasChatTitle(req.Title)
	messages, count, err := model.NormalizeCanvasChatMessages(req.Messages)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return "", "", 0, false
	}
	return title, messages, count, true
}

// GetMyCanvasChats GET /user/canvas/:id/chat 对话历史，不含正文。
func GetMyCanvasChats(c *gin.Context) {
	cv, ok := loadOwnCanvas(c)
	if !ok {
		return
	}
	list, err := model.ListCanvasChats(c.GetInt("id"), cv.Id)
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	items := make([]map[string]any, 0, len(list))
	for _, row := range list {
		items = append(items, row.Summary())
	}
	common.ApiSuccess(c, gin.H{"items": items})
}

// GetMyCanvasChat GET /user/canvas/:id/chat/:sid 一段对话，含消息。
func GetMyCanvasChat(c *gin.Context) {
	cv, ok := loadOwnCanvas(c)
	if !ok {
		return
	}
	sid := paramInt(c, "sid")
	if sid == 0 {
		common.ApiErrorMsg(c, "请指定对话")
		return
	}
	row, err := model.GetCanvasChat(c.GetInt("id"), cv.Id, sid)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, row.Detail())
}

// CreateMyCanvasChat POST /user/canvas/:id/chat 新建一段对话。
func CreateMyCanvasChat(c *gin.Context) {
	userId := c.GetInt("id")
	cv, ok := loadOwnCanvas(c)
	if !ok {
		return
	}
	title, messages, count, ok := readCanvasChatBody(c)
	if !ok {
		return
	}
	row, err := model.CreateCanvasChat(userId, cv.Id, title, messages, count)
	if err != nil {
		common.ApiErrorMsg(c, "保存失败")
		return
	}
	common.ApiSuccess(c, row.Detail())
}

// SaveMyCanvasChat PUT /user/canvas/:id/chat/:sid 覆盖保存一段对话。
func SaveMyCanvasChat(c *gin.Context) {
	userId := c.GetInt("id")
	cv, ok := loadOwnCanvas(c)
	if !ok {
		return
	}
	sid := paramInt(c, "sid")
	if sid == 0 {
		common.ApiErrorMsg(c, "请指定对话")
		return
	}
	title, messages, count, ok := readCanvasChatBody(c)
	if !ok {
		return
	}
	row, err := model.SaveCanvasChat(userId, cv.Id, sid, title, messages, count)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			common.ApiErrorMsg(c, model.ErrNotFound.Error())
			return
		}
		common.ApiErrorMsg(c, "保存失败")
		return
	}
	common.ApiSuccess(c, row.Detail())
}

// DeleteMyCanvasChat DELETE /user/canvas/:id/chat/:sid 删除一段对话。
func DeleteMyCanvasChat(c *gin.Context) {
	cv, ok := loadOwnCanvas(c)
	if !ok {
		return
	}
	sid := paramInt(c, "sid")
	if sid == 0 {
		common.ApiErrorMsg(c, "请指定对话")
		return
	}
	if err := model.DeleteCanvasChat(c.GetInt("id"), cv.Id, sid); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// ClearMyCanvasChats DELETE /user/canvas/:id/chat 清空这张画布的对话历史。
func ClearMyCanvasChats(c *gin.Context) {
	cv, ok := loadOwnCanvas(c)
	if !ok {
		return
	}
	n, err := model.ClearCanvasChats(c.GetInt("id"), cv.Id)
	if err != nil {
		common.ApiErrorMsg(c, "删除失败")
		return
	}
	common.ApiSuccess(c, gin.H{"deleted": n})
}
