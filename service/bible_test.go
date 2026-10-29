package service

import (
	"encoding/json"
	"strings"
	"testing"

	"easytv-api/model"
)

func sampleBible() *model.Bible {
	b := &model.Bible{Title: "面馆", Characters: []model.BibleCharacter{
		{Key: "c1", Name: "程序员", Look: "黑框眼镜", Relationships: []model.BibleRelationship{{To: "c2", Relation: "常客与老板"}}},
		{Key: "c2", Name: "老闻", Look: "工作服"},
	}, Props: []model.BibleProp{{Key: "p1", Name: "录音手机", Holder: "c2", State: "屏幕碎了"}},
		Threads: []model.BibleThread{{Key: "t1", Title: "录音里是谁", Status: "open", OpenedIn: 1}},
		Facts:   []model.BibleFact{{Key: "f1", Text: "手机里有录音", RevealedIn: 1, AudienceKnows: true, KnownBy: []string{"c2"}}}}
	if err := b.Normalize(); err != nil {
		panic(err)
	}
	return b
}

func TestBibleNormalize(t *testing.T) {
	b := sampleBible()
	if b.Characters[0].Relationships[0].To != "c2" || b.Props[0].Holder != "c2" {
		t.Fatalf("normalize 改坏了数据：%+v", b)
	}
	bad := sampleBible()
	bad.Props[0].Holder = "c9"
	if err := bad.Normalize(); err == nil || !strings.Contains(err.Error(), "持有人") {
		t.Fatalf("持有人不存在应报错：%v", err)
	}
	bad2 := sampleBible()
	bad2.Facts[0].KnownBy = []string{"c9"}
	if err := bad2.Normalize(); err == nil {
		t.Fatal("知情人不存在应报错")
	}
	dup := sampleBible()
	dup.Characters = append(dup.Characters, model.BibleCharacter{Key: "c1", Name: "重复"})
	if err := dup.Normalize(); err == nil {
		t.Fatal("人物 key 重复应报错")
	}
}

func TestBibleContextPrompt(t *testing.T) {
	b := sampleBible()
	ctx := BibleContextPrompt(b, 2)
	for _, want := range []string{"程序员（key=c1）", "与老闻：常客与老板", "录音手机：在老闻手里，屏幕碎了", "手机里有录音（观众已知；老闻知道）", "录音里是谁"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("缺少「%s」：\n%s", want, ctx)
		}
	}
	// 第 1 集生成时，第 1 集才揭示的事实不该出现
	if strings.Contains(BibleContextPrompt(b, 1), "手机里有录音") {
		t.Fatal("揭示集之后的事实不该带进更早的集")
	}
	if BibleContextPrompt(&model.Bible{}, 0) != "" {
		t.Fatal("空圣经应返回空串")
	}
}

func TestImpactAnalysis(t *testing.T) {
	b := sampleBible()
	ep1, _ := ParsePlanText(`{"title":"第一集","style":"","aspect":"16:9","characters":[{"key":"c1","name":"程序员","look":"x"},{"key":"c2","name":"老闻","look":"y"}],
"scenes":[{"key":"s1","name":"餐厅","look":"暖光"},{"key":"s2","name":"走廊","look":"冷光"}],
"shots":[{"no":1,"scene":"s1","characters":["c1","c2"],"prompt":"两人对坐","seconds":4},{"no":2,"scene":"s2","characters":["c1"],"prompt":"程序员盯着录音手机","seconds":4},{"no":3,"scene":"s2","characters":[],"prompt":"空镜","seconds":3}]}`)
	_ = ep1.Normalize(12)
	ep2, _ := ParsePlanText(`{"title":"第二集","style":"","aspect":"16:9","characters":[{"key":"c2","name":"老闻","look":"y"}],
"scenes":[{"key":"s1","name":"后厨","look":"x"}],"shots":[{"no":1,"scene":"s1","characters":["c2"],"prompt":"老闻擦碗","seconds":4}]}`)
	_ = ep2.Normalize(12)
	mk := func(id, no int, plan *model.Plan) *model.Screenplay {
		sp := &model.Screenplay{Id: id, EpisodeNo: no}
		b, _ := marshalPlan(plan)
		sp.Plan = b
		return sp
	}
	eps := []*model.Screenplay{mk(1, 1, ep1), mk(2, 2, ep2)}
	// 人物 c2：第 1 集镜 1（出场）、第 2 集镜 1
	r := ImpactAnalysis(b, eps, ImpactQuery{Character: "c2"})
	if len(r) != 2 || len(r[0].Shots) != 1 || r[0].Shots[0] != 1 || r[0].Scenes[0] != "餐厅" || r[1].Shots[0] != 1 {
		t.Fatalf("人物影响 = %+v", r)
	}
	// 道具：第 1 集镜 2 提到「录音手机」
	r = ImpactAnalysis(b, eps, ImpactQuery{Prop: "p1"})
	if len(r) != 1 || r[0].EpisodeNo != 1 || len(r[0].Shots) != 1 || r[0].Shots[0] != 2 || r[0].Scenes[0] != "走廊" {
		t.Fatalf("道具影响 = %+v", r)
	}
	// 事实 f1（老闻知道，第 1 集揭示）：老闻出场的镜
	r = ImpactAnalysis(b, eps, ImpactQuery{Fact: "f1"})
	if len(r) != 2 || r[0].Shots[0] != 1 || r[1].Shots[0] != 1 || !strings.Contains(r[0].Why, "知情角色") {
		t.Fatalf("事实影响 = %+v", r)
	}
	if got := ImpactAnalysis(b, eps, ImpactQuery{Text: "空镜"}); len(got) != 1 || got[0].Shots[0] != 3 {
		t.Fatalf("文本影响 = %+v", got)
	}
	if ImpactAnalysis(b, eps, ImpactQuery{Character: "c9"}) != nil {
		t.Fatal("不存在的人物应返回 nil")
	}
}

func marshalPlan(p *model.Plan) (string, error) {
	b, err := json.Marshal(p)
	return string(b), err
}

func TestMergeEpisodeRecord(t *testing.T) {
	b := sampleBible()
	plan, _ := ParsePlanText(`{"title":"第二集","style":"","aspect":"16:9","characters":[{"key":"c2","name":"老闻","look":"y"},{"key":"c3","name":"小雨","look":"马尾","voice":"nova"}],
"scenes":[{"key":"s1","name":"后厨","look":"x"}],"shots":[{"no":1,"scene":"s1","characters":["c2","c3"],"prompt":"两人","seconds":4}]}`)
	_ = plan.Normalize(12)
	draft := &EpisodeRecordDraft{Summary: "小雨拿走了手机"}
	draft.Revealed = append(draft.Revealed, struct {
		Text          string   `json:"text"`
		AudienceKnows bool     `json:"audience_knows"`
		KnownBy       []string `json:"known_by"`
	}{Text: "小雨是录音里的人", AudienceKnows: false, KnownBy: []string{"c3", "c9"}})
	draft.OpenQuestions = []string{"小雨为什么要录音"}
	draft.PropChanges = append(draft.PropChanges, struct {
		Prop   string `json:"prop"`
		Holder string `json:"holder"`
		State  string `json:"state"`
	}{Prop: "p1", Holder: "c3", State: "被小雨拿走"})
	MergeEpisodeRecord(b, plan, &model.Screenplay{Id: 7}, 2, draft)
	if err := b.Normalize(); err != nil {
		t.Fatal(err)
	}
	if b.CharacterByKey("c3") == nil || b.CharacterByKey("c3").Voice != "nova" {
		t.Fatal("策划案里的新人物应补进圣经并带音色")
	}
	if len(b.Facts) != 2 || b.Facts[1].Key != "f2_1" || b.Facts[1].RevealedIn != 2 || len(b.Facts[1].KnownBy) != 1 || b.Facts[1].KnownBy[0] != "c3" {
		t.Fatalf("事实 = %+v", b.Facts)
	}
	if b.CharacterByKey("c3").Knows[0] != "f2_1" {
		t.Fatal("知情人应记上事实 key")
	}
	if b.PropByKey("p1").Holder != "c3" || b.PropByKey("p1").State != "被小雨拿走" {
		t.Fatalf("道具归属应更新：%+v", b.PropByKey("p1"))
	}
	if len(b.Threads) != 2 || b.Threads[1].OpenedIn != 2 {
		t.Fatalf("线索 = %+v", b.Threads)
	}
	ep := b.Episodes[len(b.Episodes)-1]
	if ep.No != 2 || ep.ScreenplayId != 7 || len(ep.Characters) != 2 || ep.Revealed[0] != "f2_1" || ep.PropChanges[0].Holder != "c3" {
		t.Fatalf("本集记录 = %+v", ep)
	}
	// 同集号再记一次：覆盖而不是追加
	MergeEpisodeRecord(b, plan, &model.Screenplay{Id: 7}, 2, &EpisodeRecordDraft{Summary: "改过的梗概"})
	if n := len(b.Episodes); n != 1 || b.Episodes[0].Summary != "改过的梗概" {
		t.Fatalf("同集应覆盖：%d %+v", n, b.Episodes)
	}
	if extractJSON("好的，如下：\n```json\n{\"a\":1}\n```") != `{"a":1}` {
		t.Fatal("extractJSON 应剥掉包裹")
	}
}

// TestMergeEpisodeRecordByNameAndDedupe 模型常用名字代替 key、把已有线索再列一遍：按名字认、按文本去重。
func TestMergeEpisodeRecordByNameAndDedupe(t *testing.T) {
	b := sampleBible()
	plan, _ := ParsePlanText(`{"title":"x","style":"","aspect":"16:9","characters":[{"key":"c1","name":"程序员","look":"x"}],"scenes":[{"key":"s1","name":"a","look":"b"}],"shots":[{"no":1,"scene":"s1","characters":["c1"],"prompt":"p","seconds":4}]}`)
	_ = plan.Normalize(12)
	d := &EpisodeRecordDraft{Summary: "s", OpenQuestions: []string{"录音里是谁"}}
	d.Revealed = append(d.Revealed, struct {
		Text          string   `json:"text"`
		AudienceKnows bool     `json:"audience_knows"`
		KnownBy       []string `json:"known_by"`
	}{Text: "手机里有录音", AudienceKnows: true, KnownBy: []string{"程序员"}})
	d.PropChanges = append(d.PropChanges, struct {
		Prop   string `json:"prop"`
		Holder string `json:"holder"`
		State  string `json:"state"`
	}{Prop: "录音手机", Holder: "程序员", State: "被拿走"})
	MergeEpisodeRecord(b, plan, &model.Screenplay{Id: 1}, 2, d)
	if len(b.Threads) != 1 || len(b.Facts) != 1 {
		t.Fatalf("同文本的线索 / 事实不该重复：threads=%d facts=%d", len(b.Threads), len(b.Facts))
	}
	if b.PropByKey("p1").Holder != "c1" || b.PropByKey("p1").State != "被拿走" {
		t.Fatalf("按名字认道具与持有人：%+v", b.PropByKey("p1"))
	}
	if b.Episodes[0].Open[0] != "t1" {
		t.Fatalf("本集记录应引用已有线索：%+v", b.Episodes[0])
	}
	// 沿用设定：音色补进策划案
	if !ApplyBibleToPlan(b, plan) || plan.Characters[0].Voice != "" {
		// sampleBible 的 c1 没设音色，只有 look 已填 → 不改
		t.Log("c1 无音色，跳过")
	}
	b.Characters[0].Voice, b.Characters[0].VoiceStyle = "onyx", "低沉"
	if !ApplyBibleToPlan(b, plan) || plan.Characters[0].Voice != "onyx" || plan.Characters[0].VoiceStyle != "低沉" {
		t.Fatalf("应补进音色：%+v", plan.Characters[0])
	}
}
