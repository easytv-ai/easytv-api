package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"easytv-api/model"
	"easytv-api/setting"
)

// 导演台 AI 助手：单轮文本任务，和画布对话共用每日免费次数。
// 不能走 POST /user/task：客户端不许自报 params.agent / params.chat；
// 也不能走 POST /user/canvas/:id/agent：那是多轮 apply_graph，会把导演指令当成工作流计划。

const (
	directorChatKind      = "director"
	directorChatPromptMax = 20000
)

// NewDirectorChatTask 组装导演助手文本任务（未入库）。
// free 按创建那一刻的 ChatFreeEnabled 写入 params.chat，之后只认落库标记。
func NewDirectorChatTask(userId int, modelName, prompt string) (*model.GenerationTask, GenerationProvider, error) {
	if !setting.IsAgentEnabled() {
		return nil, nil, errors.New("对话功能暂未开启")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, nil, errors.New("请输入问题")
	}
	if len([]rune(prompt)) > directorChatPromptMax {
		return nil, nil, fmt.Errorf("问题太长了，最多 %d 字", directorChatPromptMax)
	}
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		modelName = PickDefaultModel(model.TaskTypeText)
	}
	if modelName == "" {
		return nil, nil, errors.New("暂无可用的文本模型")
	}
	provider := FindProvider(model.TaskTypeText, modelName)
	if provider == nil {
		return nil, nil, fmt.Errorf("模型 %s 暂未接入", modelName)
	}
	raw, err := json.Marshal(map[string]any{
		"chat": map[string]any{
			"kind": directorChatKind,
			"free": setting.IsChatFreeEnabled(),
		},
	})
	if err != nil {
		return nil, nil, err
	}
	return &model.GenerationTask{
		UserId:   userId,
		Type:     model.TaskTypeText,
		Provider: provider.Name(),
		Model:    modelName,
		Prompt:   prompt,
		Params:   string(raw),
		Inputs:   "[]",
	}, provider, nil
}

func directorChatKindOf(params string) string {
	var p struct {
		Chat *struct {
			Kind string `json:"kind"`
		} `json:"chat"`
	}
	if json.Unmarshal([]byte(params), &p) != nil || p.Chat == nil {
		return ""
	}
	return strings.TrimSpace(p.Chat.Kind)
}

func directorChatFree(task *model.GenerationTask) bool {
	if task == nil {
		return false
	}
	var p struct {
		Chat *struct {
			Kind string `json:"kind"`
			Free bool   `json:"free"`
		} `json:"chat"`
	}
	if json.Unmarshal([]byte(task.Params), &p) != nil || p.Chat == nil {
		return false
	}
	return p.Chat.Kind == directorChatKind && p.Chat.Free
}
