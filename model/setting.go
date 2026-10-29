package model

import (
	"sync"
)

// Setting 系统配置表（key-value），与 easyapi model/setting.go 完全一致：Key 即主键，Value 存文本。
type Setting struct {
	Key   string `json:"key" gorm:"primaryKey;comment:配置键"`
	Value string `json:"value" gorm:"type:text;comment:配置值"`
}

func (Setting) TableName() string { return "setting" }

// settingMap 内存缓存（启动时全量加载 + 定时热同步）。
var (
	settingMapMu sync.RWMutex
	settingMap   = make(map[string]string)
)

// InitSettingMap 启动时从 DB 加载全部配置到内存。
func InitSettingMap() error {
	var list []*Setting
	if err := DB.Find(&list).Error; err != nil {
		return err
	}
	settingMapMu.Lock()
	defer settingMapMu.Unlock()
	settingMap = make(map[string]string, len(list))
	for _, s := range list {
		settingMap[s.Key] = s.Value
	}
	return nil
}

// GetSetting 读取配置（内存优先，缺失回查 DB）。
func GetSetting(key string) string {
	settingMapMu.RLock()
	val, ok := settingMap[key]
	settingMapMu.RUnlock()
	if ok {
		return val
	}
	s := &Setting{}
	if err := DB.Where("`key` = ?", key).First(s).Error; err != nil {
		return ""
	}
	return s.Value
}

// GetAllSettings 返回全部配置快照。
func GetAllSettings() map[string]string {
	settingMapMu.RLock()
	defer settingMapMu.RUnlock()
	result := make(map[string]string, len(settingMap))
	for k, v := range settingMap {
		result[k] = v
	}
	return result
}

// UpdateSetting 写入配置（upsert 语义 + 更新内存缓存）。
func UpdateSetting(key string, value string) error {
	s := &Setting{Key: key, Value: value}
	if err := DB.Save(s).Error; err != nil {
		return err
	}
	settingMapMu.Lock()
	settingMap[key] = value
	settingMapMu.Unlock()
	return nil
}

// SetSettingCache 只写内存缓存不落库（测试与临时覆盖用；下次 SyncSettings 会被 DB 值覆盖）。
func SetSettingCache(key, value string) {
	settingMapMu.Lock()
	defer settingMapMu.Unlock()
	if value == "" {
		delete(settingMap, key)
		return
	}
	settingMap[key] = value
}

// SyncSettings 从 DB 热同步配置到内存（后台任务调用）。
func SyncSettings() error {
	return InitSettingMap()
}
