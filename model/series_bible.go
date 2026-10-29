package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// SeriesBible 剧集圣经（API.md 27，复审 G01）：一个项目一份，贯穿全剧的人物关系与知情、道具归属、伏笔线索、每集记录。
// 单独成表而不是塞进 project：内容会随集数增长，且要有自己的修订号做乐观锁。
type SeriesBible struct {
	Id        int    `json:"id"`
	ProjectId int    `json:"project_id" gorm:"uniqueIndex"`
	UserId    int    `json:"user_id" gorm:"index"`
	Data      string `json:"-" gorm:"type:longtext"`
	Revision  int    `json:"revision" gorm:"default:1"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint"`

	BibleView json.RawMessage `json:"bible" gorm:"-"`
}

func (SeriesBible) TableName() string { return "series_bible" }

// Bible 圣经内容。
type Bible struct {
	Title      string           `json:"title"`
	Logline    string           `json:"logline"`
	Characters []BibleCharacter `json:"characters"`
	Props      []BibleProp      `json:"props"`
	Threads    []BibleThread    `json:"threads"`
	Facts      []BibleFact      `json:"facts"`
	Episodes   []BibleEpisode   `json:"episodes"`
}

// BibleCharacter 人物：身份固定的部分（外貌 / 声音）+ 关系 + 知道哪些事实。
type BibleCharacter struct {
	Key           string              `json:"key"`
	Name          string              `json:"name"`
	Look          string              `json:"look"`
	Voice         string              `json:"voice,omitempty"`
	VoiceStyle    string              `json:"voice_style,omitempty"`
	Traits        string              `json:"traits,omitempty"`
	Relationships []BibleRelationship `json:"relationships"`
	// Knows 该角色知道的事实 key（与观众知道的区分，避免后续剧情提前泄密）
	Knows []string `json:"knows"`
}

type BibleRelationship struct {
	To       string `json:"to"`
	Relation string `json:"relation"`
}

// BibleProp 关键道具：现在在谁手里、什么状态。
type BibleProp struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Holder string `json:"holder"`
	State  string `json:"state"`
	Note   string `json:"note,omitempty"`
}

// BibleThread 伏笔 / 未解决的问题。
type BibleThread struct {
	Key        string `json:"key"`
	Title      string `json:"title"`
	Status     string `json:"status"` // open / resolved
	OpenedIn   int    `json:"opened_in"`
	ResolvedIn int    `json:"resolved_in,omitempty"`
}

// BibleFact 已揭示的信息：观众是否知道、哪些角色知道。
type BibleFact struct {
	Key           string   `json:"key"`
	Text          string   `json:"text"`
	RevealedIn    int      `json:"revealed_in"`
	AudienceKnows bool     `json:"audience_knows"`
	KnownBy       []string `json:"known_by"`
}

// BibleEpisode 一集的连续性记录。
type BibleEpisode struct {
	No            int               `json:"no"`
	ScreenplayId  int               `json:"screenplay_id"`
	Title         string            `json:"title"`
	Summary       string            `json:"summary"`
	Revealed      []string          `json:"revealed"`      // 事实 key
	Open          []string          `json:"open"`          // 线索 key
	Relationships []string          `json:"relationships"` // 本集人物关系变化（文本）
	PropChanges   []BiblePropChange `json:"prop_changes"`
	Characters    []string          `json:"characters"` // 出场人物 key
	UpdatedAt     int64             `json:"updated_at"`
}

type BiblePropChange struct {
	Prop   string `json:"prop"`
	Holder string `json:"holder"`
	State  string `json:"state"`
}

const (
	BibleMaxCharacters = 20
	BibleMaxProps      = 40
	BibleMaxThreads    = 60
	BibleMaxFacts      = 120
	BibleMaxEpisodes   = 60
	BibleTextMax       = 300
)

var ErrBibleInvalid = errors.New("剧集设定不合法")
var ErrBibleVersionConflict = errors.New("剧集设定已在其他地方修改，请刷新后再保存")

// Normalize 截断、补 key、校验引用（关系 / 知情 / 道具持有人 / 事实揭示集都必须指向存在的对象）。
func (b *Bible) Normalize() error {
	trim := func(s string, n int) string { return truncRunes(strings.TrimSpace(s), n) }
	b.Title, b.Logline = trim(b.Title, 60), trim(b.Logline, BibleTextMax)
	if len(b.Characters) > BibleMaxCharacters {
		return fmt.Errorf("%w：人物最多 %d 个", ErrBibleInvalid, BibleMaxCharacters)
	}
	if len(b.Props) > BibleMaxProps || len(b.Threads) > BibleMaxThreads || len(b.Facts) > BibleMaxFacts || len(b.Episodes) > BibleMaxEpisodes {
		return fmt.Errorf("%w：道具 / 线索 / 事实 / 集数超出上限", ErrBibleInvalid)
	}
	charKeys := map[string]bool{}
	for i := range b.Characters {
		c := &b.Characters[i]
		c.Key = trim(c.Key, 20)
		if c.Key == "" {
			c.Key = "c" + itoa(i+1)
		}
		if charKeys[c.Key] {
			return fmt.Errorf("%w：人物 key %s 重复", ErrBibleInvalid, c.Key)
		}
		charKeys[c.Key] = true
		c.Name, c.Look, c.Voice, c.VoiceStyle, c.Traits = trim(c.Name, 30), trim(c.Look, BibleTextMax), trim(c.Voice, 40), trim(c.VoiceStyle, 40), trim(c.Traits, BibleTextMax)
		if c.Name == "" {
			return fmt.Errorf("%w：人物 %s 缺少名字", ErrBibleInvalid, c.Key)
		}
		if c.Relationships == nil {
			c.Relationships = []BibleRelationship{}
		}
		if c.Knows == nil {
			c.Knows = []string{}
		}
	}
	for i := range b.Characters {
		c := &b.Characters[i]
		rels := make([]BibleRelationship, 0, len(c.Relationships))
		for _, r := range c.Relationships {
			r.To, r.Relation = trim(r.To, 20), trim(r.Relation, 100)
			if r.To == "" || r.Relation == "" {
				continue
			}
			if !charKeys[r.To] {
				return fmt.Errorf("%w：%s 的关系指向不存在的人物 %s", ErrBibleInvalid, c.Name, r.To)
			}
			rels = append(rels, r)
		}
		c.Relationships = rels
	}
	propKeys := map[string]bool{}
	for i := range b.Props {
		p := &b.Props[i]
		p.Key = trim(p.Key, 20)
		if p.Key == "" {
			p.Key = "p" + itoa(i+1)
		}
		if propKeys[p.Key] {
			return fmt.Errorf("%w：道具 key %s 重复", ErrBibleInvalid, p.Key)
		}
		propKeys[p.Key] = true
		p.Name, p.Holder, p.State, p.Note = trim(p.Name, 40), trim(p.Holder, 20), trim(p.State, 100), trim(p.Note, BibleTextMax)
		if p.Name == "" {
			return fmt.Errorf("%w：道具 %s 缺少名字", ErrBibleInvalid, p.Key)
		}
		if p.Holder != "" && !charKeys[p.Holder] {
			return fmt.Errorf("%w：道具「%s」的持有人 %s 不存在", ErrBibleInvalid, p.Name, p.Holder)
		}
	}
	threadKeys := map[string]bool{}
	for i := range b.Threads {
		t := &b.Threads[i]
		t.Key = trim(t.Key, 20)
		if t.Key == "" {
			t.Key = "t" + itoa(i+1)
		}
		if threadKeys[t.Key] {
			return fmt.Errorf("%w：线索 key %s 重复", ErrBibleInvalid, t.Key)
		}
		threadKeys[t.Key] = true
		t.Title = trim(t.Title, 100)
		if t.Title == "" {
			return fmt.Errorf("%w：线索 %s 缺少标题", ErrBibleInvalid, t.Key)
		}
		if t.Status != "resolved" {
			t.Status = "open"
			t.ResolvedIn = 0
		}
		if t.OpenedIn < 0 {
			t.OpenedIn = 0
		}
	}
	factKeys := map[string]bool{}
	for i := range b.Facts {
		f := &b.Facts[i]
		f.Key = trim(f.Key, 20)
		if f.Key == "" {
			f.Key = "f" + itoa(i+1)
		}
		if factKeys[f.Key] {
			return fmt.Errorf("%w：事实 key %s 重复", ErrBibleInvalid, f.Key)
		}
		factKeys[f.Key] = true
		f.Text = trim(f.Text, BibleTextMax)
		if f.Text == "" {
			return fmt.Errorf("%w：事实 %s 缺少内容", ErrBibleInvalid, f.Key)
		}
		if f.RevealedIn < 0 {
			f.RevealedIn = 0
		}
		known := make([]string, 0, len(f.KnownBy))
		for _, k := range f.KnownBy {
			k = trim(k, 20)
			if k == "" {
				continue
			}
			if !charKeys[k] {
				return fmt.Errorf("%w：事实「%s」的知情人 %s 不存在", ErrBibleInvalid, truncRunes(f.Text, 12), k)
			}
			known = append(known, k)
		}
		f.KnownBy = known
	}
	for i := range b.Characters {
		c := &b.Characters[i]
		knows := make([]string, 0, len(c.Knows))
		for _, k := range c.Knows {
			k = trim(k, 20)
			if k != "" && factKeys[k] {
				knows = append(knows, k)
			}
		}
		c.Knows = knows
	}
	for i := range b.Episodes {
		e := &b.Episodes[i]
		if e.No <= 0 {
			e.No = i + 1
		}
		e.Title, e.Summary = trim(e.Title, 60), trim(e.Summary, 1000)
		e.Revealed = filterKeys(e.Revealed, factKeys)
		e.Open = filterKeys(e.Open, threadKeys)
		e.Characters = filterKeys(e.Characters, charKeys)
		rels := make([]string, 0, len(e.Relationships))
		for _, r := range e.Relationships {
			if r = trim(r, 200); r != "" {
				rels = append(rels, r)
			}
		}
		e.Relationships = rels
		changes := make([]BiblePropChange, 0, len(e.PropChanges))
		for _, pc := range e.PropChanges {
			pc.Prop, pc.Holder, pc.State = trim(pc.Prop, 20), trim(pc.Holder, 20), trim(pc.State, 100)
			if pc.Prop == "" || !propKeys[pc.Prop] {
				continue
			}
			if pc.Holder != "" && !charKeys[pc.Holder] {
				pc.Holder = ""
			}
			changes = append(changes, pc)
		}
		e.PropChanges = changes
	}
	if b.Characters == nil {
		b.Characters = []BibleCharacter{}
	}
	if b.Props == nil {
		b.Props = []BibleProp{}
	}
	if b.Threads == nil {
		b.Threads = []BibleThread{}
	}
	if b.Facts == nil {
		b.Facts = []BibleFact{}
	}
	if b.Episodes == nil {
		b.Episodes = []BibleEpisode{}
	}
	return nil
}

func filterKeys(list []string, allowed map[string]bool) []string {
	out := make([]string, 0, len(list))
	seen := map[string]bool{}
	for _, k := range list {
		k = strings.TrimSpace(k)
		if k == "" || !allowed[k] || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// CharacterByKey / PropByKey 查找。
func (b *Bible) CharacterByKey(key string) *BibleCharacter {
	for i := range b.Characters {
		if b.Characters[i].Key == key {
			return &b.Characters[i]
		}
	}
	return nil
}

func (b *Bible) PropByKey(key string) *BibleProp {
	for i := range b.Props {
		if b.Props[i].Key == key {
			return &b.Props[i]
		}
	}
	return nil
}

func (s *SeriesBible) FillView() {
	if s.Data == "" {
		s.BibleView = json.RawMessage(`{"title":"","logline":"","characters":[],"props":[],"threads":[],"facts":[],"episodes":[]}`)
		return
	}
	s.BibleView = json.RawMessage(s.Data)
}

func (s *SeriesBible) Parse() (*Bible, error) {
	b := &Bible{}
	if s.Data == "" {
		_ = b.Normalize()
		return b, nil
	}
	if err := json.Unmarshal([]byte(s.Data), b); err != nil {
		return nil, err
	}
	return b, nil
}

// GetSeriesBible 取项目的圣经；没有返回 ErrNotFound。
func GetSeriesBible(projectId int) (*SeriesBible, error) {
	row := &SeriesBible{}
	if err := DB.Where("project_id = ?", projectId).First(row).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	row.FillView()
	return row, nil
}

// SaveSeriesBible 乐观锁保存：首次 revision 传 0。返回新修订号。
func SaveSeriesBible(userId, projectId, revision int, b *Bible) (int, error) {
	data, err := json.Marshal(b)
	if err != nil {
		return 0, err
	}
	now := common.GetTimestamp()
	next := 0
	err = DB.Transaction(func(tx *gorm.DB) error {
		row := &SeriesBible{}
		err := tx.Where("project_id = ?", projectId).First(row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if revision != 0 {
				return ErrBibleVersionConflict
			}
			next = 1
			return tx.Create(&SeriesBible{ProjectId: projectId, UserId: userId, Data: string(data), Revision: 1, UpdatedAt: now}).Error
		}
		if err != nil {
			return err
		}
		res := tx.Model(&SeriesBible{}).Where("id = ? AND revision = ?", row.Id, revision).
			Updates(map[string]any{"data": string(data), "revision": gorm.Expr("revision + 1"), "updated_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrBibleVersionConflict
		}
		next = revision + 1
		return nil
	})
	return next, err
}
