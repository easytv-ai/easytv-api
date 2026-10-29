package controller

import (
	"strings"

	"easytv-api/common"
	"easytv-api/model"

	"github.com/gin-gonic/gin"
)

// ===== 迭代四管理端：主体库只读、团队额度（API.md 10.9）=====

// AdminGetSubjects GET /admin/subject?user_id=&team_id=&kind=&shared=&keyword=&page
func AdminGetSubjects(c *gin.Context) {
	q := model.DB.Model(&model.Subject{})
	if v := queryInt(c, "user_id", 0); v > 0 {
		q = q.Where("user_id = ?", v)
	}
	if v := queryInt(c, "team_id", 0); v > 0 {
		q = q.Where("team_id = ?", v)
	}
	if k := strings.TrimSpace(c.Query("kind")); model.SubjectKinds[k] {
		q = q.Where("kind = ?", k)
	}
	if v := strings.TrimSpace(c.Query("shared")); v == "0" || v == "1" {
		q = q.Where("shared = ?", v)
	}
	if kw := strings.TrimSpace(c.Query("keyword")); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("(name LIKE ? OR look LIKE ?)", like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	page := parsePageInfo(c)
	var list []*model.Subject
	if err := q.Order("id desc").Limit(page.GetPageSize()).Offset(page.GetStartIdx()).Find(&list).Error; err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	ownerIds := make([]int, 0, len(list))
	for _, s := range list {
		ownerIds = append(ownerIds, s.UserId)
	}
	names := model.GetUserNicknames(ownerIds)
	items := make([]gin.H, 0, len(list))
	for _, s := range list {
		s.FillView(0)
		items = append(items, gin.H{
			"id": s.Id, "user_id": s.UserId, "owner_name": names[s.UserId], "team_id": s.TeamId,
			"kind": s.Kind, "name": s.Name, "look": s.Look, "prompt": s.Prompt, "cover_url": s.CoverUrl,
			"refs": s.RefsView, "refs_count": len(s.ParseRefs()), "shared": s.Shared, "uses": s.Uses,
			"created_at": s.CreatedAt, "updated_at": s.UpdatedAt,
		})
	}
	pagedResponse(c, items, total)
}

// AdminDeleteSubject DELETE /admin/subject/:id 违规主体软删
func AdminDeleteSubject(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的 ID")
		return
	}
	if err := model.DB.Where("id = ?", id).Delete(&model.Subject{}).Error; err != nil {
		common.ApiErrorMsg(c, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// AdminGetTeams GET /admin/team?keyword=&owner_id=&page
func AdminGetTeams(c *gin.Context) {
	list, total, err := model.GetTeamsAdmin(c.Query("keyword"), queryInt(c, "owner_id", 0), parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// AdminGetTeamMembers GET /admin/team/:id/members
func AdminGetTeamMembers(c *gin.Context) {
	members, err := model.GetTeamMembers(paramInt(c, "id"))
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	model.AttachMemberQuotas(members)
	common.ApiSuccess(c, gin.H{"items": members, "total": len(members)})
}

// AdminSetTeamMemberLimit PUT /admin/team/member/:id/limit { points_limit }（Root）
func AdminSetTeamMemberLimit(c *gin.Context) {
	id := paramInt(c, "id")
	var req struct {
		PointsLimit int `json:"points_limit"`
	}
	if id == 0 || common.DecodeJson(c.Request.Body, &req) != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.PointsLimit < 0 || req.PointsLimit > 10000000 {
		common.ApiErrorMsg(c, "额度需在 0 到 1000 万之间，0 表示不限")
		return
	}
	if err := model.SetMemberPointsLimitById(id, req.PointsLimit); err != nil {
		respondModelError(c, err, "保存失败")
		return
	}
	common.ApiSuccess(c, nil)
}
