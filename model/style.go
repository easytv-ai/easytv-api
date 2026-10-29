package model

import (
	"errors"
	"strings"

	"easytv-api/common"

	"gorm.io/gorm"
)

// ===== 风格库（L5，运营手工录入，见 API.md 34）=====

// Style 一个有名字的整体风格：封面 + 一段拼到生成提示词末尾的片段。
// user_id 预留给 L18 用户自定义风格，0 表示官方。
type Style struct {
	Id       int    `json:"id" gorm:"comment:风格ID"`
	UserId   int    `json:"user_id" gorm:"index;default:0;comment:0 官方，>0 用户自定义"`
	Name     string `json:"name" gorm:"type:varchar(50);comment:风格名"`
	CoverUrl string `json:"cover_url" gorm:"type:varchar(1000);default:'';comment:封面图"`
	// Prompt 生成时追加到节点提示词末尾的片段
	Prompt   string `json:"prompt" gorm:"type:text;comment:风格提示词片段"`
	Negative string `json:"negative" gorm:"type:varchar(500);default:'';comment:负向提示词（可选）"`
	Tags     string `json:"tags" gorm:"type:varchar(255);default:'';comment:标签，逗号分隔"`
	Category string `json:"category" gorm:"type:varchar(50);default:'';index;comment:分类"`
	// Kinds 适用节点类型，逗号分隔的 image / video 子集；空 = 都适用
	Kinds     string `json:"kinds" gorm:"type:varchar(50);default:'';comment:适用类型 image,video，空为都可"`
	Uses      int    `json:"uses" gorm:"default:0;comment:套用次数"`
	SortOrder int    `json:"sort_order" gorm:"default:0;index;comment:越大越靠前"`
	// Status 四态（API.md 34.5）：1 可用（官方=已上架 / 用户=私有）、2 下架（官方）、3 待审核（用户投稿）、4 已收录（用户投稿通过，公开可见）
	Status int `json:"status" gorm:"index;default:1;comment:1 可用 2 下架 3 待审核 4 已收录"`
	// SourceAssetId 「定风格」时的来源图资产 ID，只留痕
	SourceAssetId int            `json:"source_asset_id" gorm:"default:0;comment:定风格时的来源图资产ID"`
	CreatedAt     int64          `json:"created_at" gorm:"bigint"`
	UpdatedAt     int64          `json:"updated_at" gorm:"bigint"`
	DeletedAt     gorm.DeletedAt `json:"-" gorm:"index"`
}

func (Style) TableName() string { return "style" }

const (
	StyleStatusOn       = 1
	StyleStatusOff      = 2
	StyleStatusPending  = 3 // 用户投稿待审核
	StyleStatusAccepted = 4 // 用户投稿已收录，公开可见

	// UserStyleMax 每个用户最多保存的自定义风格数
	UserStyleMax = 100

	StyleNameMax     = 50
	StylePromptMax   = 2000
	StyleNegativeMax = 500

	StyleKindImage = "image"
	StyleKindVideo = "video"
)

// IsValidStyleKind 校验适用类型。
func IsValidStyleKind(k string) bool {
	return k == StyleKindImage || k == StyleKindVideo
}

// IsValidStyleStatus 状态是否在四态之内。
func IsValidStyleStatus(s int) bool {
	return s >= StyleStatusOn && s <= StyleStatusAccepted
}

// IsPublicStyle 是否对所有人可见：官方上架，或用户投稿已收录。
func IsPublicStyle(s *Style) bool {
	if s == nil {
		return false
	}
	return (s.UserId == 0 && s.Status == StyleStatusOn) || s.Status == StyleStatusAccepted
}

// IsOfficialStyle 是否官方风格。
func (s *Style) IsOfficialStyle() bool { return s.UserId == 0 }

// CanUseStyle 套用计数口径：公开可见，或本人的私有风格（任意状态）。
func CanUseStyle(s *Style, userId int) bool {
	if IsPublicStyle(s) {
		return true
	}
	return s != nil && userId > 0 && s.UserId == userId
}

// publicStyleClause 公开列表 / 分类的可见性 SQL。
const publicStyleClause = "(user_id = 0 AND status = ?) OR status = ?"

func applyPublicStyleFilter(query *gorm.DB) *gorm.DB {
	return query.Where(publicStyleClause, StyleStatusOn, StyleStatusAccepted)
}

// splitCsv 逗号分隔字段拆成数组，去空白去空项。
func splitCsv(s string) []string {
	out := []string{}
	for _, item := range strings.Split(s, ",") {
		if v := strings.TrimSpace(item); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// TagList 拆成数组给前端。
func (s *Style) TagList() []string { return splitCsv(s.Tags) }

// KindList 拆成数组给前端；空数组表示图片 / 视频都适用。
func (s *Style) KindList() []string { return splitCsv(s.Kinds) }

// View 列表 / 详情响应；admin 为 true 时附带状态、排序与时间。
func (s *Style) View(admin bool) map[string]any {
	view := map[string]any{
		"id":        s.Id,
		"name":      s.Name,
		"cover_url": s.CoverUrl,
		"prompt":    s.Prompt,
		"negative":  s.Negative,
		"tags":      s.TagList(),
		"category":  s.Category,
		"kinds":     s.KindList(),
		"uses":      s.Uses,
	}
	if admin {
		view["user_id"] = s.UserId
		view["sort_order"] = s.SortOrder
		view["status"] = s.Status
		view["source_asset_id"] = s.SourceAssetId
		view["created_at"] = s.CreatedAt
		view["updated_at"] = s.UpdatedAt
	}
	return view
}

// OwnerView 「我的风格」视图：公开视图 + 状态、来源图与时间。
func (s *Style) OwnerView() map[string]any {
	view := s.View(false)
	view["status"] = s.Status
	view["source_asset_id"] = s.SourceAssetId
	view["created_at"] = s.CreatedAt
	view["updated_at"] = s.UpdatedAt
	return view
}

// CountUserStyles 用户名下（未软删）的风格数，用于 100 条上限。
func CountUserStyles(userId int) (int64, error) {
	var n int64
	err := DB.Model(&Style{}).Where("user_id = ?", userId).Count(&n).Error
	return n, err
}

// UpdateStyleFields 按 ID 更新若干字段，自动补 updated_at。
func UpdateStyleFields(id int, fields map[string]any) error {
	fields["updated_at"] = common.GetTimestamp()
	return DB.Model(&Style{}).Where("id = ?", id).Updates(fields).Error
}

func (s *Style) Insert() error {
	now := common.GetTimestamp()
	s.CreatedAt, s.UpdatedAt = now, now
	if s.Status == 0 {
		s.Status = StyleStatusOn
	}
	return DB.Create(s).Error
}

// GetStyleById 按 ID 查询。
func GetStyleById(id int) (*Style, error) {
	s := &Style{}
	if err := DB.Where("id = ?", id).First(s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return s, nil
}

// StyleFilter 列表过滤条件。
type StyleFilter struct {
	Keyword  string
	Tag      string
	Category string
	// Kind image / video：命中 kinds 为空（都可）或包含该类型的行
	Kind string
	// Status 为 nil 不按状态过滤
	Status *int
	// UserId 为 nil 不按归属过滤；「我的列表」传本人 ID
	UserId *int
	// Source official（user_id = 0）/ user（user_id > 0）/ 空不限
	Source string
	// Public 为 true 叠加公开可见口径：(user_id = 0 AND status = 1) OR status = 4
	Public bool
}

const (
	StyleSourceOfficial = "official"
	StyleSourceUser     = "user"
)

// applyStyleFilter 把过滤条件拼到查询上（列表与计数共用）。
func applyStyleFilter(query *gorm.DB, f StyleFilter) *gorm.DB {
	if f.Keyword != "" {
		like := "%" + f.Keyword + "%"
		query = query.Where("name LIKE ? OR prompt LIKE ?", like, like)
	}
	if f.Tag != "" {
		query = query.Where("tags LIKE ?", "%"+f.Tag+"%")
	}
	if f.Category != "" {
		query = query.Where("category = ?", f.Category)
	}
	query = applyStyleKindFilter(query, f.Kind)
	if f.Status != nil {
		query = query.Where("status = ?", *f.Status)
	}
	if f.UserId != nil {
		query = query.Where("user_id = ?", *f.UserId)
	}
	switch f.Source {
	case StyleSourceOfficial:
		query = query.Where("user_id = 0")
	case StyleSourceUser:
		query = query.Where("user_id > 0")
	}
	if f.Public {
		query = applyPublicStyleFilter(query)
	}
	return query
}

// applyStyleKindFilter 把 kind 过滤拼到查询上：空 kinds 表示都适用。
func applyStyleKindFilter(query *gorm.DB, kind string) *gorm.DB {
	if kind == "" {
		return query
	}
	return query.Where("kinds = '' OR kinds LIKE ?", "%"+kind+"%")
}

// GetStyles 分页列表，按 sort_order 降序、id 降序。
func GetStyles(f StyleFilter, page *common.PageInfo) (list []*Style, total int64, err error) {
	query := applyStyleFilter(DB.Model(&Style{}), f)
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err = query.Order("sort_order desc, id desc").
		Offset(page.GetStartIdx()).Limit(page.GetPageSize()).Find(&list).Error
	return list, total, err
}

// GetStyleCategories 前台分类页签：公开可见风格的非空分类去重，按出现顺序（sort_order 降序）。
func GetStyleCategories() ([]string, error) {
	var rows []string
	if err := applyPublicStyleFilter(DB.Model(&Style{})).Where("category <> ''").
		Order("sort_order desc, id desc").Pluck("category", &rows).Error; err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []string{}
	for _, row := range rows {
		v := strings.TrimSpace(row)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out, nil
}

// IncreaseStyleUses 套用风格时 +1。
func IncreaseStyleUses(id int) {
	DB.Model(&Style{}).Where("id = ?", id).Update("uses", gorm.Expr("uses + 1"))
}

// seedStyleRow 官方内置风格。
type seedStyleRow struct {
	name, category, tags, prompt, negative string
}

// OfficialStyleSeeds 首批官方风格（封面留空，运营在管理端上传）。
var OfficialStyleSeeds = []seedStyleRow{
	{"电影胶片", "电影感", "胶片,颗粒,宽银幕", "电影胶片质感，35mm 胶片颗粒，柔和的高光过渡，宽银幕 2.39:1 构图，自然光影层次，色彩略带褪色的暖调", "过度锐化,塑料感,过曝"},
	{"赛博朋克", "氛围", "霓虹,未来,夜景", "赛博朋克风格，霓虹青色与品红光源交错，雨夜街道反光，高对比冷暖光，未来都市的全息广告与雾气", "白天,田园,低饱和"},
	{"日系清新", "摄影", "日系,清新,通透", "日系清新摄影风格，高调曝光，通透空气感，淡青与奶白色调，柔和自然光，画面干净留白", "浓重阴影,高饱和,厚重"},
	{"水墨国风", "插画", "水墨,国风,留白", "中国水墨画风格，宣纸质感，浓淡相宜的墨色晕染，大面积留白，写意山水与朱砂点缀", "写实照片,霓虹,金属"},
	{"黑白极简", "摄影", "黑白,极简,高对比", "黑白极简风格，强烈的明暗对比，简洁几何构图，大量负空间，细腻的灰阶过渡", "彩色,杂乱背景"},
	{"复古港片", "电影感", "港片,复古,霓虹", "九十年代香港电影风格，暖黄钨丝灯与霓虹招牌，潮湿街巷，胶片颗粒，略带绿调的阴影，王家卫式抽帧氛围", "现代都市,干净明亮"},
	{"皮克斯三维", "动画", "三维,皮克斯,卡通", "皮克斯风格三维动画，圆润可爱的角色造型，次表面散射的柔和皮肤质感，明亮饱满的色彩，柔和的全局光照", "写实照片,恐怖,阴暗"},
	{"吉卜力手绘", "动画", "手绘,吉卜力,治愈", "吉卜力工作室手绘动画风格，水彩背景，蓬松的积云与青绿山野，柔和温暖的日光，温馨治愈的氛围", "三维渲染,写实照片,金属质感"},
	{"美式漫画", "插画", "漫画,粗线条,网点", "美式漫画风格，粗黑描边，高饱和平涂色块，网点阴影，动态夸张的透视与分镜感", "柔焦,水彩,写实"},
	{"写实纪录片", "电影感", "纪录片,写实,自然光", "纪录片写实风格，手持镜头的轻微晃动，自然光，克制的色彩，真实的环境细节与生活质感", "夸张特效,卡通,过度调色"},
	{"霓虹夜景", "氛围", "霓虹,夜景,湿地面", "都市霓虹夜景，湿润路面倒映五彩灯光，浅景深散景，暖冷光源对撞，夜色中的电影感", "白天,雾霾,平淡光线"},
	{"暖调生活", "摄影", "暖调,生活,日常", "温暖生活方式摄影，午后阳光斜射，米色与焦糖色调，柔和的窗光，居家与日常的松弛感", "冷调,工业,阴沉"},
	{"冷峻悬疑", "氛围", "悬疑,冷调,阴影", "冷峻悬疑氛围，青蓝低饱和色调，硬光与深邃阴影，雾气弥漫，压抑而克制的构图", "明亮,欢快,暖色"},
	{"梦幻柔焦", "氛围", "梦幻,柔焦,光晕", "梦幻柔焦风格，柔光滤镜，粉紫与浅金色的光晕，轻微过曝的高光，漂浮的微粒与朦胧感", "硬光,锐利,冷峻"},
	{"高级时尚大片", "摄影", "时尚,大片,棚拍", "高级时尚杂志大片风格，棚拍硬光与轮廓光，干净的纯色背景，精致的妆造与姿态，锐利的细节与高级灰调", "随意抓拍,杂乱,低分辨率"},
	{"像素游戏", "动画", "像素,复古游戏,8bit", "复古像素游戏风格，16 位色限制调色板，明显的像素颗粒与抖动，横版卷轴游戏画面感", "写实,平滑渐变,三维渲染"},
}

// SeedStyles 表为空时写入官方内置风格；已有数据直接返回，可重复调用。
func SeedStyles() (created int, err error) {
	var count int64
	if err = DB.Model(&Style{}).Count(&count).Error; err != nil {
		return 0, err
	}
	if count > 0 {
		return 0, nil
	}
	total := len(OfficialStyleSeeds)
	for i, row := range OfficialStyleSeeds {
		s := &Style{
			Name:      row.name,
			Category:  row.category,
			Tags:      row.tags,
			Prompt:    row.prompt,
			Negative:  row.negative,
			SortOrder: total - i,
			Status:    StyleStatusOn,
		}
		if err = s.Insert(); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}
