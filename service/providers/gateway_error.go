package providers

import (
	"errors"
	"fmt"
	"strings"

	"easytv-api/common"
)

// 网关报错的人话映射。
//
// 原来任务失败后展示给创作者的是原始响应，比如
//   网关返回 502：{"code":502,"msg":"cloudsway returned HTTP 500: Internal server error, please try again later","data":{}}
// 创作者读不出该怎么办。这里把已知情况翻成一句能照着做的话，原文照旧写日志，排查不受影响。

// friendlyGatewayError 把 HTTP 状态与网关消息翻成给创作者看的一句话。
// 返回空串表示没有对应规则，调用方保留原文。
func friendlyGatewayError(status int, msg string) string {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "unsupported duration"):
		return "该模型不支持这个时长，请在时长里换一档（可让运营在后台给模型填上支持的秒数）"
	case strings.Contains(low, "unsupported resolution"):
		return "该模型不支持这个分辨率，请换一档（可让运营在后台给模型填上支持的分辨率）"
	case strings.Contains(low, "inpaint image must match"):
		return "该模型要求首帧图与输出尺寸完全一致，请让运营在后台为它打开「首帧尺寸需与输出一致」"
	case strings.Contains(low, "image format is invalid"):
		// 实测 Kling 系列带参考视频：网关把视频塞进上游的图片参考字段，上游回这句
		return "这个模型只接受图片参考，收不了视频或音频素材；请把参考换成图片，或换一个支持参考视频的模型"
	case strings.Contains(low, "content") && (strings.Contains(low, "policy") || strings.Contains(low, "moderation")):
		return "提示词或参考图被模型的内容审核拒绝，改一下描述再试"
	case strings.Contains(low, "risk control"):
		// 实测 Kling 系列：上游风控拦下（多在真人 / 名人参考图、敏感描述），异步失败回这句
		return "提示词或参考图没通过模型的风控，换张参考图或改一下描述再试"
	case status == 404 && strings.Contains(low, "deployment for this resource does not exist"):
		// 实测语音转写：定价表里有模型、网关令牌却没开通这个模型，上游回这句
		return "网关还没有给这个模型开通服务，请联系运营在网关侧开通或换一个模型"
	}
	switch status {
	case 401, 403:
		return "网关鉴权失败，请联系运营检查网关令牌"
	case 402:
		return "网关额度不足，请联系运营充值后重试"
	case 429:
		return "模型当前排队较多，稍等一会儿再试"
	}
	if status >= 500 {
		return "模型服务临时不可用，积分已全额退回，稍后重试或换一个模型"
	}
	return ""
}

// gatewayStatusError 网关非 2xx 响应；保留 HTTP 状态码，轮询据此区分「查不到」与临时错误。
type gatewayStatusError struct {
	Status int
	msg    string
}

func (e *gatewayStatusError) Error() string { return e.msg }

// gatewayCallError 构造对外错误：能翻的翻成人话并把原文写日志，翻不了的保留原样。
func gatewayCallError(status int, path, msg string) error {
	if friendly := friendlyGatewayError(status, msg); friendly != "" {
		common.SysLog(fmt.Sprintf("gateway %d %s: %s", status, path, truncateForLog(msg)))
		return &gatewayStatusError{Status: status, msg: friendly}
	}
	return &gatewayStatusError{Status: status, msg: fmt.Sprintf("网关返回 %d：%s", status, msg)}
}

// isGatewayNotFound 网关明确回 404（任务 ID 不存在）。
func isGatewayNotFound(err error) bool {
	var ge *gatewayStatusError
	return errors.As(err, &ge) && ge.Status == 404
}

func truncateForLog(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

// FriendlyGatewayErrorForTest 暴露给测试用（包外不应依赖）。
func FriendlyGatewayErrorForTest(status int, msg string) string {
	return friendlyGatewayError(status, msg)
}
