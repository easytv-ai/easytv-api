package controller

import (
	"encoding/json"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 画布（用户侧）=====

// validateGraph 校验 graph：必须是 JSON 对象且不超过上限。返回规范化字符串。
func validateGraph(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", true
	}
	if len(raw) > model.CanvasGraphMaxBytes {
		return "", false
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", false
	}
	return string(raw), true
}

// GetMyCanvases 画布列表（GET /user/canvas?project_id=）。
func GetMyCanvases(c *gin.Context) {
	userId := c.GetInt("id")
	list, total, err := model.GetUserCanvases(userId, queryInt(c, "project_id", 0), parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	views := make([]map[string]any, 0, len(list))
	for _, cv := range list {
		views = append(views, cv.View(false))
	}
	pagedResponse(c, views, total)
}

// CreateCanvas 新建画布（POST /user/canvas）。不传 project_id 时自动新建同名项目。
func CreateCanvas(c *gin.Context) {
	userId := c.GetInt("id")
	var req struct {
		Name      string          `json:"name"`
		ProjectId int             `json:"project_id"`
		Graph     json.RawMessage `json:"graph"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	name := trimLimit(req.Name, 100)
	if name == "" {
		name = "未命名草稿"
	}
	graph, ok := validateGraph(req.Graph)
	if !ok {
		common.ApiErrorMsg(c, "画布数据格式不正确或超过 2MB")
		return
	}

	projectId := req.ProjectId
	if projectId > 0 {
		if _, err := model.GetUserProject(userId, projectId); err != nil {
			respondModelError(c, err, "查询项目失败")
			return
		}
	} else {
		// 团队空间里新建的草稿，自动建的项目落在团队（API.md 20）
		p := &model.Project{UserId: userId, TeamId: teamIdOf(c), Name: name}
		if err := p.Insert(); err != nil {
			respondModelError(c, err, "创建项目失败")
			return
		}
		projectId = p.Id
	}

	cv := &model.Canvas{
		UserId:    userId,
		ProjectId: projectId,
		Name:      name,
		Graph:     graph,
	}
	if err := cv.Insert(); err != nil {
		respondModelError(c, err, "创建画布失败")
		return
	}
	model.TouchProject(projectId)
	service.TrackOnce(userId, model.TrackEventFirstCanvas, map[string]any{"canvas_id": cv.Id})
	common.ApiSuccess(c, cv.View(true))
}

// GetMyCanvas 读取画布（GET /user/canvas/:id）。
func GetMyCanvas(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的画布 ID")
		return
	}
	// 自己的画布，或队友的画布（API.md 15）：查看者只读，编辑者 / 管理员可编辑；分享与删除仍只认作者。
	// 移出团队后 CanvasAccess 立即不再放行。
	cv, perm, err := service.CanvasAccess(userId, id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	view := cv.View(true)
	view["permission"] = perm
	view["readonly"] = !service.CanvasWritable(perm)
	if perm != service.CanvasPermOwner {
		view["team_shared"] = true
		view["share_enabled"] = false
		view["share_token"] = ""
		// 只给昵称：username 是队友的邮箱，不外露
		view["owner_nickname"] = ""
		if owner, err := model.GetUserById(cv.UserId); err == nil {
			view["owner_nickname"] = owner.Nickname
		}
	}
	common.ApiSuccess(c, view)
}

// writableCanvas 取可写的画布（作者或队友编辑者 / 管理员），不可写时已回错误。
func writableCanvas(c *gin.Context, userId, id int) (*model.Canvas, bool) {
	cv, perm, err := service.CanvasAccess(userId, id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return nil, false
	}
	if !service.CanvasWritable(perm) {
		common.ApiErrorMsg(c, "你在团队里是查看者，只能查看这张画布")
		return nil, false
	}
	return cv, true
}

// SaveCanvas 保存画布（PUT /user/canvas/:id），乐观锁。
func SaveCanvas(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的画布 ID")
		return
	}
	var req struct {
		Name         *string         `json:"name"`
		Graph        json.RawMessage `json:"graph"`
		Version      int             `json:"version"`
		ThumbnailUrl *string         `json:"thumbnail_url"`
		// Snapshot 为 true 表示用户主动保存，单独记一条版本；否则按自动保存合并
		Snapshot bool   `json:"snapshot"`
		Note     string `json:"note"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if req.Version <= 0 {
		common.ApiErrorMsg(c, "缺少 version")
		return
	}
	fields := map[string]any{}
	var newName string
	if req.Name != nil {
		newName = trimLimit(*req.Name, 100)
		if newName == "" {
			common.ApiErrorMsg(c, "画布名称不能为空")
			return
		}
		fields["name"] = newName
	}
	if len(req.Graph) > 0 {
		graph, ok := validateGraph(req.Graph)
		if !ok {
			common.ApiErrorMsg(c, "画布数据格式不正确或超过 2MB")
			return
		}
		fields["graph"] = graph
	}
	if req.ThumbnailUrl != nil {
		fields["thumbnail_url"] = trimLimit(*req.ThumbnailUrl, 500)
	}
	if len(fields) == 0 {
		common.ApiErrorMsg(c, "没有需要保存的内容")
		return
	}

	cv, ok := writableCanvas(c, userId, id)
	if !ok {
		return
	}
	// 按作者 user_id 落库；队友编辑时版本快照也记在作者名下，备注里写上是谁改的
	newVersion, updatedAt, err := model.SaveCanvas(cv.UserId, id, req.Version, fields)
	if err != nil {
		respondModelError(c, err, "保存失败")
		return
	}
	// 版本历史：有 graph 时记快照（自动保存 10 分钟内合并，手动保存单独一条）
	if graph, ok := fields["graph"].(string); ok {
		name := cv.Name
		if newName != "" {
			name = newName
		}
		source := model.CanvasVersionSourceAuto
		if req.Snapshot {
			source = model.CanvasVersionSourceManual
		}
		note := trimLimit(req.Note, 100)
		if cv.UserId != userId {
			if names := model.GetUserNicknames([]int{userId}); names[userId] != "" && note == "" {
				note = "队友 " + names[userId] + " 编辑"
			}
		}
		if err := model.SnapshotCanvas(cv.UserId, id, newVersion, name, graph, source, note); err != nil {
			common.SysError("canvas snapshot failed: " + err.Error())
		}
	}
	// 改名同步到项目；任何保存都刷新项目 updated_at
	if newName != "" && cv.ProjectId > 0 {
		_ = model.UpdateProjectFields(cv.UserId, cv.ProjectId, map[string]any{"name": newName})
	} else {
		model.TouchProject(cv.ProjectId)
	}
	common.ApiSuccess(c, gin.H{
		"id":         id,
		"version":    newVersion,
		"updated_at": updatedAt,
	})
}

// TrashCanvas 画布进回收站（DELETE /user/canvas/:id）。
func TrashCanvas(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的画布 ID")
		return
	}
	if err := model.TrashCanvas(userId, id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// GetMyTrashCanvases 画布回收站（GET /user/canvas/trash）。
func GetMyTrashCanvases(c *gin.Context) {
	list, total, err := model.GetUserTrashCanvases(c.GetInt("id"), parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	views := make([]map[string]any, 0, len(list))
	for _, cv := range list {
		v := cv.View(false)
		v["deleted_at"] = cv.DeletedAt.Time.Unix()
		views = append(views, v)
	}
	pagedResponse(c, views, total)
}

// RestoreMyCanvas 恢复画布（POST /user/canvas/:id/restore）。
func RestoreMyCanvas(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的画布 ID")
		return
	}
	if err := model.RestoreCanvas(c.GetInt("id"), id); err != nil {
		respondModelError(c, err, "恢复失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// GetMyCanvasVersions 版本历史（GET /user/canvas/:id/versions），不含 graph。
func GetMyCanvasVersions(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的画布 ID")
		return
	}
	cv, _, err := service.CanvasAccess(userId, id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	list, err := model.GetCanvasVersions(cv.UserId, id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, int64(len(list)))
}

// GetMyCanvasVersion 单个版本（GET /user/canvas/:id/versions/:vid），含 graph，供预览。
func GetMyCanvasVersion(c *gin.Context) {
	userId := c.GetInt("id")
	id, vid := paramInt(c, "id"), paramInt(c, "vid")
	if id == 0 || vid == 0 {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	cv, _, err := service.CanvasAccess(userId, id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	v, err := model.GetCanvasVersion(cv.UserId, id, vid)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	graph := v.Graph
	if graph == "" {
		graph = model.EmptyCanvasGraph
	}
	common.ApiSuccess(c, gin.H{
		"id":         v.Id,
		"canvas_id":  v.CanvasId,
		"version":    v.Version,
		"name":       v.Name,
		"node_count": v.NodeCount,
		"source":     v.Source,
		"note":       v.Note,
		"created_at": v.CreatedAt,
		"graph":      json.RawMessage(graph),
	})
}

// RestoreMyCanvasVersion 回滚到某版本（POST /user/canvas/:id/versions/:vid/restore），返回新的 version 与 graph。
func RestoreMyCanvasVersion(c *gin.Context) {
	userId := c.GetInt("id")
	id, vid := paramInt(c, "id"), paramInt(c, "vid")
	if id == 0 || vid == 0 {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	owned, ok := writableCanvas(c, userId, id)
	if !ok {
		return
	}
	newVersion, updatedAt, err := model.RestoreCanvasVersion(owned.UserId, id, vid)
	if err != nil {
		respondModelError(c, err, "恢复失败")
		return
	}
	cv, err := model.GetCanvasById(id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	view := cv.View(true)
	view["version"] = newVersion
	view["updated_at"] = updatedAt
	common.ApiSuccess(c, view)
}

// ===== 只读分享（API.md 1.2.2）=====

// GetMyCanvasShare 分享状态（GET /user/canvas/:id/share）。
func GetMyCanvasShare(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的画布 ID")
		return
	}
	cv, err := model.GetUserCanvas(userId, id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, shareView(cv))
}

// SetMyCanvasShare 开启 / 关闭分享（PUT /user/canvas/:id/share）：`{ enabled: bool }`。
func SetMyCanvasShare(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的画布 ID")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	cv, err := model.SetCanvasShare(userId, id, req.Enabled)
	if err != nil {
		respondModelError(c, err, "更新失败")
		return
	}
	common.ApiSuccess(c, shareView(cv))
}

func shareView(cv *model.Canvas) gin.H {
	path := ""
	if cv.ShareEnabled && cv.ShareToken != "" {
		path = "/share/" + cv.ShareToken
	}
	return gin.H{
		"enabled": cv.ShareEnabled,
		"token":   cv.ShareToken,
		"path":    path,
	}
}

// GetSharedCanvas 分享页数据（GET /share/canvas/:token，匿名）：画布基本信息 + 去敏后的 graph。
func GetSharedCanvas(c *gin.Context) {
	token := strings.TrimSpace(c.Param("token"))
	cv, err := model.GetSharedCanvas(token)
	if err != nil {
		respondModelError(c, err, "分享链接不存在或已关闭")
		return
	}
	names := model.GetUserNicknames([]int{cv.UserId})
	common.ApiSuccess(c, gin.H{
		"name":       cv.Name,
		"node_count": cv.NodeCount(),
		"updated_at": cv.UpdatedAt,
		"owner":      gin.H{"id": cv.UserId, "nickname": names[cv.UserId]},
		"graph":      json.RawMessage(model.SanitizeSharedGraph(cv.Graph)),
		"mine":       c.GetInt("id") > 0 && c.GetInt("id") == cv.UserId,
	})
}

// CopySharedCanvas 复制到我的画布（POST /user/share/canvas/:token/copy），返回新画布详情。
func CopySharedCanvas(c *gin.Context) {
	token := strings.TrimSpace(c.Param("token"))
	src, err := model.GetSharedCanvas(token)
	if err != nil {
		respondModelError(c, err, "分享链接不存在或已关闭")
		return
	}
	cv, err := model.CopySharedCanvas(c.GetInt("id"), src)
	if err != nil {
		respondModelError(c, err, "复制失败")
		return
	}
	common.ApiSuccess(c, cv.View(true))
}
