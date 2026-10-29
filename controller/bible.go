package controller

import (
	"encoding/json"
	"errors"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 剧集圣经（API.md 27）=====

// GetMyProjectBible GET /user/project/:id/bible
func GetMyProjectBible(c *gin.Context) {
	userId := c.GetInt("id")
	project, err := model.GetUserProject(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询项目失败")
		return
	}
	row, err := model.GetSeriesBible(project.Id)
	if errors.Is(err, model.ErrNotFound) {
		empty := &model.Bible{}
		_ = empty.Normalize()
		empty.Title = project.Name
		common.ApiSuccess(c, gin.H{"project_id": project.Id, "revision": 0, "bible": empty, "updated_at": 0})
		return
	}
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	b, err := row.Parse()
	if err != nil {
		common.ApiErrorMsg(c, "剧集设定数据损坏")
		return
	}
	common.ApiSuccess(c, gin.H{"project_id": project.Id, "revision": row.Revision, "bible": b, "updated_at": row.UpdatedAt})
}

// SaveMyProjectBible PUT /user/project/:id/bible { revision, bible }
func SaveMyProjectBible(c *gin.Context) {
	userId := c.GetInt("id")
	project, err := model.GetUserProject(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询项目失败")
		return
	}
	var req struct {
		Revision int             `json:"revision"`
		Bible    json.RawMessage `json:"bible"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || len(req.Bible) == 0 {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	b := &model.Bible{}
	if err := json.Unmarshal(req.Bible, b); err != nil {
		common.ApiErrorMsg(c, "剧集设定格式不正确")
		return
	}
	if err := b.Normalize(); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	revision, err := model.SaveSeriesBible(userId, project.Id, req.Revision, b)
	if err != nil {
		if errors.Is(err, model.ErrBibleVersionConflict) {
			common.ApiErrorMsg(c, err.Error())
			return
		}
		respondModelError(c, err, "保存失败")
		return
	}
	service.Track(userId, "bible_save", map[string]any{"project_id": project.Id, "revision": revision})
	common.ApiSuccess(c, gin.H{"project_id": project.Id, "revision": revision, "bible": b})
}

// ImpactMyProjectBible POST /user/project/:id/bible/impact { character | prop | fact | text }
func ImpactMyProjectBible(c *gin.Context) {
	userId := c.GetInt("id")
	project, err := model.GetUserProject(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询项目失败")
		return
	}
	var q service.ImpactQuery
	if err := common.DecodeJson(c.Request.Body, &q); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	b := &model.Bible{}
	if row, err := model.GetSeriesBible(project.Id); err == nil {
		if parsed, err := row.Parse(); err == nil {
			b = parsed
		}
	}
	_ = b.Normalize()
	episodes, err := model.GetProjectScreenplays(userId, project.Id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, gin.H{"affected": service.ImpactAnalysis(b, episodes, q), "episodes": len(episodes)})
}

// GetMyProjectEpisodes GET /user/project/:id/episodes 集目录
func GetMyProjectEpisodes(c *gin.Context) {
	userId := c.GetInt("id")
	project, err := model.GetUserProject(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询项目失败")
		return
	}
	list, err := model.GetProjectScreenplays(userId, project.Id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	items := make([]gin.H, 0, len(list))
	for _, sp := range list {
		title := ""
		scenes := 0
		shots := 0
		if plan, err := sp.ParsePlan(); err == nil {
			title, scenes, shots = plan.Title, len(plan.Scenes), len(plan.Shots)
		}
		items = append(items, gin.H{
			"id": sp.Id, "episode_no": sp.EpisodeNo, "title": title, "status": sp.Status, "canvas_id": sp.CanvasId,
			"scenes": scenes, "shots": shots, "revision": sp.Revision, "stale": sp.Stale, "created_at": sp.CreatedAt, "updated_at": sp.UpdatedAt,
		})
	}
	common.ApiSuccess(c, gin.H{"project_id": project.Id, "episodes": items})
}

// SetMyScreenplayEpisode PUT /user/screenplay/:id/episode { episode_no }
func SetMyScreenplayEpisode(c *gin.Context) {
	userId := c.GetInt("id")
	sp, err := model.GetUserScreenplay(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	var req struct {
		EpisodeNo int `json:"episode_no"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || req.EpisodeNo < 0 || req.EpisodeNo > 999 {
		common.ApiErrorMsg(c, "无效的集号")
		return
	}
	if err := model.UpdateScreenplayFields(sp.Id, map[string]any{"episode_no": req.EpisodeNo}); err != nil {
		common.ApiErrorMsg(c, "保存失败")
		return
	}
	common.ApiSuccess(c, gin.H{"id": sp.Id, "episode_no": req.EpisodeNo})
}

// RecordMyScreenplayEpisode POST /user/screenplay/:id/record 用文本模型生成本集连续性记录并并入圣经
func RecordMyScreenplayEpisode(c *gin.Context) {
	userId := c.GetInt("id")
	sp, err := model.GetUserScreenplay(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	if sp.ProjectId <= 0 {
		common.ApiErrorMsg(c, "策划案没有挂在项目下，无法记录到剧集设定")
		return
	}
	if sp.Plan == "" {
		common.ApiErrorMsg(c, "策划案还没生成")
		return
	}
	teamId, err := service.ResolveTaskTeam(userId, teamIdOf(c), sp.ProjectId)
	if err != nil {
		respondModelError(c, err, "查询项目失败")
		return
	}
	bible, draft, points, err := service.GenerateEpisodeRecord(userId, teamId, sp, sp.ProjectId)
	if err != nil {
		if respondSpaceError(c, err) {
			return
		}
		common.ApiErrorMsg(c, err.Error())
		return
	}
	row, _ := model.GetSeriesBible(sp.ProjectId)
	revision := 0
	if row != nil {
		revision = row.Revision
	}
	common.ApiSuccess(c, gin.H{"bible": bible, "revision": revision, "record": draft, "points": points, "episode_no": sp.EpisodeNo})
}
