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

// ===== 短剧第二批（API.md 25）：正反打模板、审片结论、定版锁 =====

// DialogueShotsMyScreenplay POST /user/screenplay/:id/dialogue-shots
// 按两人对白生成一组正反打镜头插进策划案（revision+1），返回策划案。
func DialogueShotsMyScreenplay(c *gin.Context) {
	userId := c.GetInt("id")
	sp, err := model.GetUserScreenplay(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	var req service.DialogueTemplateRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	plan, err := sp.ParsePlan()
	if err != nil {
		common.ApiErrorMsg(c, "策划案格式不正确")
		return
	}
	shots, err := service.DialogueTemplateShots(plan, req)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if err := service.InsertShots(plan, shots, req.InsertAt); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	if err := plan.Normalize(sp.TargetSeconds); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	b, _ := json.Marshal(plan)
	fields := map[string]any{"plan": string(b), "revision": gorm.Expr("revision + 1")}
	if err := model.UpdateScreenplayFields(sp.Id, fields); err != nil {
		common.ApiErrorMsg(c, "保存失败")
		return
	}
	sp.Plan = string(b)
	sp.Revision++
	sp.FillView()
	service.Track(userId, "screenplay_dialogue_template", map[string]any{"screenplay_id": sp.Id, "shots": len(shots)})
	common.ApiSuccess(c, gin.H{"screenplay": sp, "added": len(shots)})
}

// ReviewMyCanvasShot POST /user/canvas/:id/review { node_id, asset_id, status: approved|rejected|clear, reason?, note? }
func ReviewMyCanvasShot(c *gin.Context) {
	userId := c.GetInt("id")
	cv, err := model.GetUserCanvas(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询画布失败")
		return
	}
	var req struct {
		NodeId  string `json:"node_id"`
		AssetId int    `json:"asset_id"`
		Status  string `json:"status"`
		Reason  string `json:"reason"`
		Note    string `json:"note"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil || req.AssetId <= 0 {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	// 视频候选与分镜图 / 角色图都能审（G05 先审低成本的图）
	asset, err := model.GetAssetByIdUnscoped(req.AssetId)
	if err != nil || asset.DeletedAt.Valid || (asset.Type != model.AssetTypeVideo && asset.Type != model.AssetTypeImage) {
		common.ApiErrorMsg(c, "候选素材不存在")
		return
	}
	if asset.TaskId > 0 {
		if t, err := model.GetTaskById(asset.TaskId); err == nil && t.CanvasId != 0 && t.CanvasId != cv.Id {
			common.ApiErrorMsg(c, "该候选不属于这张画布")
			return
		}
	}
	switch strings.ToLower(strings.TrimSpace(req.Status)) {
	case "clear", "":
		if err := model.DeleteShotReview(cv.Id, req.AssetId); err != nil {
			common.ApiErrorMsg(c, "撤销失败")
			return
		}
		common.ApiSuccess(c, gin.H{"asset_id": req.AssetId, "review": nil})
		return
	case "approved", "rejected":
		status := model.ShotReviewApproved
		if strings.EqualFold(strings.TrimSpace(req.Status), "rejected") {
			status = model.ShotReviewRejected
		}
		reason := strings.ToLower(strings.TrimSpace(req.Reason))
		if status == model.ShotReviewRejected && reason != "" && !model.IsShotReviewReason(reason) {
			common.ApiErrorMsg(c, "退回原因不在清单里："+reason)
			return
		}
		if status == model.ShotReviewApproved {
			reason = ""
		}
		r := &model.ShotReview{
			UserId: userId, CanvasId: cv.Id, NodeId: trimLimit(strings.TrimSpace(req.NodeId), 64),
			AssetId: req.AssetId, TaskId: asset.TaskId, Status: status, Reason: reason, Note: trimLimit(strings.TrimSpace(req.Note), 500),
		}
		if err := model.UpsertShotReview(r); err != nil {
			common.ApiErrorMsg(c, "保存失败")
			return
		}
		saved, err := model.GetShotReview(cv.Id, req.AssetId)
		if err != nil {
			saved = r
		}
		service.Track(userId, "shot_review", map[string]any{"canvas_id": cv.Id, "asset_id": req.AssetId, "status": status, "reason": reason})
		common.ApiSuccess(c, gin.H{"asset_id": req.AssetId, "review": saved})
		return
	}
	common.ApiErrorMsg(c, "status 只能是 approved / rejected / clear")
}

// LockMyCanvasTimeline POST /user/canvas/:id/timeline/lock { locked }
func LockMyCanvasTimeline(c *gin.Context) {
	userId := c.GetInt("id")
	cv, err := model.GetUserCanvas(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询画布失败")
		return
	}
	var req struct {
		Locked bool `json:"locked"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	row, err := model.SetTimelineLock(cv.Id, userId, req.Locked)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			common.ApiErrorMsg(c, "还没保存过时间线，先在合成步骤保存一次再定版")
			return
		}
		respondModelError(c, err, "操作失败")
		return
	}
	service.Track(userId, "timeline_lock", map[string]any{"canvas_id": cv.Id, "locked": req.Locked})
	common.ApiSuccess(c, gin.H{"locked": row.Locked, "locked_at": row.LockedAt, "locked_by": row.LockedBy, "version": row.Version})
}

// ProductionReportMyCanvas GET /user/canvas/:id/production-report（API.md 26.3）
func ProductionReportMyCanvas(c *gin.Context) {
	cv, err := model.GetUserCanvas(c.GetInt("id"), paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询画布失败")
		return
	}
	rep, err := service.BuildProductionReport(cv)
	if err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}
	common.ApiSuccess(c, rep)
}
