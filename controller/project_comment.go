package controller

import (
	"fmt"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// 项目评论批注（F7，API.md 10.10）：项目所有者与同团队成员可读写。

const projectCommentNotFound = "项目不存在或无权访问"

// GetProjectComments GET /user/project/:id/comment?canvas_id=&node_id=&only_open=1
func GetProjectComments(c *gin.Context) {
	projectId := paramInt(c, "id")
	userId := c.GetInt("id")
	if _, ok := service.CanAccessProject(userId, projectId); !ok {
		common.ApiErrorMsg(c, projectCommentNotFound)
		return
	}
	list, total, err := model.GetProjectComments(model.ProjectCommentFilter{
		ProjectId: projectId,
		CanvasId:  queryInt(c, "canvas_id", 0),
		NodeId:    trimLimit(c.Query("node_id"), 40),
		OnlyOpen:  c.Query("only_open") == "1",
	}, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	model.AttachProjectCommentUsers(list)
	pagedResponse(c, list, total)
}

type projectCommentPayload struct {
	Content  string `json:"content"`
	CanvasId int    `json:"canvas_id"`
	NodeId   string `json:"node_id"`
}

// CreateProjectComment POST /user/project/:id/comment
func CreateProjectComment(c *gin.Context) {
	projectId := paramInt(c, "id")
	userId := c.GetInt("id")
	p, ok := service.CanAccessProject(userId, projectId)
	if !ok {
		common.ApiErrorMsg(c, projectCommentNotFound)
		return
	}
	var req projectCommentPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	content := trimLimit(req.Content, model.ProjectCommentMaxLen)
	if content == "" {
		common.ApiErrorMsg(c, "请填写批注内容")
		return
	}
	pc := &model.ProjectComment{
		ProjectId: projectId,
		UserId:    userId,
		CanvasId:  req.CanvasId,
		NodeId:    trimLimit(req.NodeId, 40),
		Content:   content,
	}
	if err := pc.Insert(); err != nil {
		respondModelError(c, err, "提交失败")
		return
	}
	model.AttachProjectCommentUsers([]*model.ProjectComment{pc})
	// 别人在我的项目里留言时通知项目所有者；同一项目 10 分钟内只发一条，避免连续批注刷屏
	if p.UserId != userId {
		link := fmt.Sprintf("/projects?id=%d&comment=1", projectId)
		if !model.HasRecentNotification(p.UserId, model.NotificationTypeComment, link, common.GetTimestamp()-600) {
			model.PushNotification(p.UserId, model.NotificationTypeComment, "项目收到新批注",
				fmt.Sprintf("%s 在《%s》里留下批注：%s", pc.AuthorNickname, p.Name, trimLimit(content, 60)), link)
		}
	}
	common.ApiSuccess(c, pc)
}

type projectCommentResolvePayload struct {
	Resolved *bool `json:"resolved"`
}

// UpdateProjectComment PUT /user/project/comment/:id —— 标记解决 / 重新打开。
func UpdateProjectComment(c *gin.Context) {
	id := paramInt(c, "id")
	userId := c.GetInt("id")
	pc, err := model.GetProjectCommentById(id)
	if err != nil {
		common.ApiErrorMsg(c, projectCommentNotFound)
		return
	}
	p, ok := service.CanAccessProject(userId, pc.ProjectId)
	if !ok {
		common.ApiErrorMsg(c, projectCommentNotFound)
		return
	}
	var req projectCommentResolvePayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || req.Resolved == nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	// 作者、项目所有者、团队 owner 可操作
	if !canManageProjectComment(userId, pc, p) {
		common.ApiErrorMsg(c, "只有批注作者或项目所有者可以操作")
		return
	}
	if err := model.SetProjectCommentResolved(id, userId, *req.Resolved); err != nil {
		respondModelError(c, err, "操作失败")
		return
	}
	// 解决时通知批注作者（自己解决自己的不发）
	if *req.Resolved && pc.UserId != userId {
		name := ""
		if u, err := model.GetUserById(userId); err == nil && u != nil {
			name = strings.TrimSpace(u.Nickname)
			if name == "" {
				name = u.Username
			}
		}
		model.PushNotification(pc.UserId, model.NotificationTypeComment, "批注已处理",
			fmt.Sprintf("%s 把你在《%s》的批注标记为已解决", name, p.Name),
			fmt.Sprintf("/projects?id=%d&comment=1", pc.ProjectId))
	}
	common.ApiSuccess(c, nil)
}

// DeleteProjectComment DELETE /user/project/comment/:id
func DeleteProjectComment(c *gin.Context) {
	id := paramInt(c, "id")
	userId := c.GetInt("id")
	pc, err := model.GetProjectCommentById(id)
	if err != nil {
		common.ApiErrorMsg(c, projectCommentNotFound)
		return
	}
	p, ok := service.CanAccessProject(userId, pc.ProjectId)
	if !ok {
		common.ApiErrorMsg(c, projectCommentNotFound)
		return
	}
	if pc.UserId != userId && p.UserId != userId {
		common.ApiErrorMsg(c, "只有批注作者或项目所有者可以删除")
		return
	}
	if err := model.DeleteProjectComment(id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// canManageProjectComment 批注作者、项目所有者或团队 owner。
func canManageProjectComment(userId int, pc *model.ProjectComment, p *model.Project) bool {
	if pc.UserId == userId || p.UserId == userId {
		return true
	}
	if m, err := model.GetTeamMembership(userId); err == nil && m != nil && m.Role == model.TeamRoleOwner {
		return p.TeamId > 0 && m.TeamId == p.TeamId
	}
	return false
}
