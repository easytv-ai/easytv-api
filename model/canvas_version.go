package model

import (
	"errors"

	"easytv-api/common"

	"gorm.io/gorm"
)

// ===== 画布版本历史（API.md 1.2.1）=====

const (
	CanvasVersionSourceAuto    = "auto"    // 自动保存产生（10 分钟内滚动覆盖）
	CanvasVersionSourceManual  = "manual"  // 用户主动保存（Ctrl+S / 保存按钮）
	CanvasVersionSourceRestore = "restore" // 恢复某版本前对当前状态的自动备份

	// canvasVersionAutoWindow 自动快照合并窗口：窗口内的自动保存只更新最近一条自动快照
	canvasVersionAutoWindow = int64(10 * 60)
	// canvasVersionKeep 每个画布最多保留的版本数
	canvasVersionKeep = 50
)

// CanvasVersion 画布快照。graph 与 canvas.graph 同结构。
type CanvasVersion struct {
	Id        int    `json:"id" gorm:"comment:版本ID"`
	CanvasId  int    `json:"canvas_id" gorm:"index;comment:画布ID"`
	UserId    int    `json:"user_id" gorm:"index;comment:所有者"`
	Version   int    `json:"version" gorm:"comment:对应 canvas.version"`
	Name      string `json:"name" gorm:"type:varchar(100);comment:当时的画布名"`
	Graph     string `json:"-" gorm:"type:longtext;comment:画布 JSON"`
	NodeCount int    `json:"node_count" gorm:"default:0;comment:节点数"`
	Source    string `json:"source" gorm:"type:varchar(20);index;comment:auto/manual/restore"`
	Note      string `json:"note" gorm:"type:varchar(100);default:'';comment:备注"`
	CreatedAt int64  `json:"created_at" gorm:"bigint"`
}

func (CanvasVersion) TableName() string { return "canvas_version" }

func countGraphNodes(graph string) int {
	return (&Canvas{Graph: graph}).NodeCount()
}

// SnapshotCanvas 记录一次快照。auto 来源在合并窗口内滚动覆盖最近一条自动快照；超过保留数时删最旧。
func SnapshotCanvas(userId, canvasId, version int, name, graph, source, note string) error {
	now := common.GetTimestamp()
	if source == CanvasVersionSourceAuto {
		latest := &CanvasVersion{}
		err := DB.Where("canvas_id = ? AND source = ?", canvasId, CanvasVersionSourceAuto).
			Order("id desc").First(latest).Error
		if err == nil && now-latest.CreatedAt < canvasVersionAutoWindow {
			// 只有它仍是最新一条时才覆盖，避免把手动快照之前的状态改掉
			var newer int64
			DB.Model(&CanvasVersion{}).Where("canvas_id = ? AND id > ?", canvasId, latest.Id).Count(&newer)
			if newer == 0 {
				return DB.Model(latest).Updates(map[string]any{
					"version":    version,
					"name":       name,
					"graph":      graph,
					"node_count": countGraphNodes(graph),
					"created_at": now,
				}).Error
			}
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	v := &CanvasVersion{
		CanvasId:  canvasId,
		UserId:    userId,
		Version:   version,
		Name:      name,
		Graph:     graph,
		NodeCount: countGraphNodes(graph),
		Source:    source,
		Note:      note,
		CreatedAt: now,
	}
	if err := DB.Create(v).Error; err != nil {
		return err
	}
	pruneCanvasVersions(canvasId)
	return nil
}

func pruneCanvasVersions(canvasId int) {
	var ids []int
	DB.Model(&CanvasVersion{}).Where("canvas_id = ?", canvasId).
		Order("id desc").Offset(canvasVersionKeep).Limit(200).Pluck("id", &ids)
	if len(ids) > 0 {
		DB.Where("id IN ?", ids).Delete(&CanvasVersion{})
	}
}

// GetCanvasVersions 版本列表（不含 graph），最新在前。
func GetCanvasVersions(userId, canvasId int) (list []*CanvasVersion, err error) {
	err = DB.Select("id, canvas_id, user_id, version, name, node_count, source, note, created_at").
		Where("canvas_id = ? AND user_id = ?", canvasId, userId).
		Order("id desc").Limit(canvasVersionKeep).Find(&list).Error
	return list, err
}

// GetCanvasVersion 单个版本（含 graph）。
func GetCanvasVersion(userId, canvasId, id int) (*CanvasVersion, error) {
	v := &CanvasVersion{}
	if err := DB.Where("id = ? AND canvas_id = ? AND user_id = ?", id, canvasId, userId).First(v).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	return v, nil
}

// RestoreCanvasVersion 把画布内容回滚到某个版本：先备份当前状态，再覆盖 graph 并把 version +1。
func RestoreCanvasVersion(userId, canvasId, id int) (newVersion int, updatedAt int64, err error) {
	target, err := GetCanvasVersion(userId, canvasId, id)
	if err != nil {
		return 0, 0, err
	}
	cv, err := GetUserCanvas(userId, canvasId)
	if err != nil {
		return 0, 0, err
	}
	if err := SnapshotCanvas(userId, canvasId, cv.Version, cv.Name, cv.Graph, CanvasVersionSourceRestore, "恢复前自动备份"); err != nil {
		return 0, 0, err
	}
	newVersion, updatedAt, err = SaveCanvas(userId, canvasId, cv.Version, map[string]any{"graph": target.Graph})
	if err != nil {
		return 0, 0, err
	}
	TouchProject(cv.ProjectId)
	return newVersion, updatedAt, nil
}
