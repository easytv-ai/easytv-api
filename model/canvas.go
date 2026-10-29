package model

import (
	"encoding/json"
	"errors"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// Canvas 画布：工作流草稿，graph 由前端 CanvasWorkspace 定义、后端透传。见 API.md 1.2。
type Canvas struct {
	Id           int            `json:"id" gorm:"comment:画布ID"`
	UserId       int            `json:"user_id" gorm:"index;comment:所有者用户ID"`
	ProjectId    int            `json:"project_id" gorm:"index;default:0;comment:所属项目ID"`
	Name         string         `json:"name" gorm:"type:varchar(100);comment:画布名"`
	Graph        string         `json:"-" gorm:"type:longtext;comment:画布 JSON（nodes/edges/viewport）"`
	Version      int            `json:"version" gorm:"default:1;comment:乐观锁版本"`
	ThumbnailUrl string         `json:"thumbnail_url" gorm:"type:varchar(500);default:'';comment:缩略图"`
	ShareEnabled bool           `json:"share_enabled" gorm:"default:false;comment:是否开启只读分享"`
	ShareToken   string         `json:"share_token" gorm:"type:varchar(32);default:'';index;comment:分享链接 token"`
	CreatedAt    int64          `json:"created_at" gorm:"bigint;comment:创建时间（Unix 秒）"`
	UpdatedAt    int64          `json:"updated_at" gorm:"bigint;comment:更新时间（Unix 秒）"`
	DeletedAt    gorm.DeletedAt `json:"deleted_at" gorm:"index;comment:回收站时间"`
}

func (Canvas) TableName() string { return "canvas" }

// CanvasGraphMaxBytes graph JSON 上限（2 MB）。
const CanvasGraphMaxBytes = 2 * 1024 * 1024

// EmptyCanvasGraph 新建画布的默认 graph。
const EmptyCanvasGraph = `{"nodes":[],"edges":[],"viewport":{"x":0,"y":0,"zoom":1}}`

var ErrCanvasVersionConflict = errors.New("画布已在其他地方修改，请刷新后再保存")

// View 返回带 graph（解析为 JSON）的响应视图。
func (cv *Canvas) View(withGraph bool) map[string]any {
	view := map[string]any{
		"id":            cv.Id,
		"user_id":       cv.UserId,
		"project_id":    cv.ProjectId,
		"name":          cv.Name,
		"version":       cv.Version,
		"thumbnail_url": cv.ThumbnailUrl,
		"share_enabled": cv.ShareEnabled,
		"share_token":   cv.ShareToken,
		"created_at":    cv.CreatedAt,
		"updated_at":    cv.UpdatedAt,
	}
	if withGraph {
		graph := cv.Graph
		if graph == "" {
			graph = EmptyCanvasGraph
		}
		view["graph"] = json.RawMessage(graph)
	}
	return view
}

// Insert 创建画布。
func (cv *Canvas) Insert() error {
	now := common.GetTimestamp()
	cv.CreatedAt = now
	cv.UpdatedAt = now
	if cv.Version == 0 {
		cv.Version = 1
	}
	if cv.Graph == "" {
		cv.Graph = EmptyCanvasGraph
	}
	return DB.Create(cv).Error
}

// GetUserCanvas 按用户 + ID 查询（不含回收站）。
func GetUserCanvas(userId, id int) (*Canvas, error) {
	cv := &Canvas{}
	if err := DB.Where("id = ? AND user_id = ?", id, userId).First(cv).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return cv, nil
}

// GetCanvasById 按 id 取未进回收站的画布（不校验所有者，调用方负责权限）。
func GetCanvasById(id int) (*Canvas, error) {
	cv := &Canvas{}
	if err := DB.Where("id = ?", id).First(cv).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return cv, nil
}

// GetUserCanvases 用户画布列表（不含 graph）。projectId 为 0 查全部。
func GetUserCanvases(userId, projectId int, pageInfo *common.PageInfo) (list []*Canvas, total int64, err error) {
	query := DB.Model(&Canvas{}).Where("user_id = ?", userId)
	if projectId > 0 {
		query = query.Where("project_id = ?", projectId)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Omit("graph").Order("updated_at desc, id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

// GetProjectCanvases 项目下全部画布（不含 graph）。
func GetProjectCanvases(userId, projectId int) (list []*Canvas, err error) {
	err = DB.Model(&Canvas{}).Omit("graph").
		Where("user_id = ? AND project_id = ?", userId, projectId).
		Order("updated_at desc, id desc").Find(&list).Error
	return list, err
}

// SaveCanvas 乐观锁保存：version 匹配才更新并 +1。fields 中可含 name/graph/thumbnail_url。
func SaveCanvas(userId, id, version int, fields map[string]any) (newVersion int, updatedAt int64, err error) {
	updatedAt = common.GetTimestamp()
	fields["updated_at"] = updatedAt
	fields["version"] = gorm.Expr("version + 1")
	result := DB.Model(&Canvas{}).
		Where("id = ? AND user_id = ? AND version = ?", id, userId, version).
		Updates(fields)
	if result.Error != nil {
		return 0, 0, result.Error
	}
	if result.RowsAffected == 0 {
		// 区分：不存在 vs 版本冲突
		var count int64
		DB.Model(&Canvas{}).Where("id = ? AND user_id = ?", id, userId).Count(&count)
		if count == 0 {
			return 0, 0, ErrNotFound
		}
		return 0, 0, ErrCanvasVersionConflict
	}
	return version + 1, updatedAt, nil
}

// TrashCanvas 画布进回收站。
func TrashCanvas(userId, id int) error {
	result := DB.Where("id = ? AND user_id = ?", id, userId).Delete(&Canvas{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetAllCanvases 管理端列表（不含 graph）。
func GetAllCanvases(userId, projectId int, pageInfo *common.PageInfo) (list []*Canvas, total int64, err error) {
	query := DB.Model(&Canvas{})
	if userId > 0 {
		query = query.Where("user_id = ?", userId)
	}
	if projectId > 0 {
		query = query.Where("project_id = ?", projectId)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Omit("graph").Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

// NodeCount 解析 graph 统计节点数（管理端列表用）。
func (cv *Canvas) NodeCount() int {
	if cv.Graph == "" {
		return 0
	}
	var probe struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(cv.Graph), &probe); err != nil {
		return 0
	}
	return len(probe.Nodes)
}

// ===== 管理端 =====

// GetAllCanvasesAdmin 管理端列表（含 graph 以统计节点数）；trashed: "" 全部 / "1" 正常 / "2" 回收站。
func GetAllCanvasesAdmin(userId, projectId int, keyword, trashed string, pageInfo *common.PageInfo) (list []*Canvas, total int64, err error) {
	query := DB.Unscoped().Model(&Canvas{})
	switch trashed {
	case "1":
		query = query.Where("deleted_at IS NULL")
	case "2":
		query = query.Where("deleted_at IS NOT NULL")
	}
	if userId > 0 {
		query = query.Where("user_id = ?", userId)
	}
	if projectId > 0 {
		query = query.Where("project_id = ?", projectId)
	}
	if keyword != "" {
		query = query.Where("name LIKE ?", "%"+keyword+"%")
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

// AdminTrashCanvas 管理端移入回收站。
func AdminTrashCanvas(id int) error {
	result := DB.Where("id = ?", id).Delete(&Canvas{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// AdminRestoreCanvas 管理端恢复。
func AdminRestoreCanvas(id int) error {
	result := DB.Unscoped().Model(&Canvas{}).Where("id = ? AND deleted_at IS NOT NULL", id).Update("deleted_at", nil)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// AdminDeleteCanvas 管理端物理删除。
func AdminDeleteCanvas(id int) error {
	result := DB.Unscoped().Where("id = ?", id).Delete(&Canvas{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetUserTrashCanvases 用户回收站画布（不含 graph）。
func GetUserTrashCanvases(userId int, pageInfo *common.PageInfo) (list []*Canvas, total int64, err error) {
	query := DB.Unscoped().Model(&Canvas{}).Where("user_id = ? AND deleted_at IS NOT NULL", userId)
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Omit("graph").Order("deleted_at desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

// RestoreCanvas 用户恢复画布；所属项目若在回收站也一并恢复。
func RestoreCanvas(userId, id int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		cv := &Canvas{}
		if err := tx.Unscoped().Where("id = ? AND user_id = ? AND deleted_at IS NOT NULL", id, userId).First(cv).Error; err != nil {
			return ErrNotFound
		}
		if err := tx.Unscoped().Model(&Canvas{}).Where("id = ?", id).Update("deleted_at", nil).Error; err != nil {
			return err
		}
		if cv.ProjectId > 0 {
			return tx.Unscoped().Model(&Project{}).Where("id = ? AND user_id = ? AND deleted_at IS NOT NULL", cv.ProjectId, userId).Update("deleted_at", nil).Error
		}
		return nil
	})
}

// ===== 只读分享（API.md 1.2.2）=====

// SetCanvasShare 开启 / 关闭分享；首次开启生成 token，关闭后 token 保留但链接失效。
func SetCanvasShare(userId, id int, enabled bool) (*Canvas, error) {
	cv, err := GetUserCanvas(userId, id)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{"share_enabled": enabled, "updated_at": common.GetTimestamp()}
	if enabled && cv.ShareToken == "" {
		cv.ShareToken = common.GetRandomString(24)
		fields["share_token"] = cv.ShareToken
	}
	if err := DB.Model(&Canvas{}).Where("id = ?", id).Updates(fields).Error; err != nil {
		return nil, err
	}
	cv.ShareEnabled = enabled
	return cv, nil
}

// GetSharedCanvas 按分享 token 取画布（必须已开启分享、未进回收站）。
func GetSharedCanvas(token string) (*Canvas, error) {
	if len(token) < 8 {
		return nil, ErrNotFound
	}
	cv := &Canvas{}
	if err := DB.Where("share_token = ? AND share_enabled = ?", token, true).First(cv).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return cv, nil
}

// SanitizeSharedGraph 去掉与分享者账号绑定的字段（任务状态、资产 ID），保留节点内容、提示词与媒体地址。
func SanitizeSharedGraph(graph string) string {
	if graph == "" {
		return EmptyCanvasGraph
	}
	var g map[string]any
	if err := json.Unmarshal([]byte(graph), &g); err != nil {
		return EmptyCanvasGraph
	}
	nodes, _ := g["nodes"].([]any)
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		delete(node, "selected")
		data, ok := node["data"].(map[string]any)
		if !ok {
			continue
		}
		for _, k := range []string{"taskId", "taskStatus", "taskProgress", "taskError", "subjectIds"} {
			delete(data, k)
		}
		stripAssetRefs(data)
	}
	out, err := json.Marshal(g)
	if err != nil {
		return EmptyCanvasGraph
	}
	return string(out)
}

// stripAssetRefs 递归删掉资产 ID 引用（assetId / refAssetId / styleAssetId / refItems[].assetId / 导演台场景里的…）。
// 这些 ID 属于原作者，复制过去一生成就会被「输入素材不存在」拒绝；媒体地址保留，用户能看到原素材并重新选入。
func stripAssetRefs(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if k == "assetId" || strings.HasSuffix(k, "AssetId") || strings.HasSuffix(k, "AssetIds") {
				delete(x, k)
				continue
			}
			stripAssetRefs(child)
		}
	case []any:
		for _, child := range x {
			stripAssetRefs(child)
		}
	}
}

// CopySharedCanvas 把分享的画布复制成当前用户的新项目 + 画布。
func CopySharedCanvas(userId int, src *Canvas) (*Canvas, error) {
	return CopyCanvasToUser(userId, src, "副本 · "+src.Name, "从分享链接复制")
}

// CopyCanvasToUser 把画布 graph 去敏后复制成 userId 的新项目 + 画布（分享复制与做同款共用）。
// name 为新项目 / 画布名（空取「未命名草稿」，超长截断），projectDesc 记来源。
func CopyCanvasToUser(userId int, src *Canvas, name, projectDesc string) (*Canvas, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "副本 ·" || name == "同款 ·" {
		name = "未命名草稿"
	}
	if len([]rune(name)) > 96 {
		name = string([]rune(name)[:96])
	}
	p := &Project{UserId: userId, Name: name, Description: projectDesc}
	if err := p.Insert(); err != nil {
		return nil, err
	}
	cv := &Canvas{UserId: userId, ProjectId: p.Id, Name: name, Graph: SanitizeSharedGraph(src.Graph)}
	if err := cv.Insert(); err != nil {
		return nil, err
	}
	TouchProject(p.Id)
	return cv, nil
}
