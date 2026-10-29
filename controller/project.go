package controller

import (
	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 项目（用户侧）=====

// GetMyProjects 项目列表（GET /user/project?keyword=&page=&page_size=）。按请求空间（API.md 20）：
//   - 个人空间：自己 team_id=0 的项目；
//   - 团队空间：team_id=该团队的全部项目（不分作者），行带 owner_id / owner_nickname / canvas_id 与 permission / can_edit / can_delete / can_move。
//
// 兼容：个人空间请求带 scope=team 时按用户所在团队的团队空间列（老前端用法，新前端改用 EasyTV-Team 头）。
func GetMyProjects(c *gin.Context) {
	userId := c.GetInt("id")
	teamId := teamIdOf(c)
	var member *model.TeamMember
	if teamId == 0 && c.Query("scope") == "team" {
		member = service.MembershipOf(userId)
		if member == nil {
			pagedResponse(c, []*model.Project{}, 0)
			return
		}
		teamId = member.TeamId
	}
	var (
		list  []*model.Project
		total int64
		err   error
	)
	if teamId > 0 {
		list, total, err = model.GetTeamProjects(teamId, c.Query("keyword"), parsePageInfo(c))
		if err == nil && member == nil {
			member = service.MembershipOf(userId)
		}
	} else {
		list, total, err = model.GetUserProjects(userId, c.Query("keyword"), parsePageInfo(c))
	}
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	service.FillProjectAccess(userId, member, list...)
	model.AttachProjectOpenComments(list)
	pagedResponse(c, list, total)
}

// GetMyTrashProjects 回收站（GET /user/project/trash）。
func GetMyTrashProjects(c *gin.Context) {
	userId := c.GetInt("id")
	list, total, err := model.GetUserTrashProjects(userId, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// CreateProject 新建项目（POST /user/project）。
func CreateProject(c *gin.Context) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	name := trimLimit(req.Name, 100)
	if name == "" {
		common.ApiErrorMsg(c, "请填写项目名称")
		return
	}
	// 团队空间里新建的项目直接落在团队（API.md 20）
	p := &model.Project{
		UserId:      c.GetInt("id"),
		TeamId:      teamIdOf(c),
		Name:        name,
		Description: trimLimit(req.Description, 500),
	}
	if err := p.Insert(); err != nil {
		respondModelError(c, err, "创建失败")
		return
	}
	service.FillProjectAccess(p.UserId, nil, p)
	common.ApiSuccess(c, p)
}

// GetMyProject 项目详情（GET /user/project/:id），附带画布列表。
func GetMyProject(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的项目 ID")
		return
	}
	p, err := model.GetUserProject(userId, id)
	if err != nil {
		// 团队空间的项目：同团队成员按角色访问（API.md 10.10 / 15 / 20）
		shared, ok := service.CanAccessProject(userId, id)
		if !ok {
			respondModelError(c, err, "查询失败")
			return
		}
		p = shared
		model.AttachProjectOwners([]*model.Project{p})
	}
	var member *model.TeamMember
	if p.TeamId > 0 {
		member = service.MembershipOf(userId)
	}
	service.FillProjectAccess(userId, member, p)
	canvases, err := model.GetProjectCanvases(p.UserId, id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	views := make([]map[string]any, 0, len(canvases))
	for _, cv := range canvases {
		views = append(views, cv.View(false))
	}
	if len(canvases) > 0 {
		p.CanvasId = canvases[0].Id
	}
	p.OpenComments = int(model.CountOpenProjectComments(id))
	common.ApiSuccess(c, gin.H{
		"project":  p,
		"canvases": views,
	})
}

// UpdateProject 修改项目（PUT /user/project/:id）。
func UpdateProject(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的项目 ID")
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		CoverUrl    *string `json:"cover_url"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	fields := map[string]any{}
	if req.Name != nil {
		name := trimLimit(*req.Name, 100)
		if name == "" {
			common.ApiErrorMsg(c, "项目名称不能为空")
			return
		}
		fields["name"] = name
	}
	if req.Description != nil {
		fields["description"] = trimLimit(*req.Description, 500)
	}
	if req.CoverUrl != nil {
		fields["cover_url"] = trimLimit(*req.CoverUrl, 500)
	}
	if len(fields) == 0 {
		common.ApiErrorMsg(c, "没有需要更新的字段")
		return
	}
	if err := model.UpdateProjectFields(userId, id, fields); err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// TrashProject 项目进回收站（DELETE /user/project/:id）。
func TrashProject(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的项目 ID")
		return
	}
	ownerId, ok := projectOwnerForAdmin(c, userId, id, false)
	if !ok {
		return
	}
	if err := model.TrashProject(ownerId, id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// RestoreProject 从回收站恢复（POST /user/project/:id/restore）。
func RestoreProject(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的项目 ID")
		return
	}
	ownerId, ok := projectOwnerForAdmin(c, userId, id, true)
	if !ok {
		return
	}
	if err := model.RestoreProject(ownerId, id); err != nil {
		respondModelError(c, err, "恢复失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// projectOwnerForAdmin 删除 / 恢复项目的操作对象：自己的项目返回自己；团队空间里队友的项目要求操作者是该团队所有者或管理员（API.md 15 / 20）。
// trashed 为 true 时在回收站里找。
func projectOwnerForAdmin(c *gin.Context, userId, projectId int, trashed bool) (int, bool) {
	p := &model.Project{}
	q := model.DB.Where("id = ?", projectId)
	if trashed {
		q = model.DB.Unscoped().Where("id = ? AND deleted_at IS NOT NULL", projectId)
	}
	if err := q.First(p).Error; err != nil {
		respondModelError(c, model.ErrNotFound, "项目不存在")
		return 0, false
	}
	if p.UserId == userId {
		return userId, true
	}
	// 只有团队空间的项目才能由团队管理员代删 / 代恢复（API.md 20）
	m := service.MembershipOf(userId)
	if p.TeamId <= 0 || m == nil || m.TeamId != p.TeamId || m.EffectivePermission() != model.TeamPermAdmin {
		respondModelError(c, model.ErrNotFound, "项目不存在")
		return 0, false
	}
	return p.UserId, true
}

// MoveProject 项目换空间（POST /user/project/:id/move { team_id }，API.md 20）。只有项目作者能操作：
//   - team_id > 0：移入团队空间，要求作者是该团队成员且权限不是查看者；
//   - team_id = 0：移回个人空间。
//
// 项目里作者的资产（上传到项目的、项目画布上生成的）一起换空间。返回更新后的项目。
func MoveProject(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的项目 ID")
		return
	}
	var req struct {
		TeamId *int `json:"team_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || req.TeamId == nil || *req.TeamId < 0 {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	p, err := model.GetUserProject(userId, id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	member := service.MembershipOf(userId)
	if err := service.CheckProjectMove(p, member, *req.TeamId); err != nil {
		if respondSpaceError(c, err) {
			return
		}
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if err := model.MoveProjectToTeam(userId, id, *req.TeamId); err != nil {
		respondModelError(c, err, "移动失败")
		return
	}
	p, err = model.GetUserProject(userId, id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	service.FillProjectAccess(userId, member, p)
	common.ApiSuccess(c, p)
}
