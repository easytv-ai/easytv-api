package service

import (
	"strings"
	"testing"

	"easytv-api/model"

	"github.com/DATA-DOG/go-sqlmock"
)

const dialoguePlanJSON = `{"title":"对峙","style":"冷峻","aspect":"9:16",
"characters":[{"key":"c1","name":"阿杰","look":"黑框眼镜","voice":"onyx","voice_style":"低沉"},{"key":"c2","name":"小雨","look":"马尾","voice":"nova"}],
"scenes":[{"key":"s1","name":"餐厅","look":"暖光"}],
"shots":[{"no":1,"scene":"s1","characters":["c1","c2"],"prompt":"两人对坐","seconds":4,
 "dialogue":[{"speaker":"c1","text":"手机呢","emotion":"试探"},{"speaker":"c2","text":"在我这"}],
 "states":{"c2":"左手缠着绷带"}}]}`

func TestPlanDialogueNormalize(t *testing.T) {
	plan, err := ParsePlanText(dialoguePlanJSON)
	if err != nil || plan.Normalize(12) != nil {
		t.Fatalf("plan: %v", err)
	}
	s := plan.Shots[0]
	if s.Line != "手机呢 在我这" {
		t.Fatalf("line 应由对白拼出，得 %q", s.Line)
	}
	if s.States["c2"] != "左手缠着绷带" {
		t.Fatalf("states = %v", s.States)
	}
	bad, _ := ParsePlanText(dialoguePlanJSON)
	bad.Shots[0].Dialogue = append(bad.Shots[0].Dialogue, model.PlanLine{Speaker: "c9", Text: "谁"})
	if err := bad.Normalize(12); err == nil || !strings.Contains(err.Error(), "c9") {
		t.Fatalf("不存在的说话人应报错，得 %v", err)
	}
	badState, _ := ParsePlanText(dialoguePlanJSON)
	badState.Shots[0].States = map[string]string{"c9": "x"}
	if err := badState.Normalize(12); err == nil {
		t.Fatal("不存在的人物状态应报错")
	}
}

func TestVoiceSegmentsAndPrompts(t *testing.T) {
	plan, _ := ParsePlanText(dialoguePlanJSON)
	_ = plan.Normalize(12)
	segs := VoiceSegments(plan, plan.Shots[0])
	if len(segs) != 2 || segs[0].Voice != "onyx" || segs[0].Name != "阿杰" || segs[0].Style != "低沉" || segs[1].Voice != "nova" || segs[1].Text != "在我这" {
		t.Fatalf("segments = %+v", segs)
	}
	if singleVoice(segs) != "" {
		t.Fatal("两个音色不该给缺省 voice")
	}
	extra := voiceNodeExtra(plan, plan.Shots[0], "tts")
	if _, has := extra["voice"]; has {
		t.Fatal("多音色时不该写 voice")
	}
	if got := extra["segments"].([]VoiceSegment); len(got) != 2 {
		t.Fatalf("节点 segments = %v", got)
	}
	// 单人独白：一段 + 缺省 voice
	solo := *plan
	solo.Shots = []model.PlanShot{{Scene: "s1", Characters: []string{"c1"}, Prompt: "独白", Seconds: 4, Dialogue: []model.PlanLine{{Speaker: "c1", Text: "我知道了"}}}}
	_ = solo.Normalize(12)
	if extra := voiceNodeExtra(&solo, solo.Shots[0], "tts"); extra["voice"] != "onyx" {
		t.Fatalf("单人应给缺省 voice：%v", extra)
	}
	// 状态拼进提示词
	sceneLook := map[string]string{"s1": "暖光"}
	img, vid := shotPrompts(plan, plan.Shots[0], sceneLook)
	if !strings.Contains(img, "小雨：马尾（本镜：左手缠着绷带）") || strings.Contains(vid, "绷带") {
		t.Fatalf("状态应只进分镜图提示词：img=%s vid=%s", img, vid)
	}
	// 建画布：配音节点带 segments
	graph := BuildPlanGraph(plan, BuildModels{Image: "i", Video: "v", Audio: "a"})
	byId, _ := graphNodesById(t, graph)
	if segs, ok := byId["audio-1"]["data"].(map[string]any)["segments"].([]any); !ok || len(segs) != 2 {
		t.Fatalf("audio-1 segments = %v", byId["audio-1"]["data"])
	}
	// 译文：SegmentsForLang 换条目译文
	plan.Shots[0].Dialogue[1].Lines = map[string]string{"en": "I have it"}
	en := SegmentsForLang(plan, plan.Shots[0], "en")
	if en[1].Text != "I have it" || en[0].Text != "手机呢" {
		t.Fatalf("译文分段 = %+v", en)
	}
}

func TestDialogueTemplateShots(t *testing.T) {
	plan, _ := ParsePlanText(dialoguePlanJSON)
	_ = plan.Normalize(12)
	req := DialogueTemplateRequest{Scene: "s1", A: "c1", B: "c2", Seconds: 4}
	for i, l := range []struct{ sp, text string }{{"c1", "手机呢"}, {"c2", "在我这"}, {"c1", "给我"}, {"c2", "先听完"}} {
		req.Lines = append(req.Lines, struct {
			Speaker string `json:"speaker"`
			Text    string `json:"text"`
			Emotion string `json:"emotion"`
		}{l.sp, l.text, map[bool]string{true: "", false: "紧张"}[i%2 == 0]})
	}
	shots, err := DialogueTemplateShots(plan, req)
	if err != nil {
		t.Fatal(err)
	}
	// 建立 1 + 4 句 + 第 3 句后 1 个反应 + 收尾 1 = 7
	if len(shots) != 7 {
		t.Fatalf("镜数 = %d", len(shots))
	}
	if !strings.Contains(shots[0].Prompt, "阿杰在画面左侧、小雨在画面右侧") || len(shots[0].Dialogue) != 0 {
		t.Fatalf("建立镜 = %+v", shots[0])
	}
	if len(shots[1].Dialogue) != 1 || shots[1].Dialogue[0].Text != "手机呢" || !strings.Contains(shots[1].Prompt, "从小雨肩后越过拍阿杰") || !strings.Contains(shots[1].Prompt, "左侧") {
		t.Fatalf("第 1 句 = %+v", shots[1])
	}
	if !strings.Contains(shots[4].Prompt, "小雨的反应近景") || len(shots[4].Dialogue) != 0 {
		t.Fatalf("反应镜 = %+v", shots[4])
	}
	if !strings.Contains(shots[6].Prompt, "双人中景") {
		t.Fatalf("收尾 = %+v", shots[6])
	}
	// 插入 + 上限
	if err := InsertShots(plan, shots, 1); err != nil || len(plan.Shots) != 8 || plan.Shots[0].Prompt != shots[0].Prompt {
		t.Fatalf("插入到最前：err=%v n=%d", err, len(plan.Shots))
	}
	if err := plan.Normalize(30); err != nil {
		t.Fatal(err)
	}
	if plan.Shots[7].Prompt != "两人对坐" || plan.Shots[7].No != 8 {
		t.Fatalf("原镜应排到最后：%+v", plan.Shots[7])
	}
	if err := InsertShots(plan, shots, 0); err == nil {
		t.Fatal("超过 12 镜应报错")
	}
	// 说话人不在 A/B 里
	bad := req
	bad.Lines[0].Speaker = "c9"
	if _, err := DialogueTemplateShots(plan, bad); err == nil {
		t.Fatal("说话人不合法应报错")
	}
}

func TestBuildSRTWithCues(t *testing.T) {
	clips := []model.ComposeClip{
		{Duration: 5, Text: "整句", Cues: []model.ComposeCue{{Start: 0.15, Dur: 1.2, Text: "手机呢"}, {Start: 1.7, Dur: 1.0, Text: "在我这"}, {Start: 4.9, Dur: 1.0, Text: "超出被截"}}},
		{Duration: 3, Text: "第二镜整句"},
	}
	srt := BuildSRT(clips)
	if !strings.Contains(srt, "00:00:00,150 --> 00:00:01,350\n手机呢") || !strings.Contains(srt, "00:00:01,700 --> 00:00:02,700\n在我这") {
		t.Fatalf("逐条字幕不对：\n%s", srt)
	}
	if !strings.Contains(srt, "00:00:04,900 --> 00:00:05,000\n超出被截") {
		t.Fatalf("超出本镜的字幕应截到镜尾：\n%s", srt)
	}
	if !strings.Contains(srt, "第二镜整句") || strings.Contains(srt, "\n整句\n") {
		t.Fatalf("有 cues 的镜不该再出整句：\n%s", srt)
	}
}

func TestComposePreviewSizeAndEncode(t *testing.T) {
	if w, h := composePreviewSize("9:16"); w != 480 || h != 854 {
		t.Fatalf("9:16 = %dx%d", w, h)
	}
	if w, h := composePreviewSize("16:9"); w != 854 || h != 480 {
		t.Fatalf("16:9 = %dx%d", w, h)
	}
	if strings.Join(encodeArgs(true), " ") != "-preset ultrafast -crf 30" || strings.Join(encodeArgs(false), " ") != "-preset veryfast -crf 20" {
		t.Fatal("编码档不对")
	}
}

func TestEstimateAudioSegmentsBilling(t *testing.T) {
	mock := mockDB(t)
	mock.ExpectQuery("SELECT .* FROM `model_price`").WillReturnRows(sqlmock.NewRows([]string{"model", "type", "unit", "points_per_unit", "status"}).AddRow("tts", "audio", "call", 3, 1))
	points, _, err := EstimateTaskPoints("audio", "tts", `{"count":1,"segments":[{"text":"a"},{"text":"b"},{"text":"c"}]}`, false)
	if err != nil || points != 9 {
		t.Fatalf("三段应按 3 次计费：points=%d err=%v", points, err)
	}
}
