package controller

import (
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 技能模板（公开）=====

// GetPublicSkills 已上架官方技能列表（GET /skill?category=&keyword=&page=&page_size=），带登录态时附 favorited。
func GetPublicSkills(c *gin.Context) {
	on := model.SkillStatusOn
	filter := model.SkillFilter{
		Category:      strings.TrimSpace(c.Query("category")),
		Keyword:       strings.TrimSpace(c.Query("keyword")),
		Status:        &on,
		PublicVisible: true, // 官方 + 已上架的用户技能（T26）
	}
	switch strings.TrimSpace(c.Query("source")) {
	case "official":
		uid := model.SkillUserOfficial
		filter.UserId = &uid
	case "community":
		uid := model.SkillUserAllPrivate
		filter.UserId = &uid
	}
	list, total, err := model.GetSkills(filter, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	model.AttachSkillFavorited(c.GetInt("id"), list)
	pagedResponse(c, list, total)
}

// GetPublicSkill 技能详情（GET /skill/:id），使用次数 +1。
func GetPublicSkill(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的 Skill ID")
		return
	}
	s, err := model.GetSkillById(id)
	if err != nil || s.Status != model.SkillStatusOn {
		common.ApiErrorMsg(c, model.ErrNotFound.Error())
		return
	}
	model.IncreaseSkillUses(id)
	s.Uses++
	if uid := c.GetInt("id"); uid > 0 {
		s.Favorited = model.IsSkillFavorited(uid, id)
	}
	common.ApiSuccess(c, s.WithGraph())
}

// GetMySkill 我的 Skill 详情（GET /user/skill/:id），含画布模板；官方已上架技能也可读。
func GetMySkill(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的 Skill ID")
		return
	}
	userId := c.GetInt("id")
	s, err := model.GetSkillById(id)
	if err != nil || (s.UserId == 0 && s.Status != model.SkillStatusOn) {
		common.ApiErrorMsg(c, model.ErrNotFound.Error())
		return
	}
	// 别人的私有技能：只有同团队且已共享才可读（F7，API.md 10.10）
	if s.UserId != 0 && s.UserId != userId {
		if s.Shared != 1 || s.TeamId == 0 || s.TeamId != service.TeamIdOf(userId) {
			common.ApiErrorMsg(c, model.ErrNotFound.Error())
			return
		}
		s.TeamShared = true
	}
	s.Favorited = model.IsSkillFavorited(userId, id)
	common.ApiSuccess(c, s.WithGraph())
}

// ===== 用户侧：收藏 / 我的 Skill =====

// ToggleSkillFavorite 收藏 / 取消收藏（POST /user/skill/:id/favorite）。
func ToggleSkillFavorite(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的 Skill ID")
		return
	}
	favorited, stars, err := model.ToggleSkillFavorite(c.GetInt("id"), id)
	if err != nil {
		respondModelError(c, err, "操作失败")
		return
	}
	common.ApiSuccess(c, gin.H{"favorited": favorited, "stars": stars})
}

// GetMyFavoriteSkills 我的收藏（GET /user/skill/favorites?category=&keyword=）。
func GetMyFavoriteSkills(c *gin.Context) {
	list, total, err := model.GetFavoriteSkills(c.GetInt("id"),
		strings.TrimSpace(c.Query("category")), strings.TrimSpace(c.Query("keyword")), parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// GetMySkills 我的 Skill（GET /user/skill）。
func GetMySkills(c *gin.Context) {
	userId := c.GetInt("id")
	teamId := service.TeamIdOf(userId)
	list, total, err := model.GetSkills(model.SkillFilter{
		Keyword: strings.TrimSpace(c.Query("keyword")),
		UserId:  &userId,
		TeamId:  teamId,
	}, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	model.AttachSkillFavorited(userId, list)
	// 标记哪些是团队成员共享过来的，前端据此隐藏编辑与删除
	shared := []*model.Skill{}
	for _, s := range list {
		if s.UserId != userId {
			s.TeamShared = true
			shared = append(shared, s)
		}
	}
	model.AttachSkillAuthors(shared)
	pagedResponse(c, list, total)
}

type userSkillPayload struct {
	// CanvasId 从画布存为 Skill：把该画布的 graph（去敏）作为模板；传 0 表示清除模板
	CanvasId    *int    `json:"canvas_id"`
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Category    *string `json:"category"`
	CoverUrl    *string `json:"cover_url"`
	Prompt      *string `json:"prompt"`
	// 执行契约（只影响文本任务）
	Guide        *string `json:"guide"`
	OutputFormat *string `json:"output_format"`
	Scene        *string `json:"scene"`
	Howto        *string `json:"howto"`
	Output       *string `json:"output"`
	// Shared 共享给团队（F7，API.md 10.10）；用户不在团队时置 true 会报错
	Shared *bool `json:"shared"`
}

func (p *userSkillPayload) fields() (map[string]any, string) {
	f := map[string]any{}
	if p.Title != nil {
		title := trimLimit(*p.Title, 100)
		if title == "" {
			return nil, "请填写名称"
		}
		f["title"] = title
	}
	if p.Description != nil {
		f["description"] = trimLimit(*p.Description, 500)
	}
	if p.Category != nil {
		f["category"] = trimLimit(*p.Category, 50)
	}
	if p.CoverUrl != nil {
		f["cover_url"] = trimLimit(*p.CoverUrl, 1000)
	}
	if p.Prompt != nil {
		prompt := strings.TrimSpace(*p.Prompt)
		if prompt == "" {
			return nil, "请填写提示词模板"
		}
		if len(prompt) > 4000 {
			return nil, "提示词模板不能超过 4000 字符"
		}
		f["prompt"] = prompt
	}
	if p.Guide != nil {
		guide := strings.TrimSpace(*p.Guide)
		if len([]rune(guide)) > service.SkillGuideMaxChars {
			return nil, "执行规则不能超过 4000 字符"
		}
		f["guide"] = guide
	}
	if p.OutputFormat != nil {
		format := strings.TrimSpace(*p.OutputFormat)
		if len([]rune(format)) > service.SkillGuideMaxChars {
			return nil, "输出格式不能超过 4000 字符"
		}
		f["output_format"] = format
	}
	if p.Scene != nil {
		f["scene"] = trimLimit(*p.Scene, 200)
	}
	if p.Howto != nil {
		f["howto"] = trimLimit(*p.Howto, 500)
	}
	if p.Output != nil {
		f["output"] = trimLimit(*p.Output, 500)
	}
	return f, ""
}

// applyCanvasTemplate 把用户画布的 graph 写进字段（去掉任务状态与资产 ID）。
func applyCanvasTemplate(userId int, canvasId *int, f map[string]any) string {
	if canvasId == nil {
		return ""
	}
	if *canvasId <= 0 {
		f["graph"] = ""
		f["graph_nodes"] = 0
		return ""
	}
	cv, err := model.GetUserCanvas(userId, *canvasId)
	if err != nil {
		return "画布不存在"
	}
	graph := model.SanitizeSharedGraph(cv.Graph)
	f["graph"] = graph
	f["graph_nodes"] = (&model.Canvas{Graph: graph}).NodeCount()
	return ""
}

// applyTeamShare 处理 shared 开关（F7，API.md 10.10）：置 true 时写入当前团队 id。
func applyTeamShare(userId int, shared *bool, f map[string]any) string {
	if shared == nil {
		return ""
	}
	if !*shared {
		f["shared"] = 0
		return ""
	}
	teamId := service.TeamIdOf(userId)
	if teamId == 0 {
		return "你还不在团队里，无法共享给团队"
	}
	f["team_id"] = teamId
	f["shared"] = 1
	return ""
}

// CreateMySkill 新建私有 Skill（POST /user/skill）：title / prompt 必填，`{{prompt}}` 为灵感占位。
func CreateMySkill(c *gin.Context) {
	userId := c.GetInt("id")
	var req userSkillPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Title == nil || req.Prompt == nil {
		common.ApiErrorMsg(c, "请填写名称和提示词模板")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if msg := applyCanvasTemplate(userId, req.CanvasId, f); msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if msg := applyTeamShare(userId, req.Shared, f); msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	str := func(k string) string {
		v, _ := f[k].(string)
		return v
	}
	nickname := ""
	if u, err := model.GetUserById(userId); err == nil && u != nil {
		nickname = u.Nickname
		if nickname == "" {
			nickname = u.Username
		}
	}
	s := &model.Skill{
		UserId:      userId,
		Title:       str("title"),
		Description: str("description"),
		AuthorName:  nickname,
		Category:    str("category"),
		CoverUrl:    str("cover_url"),
		Prompt:      str("prompt"),
		Scene:       str("scene"),
		Howto:       str("howto"),
		Output:      str("output"),
		Status:      model.SkillStatusOn,
		Graph:       str("graph"),
	}
	if v, ok := f["graph_nodes"].(int); ok {
		s.GraphNodes = v
	}
	if v, ok := f["shared"].(int); ok {
		s.Shared = v
	}
	if v, ok := f["team_id"].(int); ok {
		s.TeamId = v
	}
	if err := s.Insert(); err != nil {
		respondModelError(c, err, "创建失败")
		return
	}
	common.ApiSuccess(c, s)
}

// UpdateMySkill 修改私有 Skill（PUT /user/skill/:id）。
func UpdateMySkill(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的 Skill ID")
		return
	}
	var req userSkillPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	f, msg := req.fields()
	if msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if msg := applyCanvasTemplate(c.GetInt("id"), req.CanvasId, f); msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if msg := applyTeamShare(c.GetInt("id"), req.Shared, f); msg != "" {
		common.ApiErrorMsg(c, msg)
		return
	}
	if len(f) == 0 {
		common.ApiErrorMsg(c, "没有需要更新的字段")
		return
	}
	if err := model.UpdateUserSkillFields(c.GetInt("id"), id, f); err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// DeleteMySkill 删除私有 Skill（DELETE /user/skill/:id）。
func DeleteMySkill(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的 Skill ID")
		return
	}
	if err := model.DeleteUserSkill(c.GetInt("id"), id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}
