package model

import (
	"errors"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// Project 项目：用户创作的容器，一个项目下可有多个画布与资产。见 API.md 1.1。
type Project struct {
	Id          int            `json:"id" gorm:"comment:项目ID"`
	UserId      int            `json:"user_id" gorm:"index;comment:所有者用户ID"`
	TeamId      int            `json:"team_id" gorm:"index;default:0;comment:所属团队空间ID（0 个人空间，API.md 20）"`
	Name        string         `json:"name" gorm:"type:varchar(100);comment:项目名"`
	Description string         `json:"description" gorm:"type:varchar(500);default:'';comment:描述"`
	CoverUrl    string         `json:"cover_url" gorm:"type:varchar(500);default:'';comment:封面地址"`
	Status      int            `json:"status" gorm:"default:1;comment:状态：1=正常"`
	CreatedAt   int64          `json:"created_at" gorm:"bigint;comment:创建时间（Unix 秒）"`
	UpdatedAt   int64          `json:"updated_at" gorm:"bigint;comment:更新时间（Unix 秒）"`
	DeletedAt   gorm.DeletedAt `json:"deleted_at" gorm:"index;comment:回收站时间"`

	// CanvasId 列表附带：该项目最近更新的画布 id（非表字段）
	CanvasId int `json:"canvas_id" gorm:"-"`
	// OpenComments 未解决批注数（F7，API.md 10.10；非表字段）
	OpenComments int `json:"open_comments" gorm:"-"`
	// OwnerNickname / TeamShared 团队视图用（非表字段）
	OwnerNickname string `json:"owner_nickname,omitempty" gorm:"-"`
	TeamShared    bool   `json:"team_shared,omitempty" gorm:"-"`
	// 团队空间列表 / 详情附带（API.md 20，非表字段）：作者 id 与当前用户对这个项目能做什么
	OwnerId    int    `json:"owner_id" gorm:"-"`
	Permission string `json:"permission,omitempty" gorm:"-"`
	CanEdit    bool   `json:"can_edit" gorm:"-"`
	CanDelete  bool   `json:"can_delete" gorm:"-"`
	CanMove    bool   `json:"can_move" gorm:"-"`
}

func (Project) TableName() string { return "project" }

const ProjectStatusNormal = 1

var ErrNotFound = errors.New("资源不存在")

// Insert 创建项目。
func (p *Project) Insert() error {
	now := common.GetTimestamp()
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.Status == 0 {
		p.Status = ProjectStatusNormal
	}
	return DB.Create(p).Error
}

// GetUserProject 按用户 + ID 查询（不含回收站）。
func GetUserProject(userId, id int) (*Project, error) {
	p := &Project{}
	if err := DB.Where("id = ? AND user_id = ?", id, userId).First(p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// GetUserProjects 个人空间项目列表（不含回收站、不含已移入团队的），附带最近画布 id。
func GetUserProjects(userId int, keyword string, pageInfo *common.PageInfo) (list []*Project, total int64, err error) {
	query := DB.Model(&Project{}).Where("user_id = ? AND team_id = 0", userId)
	if keyword != "" {
		query = query.Where("name LIKE ?", "%"+keyword+"%")
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("updated_at desc, id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	attachLatestCanvas(list)
	attachProjectCovers(list)
	return list, total, nil
}

// GetUserTrashProjects 用户回收站项目。
func GetUserTrashProjects(userId int, pageInfo *common.PageInfo) (list []*Project, total int64, err error) {
	query := DB.Unscoped().Model(&Project{}).Where("user_id = ? AND deleted_at IS NOT NULL", userId)
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("deleted_at desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	attachProjectCovers(list)
	return list, total, nil
}

// attachLatestCanvas 为项目列表填充最近更新的画布 id。
func attachLatestCanvas(list []*Project) {
	if len(list) == 0 {
		return
	}
	ids := make([]int, 0, len(list))
	for _, p := range list {
		ids = append(ids, p.Id)
	}
	type row struct {
		ProjectId int
		Id        int
	}
	var rows []row
	// 每个项目取 updated_at 最大的画布；数据量小，直接取全部后在内存挑选
	DB.Model(&Canvas{}).Select("project_id, id").Where("project_id IN ?", ids).Order("updated_at desc, id desc").Scan(&rows)
	picked := map[int]int{}
	for _, r := range rows {
		if _, ok := picked[r.ProjectId]; !ok {
			picked[r.ProjectId] = r.Id
		}
	}
	for _, p := range list {
		p.CanvasId = picked[p.Id]
	}
}

// attachProjectCovers 列表没手设封面时，用项目里最近一张能看的画面填上（只改这次响应，不写库）。
// 优先图片或视频抽帧，再退到画布缩略图。还没有任何画面的项目保持空封面。
func attachProjectCovers(list []*Project) {
	need := make([]int, 0, len(list))
	for _, p := range list {
		if p != nil && strings.TrimSpace(p.CoverUrl) == "" && p.Id > 0 {
			need = append(need, p.Id)
		}
	}
	if len(need) == 0 {
		return
	}
	best := map[int]coverCandidate{}
	collectAssetCovers(best, need)
	collectTaskCovers(best, need)
	for _, p := range list {
		if p == nil || strings.TrimSpace(p.CoverUrl) != "" {
			continue
		}
		if hit, ok := best[p.Id]; ok {
			p.CoverUrl = hit.url
		}
	}
	fillCanvasThumbCovers(list)
}

type coverCandidate struct {
	id  int
	url string
}

func considerCover(best map[int]coverCandidate, projectId, assetId int, typ, url, cover string) {
	if projectId <= 0 || assetId <= 0 {
		return
	}
	preview := ""
	switch typ {
	case AssetTypeImage:
		preview = strings.TrimSpace(url)
	case AssetTypeVideo:
		preview = strings.TrimSpace(cover)
	}
	if preview == "" {
		return
	}
	if cur, ok := best[projectId]; ok && cur.id >= assetId {
		return
	}
	best[projectId] = coverCandidate{id: assetId, url: preview}
}

type coverRow struct {
	ProjectId int
	Id        int
}

func collectAssetCovers(best map[int]coverCandidate, projectIds []int) {
	var rows []coverRow
	err := DB.Model(&Asset{}).
		Select("project_id, MAX(id) AS id").
		Where("project_id IN ?", projectIds).
		Where("(type = ? AND url <> '') OR (type = ? AND cover_url <> '')", AssetTypeImage, AssetTypeVideo).
		Group("project_id").
		Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return
	}
	applyCoverRows(best, rows)
}

func collectTaskCovers(best map[int]coverCandidate, projectIds []int) {
	// 产物有时只挂在任务 / 画布上，asset.project_id 还是 0。
	expr := "COALESCE(NULLIF(a.project_id, 0), NULLIF(t.project_id, 0), NULLIF(c.project_id, 0))"
	var rows []coverRow
	err := DB.Table("generation_task AS t").
		Select(expr+" AS project_id, MAX(a.id) AS id").
		Joins("INNER JOIN asset AS a ON a.id = t.result_asset_id AND a.deleted_at IS NULL").
		Joins("LEFT JOIN canvas AS c ON c.id = t.canvas_id").
		Where("t.result_asset_id > 0").
		Where(expr+" IN ?", projectIds).
		Where("(a.type = ? AND a.url <> '') OR (a.type = ? AND a.cover_url <> '')", AssetTypeImage, AssetTypeVideo).
		Group(expr).
		Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return
	}
	applyCoverRows(best, rows)
}

func applyCoverRows(best map[int]coverCandidate, rows []coverRow) {
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		if r.Id > 0 {
			ids = append(ids, r.Id)
		}
	}
	if len(ids) == 0 {
		return
	}
	var assets []Asset
	if err := DB.Select("id, type, url, cover_url").Where("id IN ?", ids).Find(&assets).Error; err != nil {
		return
	}
	byID := make(map[int]Asset, len(assets))
	for _, a := range assets {
		byID[a.Id] = a
	}
	for _, r := range rows {
		a, ok := byID[r.Id]
		if !ok {
			continue
		}
		considerCover(best, r.ProjectId, a.Id, a.Type, a.Url, a.CoverUrl)
	}
}

func fillCanvasThumbCovers(list []*Project) {
	need := make([]int, 0, len(list))
	for _, p := range list {
		if p != nil && strings.TrimSpace(p.CoverUrl) == "" && p.Id > 0 {
			need = append(need, p.Id)
		}
	}
	if len(need) == 0 {
		return
	}
	type row struct {
		ProjectId    int
		ThumbnailUrl string
	}
	var rows []row
	if err := DB.Unscoped().Model(&Canvas{}).
		Select("project_id, thumbnail_url").
		Where("project_id IN ? AND thumbnail_url <> ''", need).
		Order("updated_at desc, id desc").
		Scan(&rows).Error; err != nil {
		return
	}
	picked := map[int]string{}
	for _, r := range rows {
		if _, ok := picked[r.ProjectId]; !ok {
			picked[r.ProjectId] = r.ThumbnailUrl
		}
	}
	for _, p := range list {
		if p != nil && strings.TrimSpace(p.CoverUrl) == "" {
			p.CoverUrl = picked[p.Id]
		}
	}
}

// UpdateProjectFields 更新指定字段并刷新 updated_at。
func UpdateProjectFields(userId, id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	result := DB.Model(&Project{}).Where("id = ? AND user_id = ?", id, userId).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchProject 仅刷新项目 updated_at（画布保存时调用）。
func TouchProject(id int) {
	if id <= 0 {
		return
	}
	DB.Model(&Project{}).Where("id = ?", id).Update("updated_at", common.GetTimestamp())
}

// TrashProject 项目进回收站，级联其画布。
func TrashProject(userId, id int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND user_id = ?", id, userId).Delete(&Project{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return tx.Where("project_id = ? AND user_id = ?", id, userId).Delete(&Canvas{}).Error
	})
}

// RestoreProject 从回收站恢复项目及其画布。
func RestoreProject(userId, id int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Unscoped().Model(&Project{}).
			Where("id = ? AND user_id = ? AND deleted_at IS NOT NULL", id, userId).
			Update("deleted_at", nil)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return tx.Unscoped().Model(&Canvas{}).
			Where("project_id = ? AND user_id = ?", id, userId).
			Update("deleted_at", nil).Error
	})
}

// PurgeTrashedBefore 物理删除回收站中早于 cutoff（Unix 秒）的项目与画布。
func PurgeTrashedBefore(cutoff int64) (int64, error) {
	var count int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		r := tx.Unscoped().Where("deleted_at IS NOT NULL AND UNIX_TIMESTAMP(deleted_at) < ?", cutoff).Delete(&Project{})
		if r.Error != nil {
			return r.Error
		}
		count = r.RowsAffected
		return tx.Unscoped().Where("deleted_at IS NOT NULL AND UNIX_TIMESTAMP(deleted_at) < ?", cutoff).Delete(&Canvas{}).Error
	})
	return count, err
}

// GetAllProjects 管理端列表。
func GetAllProjects(userId int, keyword string, pageInfo *common.PageInfo) (list []*Project, total int64, err error) {
	query := DB.Model(&Project{})
	if userId > 0 {
		query = query.Where("user_id = ?", userId)
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

// ===== 管理端 =====

// GetAllProjectsAdmin 管理端列表；trashed: "" 全部 / "1" 正常 / "2" 回收站。
func GetAllProjectsAdmin(userId int, keyword, trashed string, pageInfo *common.PageInfo) (list []*Project, total int64, err error) {
	query := DB.Unscoped().Model(&Project{})
	switch trashed {
	case "1":
		query = query.Where("deleted_at IS NULL")
	case "2":
		query = query.Where("deleted_at IS NOT NULL")
	}
	if userId > 0 {
		query = query.Where("user_id = ?", userId)
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

// AdminTrashProject 管理端移入回收站（级联画布）。
func AdminTrashProject(id int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ?", id).Delete(&Project{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return tx.Where("project_id = ?", id).Delete(&Canvas{}).Error
	})
}

// AdminRestoreProject 管理端恢复。
func AdminRestoreProject(id int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Unscoped().Model(&Project{}).Where("id = ? AND deleted_at IS NOT NULL", id).Update("deleted_at", nil)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return tx.Unscoped().Model(&Canvas{}).Where("project_id = ?", id).Update("deleted_at", nil).Error
	})
}

// AdminDeleteProject 管理端物理删除（含画布）。
func AdminDeleteProject(id int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Unscoped().Where("id = ?", id).Delete(&Project{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return tx.Unscoped().Where("project_id = ?", id).Delete(&Canvas{}).Error
	})
}

// GetTeamProjects 团队空间的项目（API.md 20）：team_id = 该团队的全部项目，不分作者。
func GetTeamProjects(teamId int, keyword string, pageInfo *common.PageInfo) (list []*Project, total int64, err error) {
	if teamId <= 0 {
		return []*Project{}, 0, nil
	}
	query := DB.Model(&Project{}).Where("team_id = ?", teamId)
	if keyword != "" {
		query = query.Where("name LIKE ?", "%"+keyword+"%")
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("updated_at desc, id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	if err != nil {
		return nil, 0, err
	}
	// 卡片点开要进画布：和「我的项目」一样带上最近画布 id，否则前端只能开一张空白草稿
	attachLatestCanvas(list)
	attachProjectCovers(list)
	attachProjectOwners(list)
	return list, total, nil
}

// GetProjectById 按 id 取未进回收站的项目（不校验归属，调用方负责权限）。
func GetProjectById(id int) (*Project, error) {
	p := &Project{}
	if err := DB.Where("id = ?", id).First(p).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return p, nil
}

// MoveProjectToTeam 项目换空间（API.md 20）：改 project.team_id，并把作者在这个项目里的资产
// （上传到项目的、项目画布上生成的）一起改成同一空间，队友才能在团队里用到这些素材。
func MoveProjectToTeam(authorId, projectId, teamId int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Project{}).Where("id = ? AND user_id = ?", projectId, authorId).
			Updates(map[string]any{"team_id": teamId, "updated_at": common.GetTimestamp()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		canvasIds := tx.Unscoped().Model(&Canvas{}).Select("id").Where("project_id = ?", projectId)
		taskAssets := tx.Model(&GenerationTask{}).Select("result_asset_id").Where("canvas_id IN (?) AND result_asset_id > 0", canvasIds)
		return tx.Unscoped().Model(&Asset{}).
			Where("user_id = ? AND (project_id = ? OR id IN (?))", authorId, projectId, taskAssets).
			Update("team_id", teamId).Error
	})
}

// attachProjectOwners 填充所有者昵称。
func attachProjectOwners(list []*Project) {
	ids := []int{}
	for _, p := range list {
		ids = append(ids, p.UserId)
	}
	if len(ids) == 0 {
		return
	}
	type row struct {
		Id       int
		Nickname string
		Username string
	}
	rows := []row{}
	DB.Model(&User{}).Select("id, nickname, username").Where("id IN ?", ids).Find(&rows)
	names := map[int]string{}
	for _, r := range rows {
		if r.Nickname != "" {
			names[r.Id] = r.Nickname
		} else {
			names[r.Id] = r.Username
		}
	}
	for _, p := range list {
		p.OwnerNickname = names[p.UserId]
	}
}

// AttachProjectOwners 导出给团队空间列表 / 详情用。
func AttachProjectOwners(list []*Project) { attachProjectOwners(list) }

// AttachProjectOpenComments 一次 GROUP BY 填充未解决批注数，避免逐条查询。
func AttachProjectOpenComments(list []*Project) {
	ids := []int{}
	for _, p := range list {
		ids = append(ids, p.Id)
	}
	if len(ids) == 0 {
		return
	}
	type row struct {
		ProjectId int
		N         int
	}
	rows := []row{}
	DB.Model(&ProjectComment{}).
		Select("project_id, count(*) as n").
		Where("project_id IN ? AND resolved = 0", ids).
		Group("project_id").Find(&rows)
	counts := map[int]int{}
	for _, r := range rows {
		counts[r.ProjectId] = r.N
	}
	for _, p := range list {
		p.OpenComments = counts[p.Id]
	}
}
