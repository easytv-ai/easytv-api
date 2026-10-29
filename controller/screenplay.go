package controller

import (
	"encoding/json"
	"errors"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ===== 成片工作室：策划案（API.md 8.2，T23）=====

// CreateScreenplay POST /user/screenplay
func CreateScreenplay(c *gin.Context) {
	userId := c.GetInt("id")
	var req struct {
		Input         string `json:"input"`
		TargetSeconds int    `json:"target_seconds"`
		Style         string `json:"style"`
		ProjectId     int    `json:"project_id"`
		// UseBible 沿用项目的剧集设定（API.md 27.3）；EpisodeNo 集号，0 = 自动取项目下一集
		UseBible  bool `json:"use_bible"`
		EpisodeNo int  `json:"episode_no"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	input := strings.TrimSpace(req.Input)
	if len([]rune(input)) < 4 {
		common.ApiErrorMsg(c, "请描述你想拍什么，至少 4 个字")
		return
	}
	if len([]rune(input)) > 8000 {
		common.ApiErrorMsg(c, "内容过长，最多 8000 字")
		return
	}
	switch req.TargetSeconds {
	case 15, 30, 60:
	default:
		req.TargetSeconds = 30
	}
	if req.ProjectId > 0 {
		if _, err := model.GetUserProject(userId, req.ProjectId); err != nil {
			respondModelError(c, err, "查询项目失败")
			return
		}
	}
	bibleCtx := ""
	episodeNo := req.EpisodeNo
	if req.ProjectId > 0 {
		if episodeNo <= 0 {
			episodeNo = model.NextEpisodeNo(userId, req.ProjectId)
		}
		if req.UseBible {
			if row, err := model.GetSeriesBible(req.ProjectId); err == nil {
				if b, err := row.Parse(); err == nil {
					bibleCtx = service.BibleContextPrompt(b, episodeNo)
				}
			}
		}
	}
	sp, err := service.GenerateScreenplayWithContext(userId, teamIdOf(c), input, req.TargetSeconds, trimLimit(req.Style, 100), req.ProjectId, bibleCtx, episodeNo)
	if err != nil {
		if respondSpaceError(c, err) {
			return
		}
		common.ApiErrorMsg(c, err.Error())
		return
	}
	// 沿用设定：同 key 人物的音色 / 说话风格 / 外貌补进策划案（API.md 27.3）
	if bibleCtx != "" {
		if row, err := model.GetSeriesBible(req.ProjectId); err == nil {
			if b, err := row.Parse(); err == nil {
				if plan, err := sp.ParsePlan(); err == nil && service.ApplyBibleToPlan(b, plan) {
					if pb, err := json.Marshal(plan); err == nil && model.UpdateScreenplayFields(sp.Id, map[string]any{"plan": string(pb)}) == nil {
						sp.Plan = string(pb)
						sp.FillView()
					}
				}
			}
		}
	}
	common.ApiSuccess(c, sp)
}

// GetMyScreenplay GET /user/screenplay/:id
func GetMyScreenplay(c *gin.Context) {
	sp, err := model.GetUserScreenplay(c.GetInt("id"), paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, sp)
}

// UpdateMyScreenplan PUT /user/screenplay/:id { plan }
func UpdateMyScreenplay(c *gin.Context) {
	userId := c.GetInt("id")
	sp, err := model.GetUserScreenplay(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	var req struct {
		Plan json.RawMessage `json:"plan"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || len(req.Plan) == 0 {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	plan := &model.Plan{}
	if err := json.Unmarshal(req.Plan, plan); err != nil {
		common.ApiErrorMsg(c, "策划案格式不正确")
		return
	}
	if err := plan.Normalize(sp.TargetSeconds); err != nil {
		// 超限 / 引用错误原样返回（说明上限与实际数量、哪一镜引用了什么），原稿不落库（复审 D03）
		if errors.Is(err, model.ErrPlanLimit) || errors.Is(err, model.ErrPlanRef) {
			common.ApiErrorMsg(c, err.Error())
			return
		}
		common.ApiErrorMsg(c, "策划案至少要有一个镜头，且每镜有画面描述")
		return
	}
	b, _ := json.Marshal(plan)
	// 每次保存修订号 +1；已建画布时 revision != built_revision 即 stale（复审 D02）
	fields := map[string]any{"plan": string(b), "revision": gorm.Expr("revision + 1")}
	if sp.Status == model.ScreenplayStatusFailed {
		fields["status"] = model.ScreenplayStatusReady
	}
	if err := model.UpdateScreenplayFields(sp.Id, fields); err != nil {
		common.ApiErrorMsg(c, "保存失败")
		return
	}
	sp.Plan = string(b)
	sp.Revision++
	sp.FillView()
	common.ApiSuccess(c, sp)
}

// EstimateMyScreenplay POST /user/screenplay/:id/estimate { image_model?, video_model?, audio_model? }
func EstimateMyScreenplay(c *gin.Context) {
	userId := c.GetInt("id")
	sp, err := model.GetUserScreenplay(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	plan, err := sp.ParsePlan()
	if err != nil {
		common.ApiErrorMsg(c, "策划案尚未生成")
		return
	}
	var m service.BuildModels
	_ = common.DecodeJson(c.Request.Body, &m)
	common.ApiSuccess(c, service.EstimatePlan(userId, teamIdOf(c), plan, m))
}

// BuildMyScreenplay POST /user/screenplay/:id/build { image_model?, video_model?, audio_model?, sync? }
// 已建画布：sync=true 把改过的 plan 按镜号同步到既有画布（API.md 24.1），否则直接返回已有画布（existing=true）。
func BuildMyScreenplay(c *gin.Context) {
	userId := c.GetInt("id")
	sp, err := model.GetUserScreenplay(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	var req struct {
		service.BuildModels
		Sync bool `json:"sync"`
	}
	_ = common.DecodeJson(c.Request.Body, &req)
	m := req.BuildModels
	m.Image, m.Video, m.Audio = trimLimit(m.Image, 100), trimLimit(m.Video, 100), trimLimit(m.Audio, 100)
	if sp.Status == model.ScreenplayStatusBuilt && sp.CanvasId > 0 {
		if cv, err := model.GetUserCanvas(userId, sp.CanvasId); err == nil {
			if !req.Sync {
				common.ApiSuccess(c, gin.H{"canvas_id": cv.Id, "project_id": cv.ProjectId, "estimate_points": 0, "existing": true,
					"revision": sp.Revision, "built_revision": sp.BuiltRevision, "stale": sp.Stale})
				return
			}
			res, err := service.SyncScreenplayCanvas(userId, sp, cv, m)
			if err != nil {
				respondModelError(c, err, "同步失败")
				return
			}
			common.ApiSuccess(c, res)
			return
		}
	}
	for _, pair := range [][2]string{{model.TaskTypeImage, m.Image}, {model.TaskTypeVideo, m.Video}, {model.TaskTypeAudio, m.Audio}} {
		if pair[1] != "" && service.FindProvider(pair[0], pair[1]) == nil {
			common.ApiErrorMsg(c, "模型 "+pair[1]+" 暂未接入")
			return
		}
	}
	cv, est, err := service.BuildScreenplayCanvas(userId, teamIdOf(c), sp, m)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, gin.H{"canvas_id": cv.Id, "project_id": cv.ProjectId, "estimate_points": est.Total, "estimate": est})
}
