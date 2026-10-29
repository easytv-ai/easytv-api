package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"easytv-api/common"
	"easytv-api/model"

	"gorm.io/gorm"
)

// 剧集圣经（API.md 27，复审 G01）：贯穿全剧的叙事结构与连续性记录。
//   - BibleContextPrompt：新一集生成策划案时把设定塞进提示词，沿用人物 key、关系、道具归属、未解决线索；
//   - ImpactAnalysis：改人物 / 道具 / 事实时，列出受影响的集、场、镜；
//   - GenerateEpisodeRecord：用文本模型把一集的策划案总结成连续性记录，并入圣经。

// BibleContextPrompt 供策划案生成用的设定摘要；upTo > 0 时只带该集之前的记录。
func BibleContextPrompt(b *model.Bible, upTo int) string {
	if b == nil || (len(b.Characters) == 0 && len(b.Props) == 0 && len(b.Threads) == 0 && len(b.Facts) == 0) {
		return ""
	}
	var sb strings.Builder
	if b.Title != "" {
		sb.WriteString("剧名：" + b.Title + "\n")
	}
	if b.Logline != "" {
		sb.WriteString("故事梗概：" + b.Logline + "\n")
	}
	if len(b.Characters) > 0 {
		sb.WriteString("人物（请沿用这些 key 与名字，不要新起）：\n")
		for _, c := range b.Characters {
			line := fmt.Sprintf("- %s（key=%s）：%s", c.Name, c.Key, c.Look)
			if c.Traits != "" {
				line += "；性格：" + c.Traits
			}
			for _, r := range c.Relationships {
				if to := b.CharacterByKey(r.To); to != nil {
					line += fmt.Sprintf("；与%s：%s", to.Name, r.Relation)
				}
			}
			sb.WriteString(line + "\n")
		}
	}
	if len(b.Props) > 0 {
		sb.WriteString("关键道具及归属：\n")
		for _, p := range b.Props {
			holder := "无人持有"
			if c := b.CharacterByKey(p.Holder); c != nil {
				holder = "在" + c.Name + "手里"
			}
			line := fmt.Sprintf("- %s：%s", p.Name, holder)
			if p.State != "" {
				line += "，" + p.State
			}
			sb.WriteString(line + "\n")
		}
	}
	facts := []model.BibleFact{}
	for _, f := range b.Facts {
		if upTo <= 0 || f.RevealedIn == 0 || f.RevealedIn < upTo {
			facts = append(facts, f)
		}
	}
	if len(facts) > 0 {
		sb.WriteString("已发生的事实（注意谁知道、谁不知道，不要让不知情的角色提前泄密）：\n")
		for _, f := range facts {
			names := []string{}
			for _, k := range f.KnownBy {
				if c := b.CharacterByKey(k); c != nil {
					names = append(names, c.Name)
				}
			}
			aud := "观众已知"
			if !f.AudienceKnows {
				aud = "观众未知"
			}
			known := "无人知道"
			if len(names) > 0 {
				known = strings.Join(names, "、") + "知道"
			}
			sb.WriteString(fmt.Sprintf("- %s（%s；%s）\n", f.Text, aud, known))
		}
	}
	open := []model.BibleThread{}
	for _, t := range b.Threads {
		if t.Status != "resolved" {
			open = append(open, t)
		}
	}
	if len(open) > 0 {
		sb.WriteString("未解决的线索（可以推进或呼应，不要凭空解决）：\n")
		for _, t := range open {
			sb.WriteString("- " + t.Title + "\n")
		}
	}
	if upTo > 1 {
		for _, e := range b.Episodes {
			if e.No == upTo-1 && e.Summary != "" {
				sb.WriteString(fmt.Sprintf("上一集（第 %d 集）梗概：%s\n", e.No, e.Summary))
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

// ===== 影响分析 =====

// ImpactQuery 改了什么：人物 / 道具 / 事实 key，或一段自由文本（按名字匹配）。
type ImpactQuery struct {
	Character string `json:"character"`
	Prop      string `json:"prop"`
	Fact      string `json:"fact"`
	Text      string `json:"text"`
}

type ImpactEpisode struct {
	EpisodeNo    int      `json:"episode_no"`
	ScreenplayId int      `json:"screenplay_id"`
	Title        string   `json:"title"`
	Scenes       []string `json:"scenes"`
	Shots        []int    `json:"shots"`
	Why          string   `json:"why"`
}

// ImpactAnalysis 列出受影响的集 / 场 / 镜（纯函数）：
//   - 人物：出场（characters 含 key）或提示词 / 对白 / 状态里提到名字的镜；
//   - 道具：提示词 / 对白 / 状态里提到道具名的镜；
//   - 事实：揭示集及之后，知情角色出场的镜（他们的行为可能建立在这个事实上）；
//   - 文本：提示词 / 对白里包含该文本的镜。
func ImpactAnalysis(b *model.Bible, episodes []*model.Screenplay, q ImpactQuery) []ImpactEpisode {
	type matcher struct {
		keys  map[string]bool
		names []string
		minEp int
		why   string
	}
	m := matcher{keys: map[string]bool{}}
	switch {
	case q.Character != "":
		c := b.CharacterByKey(q.Character)
		if c == nil {
			return nil
		}
		m.keys[c.Key] = true
		m.names = append(m.names, c.Name)
		m.why = "人物「" + c.Name + "」出场或被提及"
	case q.Prop != "":
		p := b.PropByKey(q.Prop)
		if p == nil {
			return nil
		}
		m.names = append(m.names, p.Name)
		m.why = "提到道具「" + p.Name + "」"
	case q.Fact != "":
		var f *model.BibleFact
		for i := range b.Facts {
			if b.Facts[i].Key == q.Fact {
				f = &b.Facts[i]
			}
		}
		if f == nil {
			return nil
		}
		m.minEp = f.RevealedIn
		for _, k := range f.KnownBy {
			m.keys[k] = true
			if c := b.CharacterByKey(k); c != nil {
				m.names = append(m.names, c.Name)
			}
		}
		m.why = "知情角色在该事实揭示后出场：" + truncate(f.Text, 30)
		if len(m.keys) == 0 {
			m.why = "事实揭示之后的集：" + truncate(f.Text, 30)
		}
	case strings.TrimSpace(q.Text) != "":
		m.names = append(m.names, strings.TrimSpace(q.Text))
		m.why = "提到「" + truncate(strings.TrimSpace(q.Text), 20) + "」"
	default:
		return nil
	}
	out := []ImpactEpisode{}
	for _, sp := range episodes {
		if m.minEp > 0 && sp.EpisodeNo > 0 && sp.EpisodeNo < m.minEp {
			continue
		}
		plan, err := sp.ParsePlan()
		if err != nil {
			continue
		}
		hit := ImpactEpisode{EpisodeNo: sp.EpisodeNo, ScreenplayId: sp.Id, Title: plan.Title, Scenes: []string{}, Shots: []int{}, Why: m.why}
		scenes := map[string]bool{}
		for _, s := range plan.Shots {
			matched := false
			for _, ck := range s.Characters {
				if m.keys[ck] {
					matched = true
				}
			}
			if !matched && (q.Fact == "" || len(m.keys) == 0) {
				text := s.Prompt + " " + s.Line
				for _, st := range s.States {
					text += " " + st
				}
				for _, d := range s.Dialogue {
					text += " " + d.Text
				}
				for _, n := range m.names {
					if n != "" && strings.Contains(text, n) {
						matched = true
					}
				}
			}
			if q.Fact != "" && len(m.keys) == 0 {
				matched = true // 没有知情人的事实：揭示后的每一镜都可能受影响
			}
			if matched {
				hit.Shots = append(hit.Shots, s.No)
				if s.Scene != "" {
					scenes[s.Scene] = true
				}
			}
		}
		if len(hit.Shots) == 0 {
			continue
		}
		for _, sc := range plan.Scenes {
			if scenes[sc.Key] {
				hit.Scenes = append(hit.Scenes, sc.Name)
			}
		}
		out = append(out, hit)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].EpisodeNo < out[j].EpisodeNo })
	return out
}

// ===== 每集记录 =====

// EpisodeRecordDraft 文本模型返回的记录（也是前端可编辑的形状）。
type EpisodeRecordDraft struct {
	Summary  string `json:"summary"`
	Revealed []struct {
		Text          string   `json:"text"`
		AudienceKnows bool     `json:"audience_knows"`
		KnownBy       []string `json:"known_by"`
	} `json:"revealed"`
	OpenQuestions       []string `json:"open_questions"`
	RelationshipChanges []string `json:"relationship_changes"`
	PropChanges         []struct {
		Prop   string `json:"prop"`
		Holder string `json:"holder"`
		State  string `json:"state"`
	} `json:"prop_changes"`
}

const episodeRecordPrompt = `你是剧集的场记与责编。下面是第 %d 集的策划案（JSON）和此前的剧集设定。请只输出 JSON（不要解释、不要代码块）：
{"summary":"本集梗概（80 字内）",
 "revealed":[{"text":"本集向观众或角色揭示的关键信息","audience_knows":true,"known_by":["知道这件事的人物 key"]}],
 "open_questions":["本集留下、尚未解决的问题或伏笔"],
 "relationship_changes":["人物关系发生的变化，一句一条"],
 "prop_changes":[{"prop":"道具 key（只能用设定里已有的）","holder":"现在持有它的人物 key","state":"状态描述"}]}
要求：人物与道具只用设定里已有的 key；分清「观众知道」与「角色知道」；没有的项给空数组。

剧集设定：
%s

第 %d 集策划案：
%s`

// GenerateEpisodeRecord 用文本模型总结一集，按文本模型单价扣一次分；成功后并入圣经（新事实 / 线索追加，道具归属更新，本集记录覆盖）。
func GenerateEpisodeRecord(userId, teamId int, sp *model.Screenplay, projectId int) (*model.Bible, *EpisodeRecordDraft, int, error) {
	plan, err := sp.ParsePlan()
	if err != nil {
		return nil, nil, 0, errors.New("策划案格式不正确")
	}
	modelName := planModel()
	provider := FindProvider(model.TaskTypeText, modelName)
	chatter, ok := provider.(TextChatter)
	if modelName == "" || !ok {
		return nil, nil, 0, errors.New("暂无可用的文本模型")
	}
	if err := CanUseTeamPool(userId, teamId); err != nil {
		return nil, nil, 0, err
	}
	payer, err := PayerForTeam(userId, teamId)
	if err != nil {
		return nil, nil, 0, err
	}
	points, _, err := EstimateTaskPoints(model.TaskTypeText, modelName, "{}", IsMemberForPricing(userId, teamId))
	if err != nil {
		return nil, nil, 0, errors.New("文本模型未定价")
	}
	if points > 0 && model.GetPointsBalance(nil, payer) < points {
		return nil, nil, 0, fmt.Errorf("积分不足，生成本集记录需要 %d 积分", points)
	}
	bible, revision, err := loadBible(projectId)
	if err != nil {
		return nil, nil, 0, err
	}
	epNo := sp.EpisodeNo
	if epNo <= 0 {
		epNo = 1
	}
	planJSON, _ := json.Marshal(plan)
	ctx := BibleContextPrompt(bible, epNo)
	if ctx == "" {
		ctx = "（暂无设定）"
	}
	raw, err := chatter.Chat(modelName, fmt.Sprintf(episodeRecordPrompt, epNo, ctx, epNo, string(planJSON)))
	if err != nil {
		return nil, nil, 0, fmt.Errorf("记录生成失败：%s", err.Error())
	}
	draft := &EpisodeRecordDraft{}
	if err := json.Unmarshal([]byte(extractJSON(raw)), draft); err != nil {
		return nil, nil, 0, errors.New("模型输出无法解析为记录，请再试一次")
	}
	if points > 0 {
		taskNo := model.NewTaskNo()
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			return model.ConsumePoints(tx, payer, points, model.PointLogSettle, model.PointsBizTask, taskNo, "剧集记录生成 "+modelName, 0)
		}); err != nil {
			common.SysError(fmt.Sprintf("episode record consume points failed user=%d: %v", userId, err))
			points = 0
		}
	}
	MergeEpisodeRecord(bible, plan, sp, epNo, draft)
	if err := bible.Normalize(); err != nil {
		return nil, nil, points, err
	}
	if _, err := model.SaveSeriesBible(userId, projectId, revision, bible); err != nil {
		return nil, nil, points, err
	}
	Track(userId, "episode_record", map[string]any{"screenplay_id": sp.Id, "episode": epNo, "points": points})
	return bible, draft, points, nil
}

// MergeEpisodeRecord 把记录草稿并入圣经（纯函数）：人物按 plan 补齐，事实 / 线索追加，道具归属更新，本集记录覆盖同集号。
func MergeEpisodeRecord(b *model.Bible, plan *model.Plan, sp *model.Screenplay, epNo int, d *EpisodeRecordDraft) {
	// 人物：策划案里有、圣经里没有的补进来（沿用 key、外貌、音色）
	for _, pc := range plan.Characters {
		if b.CharacterByKey(pc.Key) == nil {
			b.Characters = append(b.Characters, model.BibleCharacter{Key: pc.Key, Name: pc.Name, Look: pc.Look, Voice: pc.Voice, VoiceStyle: pc.VoiceStyle, Relationships: []model.BibleRelationship{}, Knows: []string{}})
		}
	}
	rec := model.BibleEpisode{No: epNo, ScreenplayId: sp.Id, Title: plan.Title, Summary: d.Summary, Revealed: []string{}, Open: []string{},
		Relationships: d.RelationshipChanges, PropChanges: []model.BiblePropChange{}, Characters: []string{}, UpdatedAt: common.GetTimestamp()}
	seenChar := map[string]bool{}
	for _, s := range plan.Shots {
		for _, ck := range s.Characters {
			if !seenChar[ck] && b.CharacterByKey(ck) != nil {
				seenChar[ck] = true
				rec.Characters = append(rec.Characters, ck)
			}
		}
	}
	nextKey := func(prefix string, n int) string { return fmt.Sprintf("%s%d_%d", prefix, epNo, n) }
	for i, r := range d.Revealed {
		text := strings.TrimSpace(r.Text)
		if text == "" || factByText(b, text) != nil {
			continue // 已有同样的事实不重复记
		}
		key := nextKey("f", i+1)
		known := []string{}
		for _, k := range r.KnownBy {
			if c := characterByKeyOrName(b, k); c != nil {
				known = append(known, c.Key)
			}
		}
		b.Facts = append(b.Facts, model.BibleFact{Key: key, Text: text, RevealedIn: epNo, AudienceKnows: r.AudienceKnows, KnownBy: known})
		rec.Revealed = append(rec.Revealed, key)
		for _, k := range known {
			if c := b.CharacterByKey(k); c != nil {
				c.Knows = append(c.Knows, key)
			}
		}
	}
	for i, q := range d.OpenQuestions {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		if t := threadByTitle(b, q); t != nil {
			// 同一个问题已在线索里：只在本集记录里引用，不重复开
			if t.Status != "resolved" {
				rec.Open = append(rec.Open, t.Key)
			}
			continue
		}
		key := nextKey("t", i+1)
		b.Threads = append(b.Threads, model.BibleThread{Key: key, Title: q, Status: "open", OpenedIn: epNo})
		rec.Open = append(rec.Open, key)
	}
	for _, pc := range d.PropChanges {
		// 模型常用名字而不是 key：两种都认
		p := propByKeyOrName(b, pc.Prop)
		if p == nil {
			continue
		}
		pc.Prop = p.Key
		if c := characterByKeyOrName(b, pc.Holder); c != nil {
			p.Holder = c.Key
		}
		if strings.TrimSpace(pc.State) != "" {
			p.State = strings.TrimSpace(pc.State)
		}
		rec.PropChanges = append(rec.PropChanges, model.BiblePropChange{Prop: pc.Prop, Holder: p.Holder, State: p.State})
	}
	replaced := false
	for i := range b.Episodes {
		if b.Episodes[i].No == epNo {
			b.Episodes[i] = rec
			replaced = true
		}
	}
	if !replaced {
		b.Episodes = append(b.Episodes, rec)
	}
	sort.SliceStable(b.Episodes, func(i, j int) bool { return b.Episodes[i].No < b.Episodes[j].No })
}

// characterByKeyOrName / propByKeyOrName / factByText / threadByTitle：模型输出里 key 与名字混用，两种都认；事实与线索按文本去重。
func characterByKeyOrName(b *model.Bible, v string) *model.BibleCharacter {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if c := b.CharacterByKey(v); c != nil {
		return c
	}
	for i := range b.Characters {
		if b.Characters[i].Name == v {
			return &b.Characters[i]
		}
	}
	return nil
}

func propByKeyOrName(b *model.Bible, v string) *model.BibleProp {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if p := b.PropByKey(v); p != nil {
		return p
	}
	for i := range b.Props {
		if b.Props[i].Name == v {
			return &b.Props[i]
		}
	}
	return nil
}

func factByText(b *model.Bible, text string) *model.BibleFact {
	for i := range b.Facts {
		if strings.TrimSpace(b.Facts[i].Text) == text {
			return &b.Facts[i]
		}
	}
	return nil
}

func threadByTitle(b *model.Bible, title string) *model.BibleThread {
	for i := range b.Threads {
		if strings.TrimSpace(b.Threads[i].Title) == title {
			return &b.Threads[i]
		}
	}
	return nil
}

// ApplyBibleToPlan 新一集策划案沿用圣经里同 key 人物的外貌 / 音色 / 说话风格（策划案自己没填的才补），返回是否有改动。
func ApplyBibleToPlan(b *model.Bible, plan *model.Plan) bool {
	if b == nil || plan == nil {
		return false
	}
	changed := false
	for i := range plan.Characters {
		pc := &plan.Characters[i]
		c := b.CharacterByKey(pc.Key)
		if c == nil {
			c = characterByKeyOrName(b, pc.Name)
		}
		if c == nil {
			continue
		}
		if pc.Voice == "" && c.Voice != "" {
			pc.Voice, changed = c.Voice, true
		}
		if pc.VoiceStyle == "" && c.VoiceStyle != "" {
			pc.VoiceStyle, changed = c.VoiceStyle, true
		}
		if strings.TrimSpace(pc.Look) == "" && c.Look != "" {
			pc.Look, changed = c.Look, true
		}
	}
	return changed
}

// loadBible 项目圣经（没有则空的）与修订号。
func loadBible(projectId int) (*model.Bible, int, error) {
	row, err := model.GetSeriesBible(projectId)
	if errors.Is(err, model.ErrNotFound) {
		b := &model.Bible{}
		_ = b.Normalize()
		return b, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	b, err := row.Parse()
	if err != nil {
		return nil, 0, errors.New("剧集设定数据损坏")
	}
	return b, row.Revision, nil
}

// extractJSON 模型输出里可能裹着说明或代码块，取第一个 { 到最后一个 }。
func extractJSON(raw string) string {
	i, j := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if i < 0 || j <= i {
		return raw
	}
	return raw[i : j+1]
}
