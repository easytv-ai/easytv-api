// seed 把 easytv-web 界面里原本写死的展示数据灌进数据库，让前端改读接口后立刻有内容。
//
//	go run ./cmd/seed            # 使用 .env
//	go run ./cmd/seed --env-file .env.cn
//
// 幂等：按标题/名称判重，重复执行不会产生重复数据。
// 演示账号：SEED_USER（默认 demo@easytv.ai）/ SEED_PASSWORD（未设置时生成随机密码并打印），不存在则创建。
// SEED_RESET_CANVAS=1 强制把示例画布 graph 重置为初始内容（默认只在画布被清空时回填）。
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"easytv-api/common"
	"easytv-api/constant"
	"easytv-api/model"
	"easytv-api/service"

	"gorm.io/gorm"
)

type show struct {
	title, fullTitle, author, tag, src, cover, updatedAt string
	likes                                                int
}

// 与 easytv-web/app/composables/useShowcaseShows.ts 一致
var shows = []show{
	{"星空山巅", "概念短片《星空山巅》", "Scenic", "电影美学", "/videos/showcase/scenic.mp4", "/images/showcase/mountain.jpg", "2026-08-29 17:01", 128},
	{"礁岸纪实", "概念短片《礁岸纪实》", "Studio", "电影美学", "/videos/showcase/coast.mp4", "/images/showcase/coast.jpg", "2026-08-21 11:26", 86},
	{"林间飞瀑", "概念短片《林间飞瀑》", "Nature", "电影美学", "/videos/showcase/waterfall.mp4", "/images/showcase/waterfall.jpg", "2026-08-18 09:40", 64},
	{"青春纪实", "概念短片《青春纪实》", "Campus", "短剧", "/videos/login-hero.mp4", "/images/showcase/portrait.jpg", "2026-08-12 20:15", 50},
	{"音乐现场", "概念短片《音乐现场》", "Beat", "音乐 MV", "/videos/showcase/music.mp4", "/images/showcase/music.jpg", "2026-08-08 16:33", 42},
	{"航拍海浪", "概念短片《航拍海浪》", "Aerial", "商业广告", "/videos/showcase/ocean.mp4", "/images/showcase/ocean.jpg", "2026-08-02 14:08", 37},
	{"夜城未眠", "概念短片《夜城未眠》", "Neon", "商业广告", "/videos/showcase/city-night.mp4", "/images/showcase/city-night.jpg", "2026-08-27 22:18", 96},
	{"赛博夜雨", "概念短片《赛博夜雨》", "Pixel", "动画游戏", "/videos/showcase/neon-rain.mp4", "/images/showcase/neon-rain.jpg", "2026-08-25 19:42", 154},
	{"荒漠公路", "概念短片《荒漠公路》", "Wander", "电影美学", "/videos/showcase/desert-road.mp4", "/images/showcase/desert-road.jpg", "2026-08-19 15:06", 73},
	{"都市漫步", "概念短片《都市漫步》", "Street", "短剧", "/videos/showcase/city-walk.mp4", "/images/showcase/city-walk.jpg", "2026-08-16 10:28", 61},
	{"晚自习后", "概念短片《晚自习后》", "Campus", "短剧", "/videos/login-hero.mp4", "/images/showcase/portrait.jpg", "2026-08-14 21:05", 88},
	{"节拍坠落", "概念短片《节拍坠落》", "Beat", "音乐 MV", "/videos/showcase/music.mp4", "/images/showcase/music.jpg", "2026-08-11 18:44", 119},
	{"浪潮之城", "概念短片《浪潮之城》", "Studio", "商业广告", "/videos/showcase/ocean.mp4", "/images/showcase/ocean.jpg", "2026-08-07 13:20", 45},
	{"山巅副本", "概念短片《山巅副本》", "Pixel", "动画游戏", "/videos/showcase/scenic.mp4", "/images/showcase/mountain.jpg", "2026-08-05 16:12", 102},
}

type skill struct {
	title, desc, author, uses, tag, cover, slug string
}

// 与 easytv-web/app/pages/agent/index.vue + CanvasChat.vue 一致
var skills = []skill{
	{"东方巨构美学短剧", "一站式生成东方巨构美学短剧", "鱿鱼chill", "2.8k", "短剧漫剧", "/images/showcase/mountain.jpg", "/oriental-megastructure"},
	{"仙侠氛围美学短片", "仙侠氛围美学短片一键生成", "Studio", "1.4k", "专业影视", "/images/showcase/waterfall.jpg", "/xianxia-short"},
	{"电影感礁岸纪实", "低饱和实景质感，克制运镜成片", "Nature", "3.1k", "专业影视", "/images/showcase/coast.jpg", ""},
	{"人物口播成片", "人物特写与生活流叙事一键成片", "Campus", "980", "自媒体创作", "/images/showcase/portrait.jpg", ""},
	{"音乐现场卡点", "节奏剪辑、光影呼吸与情绪推进", "Beat", "2.1k", "音乐MV", "/images/showcase/music.jpg", "/live-music-cut"},
	{"品牌大片运镜", "大场面调度与电影感航拍成片", "Aerial", "1.8k", "商业广告", "/images/showcase/ocean.jpg", ""},
	{"夜城霓虹广告", "都市夜景光影，30 秒品牌广告成片", "Neon", "2.4k", "商业广告", "/images/showcase/city-night.jpg", "/neon-city-ad"},
	{"赛博夜雨短片", "霓虹、雨水与游戏感镜头语言", "Pixel", "3.6k", "动漫游戏", "/images/showcase/neon-rain.jpg", ""},
	{"荒漠公路公路片", "公路、尘土与宽银幕电影气质", "Wander", "1.2k", "专业影视", "/images/showcase/desert-road.jpg", "/desert-road-film"},
	{"都市漫步短剧", "街头跟拍与生活流对白一键成片", "Street", "1.6k", "短剧漫剧", "/images/showcase/city-walk.jpg", "/city-walk-drama"},
	{"晚自习后短剧", "校园夜色、人物关系与情绪推进", "Campus", "2.9k", "短剧漫剧", "/images/showcase/portrait.jpg", ""},
	{"节拍坠落 MV", "鼓点卡点、灯光呼吸与舞台运镜", "Beat", "4.2k", "音乐MV", "/images/showcase/music.jpg", ""},
	{"浪潮之城广告", "海面航拍与产品植入一体成片", "Studio", "1.1k", "商业广告", "/images/showcase/ocean.jpg", ""},
	{"山巅副本文案", "游戏世界观开场与史诗运镜", "Pixel", "2.2k", "动漫游戏", "/images/showcase/mountain.jpg", ""},
	{"林间飞瀑旅拍", "自然风光旅拍与旁白一键生成", "Nature", "870", "自媒体创作", "/images/showcase/waterfall.jpg", ""},
	{"通用分镜脚本", "一句话拆镜头、对白与转场", "EasyTV", "5.8k", "通用技能", "/images/showcase/coast.jpg", ""},
	{"口播提词成片", "提词、人像特写与字幕自动对齐", "EasyTV", "3.3k", "通用技能", "/images/showcase/portrait.jpg", ""},
	{"发现：公路叙事", "从一张风景图扩写成公路电影", "Wander", "640", "发现", "/images/showcase/desert-road.jpg", ""},
	{"皮克斯动画短片", "皮克斯质感的三维动画短片", "EasyTV", "1.9k", "动漫游戏", "/images/showcase/mountain.jpg", "/pixar-animation"},
	{"爆款短片复刻", "参考热门短片结构快速复刻", "EasyTV", "2.6k", "自媒体创作", "/images/showcase/city-night.jpg", "/viral-video-remake"},
}

var coverVideo = map[string]string{
	"/images/showcase/mountain.jpg":    "/videos/showcase/scenic.mp4",
	"/images/showcase/waterfall.jpg":   "/videos/showcase/waterfall.mp4",
	"/images/showcase/coast.jpg":       "/videos/showcase/coast.mp4",
	"/images/showcase/portrait.jpg":    "/videos/login-hero.mp4",
	"/images/showcase/music.jpg":       "/videos/showcase/music.mp4",
	"/images/showcase/ocean.jpg":       "/videos/showcase/ocean.mp4",
	"/images/showcase/city-night.jpg":  "/videos/showcase/city-night.mp4",
	"/images/showcase/neon-rain.jpg":   "/videos/showcase/neon-rain.mp4",
	"/images/showcase/desert-road.jpg": "/videos/showcase/desert-road.mp4",
	"/images/showcase/city-walk.jpg":   "/videos/showcase/city-walk.mp4",
}

var sceneByTag = map[string]string{
	"短剧漫剧":  "东方美学短剧、国风电视剧",
	"专业影视":  "电影感短片、概念预告",
	"商业广告":  "品牌广告、产品植入成片",
	"动漫游戏":  "游戏过场、赛博短片",
	"音乐MV":  "现场卡点、情绪 MV",
	"自媒体创作": "口播、人物纪实",
	"通用技能":  "分镜脚本、提词成片",
	"发现":    "从一张图扩写成短片",
}

// 官方 Skill 的执行契约（API.md 1.6）：只对文字对话生效，规定这个技能
// 按什么步骤问、按什么格式交付。填了才有用，所以按分类各给一份。
var guideByTag = map[string]string{
	"短剧漫剧": `1. 先确认三件事再动笔：总集数与单集时长、主角关系、爽点节奏（第几秒抛钩子）。用户没说就先问，不要自行假设。
2. 先给一句话主线和人物小传，等用户确认后再展开分集。
3. 每集必须有一个钩子结尾，钩子写在该集最后一行并单独标出。
4. 台词口语化，单句不超过 20 字；旁白只在转场用，一集最多两处。`,
	"专业影视": `1. 先问清片长、画幅、成片用途（预告 / 正片 / 概念片），以及有没有已定的美术参考。
2. 按场 → 镜的层级推进：先列场次梗概，确认后再写分镜。
3. 每个镜头必须写明景别、机位运动、时长（秒）和声音设计，缺一项就补问。
4. 不要在分镜里塞入无法拍摄的描述，比如人物内心活动。`,
	"商业广告": `1. 先问清品牌、核心卖点、投放渠道和时长，以及必须出现的品牌元素（logo、slogan、包装）。
2. 产出前先给 2~3 个创意方向的一句话概括，让用户挑，不要一上来写完整脚本。
3. 选定方向后按「钩子 → 痛点 → 产品介入 → 证据 → 行动号召」的结构展开。
4. 所有涉及功效、销量、排名的表述都要标注「需品牌方提供依据」。`,
	"动漫游戏": `1. 先确认世界观设定、画风参考、角色阵容，以及这是过场动画还是宣传片。
2. 角色第一次出现时给一段外观定稿描述，后续镜头统一引用，不要每镜重写。
3. 战斗或运动镜头要写清动作节拍和打击感落点。
4. 保持设定自洽，出现新设定时明确标注是新增的。`,
	"音乐MV": `1. 先问清歌曲风格、BPM 或时长、有没有歌词，以及是表演向还是叙事向。
2. 按歌曲结构分段（前奏 / 主歌 / 副歌 / 间奏 / 尾奏），每段给画面基调与剪辑节奏。
3. 卡点位置要标到秒，副歌必须有一个记忆点画面。
4. 不要让画面逐句复述歌词。`,
	"自媒体创作": `1. 先问清平台、时长、账号人设和这条内容的目标（涨粉 / 带货 / 科普）。
2. 前 3 秒必须是钩子，单独写出来并说明为什么能留住人。
3. 口播稿按说话节奏断行，每行就是一句能一口气说完的话。
4. 结尾给明确的互动引导，不要写成硬广。`,
	"通用技能": `1. 先把用户的需求复述成一句话目标，确认无误再开工；信息不足就一次性把要问的都问完。
2. 分步骤推进，每步产出可直接使用的内容，不要只给方法论。
3. 涉及具体数字、名称、时间时，没有依据就标注「待确认」，不要编造。`,
	"发现": `1. 先看用户给的图或一句话，复述你从中读到的主体、场景和情绪，确认理解一致。
2. 由这个起点扩写成一段可拍的短片构想：先给整体概念，再给 3~5 个关键镜头。
3. 每个镜头写清画面内容和它承担的叙事作用。`,
}

// 输出格式按分类分两类：需要逐镜交付的走表格，偏文案的走分段。
var outputFormatByTag = map[string]string{
	"短剧漫剧":  "先一句话主线，再按「第 N 集｜标题｜梗概｜钩子」逐集列出。展开单集时用「镜号 | 画面 | 台词 | 时长」表格。",
	"专业影视":  "场次用「场号｜地点｜时间｜内容」列出；分镜用「镜号 | 景别 | 机位运动 | 画面 | 声音 | 时长(秒)」表格。",
	"商业广告":  "创意方向阶段只给编号 + 一句话。脚本阶段用「时间轴(秒) | 画面 | 配音/字幕 | 品牌元素」表格。",
	"动漫游戏":  "先给角色外观定稿（每角色一段），再用「镜号 | 画面 | 动作节拍 | 时长(秒)」表格列分镜。",
	"音乐MV":  "按歌曲段落分节，每节给「时间区间 | 画面基调 | 关键镜头 | 剪辑节奏」四项。",
	"自媒体创作": "先单独给出前 3 秒钩子，再给口播全文（按说话节奏断行），最后一行是互动引导。",
	"通用技能":  "用编号步骤输出，每步一个小标题加具体内容。需要用户确认的地方单独起一行以「待确认：」开头。",
	"发现":    "先一段整体概念，再用「镜号 | 画面 | 叙事作用」列出关键镜头。",
}

func main() {
	envFile := flag.String("env-file", "", "环境变量文件路径（默认 .env）")
	flag.Parse()
	if *envFile != "" {
		common.EnvFile = *envFile
	}
	if err := common.LoadEnvFile(); err != nil {
		fmt.Println("load env failed:", err)
	}
	common.InitEnv()
	if err := model.InitDB(); err != nil {
		panic(err)
	}

	user := ensureDemoUser()
	fmt.Printf("demo user: id=%d username=%s\n", user.Id, user.Username)

	seedWorks(user.Id)
	seedSkills()
	seedProject(user.Id)
	seedAssets(user.Id)
	seedBilling(user.Id)
	seedContent()
	seedCategories()
	seedFeatured()
	if n, err := model.SeedStyles(); err != nil {
		fmt.Println("seed styles failed:", err)
	} else {
		fmt.Printf("styles: created %d\n", n)
	}
	fmt.Println("seed done")
}

func ensureDemoUser() *model.User {
	username := common.GetEnvOrDefaultString("SEED_USER", "demo@easytv.ai")
	password := os.Getenv("SEED_PASSWORD")
	if password == "" {
		password = common.GetRandomString(16)
		fmt.Printf("SEED_PASSWORD 未设置，演示账号已生成随机密码（仅本次输出）：%s\n", password)
	}
	if u, err := model.GetUserByEmail(username); err == nil {
		return u
	}
	hashed, err := common.Password2Hash(password)
	if err != nil {
		panic(err)
	}
	u := &model.User{
		Username: username,
		Password: hashed,
		Nickname: "EasyTV 演示",
		Role:     constant.RoleCommonUser,
		Status:   constant.UserStatusEnabled,
		Group:    "default",
	}
	if err := u.Insert(); err != nil {
		panic(err)
	}
	fmt.Printf("created demo user %s / %s\n", username, password)
	return u
}

func parseTime(s string) int64 {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		return common.GetTimestamp()
	}
	return t.Unix()
}

func parseUses(s string) int {
	s = strings.TrimSpace(strings.ToLower(s))
	if strings.HasSuffix(s, "k") {
		f, _ := strconv.ParseFloat(strings.TrimSuffix(s, "k"), 64)
		return int(f * 1000)
	}
	n, _ := strconv.Atoi(s)
	return n
}

func seedWorks(userId int) {
	created := 0
	for i, s := range shows {
		var count int64
		model.DB.Unscoped().Model(&model.Work{}).Where("user_id = ? AND title = ?", userId, s.title).Count(&count)
		if count > 0 {
			// 已存在则回填分类
			model.DB.Model(&model.Work{}).Where("user_id = ? AND title = ? AND category = ''", userId, s.title).Update("category", s.tag)
			continue
		}
		w := &model.Work{
			UserId:      userId,
			Title:       s.title,
			Category:    s.tag,
			Description: s.fullTitle + " · " + s.tag + " · " + s.author,
			CoverUrl:    s.cover,
			VideoUrl:    s.src,
			Status:      model.WorkStatusPublished,
			LikeCount:   s.likes,
			ViewCount:   s.likes*7 + i*13,
			PublishedAt: parseTime(s.updatedAt),
		}
		if err := w.Insert(); err != nil {
			panic(err)
		}
		// 保持"更新时间"与界面一致
		model.DB.Model(w).Updates(map[string]any{"created_at": w.PublishedAt, "updated_at": w.PublishedAt})
		created++
	}
	fmt.Printf("works: +%d (total %d)\n", created, len(shows))
}

func seedSkills() {
	created := 0
	for i, s := range skills {
		var count int64
		model.DB.Unscoped().Model(&model.Skill{}).Where("title = ?", s.title).Count(&count)
		if count > 0 {
			// 已存在则回填执行契约（老库 seed 过一轮时 guide 还是空的）
			if g := guideByTag[s.tag]; g != "" {
				model.DB.Model(&model.Skill{}).
					Where("title = ? AND user_id = 0 AND (guide IS NULL OR guide = '')", s.title).
					Updates(map[string]any{"guide": g, "output_format": outputFormatByTag[s.tag]})
			}
			continue
		}
		uses := parseUses(s.uses)
		sk := &model.Skill{
			Title:        s.title,
			Slug:         s.slug,
			Description:  s.desc,
			AuthorName:   s.author,
			Category:     s.tag,
			CoverUrl:     s.cover,
			VideoUrl:     coverVideo[s.cover],
			Scene:        sceneByTag[s.tag],
			Howto:        "一份剧本或一句话，可选人脸/造型参考图",
			Output:       "角色/道具/场景资产图、逐镜分镜表格、视频成片",
			Prompt:       "请以「" + s.title + "」的风格创作：{{prompt}}",
			Guide:        guideByTag[s.tag],
			OutputFormat: outputFormatByTag[s.tag],
			Uses:         uses,
			Stars:        int(float64(uses) * 0.4),
			SortOrder:    len(skills) - i,
			Status:       model.SkillStatusOn,
		}
		if err := sk.Insert(); err != nil {
			panic(err)
		}
		created++
	}
	fmt.Printf("skills: +%d (total %d)\n", created, len(skills))
}

func seedProject(userId int) {
	const name = "示例：星空山巅分镜"
	var count int64
	model.DB.Unscoped().Model(&model.Project{}).Where("user_id = ? AND name = ?", userId, name).Count(&count)
	graph := `{"nodes":[
{"id":"text-1","type":"easytv","position":{"x":80,"y":160},"width":252,"height":220,"style":{"width":"252px","height":"220px"},"data":{"kind":"text","label":"文本节点 1","content":"夜幕降临，镜头从山脚缓缓上摇，星河横贯天际。","prompt":"","mode":"idle"}},
{"id":"image-1","type":"easytv","position":{"x":420,"y":120},"width":320,"height":220,"style":{"width":"320px","height":"220px"},"data":{"kind":"image","label":"图片节点 1","content":"","prompt":"星空下的雪山山巅，电影感，冷色调","mode":"idle"}},
{"id":"video-1","type":"easytv","position":{"x":820,"y":160},"width":320,"height":220,"style":{"width":"320px","height":"220px"},"data":{"kind":"video","label":"视频节点 1","content":"","prompt":"镜头缓慢推进，星轨流动，5 秒","mode":"idle"}}
],"edges":[
{"id":"e-text-1-image-1","source":"text-1","target":"image-1","sourceHandle":"out","targetHandle":"in","type":"easytv"},
{"id":"e-image-1-video-1","source":"image-1","target":"video-1","sourceHandle":"out","targetHandle":"in","type":"easytv"}
],"viewport":{"x":40,"y":40,"zoom":0.9}}`
	if count > 0 {
		// 已存在：示例画布被清空时回填 graph（幂等修复）
		var existing model.Project
		model.DB.Unscoped().Where("user_id = ? AND name = ?", userId, name).First(&existing)
		var cvs []*model.Canvas
		model.DB.Where("user_id = ? AND project_id = ?", userId, existing.Id).Find(&cvs)
		for _, cv := range cvs {
			if cv.NodeCount() == 0 || common.GetEnvOrDefaultBool("SEED_RESET_CANVAS", false) {
				model.DB.Model(cv).Updates(map[string]any{"graph": graph, "version": gorm.Expr("version + 1"), "updated_at": common.GetTimestamp()})
				fmt.Printf("project: exists, canvas %d graph restored\n", cv.Id)
			}
		}
		if len(cvs) == 0 {
			fmt.Println("project: exists")
		}
		return
	}
	p := &model.Project{UserId: userId, Name: name, Description: "seed 生成的示例项目，可直接在画布里打开", CoverUrl: "/images/showcase/mountain.jpg"}
	if err := p.Insert(); err != nil {
		panic(err)
	}
	cv := &model.Canvas{UserId: userId, ProjectId: p.Id, Name: name, Graph: graph}
	if err := cv.Insert(); err != nil {
		panic(err)
	}
	fmt.Printf("project: +1 (id=%d canvas=%d)\n", p.Id, cv.Id)
}

func seedAssets(userId int) {
	items := []struct {
		name, url, mime, typ string
	}{
		{"mountain.jpg", "/images/showcase/mountain.jpg", "image/jpeg", model.AssetTypeImage},
		{"coast.jpg", "/images/showcase/coast.jpg", "image/jpeg", model.AssetTypeImage},
		{"neon-rain.jpg", "/images/showcase/neon-rain.jpg", "image/jpeg", model.AssetTypeImage},
		{"scenic.mp4", "/videos/showcase/scenic.mp4", "video/mp4", model.AssetTypeVideo},
		{"city-night.mp4", "/videos/showcase/city-night.mp4", "video/mp4", model.AssetTypeVideo},
		{"music.mp4", "/videos/showcase/music.mp4", "video/mp4", model.AssetTypeVideo},
	}
	created := 0
	for _, it := range items {
		var count int64
		model.DB.Unscoped().Model(&model.Asset{}).Where("user_id = ? AND name = ?", userId, it.name).Count(&count)
		if count > 0 {
			continue
		}
		a := &model.Asset{UserId: userId, Type: it.typ, Name: it.name, Url: it.url, Mime: it.mime, Source: model.AssetSourceUpload}
		if err := a.Insert(); err != nil {
			panic(err)
		}
		created++
	}
	fmt.Printf("assets: +%d (total %d)\n", created, len(items))
}

// seedBilling 会员计划 / 积分包 / 促销 / 模型定价（与 easytv-web pricing-plans.ts 一致）+ 演示账号赠送积分。
func seedBilling(userId int) {
	plans := []model.MembershipPlan{
		{Code: "starter", Name: "入门", MonthlyOrigin: 199, MonthlyPrice: 129, MonthlyPoints: 800, AddOnPoints: 400, AddOnPrice: 36, Concurrent: 6, StorageGb: 50, SortOrder: 0},
		{Code: "creator", Name: "创作", Featured: true, MonthlyOrigin: 399, MonthlyPrice: 259, MonthlyPoints: 1800, AddOnPoints: 800, AddOnPrice: 69, Concurrent: 10, StorageGb: 130, SortOrder: 1},
		{Code: "pro", Name: "专业", MonthlyOrigin: 999, MonthlyPrice: 599, MonthlyPoints: 5800, AddOnPoints: 2400, AddOnPrice: 199, Concurrent: 16, StorageGb: 200, SortOrder: 2},
		{Code: "flagship", Name: "旗舰", MonthlyOrigin: 1999, MonthlyPrice: 999, MonthlyPoints: 15800, AddOnPoints: 6000, AddOnPrice: 459, Concurrent: 20, StorageGb: 500, SortOrder: 3},
	}
	created := 0
	for i := range plans {
		if _, err := model.GetMembershipPlanByCode(plans[i].Code); err == nil {
			continue
		}
		if err := plans[i].Insert(); err != nil {
			panic(err)
		}
		created++
	}
	fmt.Printf("plans: +%d (total %d)\n", created, len(plans))

	packs := []model.PointPack{
		{Code: "p30", Points: 600, ValidDays: 30, Origin: 39, MemberPrice: 26, SortOrder: 0},
		{Code: "p90", Points: 1800, ValidDays: 90, Origin: 99, MemberPrice: 69, SortOrder: 1},
		{Code: "p365", Points: 6000, ValidDays: 365, Origin: 299, MemberPrice: 199, Featured: true, SortOrder: 2},
		{Code: "p730", Points: 20000, ValidDays: 730, Origin: 799, MemberPrice: 499, SortOrder: 3},
	}
	created = 0
	for i := range packs {
		if _, err := model.GetPointPackByCode(packs[i].Code); err == nil {
			continue
		}
		if err := packs[i].Insert(); err != nil {
			panic(err)
		}
		created++
	}
	fmt.Printf("packs: +%d (total %d)\n", created, len(packs))

	if model.GetSetting(model.SettingKeyBillingPromo) == "" {
		promo := model.DefaultBillingPromo()
		promo.Subtitle = "创作会员限时优惠，视频模型会员价结算，9 月 11 日 24 点截止"
		promo.EndsAt = parseTime("2026-09-12 00:00")
		if err := model.SaveBillingPromo(promo); err != nil {
			panic(err)
		}
		fmt.Println("promo: saved")
	}

	// 模型定价：模型标识需与 EASYAPI_*_MODELS 一致，这里用 .env 示例值
	prices := []model.ModelPrice{
		{Model: "kling-v1", Type: model.TaskTypeVideo, Label: "可灵 1.0 · 720P", Unit: model.PriceUnitSecond, PointsPerUnit: 8, MemberPointsPerUnit: 6},
		{Model: "jimeng_vgfm_t2v_l20", Type: model.TaskTypeVideo, Label: "即梦 T2V", Unit: model.PriceUnitSecond, PointsPerUnit: 5, MemberPointsPerUnit: 4},
		{Model: "gpt-image-1", Type: model.TaskTypeImage, Label: "GPT Image 1", Unit: model.PriceUnitImage, PointsPerUnit: 2},
		{Model: "flux-schnell", Type: model.TaskTypeImage, Label: "Flux Schnell", Unit: model.PriceUnitImage, PointsPerUnit: 1},
		{Model: "gpt-4o-mini", Type: model.TaskTypeText, Label: "GPT-4o mini", Unit: model.PriceUnitCall, PointsPerUnit: 1},
		{Model: "deepseek-chat", Type: model.TaskTypeText, Label: "DeepSeek V3", Unit: model.PriceUnitCall, PointsPerUnit: 1},
		{Model: "tts-1", Type: model.TaskTypeAudio, Label: "TTS 1", Unit: model.PriceUnitCall, PointsPerUnit: 2},
	}
	created = 0
	for i := range prices {
		var count int64
		model.DB.Model(&model.ModelPrice{}).Where("model = ?", prices[i].Model).Count(&count)
		if count > 0 {
			continue
		}
		if err := prices[i].Insert(); err != nil {
			panic(err)
		}
		created++
	}
	fmt.Printf("model prices: +%d (total %d)\n", created, len(prices))

	// 演示账号赠送 1000 积分（有效期 365 天），已赠过不重复
	var giftCount int64
	model.DB.Model(&model.PointLot{}).Where("user_id = ? AND source = ?", userId, model.LotSourceGift).Count(&giftCount)
	if giftCount == 0 {
		if err := service.GiftPoints(userId, 1000, 365, "演示账号赠送积分"); err != nil {
			panic(err)
		}
		fmt.Println("gift: +1000 points to demo user")
	}
}

// seedContent 首页轮播（取展示数据里点赞最高的 5 部）与两条公告。
func seedContent() {
	var count int64
	model.DB.Model(&model.Banner{}).Count(&count)
	if count == 0 {
		// 按 likes 倒序取 5 部
		sorted := append([]show(nil), shows...)
		for i := 0; i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[j].likes > sorted[i].likes {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		created := 0
		for i, s := range sorted[:5] {
			var w model.Work
			link := "/studio"
			if model.DB.Where("title = ?", s.title).First(&w).Error == nil {
				link = fmt.Sprintf("/watch/%d", w.Id)
			}
			b := &model.Banner{
				Title:     s.fullTitle,
				Subtitle:  fmt.Sprintf("%s · %d 喜欢", s.author, s.likes),
				Tag:       s.tag,
				CoverUrl:  s.cover,
				VideoUrl:  s.src,
				LinkUrl:   link,
				Cta:       "立即观看",
				SortOrder: 5 - i,
			}
			if err := b.Insert(); err != nil {
				panic(err)
			}
			created++
		}
		fmt.Printf("banners: +%d\n", created)
	}

	model.DB.Model(&model.Notice{}).Count(&count)
	if count == 0 {
		notices := []model.Notice{
			{Title: "欢迎来到 EasyTV", Content: "创作台已支持自由画布、Skill 与生成任务。\n新注册用户赠送体验积分，积分按批次到期，请在钱包里查看。", Type: model.NoticeTypeSystem, Pinned: true},
			{Title: "年会员限时 6.2 折", Content: "9 月 11 日 24 点前开通年会员享 6.2 折，视频模型按会员价结算。", Type: model.NoticeTypeActivity},
		}
		for i := range notices {
			if err := notices[i].Insert(); err != nil {
				panic(err)
			}
		}
		fmt.Printf("notices: +%d\n", len(notices))
	}
}

// seedCategories 前台筛选标签：成片分类与 Skill 分类（与 easytv-web 原写死的 BASE_TAGS 一致）。
func seedCategories() {
	groups := map[string][]string{
		model.CategoryScopeWork:  {"电影美学", "商业广告", "短剧", "动画游戏", "音乐 MV"},
		model.CategoryScopeSkill: {"专业影视", "商业广告", "短剧漫剧", "动漫游戏", "音乐MV", "自媒体创作", "通用技能", "发现"},
	}
	created := 0
	for scope, names := range groups {
		for i, name := range names {
			var count int64
			model.DB.Model(&model.Category{}).Where("scope = ? AND name = ?", scope, name).Count(&count)
			if count > 0 {
				continue
			}
			cat := &model.Category{Name: name, Scope: scope, SortOrder: i + 1}
			if err := cat.Insert(); err != nil {
				panic(err)
			}
			created++
		}
	}
	fmt.Printf("categories: +%d\n", created)
}

// seedFeatured 精选成片：把点赞最高的 5 部标为精选并按点赞排序；已发布作品的发布审核默认视为通过。
func seedFeatured() {
	var count int64
	model.DB.Model(&model.Work{}).Where("featured = 1").Count(&count)
	if count == 0 {
		var top []*model.Work
		model.DB.Where("status = ?", model.WorkStatusPublished).Order("like_count desc").Limit(5).Find(&top)
		for i, w := range top {
			model.DB.Model(w).Updates(map[string]any{"featured": 1, "sort_order": i + 1})
		}
		fmt.Printf("featured: +%d\n", len(top))
	}
	res := model.DB.Model(&model.Work{}).Where("status = ? AND review_status = ? AND published_at < ?", model.WorkStatusPublished, model.WorkReviewPending, common.GetTimestamp()-60).
		Update("review_status", model.WorkReviewApproved)
	if res.RowsAffected > 0 {
		fmt.Printf("review: %d existing works marked approved\n", res.RowsAffected)
	}
}
