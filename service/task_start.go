package service

import (
	"errors"
	"fmt"

	"easytv-api/model"
)

// ErrTaskModel 入库 / 扣分时的数据库错误，控制器按 respondModelError 处理（Msg 是兜底文案）。
type ErrTaskModel struct {
	Err error
	Msg string
}

func (e *ErrTaskModel) Error() string { return e.Msg + "：" + e.Err.Error() }
func (e *ErrTaskModel) Unwrap() error { return e.Err }

// StartTask 创建并提交一个生成任务：并发校验 → 存储空间 → 估价与参数能力校验 → 入库 → 预扣积分 → 提交网关。
// 网关同步报错时任务标失败并退分，返回的 task 状态为 failed、err 为 nil（与接口「创建成功但任务失败」的语义一致）。
// 创建接口与服务端批量编排（API.md 13）共用。task.TeamId 由调用方在创建前确定（API.md 20）。
func StartTask(task *model.GenerationTask, provider GenerationProvider) (*model.GenerationTask, error) {
	if err := CanUseTeamPool(task.UserId, task.TeamId); err != nil {
		return nil, err
	}
	// 团队空间：团队版会员失效就别入库了，直接告诉用户切回个人空间或续费
	if _, err := PayerForTeam(task.UserId, task.TeamId); err != nil {
		return nil, err
	}
	if err := CheckConcurrency(task.UserId, task.TeamId); err != nil {
		return nil, err
	}
	// 免积分对话（API.md 32 / 契约第 4 条）：不估价、不校验模型参数、不预扣积分，
	// 改为原子消费一次每日对话次数；次数用完时不建任务、不提交网关。
	if IsFreeChatTask(task) {
		return startFreeChatTask(task, provider)
	}
	// 生成产物都进资产库，空间已满就别再花积分生成了
	if task.Type != model.TaskTypeText {
		if err := CheckStorage(task.UserId, task.TeamId, 0); err != nil {
			return nil, err
		}
	}
	// 重试沿用旧任务参数，老数据里可能带 count>1：只会产出一份，就只按一份计费
	if TaskResultCount(task.Params) > 1 {
		task.Params = WithSingleCount(task.Params)
	}
	isMember := IsMemberForPricing(task.UserId, task.TeamId)
	points, price, err := EstimateTaskPoints(task.Type, task.Model, task.Params, isMember)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, fmt.Errorf("模型 %s 未定价，请联系运营", task.Model)
		}
		return nil, err
	}
	// 模型声明了支持的时长 / 分辨率就先挡下来，别让网关回 400 再退款
	if err := CheckModelParams(price, task.Params); err != nil {
		return nil, err
	}
	// sora 系要求首帧图与输出尺寸完全一致，提交前裁一张同尺寸的副本
	task.Inputs = FitTaskFirstFrame(task.UserId, price, task.Inputs, task.Params)
	if err := task.Insert(); err != nil {
		return nil, &ErrTaskModel{Err: err, Msg: "创建任务失败"}
	}
	TrackOnce(task.UserId, model.TrackEventFirstTask, map[string]any{"type": task.Type, "model": task.Model})
	if err := FreezeTaskPoints(task, points); err != nil {
		_ = model.SetTaskFailed(task.Id, err.Error())
		var insufficient model.ErrInsufficientPoints
		if errors.As(err, &insufficient) {
			return nil, err
		}
		return nil, &ErrTaskModel{Err: err, Msg: "积分扣减失败"}
	}
	task.PointsFrozen = points
	if err := provider.Submit(task); err != nil {
		_ = FailTask(task.Id, err.Error())
		task.Status = model.TaskStatusFailed
		task.ErrorMsg = err.Error()
		task.PointsFrozen = 0
	}
	return task, nil
}

// startFreeChatTask 免积分对话任务的创建：先原子消费一次每日次数（会员 / 不限次配置不写计数），
// 再入库、提交网关。次数用完（ErrChatQuotaExceeded）直接返回给上层，不建任务、不提交网关；
// 入库失败要把这一次退回去，别让用户白掉一次。全程不预扣积分、不写 point_log。
func startFreeChatTask(task *model.GenerationTask, provider GenerationProvider) (*model.GenerationTask, error) {
	quota, err := ConsumeChatQuota(task.UserId, task.TeamId)
	if err != nil {
		return nil, err
	}
	task.PointsFrozen = 0
	// 把「消费了哪天」落进 params：会员 / 不限次没消费就写空串，失败 / 取消时按这个标记决定退不退
	if quota.Unlimited {
		task.Params = WriteChatQuotaDay(task.Params, "")
	} else {
		task.Params = WriteChatQuotaDay(task.Params, quota.Date)
	}
	if err := task.Insert(); err != nil {
		// 退消费日（消费时返回的日期），不是退款时刻的日期；没消费过就没得退
		if !quota.Unlimited {
			_ = RefundChatQuotaOn(task.UserId, quota.Date)
		}
		return nil, &ErrTaskModel{Err: err, Msg: "创建任务失败"}
	}
	TrackOnce(task.UserId, model.TrackEventFirstTask, map[string]any{"type": task.Type, "model": task.Model})
	if err := provider.Submit(task); err != nil {
		// 同步失败：FailTask 会置失败并退还这一次次数（免积分任务没有积分可退）
		_ = FailTask(task.Id, err.Error())
		task.Status = model.TaskStatusFailed
		task.ErrorMsg = err.Error()
		task.PointsFrozen = 0
	}
	return task, nil
}
