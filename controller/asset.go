package controller

import (
	"mime"
	"path"
	"strings"

	"easytv-api/common"
	"easytv-api/model"
	"easytv-api/service"

	"github.com/gin-gonic/gin"
)

// ===== 资产（用户侧）=====

// GetMyAssets 资产列表（GET /user/asset?type=&project_id=&source=&keyword=）。
// 按请求空间（API.md 20）：个人空间只列自己 team_id=0 的资产；团队空间列该团队的全部资产（不分上传者）。
func GetMyAssets(c *gin.Context) {
	filter := model.AssetFilter{
		UserId:    c.GetInt("id"),
		Space:     true,
		TeamId:    teamIdOf(c),
		ProjectId: queryInt(c, "project_id", 0),
		Type:      strings.TrimSpace(c.Query("type")),
		Source:    strings.TrimSpace(c.Query("source")),
		Keyword:   strings.TrimSpace(c.Query("keyword")),
	}
	if filter.Type != "" && !model.IsValidAssetType(filter.Type) {
		common.ApiErrorMsg(c, "无效的资产类型")
		return
	}
	list, total, err := model.GetAssets(filter, parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// UploadAsset 上传资产（POST /user/asset/upload，multipart 字段 file，可选 project_id）。
func UploadAsset(c *gin.Context) {
	userId := c.GetInt("id")
	if service.DefaultStorage == nil {
		common.ApiErrorMsg(c, "存储未初始化")
		return
	}
	header, err := c.FormFile("file")
	if err != nil {
		common.ApiErrorMsg(c, "请选择要上传的文件")
		return
	}
	if header.Size <= 0 {
		common.ApiErrorMsg(c, "文件为空")
		return
	}
	if header.Size > service.UploadMaxBytes {
		common.ApiErrorMsg(c, "文件超过大小限制")
		return
	}
	// 资产空间（API.md 20）：传了项目就跟项目走（自己的项目，或自己所在团队的团队项目），否则用请求空间
	teamId := teamIdOf(c)
	projectId := 0
	if raw := strings.TrimSpace(c.PostForm("project_id")); raw != "" {
		projectId = queryIntFromString(raw)
		if projectId > 0 {
			p, ok := service.CanAccessProject(userId, projectId)
			if !ok {
				respondModelError(c, model.ErrNotFound, "查询项目失败")
				return
			}
			teamId = p.TeamId
		}
	}
	// 套餐云存储额度（团队空间按团队 owner 的套餐、全队共享）
	if err := service.CheckStorage(userId, teamId, header.Size); err != nil {
		common.ApiErrorMsg(c, err.Error())
		return
	}

	filename := trimLimit(path.Base(header.Filename), 200)
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" || mimeType == "application/octet-stream" {
		if byExt := mime.TypeByExtension(strings.ToLower(path.Ext(filename))); byExt != "" {
			mimeType = byExt
		}
	}
	mimeType = trimLimit(mimeType, 100)

	src, err := header.Open()
	if err != nil {
		common.ApiErrorMsg(c, "读取文件失败")
		return
	}
	defer src.Close()

	key := service.NewStorageKey(filename)
	url, err := service.DefaultStorage.Save(key, src)
	if err != nil {
		common.SysError("asset save failed: " + err.Error())
		common.ApiErrorMsg(c, "文件保存失败")
		return
	}

	asset := &model.Asset{
		UserId:     userId,
		TeamId:     teamId,
		ProjectId:  projectId,
		Type:       model.AssetTypeFromMime(mimeType),
		Name:       filename,
		Url:        url,
		StorageKey: key,
		Mime:       mimeType,
		Size:       header.Size,
		Source:     model.AssetSourceUpload,
	}
	if err := asset.Insert(); err != nil {
		_ = service.DefaultStorage.Remove(key)
		respondModelError(c, err, "写入资产记录失败")
		return
	}
	if asset.Type == model.AssetTypeVideo {
		go service.EnsureAssetCover(asset)
	}
	common.ApiSuccess(c, asset)
}

// DeleteMyAsset 删除资产（DELETE /user/asset/:id），同时移除本地文件。
func DeleteMyAsset(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的资产 ID")
		return
	}
	asset, err := model.GetUserAsset(userId, id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	// 还在被画布 / 主体 / 作品 / 时间线引用：先告诉用户影响范围，确认后带 force=1 再删（API.md 14.1）
	if c.Query("force") != "1" {
		if refs := service.FindAssetRefs(userId, id); len(refs) > 0 {
			common.ApiErrorData(c, service.AssetRefsSummary(refs)+"，删除后这些地方会失效", gin.H{"reason": "in_use", "refs": refs})
			return
		}
	}
	if err := model.DeleteUserAsset(userId, id); err != nil {
		respondModelError(c, err, "删除失败")
		return
	}
	// 进回收站，文件保留 30 天由后台任务清理，以便恢复
	_ = asset
	common.ApiSuccess(c, nil)
}

// GetMyTrashAssets 资产回收站（GET /user/asset/trash）。
func GetMyTrashAssets(c *gin.Context) {
	list, total, err := model.GetUserTrashAssets(c.GetInt("id"), parsePageInfo(c))
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	pagedResponse(c, list, total)
}

// RestoreMyAsset 恢复资产（POST /user/asset/:id/restore）。
func RestoreMyAsset(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的资产 ID")
		return
	}
	if err := model.RestoreAsset(c.GetInt("id"), id); err != nil {
		respondModelError(c, err, "恢复失败")
		return
	}
	common.ApiSuccess(c, nil)
}

// queryIntFromString 表单字段转正整数，非法返回 0。
func queryIntFromString(raw string) int {
	v := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0
		}
		v = v*10 + int(ch-'0')
		if v > 1<<30 {
			return 0
		}
	}
	return v
}

// GetMyAsset 单个资产（GET /user/asset/:id），画布从外部带 asset 参数进入时用。
// 自己的资产，或团队空间里该团队的资产（API.md 20）。
func GetMyAsset(c *gin.Context) {
	id := paramInt(c, "id")
	if id == 0 {
		common.ApiErrorMsg(c, "无效的资产 ID")
		return
	}
	asset, err := service.UsableAsset(c.GetInt("id"), teamIdOf(c), id)
	if err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, asset)
}

// GetMyAssetRefs GET /user/asset/:id/refs：素材被哪些画布节点 / 主体 / 作品 / 成片时间线引用。
func GetMyAssetRefs(c *gin.Context) {
	userId := c.GetInt("id")
	id := paramInt(c, "id")
	if _, err := model.GetUserAsset(userId, id); err != nil {
		respondModelError(c, err, "查询失败")
		return
	}
	common.ApiSuccess(c, gin.H{"refs": service.FindAssetRefs(userId, id)})
}

// ImportMyAssets POST /user/asset/import：把别的空间的资产引入当前空间（API.md 36.1，跨画布粘贴用）。
func ImportMyAssets(c *gin.Context) {
	var req struct {
		AssetIds []int `json:"asset_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.AssetIds) == 0 {
		common.ApiErrorMsg(c, "请指定要引入的素材")
		return
	}
	res, err := service.ImportAssets(c.GetInt("id"), teamIdOf(c), req.AssetIds)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, res)
}
