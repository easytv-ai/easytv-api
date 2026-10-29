package controller

import (
	"encoding/json"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 主体库（API.md 10.2，F2）=====

// GetMySubjects GET /user/subject?kind=&keyword=&page
// 个人空间只列自己 team_id=0 的主体；团队空间列该团队的全部主体（API.md 20）。
func GetMySubjects(c *gin.Context) {
	userId := c.GetInt("id")
	list, total, err := model.GetSubjects(model.SubjectFilter{
		UserId: userId, TeamId: teamIdOf(c),
		Kind: strings.TrimSpace(c.Query("kind")), Keyword: c.Query("keyword"),
	}, parsePageInfo(c))
	if err != nil {
		common.ApiErrorMsg(c, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

type subjectPayload struct {
	Kind         *string             `json:"kind"`
	Name         *string             `json:"name"`
	Look         *string             `json:"look"`
	Prompt       *string             `json:"prompt"`
	CoverAssetId *int                `json:"cover_asset_id"`
	Refs         *[]model.SubjectRef `json:"refs"`
	Shared       *int                `json:"shared"`
}

// CreateMySubject POST /user/subject
func CreateMySubject(c *gin.Context) {
	userId := c.GetInt("id")
	var req subjectPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	name := ""
	if req.Name != nil {
		name = trimLimit(*req.Name, 50)
	}
	if name == "" {
		common.ApiErrorMsg(c, "请填写名称")
		return
	}
	kind := model.SubjectKindCharacter
	if req.Kind != nil && model.SubjectKinds[*req.Kind] {
		kind = *req.Kind
	}
	// 团队空间建的主体归团队并对队友共享；个人空间的主体只自己可见（API.md 20）
	s := &model.Subject{UserId: userId, TeamId: teamIdOf(c), Kind: kind, Name: name}
	if s.TeamId > 0 {
		s.Shared = 1
	}
	if req.Look != nil {
		s.Look = trimLimit(*req.Look, 1000)
	}
	if req.Prompt != nil {
		s.Prompt = trimLimit(*req.Prompt, 1000)
	}
	if req.CoverAssetId != nil && *req.CoverAssetId > 0 {
		asset, err := model.GetUserAsset(userId, *req.CoverAssetId)
		if err != nil || asset.Type != model.AssetTypeImage {
			common.ApiErrorMsg(c, "封面需要是你资产库里的图片")
			return
		}
		s.CoverAssetId, s.CoverUrl = asset.Id, asset.Url
		s.Refs = subjectRefsJSON([]model.SubjectRef{{AssetId: asset.Id, Url: asset.Url, Label: "封面"}})
	}
	if req.Refs != nil {
		s.Refs = subjectRefsJSON(validSubjectRefs(userId, *req.Refs))
	}
	if err := s.Insert(); err != nil {
		respondModelError(c, err, "创建失败")
		return
	}
	s.FillView(userId)
	common.ApiSuccess(c, s)
}

func subjectRefsJSON(refs []model.SubjectRef) string {
	b, _ := json.Marshal(refs)
	return string(b)
}

// validSubjectRefs 只保留属于自己的图片资产，最多 6 张。
func validSubjectRefs(userId int, refs []model.SubjectRef) []model.SubjectRef {
	out := []model.SubjectRef{}
	for _, r := range refs {
		if r.AssetId <= 0 || len(out) >= 6 {
			continue
		}
		asset, err := model.GetUserAsset(userId, r.AssetId)
		if err != nil || asset.Type != model.AssetTypeImage {
			continue
		}
		out = append(out, model.SubjectRef{AssetId: asset.Id, Url: asset.Url, Label: trimLimit(r.Label, 20)})
	}
	return out
}

// UpdateMySubject PUT /user/subject/:id
func UpdateMySubject(c *gin.Context) {
	userId := c.GetInt("id")
	s, err := model.GetOwnSubject(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	var req subjectPayload
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	fields := map[string]any{}
	if req.Name != nil {
		name := trimLimit(*req.Name, 50)
		if name == "" {
			common.ApiErrorMsg(c, "名称不能为空")
			return
		}
		fields["name"] = name
	}
	if req.Kind != nil && model.SubjectKinds[*req.Kind] {
		fields["kind"] = *req.Kind
	}
	if req.Look != nil {
		fields["look"] = trimLimit(*req.Look, 1000)
	}
	if req.Prompt != nil {
		fields["prompt"] = trimLimit(*req.Prompt, 1000)
	}
	// shared 不再可改：主体的团队归属在创建时由空间决定（API.md 20），这里忽略请求里的 shared
	if req.CoverAssetId != nil {
		if *req.CoverAssetId <= 0 {
			fields["cover_asset_id"], fields["cover_url"] = 0, ""
		} else {
			asset, err := model.GetUserAsset(userId, *req.CoverAssetId)
			if err != nil || asset.Type != model.AssetTypeImage {
				common.ApiErrorMsg(c, "封面需要是你资产库里的图片")
				return
			}
			fields["cover_asset_id"], fields["cover_url"] = asset.Id, asset.Url
		}
	}
	if req.Refs != nil {
		fields["refs"] = subjectRefsJSON(validSubjectRefs(userId, *req.Refs))
	}
	if len(fields) == 0 {
		common.ApiErrorMsg(c, "没有需要更新的字段")
		return
	}
	if err := model.UpdateSubjectFields(userId, s.Id, fields); err != nil {
		respondModelError(c, err, "保存失败")
		return
	}
	s, _ = model.GetOwnSubject(userId, s.Id)
	s.FillView(userId)
	common.ApiSuccess(c, s)
}

// DeleteMySubject DELETE /user/subject/:id
func DeleteMySubject(c *gin.Context) {
	if err := model.DeleteUserSubject(c.GetInt("id"), paramInt(c, "id")); err != nil {
		common.ApiErrorMsg(c, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// GenerateSubjectViews POST /user/subject/:id/generate { model? }
func GenerateSubjectViews(c *gin.Context) {
	userId := c.GetInt("id")
	s, err := model.GetOwnSubject(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	var req struct {
		Model string `json:"model"`
	}
	_ = common.DecodeJson(c.Request.Body, &req)
	tasks, err := service.GenerateSubjectViews(userId, teamIdOf(c), s, trimLimit(req.Model, 100))
	if err != nil {
		if respondSpaceError(c, err) {
			return
		}
		common.ApiErrorMsg(c, err.Error())
		return
	}
	views := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		views = append(views, taskView(t))
	}
	common.ApiSuccess(c, gin.H{"tasks": views})
}

// SyncMySubjectRefs POST /user/subject/:id/sync 把已完成的参考图任务结果回写
func SyncMySubjectRefs(c *gin.Context) {
	userId := c.GetInt("id")
	s, err := model.GetOwnSubject(userId, paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	refs := service.SyncSubjectRefs(userId, s)
	s, _ = model.GetOwnSubject(userId, s.Id)
	s.FillView(userId)
	common.ApiSuccess(c, gin.H{"subject": s, "refs": refs})
}

// CreateSubjectFromAsset POST /user/subject/from-asset { asset_id, kind, name }
func CreateSubjectFromAsset(c *gin.Context) {
	userId := c.GetInt("id")
	var req struct {
		AssetId int    `json:"asset_id"`
		Kind    string `json:"kind"`
		Name    string `json:"name"`
		Look    string `json:"look"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	asset, err := model.GetUserAsset(userId, req.AssetId)
	if err != nil || asset.Type != model.AssetTypeImage {
		common.ApiErrorMsg(c, "需要选一张你资产库里的图片")
		return
	}
	name := trimLimit(req.Name, 50)
	if name == "" {
		name = trimLimit(asset.Name, 50)
	}
	kind := model.SubjectKindCharacter
	if model.SubjectKinds[req.Kind] {
		kind = req.Kind
	}
	s := &model.Subject{
		UserId: userId, TeamId: teamIdOf(c), Shared: boolInt(teamIdOf(c) > 0), Kind: kind, Name: name,
		Look: trimLimit(req.Look, 1000), CoverAssetId: asset.Id, CoverUrl: asset.Url,
		Refs: subjectRefsJSON([]model.SubjectRef{{AssetId: asset.Id, Url: asset.Url, Label: "参考"}}),
	}
	if err := s.Insert(); err != nil {
		respondModelError(c, err, "创建失败")
		return
	}
	s.FillView(userId)
	common.ApiSuccess(c, s)
}

// GetSubjectVersions GET /user/subject/:id/versions：自己的或团队共享的主体的版本快照（新的在前，最多 30 条）。
func GetSubjectVersions(c *gin.Context) {
	userId := c.GetInt("id")
	s, err := model.GetUserSubject(userId, teamIdOf(c), paramInt(c, "id"))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	list, err := model.GetSubjectVersions(s.Id, 30)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, gin.H{"version": s.Version, "versions": list})
}

// boolInt true → 1。
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
