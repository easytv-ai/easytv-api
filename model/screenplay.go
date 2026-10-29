package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// Screenplay 成片工作室的策划案（T23）：一句话 / 剧本 → 结构化 plan → 建画布。
type Screenplay struct {
	Id            int    `json:"id"`
	UserId        int    `json:"user_id" gorm:"index"`
	ProjectId     int    `json:"project_id" gorm:"default:0"`
	CanvasId      int    `json:"canvas_id" gorm:"default:0;comment:build 后的画布"`
	Input         string `json:"input" gorm:"type:text;comment:一句话或剧本"`
	TargetSeconds int    `json:"target_seconds" gorm:"default:30"`
	Style         string `json:"style" gorm:"type:varchar(100);default:''"`
	Model         string `json:"model" gorm:"type:varchar(100);default:'';comment:策划案用的文本模型"`
	Plan          string `json:"-" gorm:"type:longtext;comment:策划案 JSON"`
	Status        int    `json:"status" gorm:"index;default:0;comment:0 生成中 1 待确认 2 已建画布 -1 失败"`
	Error         string `json:"error" gorm:"type:varchar(500);default:''"`
	TaskNo        string `json:"task_no" gorm:"type:varchar(32);default:'';comment:扣分流水号"`
	PointsUsed    int    `json:"points_used" gorm:"default:0"`
	// EpisodeNo 集号（API.md 27）：同一项目里的第几集，0 = 未编号
	EpisodeNo int `json:"episode_no" gorm:"default:0;index"`
	// Revision 剧本修订号：每次 PUT plan / 翻译写回 +1；BuiltRevision 画布基于哪一稿（build / sync 时记）。API.md 24.1
	Revision      int            `json:"revision" gorm:"default:0;comment:剧本修订号"`
	BuiltRevision int            `json:"built_revision" gorm:"default:0;comment:画布基于的修订号"`
	CreatedAt     int64          `json:"created_at" gorm:"bigint"`
	UpdatedAt     int64          `json:"updated_at" gorm:"bigint"`
	DeletedAt     gorm.DeletedAt `json:"-" gorm:"index"`

	PlanView json.RawMessage `json:"plan" gorm:"-"`
	// Stale 已建画布且剧本在建画布后又改过（revision != built_revision），前端据此提示「同步到画布」
	Stale bool `json:"stale" gorm:"-"`
}

func (Screenplay) TableName() string { return "screenplay" }

const (
	ScreenplayStatusGenerating = 0
	ScreenplayStatusReady      = 1
	ScreenplayStatusBuilt      = 2
	ScreenplayStatusFailed     = -1

	ScreenplayMaxShots      = 12
	ScreenplayMaxCharacters = 4
	ScreenplayMaxScenes     = 4
)

// Plan 策划案结构（API.md 8.2）。
type Plan struct {
	Title      string          `json:"title"`
	Style      string          `json:"style"`
	Aspect     string          `json:"aspect"`
	Characters []PlanCharacter `json:"characters"`
	Scenes     []PlanScene     `json:"scenes"`
	Shots      []PlanShot      `json:"shots"`
}

type PlanCharacter struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Look string `json:"look"`
	// Voice 角色声音（TTS 音色名，如 nova）；VoiceStyle 说话风格（如「沉稳」），拼进配音提示（API.md 25.1）
	Voice      string `json:"voice,omitempty"`
	VoiceStyle string `json:"voice_style,omitempty"`
}

// PlanLine 一条对白（API.md 25.1）：谁说、说什么、什么情绪；Lines 多语言译文。
// Speaker 为人物 key，空串 / narrator 表示旁白。
type PlanLine struct {
	Speaker string            `json:"speaker"`
	Text    string            `json:"text"`
	Emotion string            `json:"emotion,omitempty"`
	Lines   map[string]string `json:"lines,omitempty"`
}

const (
	PlanMaxDialogue = 12
	PlanLineMaxLen  = 200
	PlanStateMaxLen = 100
	PlanNarrator    = "narrator"
)

type PlanScene struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Look string `json:"look"`
	// 场次表（API.md 27.2）：谁想得到什么 / 受到什么阻碍 / 结束时发生什么变化
	Objective string `json:"objective,omitempty"`
	Conflict  string `json:"conflict,omitempty"`
	Turn      string `json:"turn,omitempty"`
}

type PlanShot struct {
	// Id 稳定镜头标识（sh_xxxxxxxx）：改稿 / 删中间一镜后画布节点仍按它对应，No 只是展示序号（API.md 24.5）
	Id         string   `json:"id,omitempty"`
	No         int      `json:"no"`
	Scene      string   `json:"scene"`
	Characters []string `json:"characters"`
	Prompt     string   `json:"prompt"`
	Motion     string   `json:"motion"`
	Line       string   `json:"line"`
	Seconds    int      `json:"seconds"`
	// Lines 多语言台词（F5）：语种 → 译文，缺失回退 Line
	Lines map[string]string `json:"lines,omitempty"`
	// Dialogue 对白条目（API.md 25.1）：非空时 Line 由它拼出（各条正文用空格连接），配音按条分段、字幕按条出
	Dialogue []PlanLine `json:"dialogue,omitempty"`
	// States 本镜人物状态（API.md 25.2）：人物 key → 服装 / 持物 / 伤痕等随剧情变化的描述，拼进分镜与视频提示词
	States map[string]string `json:"states,omitempty"`
}

// CharacterByKey 找人物；没有返回 nil。
func (p *Plan) CharacterByKey(key string) *PlanCharacter {
	for i := range p.Characters {
		if p.Characters[i].Key == key {
			return &p.Characters[i]
		}
	}
	return nil
}

// SpeakerName 对白说话人的展示名：旁白 → 「旁白」，找不到的 key 原样返回。
func (p *Plan) SpeakerName(speaker string) string {
	if speaker == "" || speaker == PlanNarrator {
		return "旁白"
	}
	if c := p.CharacterByKey(speaker); c != nil {
		return c.Name
	}
	return speaker
}

var ErrPlanInvalid = errors.New("策划案格式不正确")

// ErrPlanLimit 超出上限（镜头 / 人物 / 场景）。原稿保持不动，不再静默截断（复审 D03）。
var ErrPlanLimit = errors.New("策划案超出上限")

// ErrPlanRef 镜头引用了不存在的人物 / 场景（复审 D03）。
var ErrPlanRef = errors.New("策划案引用不存在")

// Normalize 裁剪文本长度、补默认值、校验上限与引用；返回错误表示不可用，出错时不修改 p 的镜头 / 人物 / 场景数量。
// 超限（镜头 > 12 / 人物 > 4 / 场景 > 4）返回 ErrPlanLimit 并说明上限与实际数量；
// 镜头引用了不存在的人物或场景 key 返回 ErrPlanRef 并指出是哪一镜。
func (p *Plan) Normalize(targetSeconds int) error {
	if len(p.Shots) > ScreenplayMaxShots {
		return fmt.Errorf("%w：镜头最多 %d 个，当前 %d 个，请删减镜头或拆成多集", ErrPlanLimit, ScreenplayMaxShots, len(p.Shots))
	}
	if len(p.Characters) > ScreenplayMaxCharacters {
		return fmt.Errorf("%w：人物最多 %d 个，当前 %d 个", ErrPlanLimit, ScreenplayMaxCharacters, len(p.Characters))
	}
	if len(p.Scenes) > ScreenplayMaxScenes {
		return fmt.Errorf("%w：场景最多 %d 个，当前 %d 个", ErrPlanLimit, ScreenplayMaxScenes, len(p.Scenes))
	}
	if len(p.Shots) == 0 {
		return ErrPlanInvalid
	}
	p.Title = truncRunes(strings.TrimSpace(p.Title), 60)
	p.Style = truncRunes(strings.TrimSpace(p.Style), 100)
	if p.Aspect != "9:16" && p.Aspect != "1:1" {
		p.Aspect = "16:9"
	}
	for i := range p.Characters {
		c := &p.Characters[i]
		if c.Key == "" {
			c.Key = "c" + itoa(i+1)
		}
		c.Name = truncRunes(strings.TrimSpace(c.Name), 30)
		c.Look = truncRunes(strings.TrimSpace(c.Look), 300)
		c.Voice = truncRunes(strings.TrimSpace(c.Voice), 40)
		c.VoiceStyle = truncRunes(strings.TrimSpace(c.VoiceStyle), 40)
	}
	for i := range p.Scenes {
		s := &p.Scenes[i]
		if s.Key == "" {
			s.Key = "s" + itoa(i+1)
		}
		s.Name = truncRunes(strings.TrimSpace(s.Name), 30)
		s.Look = truncRunes(strings.TrimSpace(s.Look), 300)
		s.Objective = truncRunes(strings.TrimSpace(s.Objective), 200)
		s.Conflict = truncRunes(strings.TrimSpace(s.Conflict), 200)
		s.Turn = truncRunes(strings.TrimSpace(s.Turn), 200)
	}
	perShot := 5
	if targetSeconds > 0 && len(p.Shots) > 0 {
		perShot = targetSeconds / len(p.Shots)
		if perShot < 3 {
			perShot = 3
		}
		if perShot > 10 {
			perShot = 10
		}
	}
	charKeys := map[string]bool{}
	for _, c := range p.Characters {
		charKeys[c.Key] = true
	}
	sceneKeys := map[string]bool{}
	for _, s := range p.Scenes {
		sceneKeys[s.Key] = true
	}
	seenIds := map[string]bool{}
	for i := range p.Shots {
		s := &p.Shots[i]
		s.No = i + 1
		s.Id = strings.TrimSpace(s.Id)
		if s.Id == "" || len(s.Id) > 32 || seenIds[s.Id] {
			s.Id = NewPlanShotId()
		}
		seenIds[s.Id] = true
		s.Prompt = truncRunes(strings.TrimSpace(s.Prompt), 500)
		s.Motion = truncRunes(strings.TrimSpace(s.Motion), 100)
		s.Line = truncRunes(strings.TrimSpace(s.Line), 200)
		s.Scene = strings.TrimSpace(s.Scene)
		if s.Prompt == "" {
			return ErrPlanInvalid
		}
		if s.Seconds < 3 || s.Seconds > 10 {
			s.Seconds = perShot
		}
		// 引用校验：场景可以留空（未指定），非空必须存在；人物去掉空串后必须都存在
		if s.Scene != "" && !sceneKeys[s.Scene] {
			return fmt.Errorf("%w：第 %d 镜引用了不存在的场景 %s", ErrPlanRef, s.No, s.Scene)
		}
		chars := make([]string, 0, len(s.Characters))
		for _, ck := range s.Characters {
			ck = strings.TrimSpace(ck)
			if ck == "" {
				continue
			}
			if !charKeys[ck] {
				return fmt.Errorf("%w：第 %d 镜引用了不存在的人物 %s", ErrPlanRef, s.No, ck)
			}
			chars = append(chars, ck)
		}
		s.Characters = chars
		// 对白条目：去空、截断、限条数，说话人必须是人物或旁白；非空时台词由对白拼出
		if len(s.Dialogue) > PlanMaxDialogue {
			return fmt.Errorf("%w：第 %d 镜对白最多 %d 条，当前 %d 条", ErrPlanLimit, s.No, PlanMaxDialogue, len(s.Dialogue))
		}
		dialogue := make([]PlanLine, 0, len(s.Dialogue))
		for _, d := range s.Dialogue {
			d.Text = truncRunes(strings.TrimSpace(d.Text), PlanLineMaxLen)
			if d.Text == "" {
				continue
			}
			d.Speaker = strings.TrimSpace(d.Speaker)
			if d.Speaker == PlanNarrator {
				d.Speaker = ""
			}
			if d.Speaker != "" && !charKeys[d.Speaker] {
				return fmt.Errorf("%w：第 %d 镜对白引用了不存在的人物 %s", ErrPlanRef, s.No, d.Speaker)
			}
			d.Emotion = truncRunes(strings.TrimSpace(d.Emotion), 30)
			for k, v := range d.Lines {
				d.Lines[k] = truncRunes(strings.TrimSpace(v), PlanLineMaxLen)
			}
			dialogue = append(dialogue, d)
		}
		s.Dialogue = dialogue
		if len(s.Dialogue) > 0 {
			texts := make([]string, 0, len(s.Dialogue))
			for _, d := range s.Dialogue {
				texts = append(texts, d.Text)
			}
			s.Line = truncRunes(strings.Join(texts, " "), 500)
		}
		// 本镜人物状态：只认人物 key
		if len(s.States) > 0 {
			states := map[string]string{}
			for k, v := range s.States {
				k, v = strings.TrimSpace(k), truncRunes(strings.TrimSpace(v), PlanStateMaxLen)
				if k == "" || v == "" {
					continue
				}
				if !charKeys[k] {
					return fmt.Errorf("%w：第 %d 镜人物状态引用了不存在的人物 %s", ErrPlanRef, s.No, k)
				}
				states[k] = v
			}
			if len(states) == 0 {
				states = nil
			}
			s.States = states
		}
	}
	return nil
}

// TotalSeconds 镜头时长合计。
func (p *Plan) TotalSeconds() int {
	n := 0
	for _, s := range p.Shots {
		n += s.Seconds
	}
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }

// truncRunes 按字符数截断。
func truncRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// Insert / Get / Save --------------------------------------------------------

func (s *Screenplay) Insert() error {
	now := common.GetTimestamp()
	s.CreatedAt, s.UpdatedAt = now, now
	return DB.Create(s).Error
}

func GetUserScreenplay(userId, id int) (*Screenplay, error) {
	s := &Screenplay{}
	if err := DB.Where("id = ? AND user_id = ?", id, userId).First(s).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	s.FillView()
	return s, nil
}

// GetScreenplayByCanvas 建过这张画布的策划案（取最新一条）。
func GetScreenplayByCanvas(userId, canvasId int) (*Screenplay, error) {
	s := &Screenplay{}
	if err := DB.Where("user_id = ? AND canvas_id = ?", userId, canvasId).Order("id desc").First(s).Error; err != nil {
		return nil, wrapNotFound(err)
	}
	s.FillView()
	return s, nil
}

// FillView 把 plan 字符串填到响应字段，并算出 stale（已建画布且剧本修订号与画布基于的稿不一致）。
func (s *Screenplay) FillView() {
	s.Stale = s.Status == ScreenplayStatusBuilt && s.CanvasId > 0 && s.Revision != s.BuiltRevision
	if s.Plan == "" {
		s.PlanView = json.RawMessage("null")
		return
	}
	s.PlanView = json.RawMessage(s.Plan)
}

// ParsePlan 解析已存的 plan。
func (s *Screenplay) ParsePlan() (*Plan, error) {
	if s.Plan == "" {
		return nil, ErrPlanInvalid
	}
	p := &Plan{}
	if err := json.Unmarshal([]byte(s.Plan), p); err != nil {
		return nil, ErrPlanInvalid
	}
	return p, nil
}

func UpdateScreenplayFields(id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	return DB.Model(&Screenplay{}).Where("id = ?", id).Updates(fields).Error
}

// NewPlanShotId 稳定镜头 id。
func NewPlanShotId() string {
	return "sh_" + common.GetRandomString(8)
}

// GetProjectScreenplays 项目下的策划案（集目录），按集号、创建时间排。
func GetProjectScreenplays(userId, projectId int) ([]*Screenplay, error) {
	var list []*Screenplay
	err := DB.Where("user_id = ? AND project_id = ?", userId, projectId).Order("episode_no asc, id asc").Find(&list).Error
	for _, sp := range list {
		sp.FillView()
	}
	return list, err
}

// NextEpisodeNo 项目下一集的集号。
func NextEpisodeNo(userId, projectId int) int {
	var maxNo int
	DB.Model(&Screenplay{}).Where("user_id = ? AND project_id = ?", userId, projectId).Select("COALESCE(MAX(episode_no),0)").Scan(&maxNo)
	return maxNo + 1
}
