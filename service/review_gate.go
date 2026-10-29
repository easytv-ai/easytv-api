package service

import (
	"fmt"
	"strings"

	"easytv-api/model"
)

// 审批门禁（API.md 26.1，复审 G05）：先审低成本的分镜图，再放行高成本的视频生成。
//
// 门禁存在画布时间线里（timeline.review_gate）：
//   - ""（关闭）：不拦；
//   - rejected：输入素材里有被退回的图就拦；
//   - approved：视频的首帧图（分镜图）必须已通过审片才放行，退回的同样拦。
// 单个生成（POST /user/task）与批量生成都走同一个检查；被拦的批量条目标 blocked，不影响已通过的镜头。

const (
	ReviewGateOff      = ""
	ReviewGateRejected = "rejected"
	ReviewGateApproved = "approved"
)

// NormalizeReviewGate 非法值当关闭。
func NormalizeReviewGate(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case ReviewGateRejected:
		return ReviewGateRejected
	case ReviewGateApproved:
		return ReviewGateApproved
	}
	return ReviewGateOff
}

// CanvasReviewGate 画布当前的门禁设置（没保存过时间线 = 关闭）。
func CanvasReviewGate(canvasId int) string {
	_, tl, err := model.GetCanvasTimeline(canvasId)
	if err != nil || tl == nil {
		return ReviewGateOff
	}
	return NormalizeReviewGate(tl.ReviewGate)
}

// GateInput 门禁要看的输入：素材 id 与角色。
type GateInput struct {
	AssetId int
	Role    string
}

// ErrReviewGate 被门禁拦下。
type ErrReviewGate struct{ Msg string }

func (e *ErrReviewGate) Error() string { return e.Msg }

// ReviewGateCheck 按门禁校验一次视频生成的输入（纯函数版：reviews 由调用方给）。
func ReviewGateCheck(gate string, inputs []GateInput, reviews map[int]*model.ShotReview) error {
	gate = NormalizeReviewGate(gate)
	if gate == ReviewGateOff || len(inputs) == 0 {
		return nil
	}
	for _, in := range inputs {
		r := reviews[in.AssetId]
		if r != nil && r.Status == model.ShotReviewRejected {
			reason := reviewReasonLabel(r.Reason)
			if reason != "" {
				reason = "（" + reason + "）"
			}
			return &ErrReviewGate{Msg: fmt.Sprintf("审批门禁：素材 %d 已被退回%s，先重新生成或撤销退回再生成视频", in.AssetId, reason)}
		}
		if gate == ReviewGateApproved && in.Role == "first_frame" && (r == nil || r.Status != model.ShotReviewApproved) {
			return &ErrReviewGate{Msg: fmt.Sprintf("审批门禁：分镜图（素材 %d）还没通过审片，通过后再生成视频", in.AssetId)}
		}
	}
	return nil
}

// ReviewGateCheckCanvas 读画布门禁与审片结论后校验；只对视频任务生效。
func ReviewGateCheckCanvas(canvasId int, taskType string, inputs []GateInput) error {
	if canvasId <= 0 || taskType != model.TaskTypeVideo {
		return nil
	}
	gate := CanvasReviewGate(canvasId)
	if gate == ReviewGateOff {
		return nil
	}
	reviews, err := model.GetShotReviews(canvasId)
	if err != nil {
		return nil // 读不到结论不拦，别把生成卡死
	}
	return ReviewGateCheck(gate, inputs, reviews)
}

var reviewReasonLabels = map[string]string{
	"identity": "人物不对", "performance": "表演", "lipsync": "口型", "composition": "构图",
	"continuity": "连戏", "duration": "时长", "technical": "技术故障",
}

func reviewReasonLabel(reason string) string {
	return reviewReasonLabels[reason]
}
