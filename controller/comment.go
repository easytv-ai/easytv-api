package controller

import (
	"fmt"
	"strings"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// ===== 作品评论（API.md 1.10 / 2.x）=====

// GetWorkComments 公开评论列表（GET /work/:id/comments?page=&page_size=）：一级评论分页，每条附最多 3 条回复。
func GetWorkComments(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的作品 ID")
		return
	}
	list, total, err := model.GetWorkComments(id, c.GetInt("id"), parsePageInfo(c), 3)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// GetCommentReplies 展开某条评论的全部回复（GET /comment/:id/replies）。
func GetCommentReplies(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的评论 ID")
		return
	}
	list, total, err := model.GetCommentReplies(id, c.GetInt("id"), parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if list == nil {
		list = []*model.WorkComment{}
	}
	pagedResponse(c, list, total)
}

// CreateWorkComment 发表评论 / 回复（POST /user/work/:id/comment）：`{ content, parent_id? }`。
func CreateWorkComment(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的作品 ID")
		return
	}
	var req struct {
		Content  string `json:"content"`
		ParentId int    `json:"parent_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		common.ApiErrorMsg(c, "评论内容不能为空")
		return
	}
	if len([]rune(content)) > model.CommentMaxLen {
		common.ApiErrorMsg(c, model.ErrCommentTooLong.Error())
		return
	}
	comment, err := model.CreateWorkComment(c.GetInt("id"), id, req.ParentId, content)
	if err != nil {
		respondModelError(c, err, "发表失败")
		return
	}
	notifyNewComment(comment)
	common.ApiSuccess(c, comment)
}

// notifyNewComment 回复通知被回复的人，一级评论通知作品作者（自己评论自己不通知）。
func notifyNewComment(comment *model.WorkComment) {
	if comment == nil {
		return
	}
	who := "有人"
	if comment.Author != nil && comment.Author.Nickname != "" {
		who = comment.Author.Nickname
	}
	link := fmt.Sprintf("/watch/%d", comment.WorkId)
	excerpt := truncateRunes(comment.Content, 60)
	if comment.ReplyToId > 0 && comment.ReplyToId != comment.UserId {
		model.PushNotification(comment.ReplyToId, model.NotificationTypeComment, who+" 回复了你的评论", excerpt, link)
	}
	w, err := model.GetWorkById(comment.WorkId)
	if err != nil || w.UserId == comment.UserId || w.UserId == comment.ReplyToId {
		return
	}
	model.PushNotification(w.UserId, model.NotificationTypeComment, who+" 评论了《"+w.Title+"》", excerpt, link)
}

// DeleteMyComment 删除评论（DELETE /user/comment/:id）：评论人本人或作品作者可删。
func DeleteMyComment(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的评论 ID")
		return
	}
	if err := model.DeleteWorkComment(c.GetInt("id"), id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// ReportComment 举报评论（POST /user/comment/:id/report）：`{ reason, content? }`，走社区审核。
func ReportComment(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的评论 ID")
		return
	}
	var req struct {
		Reason  string `json:"reason"`
		Content string `json:"content"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	reason := trimLimit(req.Reason, 50)
	if reason == "" {
		common.ApiErrorMsg(c, "请选择举报原因")
		return
	}
	r, err := model.CreateCommentReport(c.GetInt("id"), id, reason, trimLimit(req.Content, 500))
	if err != nil {
		respondModelError(c, err, "提交失败")
		return
	}
	common.ApiSuccess(c, r)
}

// ----- 管理端 -----

// AdminGetComments 评论列表（GET /admin/comment?status=&work_id=&user_id=&keyword=）。
func AdminGetComments(c *gin.Context) {
	filter := model.CommentAdminFilter{
		WorkId:  queryInt(c, "work_id", 0),
		UserId:  queryInt(c, "user_id", 0),
		Keyword: strings.TrimSpace(c.Query("keyword")),
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		status := queryInt(c, "status", 0)
		filter.Status = &status
	}
	list, total, err := model.GetWorkCommentsAdmin(filter, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// AdminSetCommentStatus 隐藏 / 恢复评论（PUT /admin/comment/:id/status）：`{ status: 1 | 2, remark? }`，隐藏时通知评论人。
func AdminSetCommentStatus(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的评论 ID")
		return
	}
	var req struct {
		Status int    `json:"status"`
		Remark string `json:"remark"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Status != model.CommentStatusNormal && req.Status != model.CommentStatusHidden {
		common.ApiErrorMsg(c, "无效的状态")
		return
	}
	comment, err := model.SetWorkCommentStatus(id, req.Status, trimLimit(req.Remark, 255))
	if err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.SysLog(fmt.Sprintf("admin %d set comment %d status=%d", c.GetInt("id"), id, req.Status))
	if req.Status == model.CommentStatusHidden {
		notifyCommentHidden(comment, req.Remark)
	}
	common.ApiSuccess(c, nil)
}

// AdminDeleteComment 删除评论（DELETE /admin/comment/:id）。
func AdminDeleteComment(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的评论 ID")
		return
	}
	if err := model.AdminDeleteWorkComment(id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// notifyCommentHidden 评论被隐藏时通知评论人。
func notifyCommentHidden(comment *model.WorkComment, remark string) {
	if comment == nil {
		return
	}
	title := "你的评论已被隐藏"
	content := "评论「" + truncateRunes(comment.Content, 40) + "」不再对其他人展示。"
	if remark = strings.TrimSpace(remark); remark != "" {
		content += "原因：" + remark
	}
	link := fmt.Sprintf("/watch/%d", comment.WorkId)
	model.PushNotification(comment.UserId, model.NotificationTypeComment, title, content, link)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
