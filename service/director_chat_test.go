package service

import (
	"encoding/json"
	"strings"
	"testing"

	"easytv-api/model"
	"easytv-api/setting"
)

func registerDirectorChatProvider(t *testing.T) *chatTestProvider {
	t.Helper()
	p := &chatTestProvider{}
	RegisterProvider(p)
	t.Cleanup(func() { RemoveProvider(p.Name()) })
	return p
}

func TestNewDirectorChatTaskWritesFreeFlag(t *testing.T) {
	registerDirectorChatProvider(t)
	seedChatSetting(t, setting.AgentEnabled, "true")
	seedChatSetting(t, setting.ChatFreeEnabled, "true")

	task, provider, err := NewDirectorChatTask(8, "gpt-4o-mini", "加一个角色放到右边")
	if err != nil {
		t.Fatalf("NewDirectorChatTask: %v", err)
	}
	if provider == nil || task == nil {
		t.Fatal("应返回任务和 provider")
	}
	if task.Type != model.TaskTypeText || task.Prompt != "加一个角色放到右边" {
		t.Fatalf("任务字段不对：%+v", task)
	}
	if IsAgentTask(task) {
		t.Fatalf("导演对话不能被认成画布 agent，否则会跑 apply_graph：%s", task.Params)
	}
	if !IsFreeChatTask(task) {
		t.Fatalf("开关开启时应落 free=true：%s", task.Params)
	}
	var p struct {
		Chat *struct {
			Kind string `json:"kind"`
			Free bool   `json:"free"`
		} `json:"chat"`
	}
	if json.Unmarshal([]byte(task.Params), &p) != nil || p.Chat == nil || p.Chat.Kind != "director" || !p.Chat.Free {
		t.Fatalf("params.chat 不对：%s", task.Params)
	}
}

func TestNewDirectorChatTaskRespectsSwitches(t *testing.T) {
	registerDirectorChatProvider(t)
	seedChatSetting(t, setting.AgentEnabled, "false")
	seedChatSetting(t, setting.ChatFreeEnabled, "true")
	if _, _, err := NewDirectorChatTask(8, "gpt-4o-mini", "加一个角色"); err == nil || !strings.Contains(err.Error(), "暂未开启") {
		t.Fatalf("对话关掉时应拒绝：%v", err)
	}

	seedChatSetting(t, setting.AgentEnabled, "true")
	seedChatSetting(t, setting.ChatFreeEnabled, "false")
	task, _, err := NewDirectorChatTask(8, "gpt-4o-mini", "加一个角色")
	if err != nil {
		t.Fatalf("开关打开后应能建任务：%v", err)
	}
	if IsFreeChatTask(task) {
		t.Fatalf("免费对话关掉时应落 free=false：%s", task.Params)
	}
	if _, _, err := NewDirectorChatTask(8, "gpt-4o-mini", "   "); err == nil || err.Error() != "请输入问题" {
		t.Fatalf("空提示应拒绝：%v", err)
	}
	if _, _, err := NewDirectorChatTask(8, "missing-model", "加一个角色"); err == nil || !strings.Contains(err.Error(), "暂未接入") {
		t.Fatalf("模型未接入应拒绝：%v", err)
	}
}

func TestWriteChatQuotaDayDirectorRoundTrip(t *testing.T) {
	base := `{"chat":{"kind":"director","free":true}}`
	if _, tagged := ChatQuotaDayOf(&model.GenerationTask{Params: base}); tagged {
		t.Fatal("老任务没有标记")
	}
	withDay := WriteChatQuotaDay(base, "2026-10-15")
	if day, tagged := ChatQuotaDayOf(&model.GenerationTask{Params: withDay}); !tagged || day != "2026-10-15" {
		t.Fatalf("应读回消费日：%s", withDay)
	}
	if !IsFreeChatTask(&model.GenerationTask{Type: model.TaskTypeText, Params: withDay}) {
		t.Fatalf("写标记不应破坏 free：%s", withDay)
	}
	empty := WriteChatQuotaDay(withDay, "")
	if day, tagged := ChatQuotaDayOf(&model.GenerationTask{Params: empty}); !tagged || day != "" {
		t.Fatalf("空标记 = 没消费：%s", empty)
	}
}

func TestRewriteChatFreeFlagDirector(t *testing.T) {
	base := `{"chat":{"kind":"director","free":true}}`
	off := RewriteChatFreeFlag(base, false)
	if IsFreeChatTask(&model.GenerationTask{Type: model.TaskTypeText, Params: off}) {
		t.Fatalf("重估关闭后不应再免费：%s", off)
	}
	on := RewriteChatFreeFlag(off, true)
	if !IsFreeChatTask(&model.GenerationTask{Type: model.TaskTypeText, Params: on}) {
		t.Fatalf("重估开启后应免费：%s", on)
	}
}

func TestCheckNoClientAgentParamsRejectsChat(t *testing.T) {
	if err := CheckNoClientAgentParams(`{"chat":{"kind":"director","free":true}}`); err != ErrClientChatParams {
		t.Fatalf("自带 params.chat 必须被拒：%v", err)
	}
	if err := CheckNoClientAgentParams(`{"Chat":{"kind":"director"}}`); err != ErrClientChatParams {
		t.Fatalf("大小写变体也要拦：%v", err)
	}
	if err := CheckNoClientAgentParams(`{"chat":{}}`); err != nil {
		t.Fatalf("空对象不拦：%v", err)
	}
	if err := CheckNoClientAgentParams(`{"count":1}`); err != nil {
		t.Fatalf("普通 params 不拦：%v", err)
	}
}
