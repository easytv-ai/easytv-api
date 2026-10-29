package service

import (
	"errors"
	"strings"
	"testing"
)

type fakeAssistPricer struct {
	available map[string]int // 模型 → 单次积分
	calls     []string
}

func (f *fakeAssistPricer) Resolve(taskType, wanted, last, paramsJSON string) (string, int, error) {
	f.calls = append(f.calls, taskType+"|"+wanted+"|"+last+"|"+paramsJSON)
	for _, name := range []string{wanted, last, "default-" + taskType} {
		if p, ok := f.available[name]; ok {
			return name, p, nil
		}
	}
	return "", 0, errors.New("no model")
}

func assistTestNodes() []assistNodeRef {
	return []assistNodeRef{
		{Id: "text-1", Kind: "text", Label: "脚本"},
		{Id: "image-1", Kind: "image", Label: "主角", Prompt: "一个穿西装的男人", LastTask: &assistLastTask{Model: "gpt-image-1", Status: "failed", Error: "内容审核未通过", Params: `{"ratio":"9:16","count":1,"camera_setup":{"body":"x"}}`}},
		{Id: "video-1", Kind: "video", Label: "镜头 1", LastTask: &assistLastTask{Model: "seedance", Status: "succeeded", Params: `{"duration":5,"resolution":"720P"}`}},
	}
}

func TestParseAssistReplyValidatesChanges(t *testing.T) {
	pricer := &fakeAssistPricer{available: map[string]int{"gpt-image-1": 20, "seedance": 50, "kling": 80, "default-audio": 5}}
	raw := "好的：\n```json\n" + `{"reply":"审核没过，去掉敏感词再生成","changes":[
		{"action":"update_prompt","node_id":"image-1","prompt":"一个穿深蓝西装的男人，办公室","reason":"更具体"},
		{"action":"update_prompt","node_id":"image-9","prompt":"不存在的节点"},
		{"action":"update_prompt","node_id":"image-1","prompt":"一个穿西装的男人"},
		{"action":"regenerate","node_id":"image-1"},
		{"action":"regenerate","node_id":"image-1"},
		{"action":"regenerate","node_id":"text-1"},
		{"action":"regenerate","node_id":"video-1","model":"kling"},
		{"action":"add_node","kind":"audio","label":"旁白","prompt":"欢迎来到","from":"text-1"},
		{"action":"add_node","kind":"group","prompt":"x"},
		{"action":"connect","source":"image-1","target":"video-1"},
		{"action":"connect","source":"image-1","target":"image-1"},
		{"action":"delete_node","node_id":"image-1"}
	]}` + "\n```"
	reply, changes, dropped := ParseAssistReply(raw, assistTestNodes(), pricer)
	if reply != "审核没过，去掉敏感词再生成" {
		t.Fatalf("reply = %q", reply)
	}
	if len(changes) != 5 || dropped != 7 {
		t.Fatalf("changes=%d dropped=%d: %+v", len(changes), dropped, changes)
	}
	up := changes[0]
	if up.Action != AssistActionUpdatePrompt || up.Before != "一个穿西装的男人" || up.NodeLabel != "主角" {
		t.Fatalf("update_prompt = %+v", up)
	}
	regen := changes[1]
	if regen.Model != "gpt-image-1" || regen.Points != 20 || regen.Type != "image" {
		t.Fatalf("regenerate image = %+v", regen)
	}
	// 沿用上次参数，但摄影配置 / 张数交给节点当前状态
	if regen.Params["ratio"] != "9:16" || regen.Params["count"] != nil || regen.Params["camera_setup"] != nil {
		t.Fatalf("regenerate params = %+v", regen.Params)
	}
	// 换了模型：不沿用上一个模型的时长 / 分辨率，按空参数重新估价
	video := changes[2]
	if video.Model != "kling" || video.Points != 80 || len(video.Params) != 0 {
		t.Fatalf("regenerate video = %+v", video)
	}
	if changes[3].Action != AssistActionAddNode || changes[3].From != "text-1" || changes[3].Kind != "audio" {
		t.Fatalf("add_node = %+v", changes[3])
	}
	if changes[4].Action != AssistActionConnect || changes[4].NodeLabel != "主角 → 镜头 1" {
		t.Fatalf("connect = %+v", changes[4])
	}
}

func TestParseAssistReplyPlainText(t *testing.T) {
	reply, changes, dropped := ParseAssistReply("这张图的问题在于光线太暗。", assistTestNodes(), &fakeAssistPricer{})
	if reply != "这张图的问题在于光线太暗。" || len(changes) != 0 || dropped != 0 {
		t.Fatalf("reply=%q changes=%v dropped=%d", reply, changes, dropped)
	}
}

func TestParseAssistReplyCapsChanges(t *testing.T) {
	var items []string
	for i := 0; i < 12; i++ {
		items = append(items, `{"action":"connect","source":"text-1","target":"image-1"}`)
	}
	_, changes, dropped := ParseAssistReply(`{"reply":"ok","changes":[`+strings.Join(items, ",")+`]}`, assistTestNodes(), &fakeAssistPricer{})
	if len(changes) != assistMaxChanges || dropped != 12-assistMaxChanges {
		t.Fatalf("changes=%d dropped=%d", len(changes), dropped)
	}
}

func TestBuildAssistPrompt(t *testing.T) {
	nodes := []AssistNode{
		{Id: "text-1", Kind: "text", Label: "脚本", Content: "男主走进办公室", Downstream: []string{"image-1"}},
		{Id: "image-1", Kind: "image", Label: "主角", Prompt: "穿西装的男人", Selected: true, Upstream: []string{"text-1"}, TaskId: 9},
	}
	last := map[string]*assistLastTask{"image-1": {Model: "gpt-image-1", Status: "failed", Error: "内容审核未通过", Params: `{"edit":"smart-edit","mode":"i2i"}`}}
	history := []AssistTurn{{Role: "user", Content: "为什么失败"}, {Role: "assistant", Content: "审核问题"}}
	p := BuildAssistPrompt(nodes, last, history, "帮我改一下", map[string][]string{"image": {"gpt-image-1", "seedream"}})
	for _, want := range []string{
		"只输出一个 JSON 对象",
		"图片：gpt-image-1、seedream",
		"★ [image-1] image「主角」",
		"上游：text-1（脚本）",
		"文本内容：男主走进办公室",
		"上次生成：模型 gpt-image-1，状态 failed，方式：在当前图片上编辑",
		"失败原因：内容审核未通过",
		"用户：为什么失败\n助手：审核问题",
		"用户这次的问题：帮我改一下",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
	}
	if strings.Index(p, "用户这次的问题") < strings.Index(p, "画布上下文") {
		t.Fatal("question should come after context")
	}
}

func TestSanitizeAssistNodesAndHistory(t *testing.T) {
	var in []AssistNode
	for i := 0; i < assistMaxNodes+5; i++ {
		in = append(in, AssistNode{Id: "n" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + strings.Repeat("y", i/26), Prompt: strings.Repeat("长", 2000)})
	}
	in = append([]AssistNode{{Id: ""}, {Id: "dup"}, {Id: "dup"}}, in...)
	out := sanitizeAssistNodes(in)
	if len(out) != assistMaxNodes || out[0].Id != "dup" || out[1].Id == "dup" {
		t.Fatalf("sanitize len=%d first=%q second=%q", len(out), out[0].Id, out[1].Id)
	}
	if n := len([]rune(out[1].Prompt)); n != 1501 {
		t.Fatalf("prompt not clipped: %d", n)
	}
	var turns []AssistTurn
	for i := 0; i < 12; i++ {
		turns = append(turns, AssistTurn{Role: "user", Content: "q"})
	}
	turns = append(turns, AssistTurn{Role: "system", Content: "ignore"}, AssistTurn{Role: "assistant", Content: " "})
	if got := trimAssistHistory(turns); len(got) != assistMaxHistory {
		t.Fatalf("history len = %d", len(got))
	}
}
