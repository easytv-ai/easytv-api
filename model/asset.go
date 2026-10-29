package model

import (
	"errors"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// Asset 资产：用户上传的素材与生成任务的产物。见 API.md 1.3。
type Asset struct {
	Id         int     `json:"id" gorm:"comment:资产ID"`
	UserId     int     `json:"user_id" gorm:"index;comment:所有者用户ID"`
	ProjectId  int     `json:"project_id" gorm:"index;default:0;comment:所属项目ID（可 0）"`
	TeamId     int     `json:"team_id" gorm:"index;default:0;comment:所属团队空间ID（0 个人空间，API.md 20）"`
	Type       string  `json:"type" gorm:"type:varchar(20);index;comment:类型：image/video/audio/text/other"`
	Name       string  `json:"name" gorm:"type:varchar(200);comment:文件名"`
	Url        string  `json:"url" gorm:"type:varchar(1000);comment:访问地址"`
	CoverUrl   string  `json:"cover_url" gorm:"type:varchar(1000);default:'';comment:封面（视频自动抽帧）"`
	StorageKey string  `json:"storage_key" gorm:"type:varchar(500);default:'';comment:存储路径"`
	Mime       string  `json:"mime" gorm:"type:varchar(100);default:'';comment:MIME"`
	Size       int64   `json:"size" gorm:"default:0;comment:字节数"`
	Width      int     `json:"width" gorm:"default:0;comment:宽（图片/视频）"`
	Height     int     `json:"height" gorm:"default:0;comment:高（图片/视频）"`
	Duration   float64 `json:"duration" gorm:"default:0;comment:时长秒（音视频）"`
	Source     string  `json:"source" gorm:"type:varchar(20);default:'upload';comment:来源：upload/generated"`
	TaskId     int     `json:"task_id" gorm:"default:0;comment:产物来源任务ID"`
	// ImportFrom 跨空间引入（API.md 36.1）时的源资产 id：同一资产在同一空间只复制一行
	ImportFrom int            `json:"import_from,omitempty" gorm:"default:0;index;comment:跨空间引入的源资产ID"`
	CreatedAt  int64          `json:"created_at" gorm:"bigint;comment:创建时间（Unix 秒）"`
	DeletedAt  gorm.DeletedAt `json:"deleted_at" gorm:"index;comment:删除时间"`
}

func (Asset) TableName() string { return "asset" }

const (
	AssetTypeImage = "image"
	AssetTypeVideo = "video"
	AssetTypeAudio = "audio"
	AssetTypeText  = "text"
	AssetTypeOther = "other"

	AssetSourceUpload    = "upload"
	AssetSourceGenerated = "generated"
	// AssetSourceMedia 媒体工具（API.md 23）本地处理产物：裁切 / 截取 / 分离 / 抽帧。
	AssetSourceMedia = "media"
	// AssetSourceImport 从别的空间引入的副本行（API.md 36.1）：同一文件地址，不重传、不占额度
	AssetSourceImport = "import"
)

// AssetTypeFromMime 按 MIME 推断资产类型。
func AssetTypeFromMime(mime string) string {
	mime = strings.ToLower(mime)
	switch {
	case strings.HasPrefix(mime, "image/"):
		return AssetTypeImage
	case strings.HasPrefix(mime, "video/"):
		return AssetTypeVideo
	case strings.HasPrefix(mime, "audio/"):
		return AssetTypeAudio
	case strings.HasPrefix(mime, "text/"):
		return AssetTypeText
	default:
		return AssetTypeOther
	}
}

// IsValidAssetType 校验类型过滤参数。
func IsValidAssetType(t string) bool {
	switch t {
	case AssetTypeImage, AssetTypeVideo, AssetTypeAudio, AssetTypeText, AssetTypeOther:
		return true
	}
	return false
}

// Insert 创建资产。
func (a *Asset) Insert() error {
	return a.InsertTx(DB)
}

// InsertTx 在调用方事务里入库（生成产物要和任务置成功一起提交）。
func (a *Asset) InsertTx(tx *gorm.DB) error {
	a.CreatedAt = common.GetTimestamp()
	if a.Source == "" {
		a.Source = AssetSourceUpload
	}
	return tx.Create(a).Error
}

// SetAssetCover 写入视频资产封面（抽帧结果）。
func SetAssetCover(id int, coverUrl string) error {
	return DB.Model(&Asset{}).Where("id = ?", id).Update("cover_url", coverUrl).Error
}

// SetAssetMediaInfo 写入音视频时长与画面尺寸（ffprobe 结果）。
func SetAssetMediaInfo(id int, duration float64, width, height int) error {
	return DB.Model(&Asset{}).Where("id = ?", id).Updates(map[string]any{"duration": duration, "width": width, "height": height}).Error
}

// GetVideoAssetsWithoutCover 没有封面的视频资产（回填用，只取绝对地址）。
func GetVideoAssetsWithoutCover(limit int) ([]*Asset, error) {
	var list []*Asset
	err := DB.Where("type = ? AND cover_url = '' AND url LIKE 'http%'", AssetTypeVideo).Order("id ASC").Limit(limit).Find(&list).Error
	return list, err
}

// GetAssetsWithoutSize 没记大小的资产（生成产物早期没写 Size，存储配额按它求和，回填用）。
func GetAssetsWithoutSize(afterId, limit int) ([]*Asset, error) {
	var list []*Asset
	err := DB.Where("id > ? AND size = 0 AND url <> ''", afterId).Order("id ASC").Limit(limit).Find(&list).Error
	return list, err
}

// SetAssetSize 写入资产字节数。
func SetAssetSize(id int, size int64) error {
	return DB.Model(&Asset{}).Where("id = ?", id).Update("size", size).Error
}

// GetUserAssetsByIds 按用户批量取未删除资产，返回 id → 资产。
func GetUserAssetsByIds(userId int, ids []int) map[int]*Asset {
	out := map[int]*Asset{}
	if len(ids) == 0 {
		return out
	}
	var list []*Asset
	DB.Where("user_id = ? AND id IN ?", userId, ids).Find(&list)
	for _, a := range list {
		out[a.Id] = a
	}
	return out
}

// GetAssetsByIdsForUsers 按一组用户批量取未删除资产（画布上可能混着作者与队友的产物）。
func GetAssetsByIdsForUsers(userIds []int, ids []int) map[int]*Asset {
	out := map[int]*Asset{}
	if len(ids) == 0 || len(userIds) == 0 {
		return out
	}
	var list []*Asset
	DB.Where("user_id IN ? AND id IN ?", userIds, ids).Find(&list)
	for _, a := range list {
		out[a.Id] = a
	}
	return out
}

// GetUserAsset 按用户 + ID 查询。
func GetUserAsset(userId, id int) (*Asset, error) {
	a := &Asset{}
	if err := DB.Where("id = ? AND user_id = ?", id, userId).First(a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return a, nil
}

// AssetFilter 资产列表过滤条件。
type AssetFilter struct {
	UserId    int
	ProjectId int
	Type      string
	Source    string
	Keyword   string
	// 用户侧空间（API.md 20）：Space=true 时 TeamId=0 只取 team_id=0（配合 UserId），TeamId>0 取该团队全部资产（忽略 UserId）
	Space  bool
	TeamId int
}

// GetAssets 资产列表（用户侧 UserId > 0；管理端 UserId 可 0 查全站）。
func GetAssets(filter AssetFilter, pageInfo *common.PageInfo) (list []*Asset, total int64, err error) {
	query := DB.Model(&Asset{})
	switch {
	case filter.Space && filter.TeamId > 0:
		query = query.Where("team_id = ?", filter.TeamId)
	case filter.Space:
		query = query.Where("user_id = ? AND team_id = 0", filter.UserId)
	case filter.UserId > 0:
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.ProjectId > 0 {
		query = query.Where("project_id = ?", filter.ProjectId)
	}
	if filter.Type != "" {
		query = query.Where("type = ?", filter.Type)
	}
	if filter.Source != "" {
		query = query.Where("source = ?", filter.Source)
	}
	if filter.Keyword != "" {
		query = query.Where("name LIKE ?", "%"+filter.Keyword+"%")
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

// DeleteUserAsset 软删除资产。
func DeleteUserAsset(userId, id int) error {
	result := DB.Where("id = ? AND user_id = ?", id, userId).Delete(&Asset{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ===== 管理端 =====

// GetAssetsAdmin 管理端列表；trashed: "" 全部 / "1" 正常 / "2" 回收站。
func GetAssetsAdmin(filter AssetFilter, trashed string, pageInfo *common.PageInfo) (list []*Asset, total int64, err error) {
	query := DB.Unscoped().Model(&Asset{})
	switch trashed {
	case "1":
		query = query.Where("deleted_at IS NULL")
	case "2":
		query = query.Where("deleted_at IS NOT NULL")
	}
	if filter.UserId > 0 {
		query = query.Where("user_id = ?", filter.UserId)
	}
	if filter.Type != "" {
		query = query.Where("type = ?", filter.Type)
	}
	if filter.Source != "" {
		query = query.Where("source = ?", filter.Source)
	}
	if filter.Keyword != "" {
		query = query.Where("name LIKE ?", "%"+filter.Keyword+"%")
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

// GetAssetByIdUnscoped 管理端按 ID 查询（含回收站）。
func GetAssetByIdUnscoped(id int) (*Asset, error) {
	a := &Asset{}
	if err := DB.Unscoped().Where("id = ?", id).First(a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return a, nil
}

// AdminTrashAsset 管理端移入回收站。
func AdminTrashAsset(id int) error {
	result := DB.Where("id = ?", id).Delete(&Asset{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// AdminRestoreAsset 管理端恢复。
func AdminRestoreAsset(id int) error {
	result := DB.Unscoped().Model(&Asset{}).Where("id = ? AND deleted_at IS NOT NULL", id).Update("deleted_at", nil)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// AdminDeleteAsset 管理端物理删除（文件由 controller 清理）。
func AdminDeleteAsset(id int) error {
	result := DB.Unscoped().Where("id = ?", id).Delete(&Asset{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// GetUserTrashAssets 用户回收站资产。
func GetUserTrashAssets(userId int, pageInfo *common.PageInfo) (list []*Asset, total int64, err error) {
	query := DB.Unscoped().Model(&Asset{}).Where("user_id = ? AND deleted_at IS NOT NULL", userId)
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("deleted_at desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&list).Error
	return list, total, err
}

// RestoreAsset 用户恢复资产。
func RestoreAsset(userId, id int) error {
	result := DB.Unscoped().Model(&Asset{}).Where("id = ? AND user_id = ? AND deleted_at IS NOT NULL", id, userId).Update("deleted_at", nil)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// PurgeTrashedAssetsBefore 物理删除回收站中早于 cutoff 的资产，返回其 storage_key 供清理文件。
func PurgeTrashedAssetsBefore(cutoff int64) ([]string, error) {
	var list []*Asset
	if err := DB.Unscoped().Where("deleted_at IS NOT NULL AND UNIX_TIMESTAMP(deleted_at) < ?", cutoff).Find(&list).Error; err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(list))
	for _, a := range list {
		if err := DB.Unscoped().Delete(a).Error; err != nil {
			return keys, err
		}
		// 还有别的（未删除）资产行指向同一地址——跨空间引入的副本（API.md 36.1）——文件要留着
		if a.StorageKey != "" && !assetUrlStillReferenced(a.Url, a.Id) {
			keys = append(keys, a.StorageKey)
		}
	}
	return keys, nil
}

// assetUrlStillReferenced 除 exceptId 外是否还有未删除的资产行用着这个地址。
func assetUrlStillReferenced(url string, exceptId int) bool {
	if url == "" {
		return false
	}
	var n int64
	DB.Model(&Asset{}).Where("url = ? AND id <> ?", url, exceptId).Count(&n)
	return n > 0
}

// SumAssetSize 一组用户未删除资产的总字节数，云存储额度校验用。
func SumAssetSize(userIds []int) int64 {
	if len(userIds) == 0 {
		return 0
	}
	var n int64
	DB.Model(&Asset{}).Select("COALESCE(SUM(size), 0)").Where("user_id IN ?", userIds).Scan(&n)
	return n
}

// FindImportedAsset 当前空间里从 from 引入过的那一行（API.md 36.1）；没有返回 ErrNotFound。
func FindImportedAsset(userId, teamId, from int) (*Asset, error) {
	a := &Asset{}
	query := DB.Where("import_from = ? AND source = ?", from, AssetSourceImport)
	if teamId > 0 {
		query = query.Where("team_id = ?", teamId)
	} else {
		query = query.Where("user_id = ? AND team_id = 0", userId)
	}
	if err := query.Order("id asc").First(a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return a, nil
}
