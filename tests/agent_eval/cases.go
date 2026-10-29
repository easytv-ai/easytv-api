// Package agent_eval 是对话式工作流生成（API.md 32）的评测台：
// 用例集 + 声明式期望 + schema/结构/可执行三轴打分 + 结果落盘。
//
// 边界（docs/plans/agent-contract.md 第 10 节 + 第 0.2 节安全红线）：
//   - 只用 service 的纯函数与注入的假目录（AgentModelCatalog/AgentPricer），
//     **不连库、不扣积分、不提交真实任务、不写库**。
//   - 判断 live 是否可用只读 .env 文本与 setting 约定，不连库、不补齐配置。
//   - 可执行轴是纯静态判定；即使打开 AGENT_EVAL_EXECUTE 也不会提交真实任务（契约 10.2 / 12.3）。
package agent_eval

import "fmt"

// ===== 用例 schema（契约 10.1，字段名冻结）=====

// Expect 一条用例的声明式期望。每一项都必须能机器判定（判定代码见 score.go），不靠人读。
type Expect struct {
	// Kinds 期望出现的节点 kind 集合，按命中率计分；空表示不检查。
	Kinds []string `json:"kinds"`
	// MinNodes / MaxNodes 节点数闭区间；落在区间外按到区间的距离线性衰减。
	MinNodes int `json:"min_nodes"`
	MaxNodes int `json:"max_nodes"`
	// MustEdges 必需连线，按 kind 对表达，如 [["text","video"]]；命中数/期望数计分。
	MustEdges [][2]string `json:"must_edges"`
	// NeedFirstFrame 期望存在 video/director 节点，其上游含 image 节点。
	NeedFirstFrame bool `json:"need_first_frame"`
	// MaxDuration 0 = 不检查；>0 时任何 video/director 节点的 duration 不得超它。
	// 判定落在静态可执行轴（ExecutionAxis）——结构轴四项权重是冻结的，不能塞进第五项。
	MaxDuration int `json:"max_duration"`
	// MaxPoints 0 = 不检查；>0 且计划估价超它 → 结构分打对折（不是硬零，契约 10.2）。
	//
	// ⚠️ 这是**打分层**字段，绝不能注入 service.AgentLoopInput.MaxPoints：
	// 注入后会变成 OVER_BUDGET（error）→ 该条 change 被丢弃 → normalized 为空 → 先命中
	// 「空图硬零」，打对折那条路径永远走不到。跑 RunAgentRounds 时 MaxPoints 一律传 0。
	MaxPoints int `json:"max_points"`
	// AllowNoChange true = 纯提问用例：changes 必须为空且 reply 非空才算通过（契约 4.3 例外）。
	AllowNoChange bool `json:"allow_no_change"`
}

// Case 一条评测用例。
type Case struct {
	Id     string `json:"id"`    // c01..c12（顺序即契约 10.1 的覆盖面清单）
	Brief  string `json:"brief"` // 中文创作需求原文，直接作为 question 喂给模型
	Mode   string `json:"mode"`  // generate / patch
	Expect Expect `json:"expect"`
}

// Problems 返回用例自身不自洽的地方（空表示没问题）。自测会逐条跑这个函数。
func (c Case) Problems() []string {
	var out []string
	if c.Id == "" {
		out = append(out, "id 为空")
	}
	if c.Brief == "" {
		out = append(out, "brief 为空")
	}
	if c.Mode != "generate" && c.Mode != "patch" {
		out = append(out, "mode 必须是 generate 或 patch，got "+c.Mode)
	}
	e := c.Expect
	if e.AllowNoChange {
		// 纯提问用例不该有结构期望（没有图可判）
		if len(e.Kinds) > 0 || e.MinNodes > 0 || e.MaxNodes > 0 || len(e.MustEdges) > 0 {
			out = append(out, "AllowNoChange 用例不该带图结构期望")
		}
		return out
	}
	if len(e.Kinds) == 0 {
		out = append(out, "Kinds 为空（非纯提问用例必须给出期望 kind 集合）")
	}
	if e.MinNodes <= 0 || e.MaxNodes <= 0 {
		out = append(out, "MinNodes/MaxNodes 必须为正")
	} else if e.MinNodes > e.MaxNodes {
		out = append(out, fmt.Sprintf("MinNodes(%d) > MaxNodes(%d)", e.MinNodes, e.MaxNodes))
	}
	if len(e.MustEdges) == 0 {
		out = append(out, "MustEdges 为空（非纯提问用例必须至少给一条必需连线）")
	}
	for _, pair := range e.MustEdges {
		if pair[0] == "" || pair[1] == "" {
			out = append(out, "MustEdges 里有空 kind")
		}
	}
	return out
}

// Cases 全部用例，顺序固定（c01..c12），与契约 10.1 的覆盖面清单一一对应：
// 竖屏短视频 / 人物口播 / 产品广告 / 音乐卡点 MV / 短剧分镜 / 图生视频首尾帧 /
// 配音旁白 / 全景图 / 3D 导演台 / 爆款复刻 / 纯提问不该改图 / 超预算应给更省方案。
func Cases() []Case {
	return []Case{
		{
			Id:    "c01",
			Brief: "做一条 15 秒竖屏短视频：先写一句口播文案，再用一张 9:16 的产品图当画面，最后做成 5 秒的竖屏视频。",
			Mode:  "generate",
			Expect: Expect{
				Kinds:          []string{"text", "image", "video"},
				MinNodes:       3,
				MaxNodes:       5,
				MustEdges:      [][2]string{{"text", "image"}, {"image", "video"}},
				NeedFirstFrame: true,
				MaxDuration:    5,
			},
		},
		{
			Id:    "c02",
			Brief: "做一条人物口播视频：一段口播文案，配一张人物出镜图，生成 5 秒口播视频，再加一段背景音乐。",
			Mode:  "generate",
			Expect: Expect{
				Kinds:          []string{"text", "image", "video", "audio"},
				MinNodes:       4,
				MaxNodes:       6,
				MustEdges:      [][2]string{{"text", "audio"}, {"image", "video"}},
				NeedFirstFrame: true,
				MaxDuration:    5,
			},
		},
		{
			Id:    "c03",
			Brief: "给新品做一条产品广告：产品图 + 卖点文案，配欢快女声旁白，出 5 秒和 10 秒两版视频。",
			Mode:  "generate",
			Expect: Expect{
				Kinds:          []string{"text", "image", "video", "audio"},
				MinNodes:       4,
				MaxNodes:       4,
				MustEdges:      [][2]string{{"image", "video"}, {"text", "audio"}},
				NeedFirstFrame: true,
				MaxDuration:    10,
			},
		},
		{
			Id:    "c04",
			Brief: "做一条音乐卡点 MV：三张不同场景图，各配一个 5 秒镜头，再配一段节奏感强的音乐。",
			Mode:  "generate",
			Expect: Expect{
				Kinds:          []string{"image", "video", "audio"},
				MinNodes:       4,
				MaxNodes:       9,
				MustEdges:      [][2]string{{"image", "video"}},
				NeedFirstFrame: true,
				MaxDuration:    5,
			},
		},
		{
			Id:    "c05",
			Brief: "做一部竖屏短剧的分镜：先写剧本，再拆出三个分镜画面，各出一个 5 秒镜头，并配一段旁白。",
			Mode:  "generate",
			Expect: Expect{
				Kinds:          []string{"text", "image", "video", "audio"},
				MinNodes:       4,
				MaxNodes:       9,
				MustEdges:      [][2]string{{"text", "image"}, {"image", "video"}},
				NeedFirstFrame: true,
				MaxDuration:    5,
			},
		},
		{
			Id:    "c06",
			Brief: "把两张图做成首尾帧视频：第一张图当首帧、第二张当尾帧，生成一段 5 秒的过渡视频。",
			Mode:  "generate",
			Expect: Expect{
				Kinds:          []string{"image", "video"},
				MinNodes:       2,
				MaxNodes:       4,
				MustEdges:      [][2]string{{"image", "video"}},
				NeedFirstFrame: true,
				MaxDuration:    5,
			},
		},
		{
			Id:    "c07",
			Brief: "给一段旁白配音：先写旁白文案，再合成女声配音，并配一张封面图。",
			Mode:  "generate",
			Expect: Expect{
				Kinds:       []string{"text", "audio", "image"},
				MinNodes:    3,
				MaxNodes:    4,
				MustEdges:   [][2]string{{"text", "audio"}, {"text", "image"}},
				MaxDuration: 0,
			},
		},
		{
			Id:    "c08",
			Brief: "做一张 360 全景图：先写一段场景描述，再出 2:1 的全景图。",
			Mode:  "generate",
			Expect: Expect{
				Kinds:     []string{"text", "panorama"},
				MinNodes:  2,
				MaxNodes:  3,
				MustEdges: [][2]string{{"text", "panorama"}},
			},
		},
		{
			Id:    "c09",
			Brief: "用 3D 导演台做一段 5 秒的运镜视频，先写一段镜头描述。",
			Mode:  "generate",
			Expect: Expect{
				Kinds:          []string{"text", "director"},
				MinNodes:       2,
				MaxNodes:       3,
				MustEdges:      [][2]string{{"text", "director"}},
				NeedFirstFrame: true,
				MaxDuration:    5,
			},
		},
		{
			Id:    "c10",
			Brief: "复刻这条爆款视频的结构：一个钩子文案、一张封面图、一个 5 秒主镜头，再来一段口播配音。",
			Mode:  "generate",
			Expect: Expect{
				Kinds:          []string{"text", "image", "video", "audio"},
				MinNodes:       4,
				MaxNodes:       5,
				MustEdges:      [][2]string{{"text", "image"}, {"image", "video"}, {"text", "audio"}},
				NeedFirstFrame: true,
				MaxDuration:    5,
			},
		},
		{
			// 纯提问：用户只想问原因，不想改图。契约 4.3 的例外条款就是为这条用例开的：
			// 没有它，这条用例即使完全答对也永远 SchemaOk=false，通过率上限被钉死在 11/12。
			Id:    "c11",
			Brief: "这条竖屏视频为什么一直生成失败？我只是问问，先别改我的图。",
			Mode:  "patch",
			Expect: Expect{
				AllowNoChange: true,
			},
		},
		{
			// 超预算：期望 100 积分以内。打分层判它是否超预算并打对折（不是硬零），
			// 跑循环时 MaxPoints 传 0 让模型自由出图（契约 2.6 / 10.2）。
			Id:    "c12",
			Brief: "预算 100 积分以内，做一条 10 秒竖屏产品视频：先出产品图，再生成 10 秒视频。",
			Mode:  "generate",
			Expect: Expect{
				Kinds:          []string{"image", "video"},
				MinNodes:       2,
				MaxNodes:       3,
				MustEdges:      [][2]string{{"image", "video"}},
				NeedFirstFrame: true,
				MaxDuration:    10,
				MaxPoints:      100,
			},
		},
	}
}

// CaseIDs 用例 id 列表，顺序与 Cases() 一致。
func CaseIDs() []string {
	all := Cases()
	ids := make([]string, 0, len(all))
	for _, c := range all {
		ids = append(ids, c.Id)
	}
	return ids
}

// CaseByID 按 id 找用例；找不到返回 false。
func CaseByID(id string) (Case, bool) {
	for _, c := range Cases() {
		if c.Id == id {
			return c, true
		}
	}
	return Case{}, false
}
