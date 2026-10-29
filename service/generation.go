package service

import (
	"sync"

	"easytv-api/model"
)

// ModelInfo 可供前端选择的模型。
type ModelInfo struct {
	Provider string `json:"provider"` // 厂商标识：minimax / kling / …
	Model    string `json:"model"`    // 模型标识，POST /user/task 的 model 字段
	Type     string `json:"type"`     // text / image / video / audio
	Label    string `json:"label"`    // 展示名
}

// GenerationProvider 生成厂商接入接口。
//
// 新增厂商：在 service/providers/ 下实现本接口，并调用 service.RegisterProvider 注册
// （EasyAPI 网关按 DB setting 配置，见 providers.RegisterProviderFromSettings；
// 配置热更新时用 service.ReplaceProvider 以同名替换）。
// Submit 必须异步执行（自行 goroutine），并通过 model.SetTaskRunning / SetTaskProgress /
// SetTaskSucceeded / SetTaskFailed 推进任务状态；成功时先写入 Asset（Source=generated）再回填。
type GenerationProvider interface {
	Name() string
	Models() []ModelInfo
	Submit(task *model.GenerationTask) error
}

var (
	providerMu sync.RWMutex
	providers  []GenerationProvider
)

// RegisterProvider 注册厂商实现。
func RegisterProvider(p GenerationProvider) {
	providerMu.Lock()
	defer providerMu.Unlock()
	providers = append(providers, p)
}

// ReplaceProvider 按名称替换已注册厂商；不存在则追加（配置热更新重建用）。
func ReplaceProvider(name string, p GenerationProvider) {
	providerMu.Lock()
	defer providerMu.Unlock()
	for i, old := range providers {
		if old.Name() == name {
			providers[i] = p
			return
		}
	}
	providers = append(providers, p)
}

// RemoveProvider 按名称注销厂商（网关关闭时）。
func RemoveProvider(name string) {
	providerMu.Lock()
	defer providerMu.Unlock()
	out := providers[:0]
	for _, p := range providers {
		if p.Name() != name {
			out = append(out, p)
		}
	}
	providers = out
}

// FindProvider 按任务类型 + 模型查找厂商；未接入返回 nil。
func FindProvider(taskType, modelName string) GenerationProvider {
	providerMu.RLock()
	defer providerMu.RUnlock()
	for _, p := range providers {
		for _, m := range p.Models() {
			if m.Type == taskType && m.Model == modelName {
				return p
			}
		}
	}
	return nil
}

// ListModels 全部可用模型（GET /generation/models）。
func ListModels() []ModelInfo {
	providerMu.RLock()
	defer providerMu.RUnlock()
	list := make([]ModelInfo, 0)
	for _, p := range providers {
		list = append(list, p.Models()...)
	}
	return list
}
