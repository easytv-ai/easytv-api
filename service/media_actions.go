package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"easytv-api/model"
	"easytv-api/setting"
)

// 媒体工具动作注册表（API.md 23）。
//
// 三种引擎：
//   - local：服务端 ffmpeg 处理，免费，一次可以出多个产物；
//   - model：走生成任务调网关模型，按模型定价计费，能不能用取决于运营有没有配对应能力的模型；
//   - existing：已经实现的接口（重拍 / 续写 / 重新生成），只在清单里给前端指路。
//
// 前端整条工具条由 GET /user/media/actions 驱动：available=false 的按钮显示为不可用并带上原因，
// 而不是假装「即将上线」。

const (
	MediaEngineExisting = "existing"

	// 抽帧上限：一次作业最多产出的帧数。抽帧是「拉片」的原料，也是存储额度消耗最快的地方。
	mediaFramesMax = 24
)

// MediaParamSpec 动作参数（下发给前端做表单）。
type MediaParamSpec struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // string / int / float / bool / enum / rect
	Default any      `json:"default,omitempty"`
	Min     any      `json:"min,omitempty"`
	Max     any      `json:"max,omitempty"`
	Options []string `json:"options,omitempty"`
	Help    string   `json:"help,omitempty"`
}

// MediaAction 动作定义。
type MediaAction struct {
	Action  string   `json:"action"`
	Label   string   `json:"label"`
	Engine  string   `json:"engine"`
	Source  []string `json:"source"`  // 允许的源素材类型
	Outputs []string `json:"outputs"` // 产物类型，多个表示一次出多份
	// Ability 模型引擎按能力选模型（model_price.abilities）
	Ability string `json:"ability,omitempty"`
	// TaskType / InputRole 模型引擎提交生成任务时的任务类型与素材角色
	TaskType  string `json:"-"`
	InputRole string `json:"-"`
	// Endpoint existing 引擎指向的既有接口
	Endpoint string `json:"endpoint,omitempty"`
	// Prompt 模型引擎的缺省提示词（多数后处理模型不收提示词，但网关要求非空）
	Prompt string           `json:"-"`
	Params []MediaParamSpec `json:"params,omitempty"`
}

// MediaActionView 动作 + 当前可用性（GET /user/media/actions 的元素）。
type MediaActionView struct {
	MediaAction
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"`
	Models    []string `json:"models,omitempty"`
	Free      bool     `json:"free"`
}

// mediaActions 全部动作定义（顺序即前端工具条顺序）。
func mediaActions() []MediaAction {
	return []MediaAction{
		// ===== 本地 ffmpeg（免费，立即可用）=====
		{
			Action: "crop", Label: "画面裁切", Engine: model.MediaEngineLocal,
			Source: []string{model.AssetTypeVideo, model.AssetTypeImage}, Outputs: []string{"same"},
			Params: []MediaParamSpec{
				{Key: "rect", Label: "裁切框", Type: "rect", Help: "归一化坐标，取值 0~1：{x,y,w,h}"},
				{Key: "size", Label: "输出尺寸", Type: "string", Help: "留空保持裁切后的原始尺寸；也可写 1280x720 或 720p"},
			},
		},
		{
			Action: "grid_split", Label: "宫格切分", Engine: model.MediaEngineLocal,
			Source: []string{model.AssetTypeImage}, Outputs: []string{model.AssetTypeImage},
			Params: []MediaParamSpec{
				{Key: "cells", Label: "切片区域", Type: "rects", Help: "归一化矩形数组 [{x,y,w,h}]，1~25 个，一次出同样多的图片产物，顺序与输入一致"},
				{Key: "size", Label: "输出尺寸", Type: "string", Help: "留空保持切片原始尺寸；也可写 1280x720 或 720p"},
			},
		},
		{
			Action: "montage", Label: "分镜组拼接", Engine: model.MediaEngineLocal,
			Source: []string{model.AssetTypeImage}, Outputs: []string{model.AssetTypeImage},
			Params: []MediaParamSpec{
				{Key: "asset_ids", Label: "素材列表", Type: "ints", Help: "有序的图片资产 id，2~25 张，须包含 asset_id 本身"},
				{Key: "cols", Label: "每行几张", Type: "int", Min: 1, Max: montageColsMax, Help: "留空按接近正方形排：4 张 2 列、9 张 3 列"},
				{Key: "ratio", Label: "单格画幅", Type: "enum", Default: "original", Options: montageRatios, Help: "original 按第一张图的比例；不合比例的图居中补边"},
				{Key: "gap", Label: "间距", Type: "int", Default: 8, Min: 0, Max: montageGapMax, Help: "格与格之间的像素"},
				{Key: "number", Label: "加序号", Type: "bool", Default: false, Help: "左上角画序号；服务器没有字体时静默跳过"},
				{Key: "cell", Label: "单格宽", Type: "int", Default: montageCellDefault, Min: montageCellMin, Max: montageCellMax},
				{Key: "bg", Label: "背景色", Type: "string", Default: montageBgDefault, Help: "white / black / #rrggbb"},
			},
		},
		{
			Action: "trim", Label: "片段截取", Engine: model.MediaEngineLocal,
			Source: []string{model.AssetTypeVideo, model.AssetTypeAudio}, Outputs: []string{"same"},
			Params: []MediaParamSpec{
				{Key: "start", Label: "开始秒数", Type: "float", Default: 0.0, Min: 0.0},
				{Key: "end", Label: "结束秒数", Type: "float", Min: 0.0, Help: "留空到结尾"},
				{Key: "duration", Label: "时长", Type: "float", Min: 0.3, Help: "与结束秒数二选一"},
				{Key: "accurate", Label: "精确截取", Type: "bool", Default: true, Help: "关闭后按关键帧切（快，但起止点不精确）"},
			},
		},
		{
			Action: "split", Label: "视频分割", Engine: model.MediaEngineLocal,
			Source: []string{model.AssetTypeVideo, model.AssetTypeAudio}, Outputs: []string{"same", "same"},
			Params: []MediaParamSpec{
				{Key: "at", Label: "分割点秒数", Type: "float", Min: 0.3, Help: "从这一秒切成前后两段，两段都至少 0.3 秒"},
			},
		},
		{
			Action: "extract_audio", Label: "音轨提取", Engine: model.MediaEngineLocal,
			Source: []string{model.AssetTypeVideo}, Outputs: []string{model.AssetTypeAudio},
			Params: []MediaParamSpec{
				{Key: "format", Label: "输出格式", Type: "enum", Default: "m4a", Options: []string{"m4a", "mp3"}},
			},
		},
		{
			Action: "mute_video", Label: "无声视频", Engine: model.MediaEngineLocal,
			Source: []string{model.AssetTypeVideo}, Outputs: []string{model.AssetTypeVideo},
		},
		{
			Action: "mix_bgm", Label: "混入配乐", Engine: model.MediaEngineLocal,
			Source: []string{model.AssetTypeVideo}, Outputs: []string{model.AssetTypeVideo},
			Params: []MediaParamSpec{
				{Key: "audio_asset_id", Label: "配乐素材", Type: "int", Help: "已生成的音频资产，须属于当前用户或所在团队"},
				{Key: "volume", Label: "配乐音量", Type: "float", Default: 0.2, Min: 0.05, Max: 1, Help: "相对原声的音量，缺省 0.2"},
			},
		},
		{
			Action: "split_av", Label: "音视频分离", Engine: model.MediaEngineLocal,
			Source: []string{model.AssetTypeVideo}, Outputs: []string{model.AssetTypeAudio, model.AssetTypeVideo},
			Params: []MediaParamSpec{
				{Key: "format", Label: "音轨格式", Type: "enum", Default: "m4a", Options: []string{"m4a", "mp3"}},
			},
		},
		{
			Action: "extract_frame", Label: "取帧", Engine: model.MediaEngineLocal,
			Source: []string{model.AssetTypeVideo}, Outputs: []string{model.AssetTypeImage},
			Params: []MediaParamSpec{
				{Key: "at", Label: "取哪一帧", Type: "string", Default: "first", Help: "first / last / 指定秒数"},
			},
		},
		{
			Action: "extract_frames", Label: "抽帧", Engine: model.MediaEngineLocal,
			Source: []string{model.AssetTypeVideo}, Outputs: []string{model.AssetTypeImage},
			Params: []MediaParamSpec{
				{Key: "mode", Label: "抽帧方式", Type: "enum", Default: "interval", Options: []string{"interval", "scene", "spread"},
					Help: "interval 按固定间隔，scene 按画面切换，spread 按帧数均匀铺满整段"},
				{Key: "count", Label: "帧数", Type: "int", Default: 6, Min: 1, Max: mediaFramesMax},
				{Key: "interval", Label: "间隔秒数", Type: "float", Default: 2.0, Min: 0.2, Help: "按间隔抽帧时生效"},
			},
		},
		{
			Action: "storyboard", Label: "逐帧拉片", Engine: model.MediaEngineLocal,
			Source: []string{model.AssetTypeVideo}, Outputs: []string{model.AssetTypeImage, model.AssetTypeText},
			Params: []MediaParamSpec{
				{Key: "count", Label: "帧数", Type: "int", Default: 6, Min: 1, Max: mediaFramesMax},
				{Key: "interval", Label: "间隔秒数", Type: "float", Default: 2.0, Min: 0.2},
				{Key: "analyze", Label: "生成解读", Type: "bool", Default: true, Help: "用文本视觉模型逐帧解读画面"},
			},
		},
		{
			Action: "remove_silence", Label: "去停顿", Engine: model.MediaEngineLocal,
			Source: []string{model.AssetTypeVideo, model.AssetTypeAudio}, Outputs: []string{"same"},
			Params: []MediaParamSpec{
				{Key: "threshold", Label: "静音阈值 dB", Type: "float", Default: silenceThresholdDefault, Min: -80.0, Max: -10.0, Help: "低于这个响度算停顿"},
				{Key: "min_silence", Label: "最短停顿秒数", Type: "float", Default: silenceMinDefault, Min: 0.2, Max: 10.0, Help: "短于它的停顿保留"},
			},
		},
		{
			Action: "compress", Label: "压缩", Engine: model.MediaEngineLocal,
			Source: []string{model.AssetTypeVideo, model.AssetTypeImage}, Outputs: []string{"same"},
			Params: []MediaParamSpec{
				{Key: "target_height", Label: "目标高度", Type: "int", Min: 120, Max: 2160, Help: "留空保持原分辨率"},
				{Key: "crf", Label: "画质", Type: "int", Default: 23, Min: 0, Max: 51, Help: "数值越大文件越小、画质越低"},
			},
		},

		// ===== 模型能力位（运营配了对应能力的模型即可用）=====
		{
			Action: "upscale", Label: "高清", Engine: model.MediaEngineModel,
			Ability: model.AbilityUpscale, TaskType: model.TaskTypeVideo, InputRole: "reference_video", Prompt: "超分",
			Source: []string{model.AssetTypeVideo, model.AssetTypeImage}, Outputs: []string{"same"},
			Params: []MediaParamSpec{
				{Key: "model", Label: "模型", Type: "string", Help: "留空自动选第一个可用的超分模型"},
				{Key: "resolution", Label: "目标分辨率", Type: "string"},
				{Key: "scale", Label: "放大倍数", Type: "float", Min: 1.0, Max: 4.0},
			},
		},
		{
			Action: "remove_subtitle", Label: "智能去字幕", Engine: model.MediaEngineModel,
			Ability: model.AbilityRemoveSubtitle, TaskType: model.TaskTypeVideo, InputRole: "reference_video", Prompt: "去字幕",
			Source: []string{model.AssetTypeVideo}, Outputs: []string{model.AssetTypeVideo},
			Params: []MediaParamSpec{
				{Key: "model", Label: "模型", Type: "string"},
				{Key: "rect", Label: "字幕区域", Type: "rect", Help: "框选字幕所在区域，留空由模型自行识别"},
				{Key: "mode", Label: "处理方式", Type: "enum", Options: []string{"inpaint", "crop"}},
			},
		},
		{
			Action: "vocal_separation", Label: "人声提取", Engine: model.MediaEngineModel,
			Ability: model.AbilityVocalSeparation, TaskType: model.TaskTypeAudio, InputRole: "reference_audio", Prompt: "人声分离",
			Source: []string{model.AssetTypeVideo, model.AssetTypeAudio}, Outputs: []string{model.AssetTypeAudio},
			Params: []MediaParamSpec{{Key: "model", Label: "模型", Type: "string"}},
		},
		{
			Action: "bgm_separation", Label: "背景音提取", Engine: model.MediaEngineModel,
			Ability: model.AbilityBgmSeparation, TaskType: model.TaskTypeAudio, InputRole: "reference_audio", Prompt: "伴奏分离",
			Source: []string{model.AssetTypeVideo, model.AssetTypeAudio}, Outputs: []string{model.AssetTypeAudio},
			Params: []MediaParamSpec{{Key: "model", Label: "模型", Type: "string"}},
		},
		{
			Action: "matting", Label: "智能抠像", Engine: model.MediaEngineModel,
			Ability: model.AbilityMatting, TaskType: model.TaskTypeVideo, InputRole: "reference_video", Prompt: "抠像",
			Source: []string{model.AssetTypeVideo, model.AssetTypeImage}, Outputs: []string{model.AssetTypeVideo},
			Params: []MediaParamSpec{{Key: "model", Label: "模型", Type: "string"}},
		},
		{
			Action: "subject_remove", Label: "主体消除", Engine: model.MediaEngineModel,
			Ability: model.AbilitySubjectRemove, TaskType: model.TaskTypeVideo, InputRole: "reference_video", Prompt: "消除主体",
			Source: []string{model.AssetTypeVideo, model.AssetTypeImage}, Outputs: []string{"same"},
			Params: []MediaParamSpec{
				{Key: "model", Label: "模型", Type: "string"},
				{Key: "rect", Label: "主体区域", Type: "rect"},
				{Key: "prompt", Label: "补充说明", Type: "string"},
			},
		},
		{
			Action: "subject_modify", Label: "主体修改", Engine: model.MediaEngineModel,
			Ability: model.AbilitySubjectModify, TaskType: model.TaskTypeVideo, InputRole: "reference_video",
			Source: []string{model.AssetTypeVideo, model.AssetTypeImage}, Outputs: []string{"same"},
			Params: []MediaParamSpec{
				{Key: "model", Label: "模型", Type: "string"},
				{Key: "rect", Label: "主体区域", Type: "rect"},
				{Key: "prompt", Label: "改成什么", Type: "string"},
			},
		},
		{
			Action: "subject_replace", Label: "主体替换", Engine: model.MediaEngineModel,
			Ability: model.AbilitySubjectReplace, TaskType: model.TaskTypeVideo, InputRole: "reference_video",
			Source: []string{model.AssetTypeVideo, model.AssetTypeImage}, Outputs: []string{"same"},
			Params: []MediaParamSpec{
				{Key: "model", Label: "模型", Type: "string"},
				{Key: "rect", Label: "主体区域", Type: "rect"},
				{Key: "prompt", Label: "替换成什么", Type: "string"},
			},
		},
		{
			Action: "motion_capture", Label: "深度动作捕捉", Engine: model.MediaEngineModel,
			Ability: model.AbilityMotionCapture, TaskType: model.TaskTypeVideo, InputRole: "reference_video", Prompt: "动作捕捉",
			Source: []string{model.AssetTypeVideo}, Outputs: []string{model.AssetTypeVideo},
			Params: []MediaParamSpec{{Key: "model", Label: "模型", Type: "string"}},
		},
		{
			Action: "depth_extract", Label: "深度提取", Engine: model.MediaEngineModel,
			Ability: model.AbilityDepthExtract, TaskType: model.TaskTypeVideo, InputRole: "reference_video",
			Prompt: "提取深度图，近处亮、远处暗，轮廓清晰，用于后续三维或景深。",
			Source: []string{model.AssetTypeVideo, model.AssetTypeImage}, Outputs: []string{"same"},
			Params: []MediaParamSpec{
				{Key: "model", Label: "模型", Type: "string", Help: "留空自动选第一个可用的深度提取模型"},
				{Key: "resolution", Label: "目标分辨率", Type: "enum", Options: []string{"480p", "720p"}},
			},
		},

		{
			// 语音转字幕：模型能力位挑模型，但不走生成任务的提交路径——本地 worker 抽音轨后直接调网关转写，
			// 计费在创建时按源时长预扣（见 media_transcribe.go）。
			Action: "transcribe", Label: "语音转字幕", Engine: model.MediaEngineModel,
			Ability: model.AbilityTranscribe, TaskType: model.TaskTypeAudio, InputRole: "reference_audio", Prompt: "语音转写",
			Source: []string{model.AssetTypeVideo, model.AssetTypeAudio}, Outputs: []string{model.AssetTypeText},
			Params: []MediaParamSpec{
				{Key: "model", Label: "模型", Type: "string", Help: "留空自动选第一个可用的转写模型"},
				{Key: "language", Label: "语种", Type: "enum", Default: "auto", Options: transcribeLanguages, Help: "auto 由模型自动识别"},
				{Key: "diarize", Label: "区分说话人", Type: "bool", Default: false, Help: "只有 diarize 模型生效，字幕前带「说话人 N：」"},
			},
		},

		// ===== 复合动作（生成任务 + 本地拼接）=====
		{
			Action: "retake_segment", Label: "片段重拍", Engine: model.MediaEnginePipeline,
			Source: []string{model.AssetTypeVideo}, Outputs: []string{model.AssetTypeVideo, model.AssetTypeVideo},
			Params: []MediaParamSpec{
				{Key: "start", Label: "开始秒数", Type: "float", Min: 0.0},
				{Key: "end", Label: "结束秒数", Type: "float", Min: 0.0, Help: "段长至少 0.5 秒"},
				{Key: "start_ratio", Label: "开始比例", Type: "float", Min: 0.0, Max: 1.0, Help: "读不到时长时用 0~1 比例代替秒数，服务端按探到的时长换算"},
				{Key: "end_ratio", Label: "结束比例", Type: "float", Min: 0.0, Max: 1.0},
				{Key: "prompt", Label: "这段拍成什么", Type: "string", Help: "留空沿用源任务的提示词"},
				{Key: "model", Label: "视频模型", Type: "string", Help: "留空按成片默认模型 → 源任务模型 → 清单第一个"},
				{Key: "keep_audio", Label: "保留原声", Type: "bool", Default: true, Help: "关闭后重拍段用新片段自带的声音"},
			},
		},

		// ===== 已有接口（清单里指路，不再造一套）=====
		{
			Action: "retake", Label: "整段重拍", Engine: MediaEngineExisting,
			Endpoint: "POST /user/task/:id/retake", Source: []string{model.AssetTypeVideo}, Outputs: []string{model.AssetTypeVideo},
			Params: []MediaParamSpec{
				{Key: "prompt", Label: "新的提示词", Type: "string", Help: "留空沿用原任务的提示词"},
				{Key: "model", Label: "模型", Type: "string", Help: "留空沿用原任务的模型"},
			},
		},
		{
			Action: "extend", Label: "智能续写", Engine: MediaEngineExisting,
			Endpoint: "POST /user/task/:id/extend", Source: []string{model.AssetTypeVideo}, Outputs: []string{model.AssetTypeVideo},
			Params: []MediaParamSpec{
				{Key: "prompt", Label: "提示词", Type: "string"},
				{Key: "seconds", Label: "秒数", Type: "int", Min: 3, Max: 15},
			},
		},
		{
			Action: "regenerate", Label: "重新生成", Engine: MediaEngineExisting,
			Endpoint: "POST /user/task/:id/retry", Source: []string{model.AssetTypeVideo, model.AssetTypeImage, model.AssetTypeAudio},
			Outputs: []string{"same"},
		},
		{
			Action: "compose", Label: "合成一条", Engine: MediaEngineExisting,
			Endpoint: "POST /user/canvas/:id/compose", Source: []string{model.AssetTypeVideo}, Outputs: []string{model.AssetTypeVideo},
			Params: []MediaParamSpec{
				{Key: "clips", Label: "视频列表", Type: "ints", Help: "已出片的视频资产，2~24 段"},
			},
		},
	}
}

// FindMediaAction 按标识取动作定义。
func FindMediaAction(name string) *MediaAction {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, a := range mediaActions() {
		if a.Action == name {
			return &a
		}
	}
	return nil
}

const (
	MediaGapDisabled     = "disabled"
	MediaGapNoFFmpeg     = "no_ffmpeg"
	MediaGapNoModel      = "no_model"
	MediaGapNotOnGateway = "not_on_gateway"
)

// AdminMediaActionView 管理端盘点：用户侧可用性 + 缺模型 / 网关未接的原因。
type AdminMediaActionView struct {
	MediaActionView
	PricedModels []string `json:"priced_models,omitempty"`
	Gap          string   `json:"gap,omitempty"`
}

// MediaActions 当前动作清单与可用性（GET /user/media/actions）。
func MediaActions() []MediaActionView {
	disabled := setting.MediaDisabledActions()
	prices, _ := model.GetModelPrices(true)
	ffmpeg := FFmpegAvailable()
	out := make([]MediaActionView, 0, len(mediaActions()))
	for _, a := range mediaActions() {
		v, _, _ := fillMediaActionView(a, disabled, prices, ffmpeg)
		out = append(out, v)
	}
	return out
}

// AdminMediaActions 管理端盘点（GET /admin/media/actions）：开关、ffmpeg、能力模型、网关是否已接。
func AdminMediaActions() ([]AdminMediaActionView, bool) {
	disabled := setting.MediaDisabledActions()
	prices, _ := model.GetModelPrices(true)
	ffmpeg := FFmpegAvailable()
	out := make([]AdminMediaActionView, 0, len(mediaActions()))
	for _, a := range mediaActions() {
		v, priced, gap := fillMediaActionView(a, disabled, prices, ffmpeg)
		out = append(out, AdminMediaActionView{MediaActionView: v, PricedModels: priced, Gap: gap})
	}
	return out, ffmpeg
}

func fillMediaActionView(a MediaAction, disabled map[string]bool, prices []*model.ModelPrice, ffmpeg bool) (MediaActionView, []string, string) {
	v := MediaActionView{MediaAction: a, Available: true, Free: a.Engine == model.MediaEngineLocal}
	var priced []string
	switch {
	case disabled[a.Action]:
		v.Available, v.Reason = false, "该功能已被关闭"
		return v, nil, MediaGapDisabled
	case a.Engine == model.MediaEngineLocal && !ffmpeg:
		v.Available, v.Reason = false, "服务器未安装 ffmpeg"
		return v, nil, MediaGapNoFFmpeg
	case a.Engine == model.MediaEngineModel:
		priced = pickPricedModelsByAbility(prices, a.Ability)
		v.Models = pickModelsByAbility(prices, a.Ability)
		if len(v.Models) == 0 {
			v.Available, v.Reason = false, fmt.Sprintf("暂未接入%s模型，请联系运营", a.Label)
			if len(priced) > 0 {
				return v, priced, MediaGapNotOnGateway
			}
			return v, priced, MediaGapNoModel
		}
		if a.Action == "transcribe" && !ffmpeg {
			v.Available, v.Reason = false, "服务器未安装 ffmpeg"
			return v, priced, MediaGapNoFFmpeg
		}
	case a.Engine == model.MediaEnginePipeline:
		priced = videoPricedModelNames(prices)
		v.Models = videoModelNames(prices)
		switch {
		case !ffmpeg:
			v.Available, v.Reason = false, "服务器未安装 ffmpeg"
			return v, priced, MediaGapNoFFmpeg
		case len(v.Models) == 0:
			v.Available, v.Reason = false, "暂无可用的视频模型，请联系运营"
			if len(priced) > 0 {
				return v, priced, MediaGapNotOnGateway
			}
			return v, priced, MediaGapNoModel
		}
	}
	return v, priced, ""
}

// ModelHasAbility 指定模型是否具备该能力（声明优先，否则按模型名推断）。
func ModelHasAbility(modelName, ability string) bool {
	if strings.TrimSpace(modelName) == "" || strings.TrimSpace(ability) == "" {
		return false
	}
	if p, err := model.GetModelPrice(modelName); err == nil {
		return priceHasAbility(p, ability)
	}
	return false
}

// PickModelsByAbility 已定价、已接入且具备该能力的模型。
func PickModelsByAbility(ability string) []string {
	prices, _ := model.GetModelPrices(true)
	return pickModelsByAbility(prices, ability)
}

// pickPricedModelsByAbility 已定价且具备该能力（不管网关有没有这个模型）。
func pickPricedModelsByAbility(prices []*model.ModelPrice, ability string) []string {
	if strings.TrimSpace(ability) == "" {
		return nil
	}
	out := []string{}
	for _, p := range prices {
		if priceHasAbility(p, ability) {
			out = append(out, p.Model)
		}
	}
	sort.Strings(out)
	return out
}

// pickModelsByAbility 从一批定价里挑出具备该能力、且网关已接入的模型。
func pickModelsByAbility(prices []*model.ModelPrice, ability string) []string {
	out := []string{}
	for _, name := range pickPricedModelsByAbility(prices, ability) {
		p := findPrice(prices, name)
		if p == nil || FindProvider(p.Type, p.Model) == nil {
			continue
		}
		out = append(out, name)
	}
	return out
}

func findPrice(prices []*model.ModelPrice, name string) *model.ModelPrice {
	for _, p := range prices {
		if p != nil && p.Model == name {
			return p
		}
	}
	return nil
}

// videoPricedModelNames 已定价的视频模型（不管网关）。
func videoPricedModelNames(prices []*model.ModelPrice) []string {
	out := []string{}
	for _, p := range prices {
		if p.Type == model.TaskTypeVideo {
			out = append(out, p.Model)
		}
	}
	sort.Strings(out)
	return out
}

// videoModelNames 已定价、已接入的视频模型。
func videoModelNames(prices []*model.ModelPrice) []string {
	out := []string{}
	for _, p := range prices {
		if p.Type == model.TaskTypeVideo && FindProvider(p.Type, p.Model) != nil {
			out = append(out, p.Model)
		}
	}
	return out
}

func priceHasAbility(p *model.ModelPrice, ability string) bool {
	return p != nil && p.HasAbility(ability)
}

// PickModelByAbility 取一个可用的能力模型；preferType 非空时优先该任务类型。
func PickModelByAbility(ability, preferType string) string {
	names := PickModelsByAbility(ability)
	if len(names) == 0 {
		return ""
	}
	if preferType != "" {
		prices, _ := model.GetModelPrices(true)
		byModel := map[string]*model.ModelPrice{}
		for _, p := range prices {
			byModel[p.Model] = p
		}
		for _, n := range names {
			if p := byModel[n]; p != nil && p.Type == preferType {
				return n
			}
		}
	}
	return names[0]
}

// SourceTypeAllowed 源素材类型是否被该动作接受。
func (a *MediaAction) SourceTypeAllowed(assetType string) bool {
	if len(a.Source) == 0 {
		return true
	}
	for _, t := range a.Source {
		if t == assetType {
			return true
		}
	}
	return false
}

// OutputType 动作的主产物类型（模型引擎把任务结果映射成产物时用）。
func (a *MediaAction) OutputType(srcType string) string {
	for _, t := range a.Outputs {
		if t == "same" {
			return srcType
		}
		return t
	}
	return srcType
}

// ---- 参数读取 ----

func mediaParamStr(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	switch v := params[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func mediaParamFloat(params map[string]any, key string, def float64) float64 {
	if params == nil {
		return def
	}
	switch v := params[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%f", &f); err == nil {
			return f
		}
	}
	return def
}

func mediaParamBool(params map[string]any, key string, def bool) bool {
	if params == nil {
		return def
	}
	switch v := params[key].(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		if s == "" {
			return def
		}
		return s == "true" || s == "1" || s == "yes"
	case float64:
		return v != 0
	}
	return def
}

// mediaRect 归一化裁切框（0~1），非法返回零值。
type mediaRect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Valid 是否是一个能用的裁切框。
func (r mediaRect) Valid() bool {
	return r.W > 0 && r.H > 0 && r.W <= 1 && r.H <= 1 && r.X >= 0 && r.Y >= 0 && r.X+r.W <= 1.001 && r.Y+r.H <= 1.001
}

// parseMediaRect 解析 params.rect（对象或 JSON 字符串）。
func parseMediaRect(params map[string]any) (mediaRect, bool) {
	if params == nil {
		return mediaRect{}, false
	}
	raw := params["rect"]
	r := mediaRect{}
	switch v := raw.(type) {
	case map[string]any:
		r = mediaRect{X: mediaParamFloat(v, "x", 0), Y: mediaParamFloat(v, "y", 0), W: mediaParamFloat(v, "w", 0), H: mediaParamFloat(v, "h", 0)}
	case string:
		if strings.TrimSpace(v) == "" {
			return mediaRect{}, false
		}
		if err := json.Unmarshal([]byte(v), &r); err != nil {
			return mediaRect{}, false
		}
	default:
		return mediaRect{}, false
	}
	return r, r.Valid()
}
