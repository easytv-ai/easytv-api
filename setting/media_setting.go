package setting

import (
	"fmt"
	"strings"
)

// 媒体工具配置（API.md 23，DB setting 表，PascalCase 键，管理端 PUT /admin/setting/:key 热更新）。
const (
	// FFmpegPath ffmpeg 可执行文件路径；空=用 PATH 里的 ffmpeg（也可用环境变量 FFMPEG_PATH）。
	FFmpegPath = "FFmpegPath"
	// FFprobePath ffprobe 可执行文件路径；空=用 PATH 里的 ffprobe（也可用环境变量 FFPROBE_PATH）。
	FFprobePath = "FFprobePath"
	// MediaActionsDisabled 禁用的媒体动作（逗号分隔，如 "storyboard,compress"）；空=全开。
	// 服务器没装字体、存储吃紧时，运营可以单独关掉耗资源的动作，不必发版。
	MediaActionsDisabled = "MediaActionsDisabled"
	// MediaJobMaxQueue 媒体作业全局队列上限，缺省 20。
	MediaJobMaxQueue = "MediaJobMaxQueue"
	// MediaJobMaxPerUser 单用户排队 + 处理中的作业上限，缺省 3。
	MediaJobMaxPerUser = "MediaJobMaxPerUser"
	// MediaStoryboardModel 逐帧拉片解读用的文本视觉模型；空=取第一个可用的文本模型。
	MediaStoryboardModel = "MediaStoryboardModel"
	// TranscribeModel 语音转字幕用的转写模型（API.md 35）；空=按 transcribe 能力位取第一个可用的。
	TranscribeModel = "TranscribeModel"
)

// GetTranscribeModel 运营指定的转写模型；空串表示按能力位自动挑。
func GetTranscribeModel() string {
	return strings.TrimSpace(GetSettingString(TranscribeModel))
}

// GetFFmpegPath 返回 ffmpeg 可执行文件；缺省交给 PATH 解析。
func GetFFmpegPath() string {
	if v := strings.TrimSpace(GetSettingString(FFmpegPath)); v != "" {
		return v
	}
	return "ffmpeg"
}

// GetFFprobePath 返回 ffprobe 可执行文件；缺省交给 PATH 解析。
func GetFFprobePath() string {
	if v := strings.TrimSpace(GetSettingString(FFprobePath)); v != "" {
		return v
	}
	return "ffprobe"
}

// MediaDisabledActions 被运营关掉的媒体动作集合（小写）。
func MediaDisabledActions() map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(GetSettingString(MediaActionsDisabled), ",") {
		if v := strings.ToLower(strings.TrimSpace(part)); v != "" {
			out[v] = true
		}
	}
	return out
}

// IsMediaActionDisabled 该动作是否被运营关闭。
func IsMediaActionDisabled(action string) bool {
	return MediaDisabledActions()[strings.ToLower(strings.TrimSpace(action))]
}

// MediaQueueLimits 媒体作业队列上限：全局与单用户（缺省 20 / 3）。
func MediaQueueLimits() (global, perUser int) {
	return getSettingInt(MediaJobMaxQueue, 20), getSettingInt(MediaJobMaxPerUser, 3)
}

// GetMediaStoryboardModel 逐帧拉片用的文本模型：MediaStoryboardModel 单独指定优先，否则用运营配的看图模型 VisionTextModel；都空=自动挑。
func GetMediaStoryboardModel() string {
	if v := strings.TrimSpace(GetSettingString(MediaStoryboardModel)); v != "" {
		return v
	}
	return strings.TrimSpace(GetSettingString(VisionTextModel))
}

func getSettingInt(key string, def int) int {
	v := strings.TrimSpace(GetSettingString(key))
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return def
	}
	return n
}
