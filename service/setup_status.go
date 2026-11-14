package service

import (
	"strings"

	"easytv-api/model"
)

// 存储类型（SetupStatus.Storage.Kind）。
const (
	StorageKindLocal = "local"
	StorageKindOSS   = "oss"
	StorageKindQiniu = "qiniu"
)

// SetupStatusInput 部署就绪状态的原始输入；只带「是否有值」，不带任何密钥。
type SetupStatusInput struct {
	GatewayBaseUrl string
	HasGatewayKey  bool
	GatewayEnabled bool
	// Prices model_price 全表（含停用行）。
	Prices        []*model.ModelPrice
	StorageKind   string
	UploadBaseUrl string
	FFmpeg        bool
	FFprobe       bool
}

// SetupGatewayStatus 网关一项。
type SetupGatewayStatus struct {
	Configured bool `json:"configured"` // 地址与令牌都有值
	Enabled    bool `json:"enabled"`    // 开关打开且有令牌
}

// SetupModelStatus 模型一项；只数生效中（status=1）的定价行，与 GET /generation/models 下发的清单同口径。
type SetupModelStatus struct {
	PricedCount int            `json:"priced_count"`
	TotalCount  int            `json:"total_count"` // 含停用行
	ByType      map[string]int `json:"by_type"`     // text / image / video / audio 四个键恒在
}

// SetupStorageStatus 存储一项。
type SetupStorageStatus struct {
	Kind string `json:"kind"` // local / oss / qiniu
	// HasPublicBaseUrl 本地磁盘时是否配了对外前缀（UploadBaseUrl）；对象存储恒为 true。
	// 为 false 时素材是 /upload/ 相对地址，网关取不到参考视频 / 音频。
	HasPublicBaseUrl bool `json:"has_public_base_url"`
}

// SetupMediaStatus 本机媒体工具。
type SetupMediaStatus struct {
	FFmpeg  bool `json:"ffmpeg"`
	FFprobe bool `json:"ffprobe"`
}

// SetupStatus GET /admin/setup/status 的视图（API.md 40）。
type SetupStatus struct {
	// Ready 网关已配置并启用，且至少有一个生效中的模型。
	Ready   bool               `json:"ready"`
	Gateway SetupGatewayStatus `json:"gateway"`
	Models  SetupModelStatus   `json:"models"`
	Storage SetupStorageStatus `json:"storage"`
	Media   SetupMediaStatus   `json:"media"`
}

// BuildSetupStatus 由原始输入算出就绪状态（纯函数）。
func BuildSetupStatus(in SetupStatusInput) SetupStatus {
	byType := map[string]int{
		model.TaskTypeText:  0,
		model.TaskTypeImage: 0,
		model.TaskTypeVideo: 0,
		model.TaskTypeAudio: 0,
	}
	priced, total := 0, 0
	for _, p := range in.Prices {
		if p == nil {
			continue
		}
		total++
		if p.Status != model.BillingStatusOn {
			continue
		}
		priced++
		if _, ok := byType[p.Type]; ok {
			byType[p.Type]++
		}
	}
	kind := in.StorageKind
	switch kind {
	case StorageKindOSS, StorageKindQiniu:
	default:
		kind = StorageKindLocal
	}
	configured := strings.TrimSpace(in.GatewayBaseUrl) != "" && in.HasGatewayKey
	enabled := in.GatewayEnabled && in.HasGatewayKey
	return SetupStatus{
		Ready:   configured && enabled && priced > 0,
		Gateway: SetupGatewayStatus{Configured: configured, Enabled: enabled},
		Models:  SetupModelStatus{PricedCount: priced, TotalCount: total, ByType: byType},
		Storage: SetupStorageStatus{
			Kind:             kind,
			HasPublicBaseUrl: kind != StorageKindLocal || strings.TrimSpace(in.UploadBaseUrl) != "",
		},
		Media: SetupMediaStatus{FFmpeg: in.FFmpeg, FFprobe: in.FFprobe},
	}
}

// CurrentStorageKind 当前生效的存储实现类型。
func CurrentStorageKind() string {
	switch DefaultStorage.(type) {
	case *OSSStorage:
		return StorageKindOSS
	case *QiniuStorage:
		return StorageKindQiniu
	default:
		return StorageKindLocal
	}
}
