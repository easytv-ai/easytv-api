package agent_eval

import (
	"encoding/json"
	"strings"
	"sync"

	"easytv-api/service"
)

// ===== 确定性 mock 生成器（关键词 → 固定图）=====
//
// 同一段 brief 永远得到同一张图、同一段 JSON（json.Marshal 对 map 按键排序，输出逐字节稳定）。
// 两个用途：
//  1. tests/agent_eval_test.go 用它跑完整的「循环 → 校验 → 打分」链路（不烧 token），
//     断言已知输入下的精确分数与 stats 字符串；
//  2. 它是「一个合格模型输出」的参照样本——用例期望必须能被它满足，
//     所以关键词映射的 id 也要能被自测断言（防止两条用例的关键词互相遮蔽）。
//
// mock 图刻意留了三处不完美（c03 节点数超上限、c05 少了 audio、c07 少一条连线、c09 没有上游图、
// c12 超预算），让自测覆盖到结构轴的每一种扣分路径与打对折路径。

// mockGraph 是模型输出的图：与前端 CanvasWorkflowGraph（app/utils/canvas-workflow.ts）同构的 JSON 形状。
// 用 map 而不是 service 的类型，是为了让 mock 真的走一遍「JSON → 解析 → 编译」的路径。
type mockGraph struct {
	Version int              `json:"version"`
	Nodes   []map[string]any `json:"nodes"`
	Edges   []map[string]any `json:"edges"`
}

func mockNode(id, kind string, x, y int, data map[string]any) map[string]any {
	return map[string]any{"id": id, "kind": kind, "x": x, "y": y, "data": data}
}

func mockEdge(source, target string) map[string]any {
	return map[string]any{"source": source, "target": target}
}

func textData(label, content string) map[string]any {
	return map[string]any{"label": label, "content": content}
}

func genData(label, prompt, model string, params map[string]any) map[string]any {
	data := map[string]any{"label": label, "prompt": prompt, "model": model}
	if len(params) > 0 {
		data["params"] = params
	}
	return data
}

func videoData(label, prompt, model string, params map[string]any, videoMode string) map[string]any {
	data := genData(label, prompt, model, params)
	if videoMode != "" {
		data["videoMode"] = videoMode
	}
	return data
}

// imageModel / videoModel / audioModel / textModel 是 mock 清单里的模型名（见 NewMockCatalog）。
const (
	mockTextModel  = "gpt-4o-mini"
	mockImageModel = "gpt-image-1"
	mockVideoModel = "kling-v1"
	mockAudioModel = "tts-1"
)

// ===== 每张固定图 =====

func graphShortVertical() mockGraph {
	return mockGraph{Version: 1,
		Nodes: []map[string]any{
			mockNode("n1", "text", 0, 0, textData("口播文案", "15 秒竖屏：3 秒钩子，8 秒卖点，4 秒行动号召")),
			mockNode("n2", "image", 400, 0, genData("产品图", "9:16 竖屏产品特写，干净背景，柔和侧光",
				mockImageModel, map[string]any{"ratio": "9:16", "clarity": "2K"})),
			mockNode("n3", "video", 800, 0, videoData("主镜头", "镜头缓慢推近，产品居中，暖光",
				mockVideoModel, map[string]any{"duration": 5, "ratio": "9:16", "mode": "i2v"}, "i2v")),
		},
		Edges: []map[string]any{mockEdge("n1", "n2"), mockEdge("n2", "n3")},
	}
}

func graphTalkingHead() mockGraph {
	return mockGraph{Version: 1,
		Nodes: []map[string]any{
			mockNode("n1", "text", 0, 0, textData("口播文案", "大家好，今天讲三个省钱小技巧")),
			mockNode("n2", "image", 400, 0, genData("人物出镜图", "半身出镜，正对镜头，室内柔光",
				mockImageModel, map[string]any{"ratio": "9:16"})),
			mockNode("n3", "video", 800, 0, videoData("口播镜头", "人物自然说话，轻微手持感",
				mockVideoModel, map[string]any{"duration": 5, "ratio": "9:16", "mode": "i2v"}, "i2v")),
			mockNode("n4", "audio", 800, 270, genData("背景音乐", "轻快的钢琴铺底，音量压低",
				mockAudioModel, nil)),
		},
		Edges: []map[string]any{mockEdge("n1", "n2"), mockEdge("n2", "n3"), mockEdge("n1", "n4")},
	}
}

// graphProductAd 刻意给 5 个节点（用例上限是 4）→ 节点数项 4/5。
func graphProductAd() mockGraph {
	return mockGraph{Version: 1,
		Nodes: []map[string]any{
			mockNode("n1", "text", 0, 0, textData("卖点文案", "三大卖点：轻、耐用、好看")),
			mockNode("n2", "image", 400, 0, genData("产品图", "产品正面特写，纯白背景",
				mockImageModel, map[string]any{"ratio": "9:16", "clarity": "2K"})),
			mockNode("n3", "video", 800, 0, videoData("5 秒版", "产品旋转展示",
				mockVideoModel, map[string]any{"duration": 5, "ratio": "9:16", "mode": "i2v"}, "i2v")),
			mockNode("n4", "video", 800, 270, videoData("10 秒版", "产品旋转 + 使用场景切换",
				mockVideoModel, map[string]any{"duration": 10, "ratio": "9:16", "mode": "i2v"}, "i2v")),
			mockNode("n5", "audio", 800, 540, genData("女声旁白", "欢快、语速偏快", mockAudioModel, nil)),
		},
		Edges: []map[string]any{
			mockEdge("n1", "n2"), mockEdge("n2", "n3"), mockEdge("n2", "n4"), mockEdge("n1", "n5"),
		},
	}
}

func graphBeatMV() mockGraph {
	return mockGraph{Version: 1,
		Nodes: []map[string]any{
			mockNode("n1", "image", 0, 0, genData("场景一", "夜色霓虹街头", mockImageModel, map[string]any{"ratio": "9:16"})),
			mockNode("n2", "image", 400, 0, genData("场景二", "天台俯瞰城市", mockImageModel, map[string]any{"ratio": "9:16"})),
			mockNode("n3", "image", 800, 0, genData("场景三", "地铁车厢内景", mockImageModel, map[string]any{"ratio": "9:16"})),
			mockNode("n4", "video", 0, 270, videoData("卡点镜头一", "霓虹灯牌闪过，节奏切镜",
				mockVideoModel, map[string]any{"duration": 5, "ratio": "9:16", "mode": "i2v"}, "i2v")),
			mockNode("n5", "video", 400, 270, videoData("卡点镜头二", "镜头快速横移",
				mockVideoModel, map[string]any{"duration": 5, "ratio": "9:16", "mode": "i2v"}, "i2v")),
			mockNode("n6", "video", 800, 270, videoData("卡点镜头三", "人物回头定格",
				mockVideoModel, map[string]any{"duration": 5, "ratio": "9:16", "mode": "i2v"}, "i2v")),
			mockNode("n7", "text", 0, 540, textData("音乐描述", "强节奏电子鼓点，BPM 120，副歌爆发")),
			mockNode("n8", "audio", 400, 540, genData("卡点音乐", "强节奏电子鼓点", mockAudioModel, nil)),
		},
		Edges: []map[string]any{
			mockEdge("n1", "n4"), mockEdge("n2", "n5"), mockEdge("n3", "n6"), mockEdge("n7", "n8"),
		},
	}
}

// graphShortDrama 刻意不带 audio（用例期望 4 个 kind，命中 3 个）→ kind 项 3/4。
func graphShortDrama() mockGraph {
	return mockGraph{Version: 1,
		Nodes: []map[string]any{
			mockNode("n1", "text", 0, 0, textData("剧本", "第一集：主角捡到一只会说话的猫")),
			mockNode("n2", "image", 400, 0, genData("分镜一", "巷口，主角蹲下看纸箱", mockImageModel, map[string]any{"ratio": "9:16"})),
			mockNode("n3", "image", 400, 270, genData("分镜二", "纸箱里一只猫抬头", mockImageModel, map[string]any{"ratio": "9:16"})),
			mockNode("n4", "image", 400, 540, genData("分镜三", "主角惊讶特写", mockImageModel, map[string]any{"ratio": "9:16"})),
			mockNode("n5", "video", 800, 0, videoData("镜头一", "推镜到纸箱", mockVideoModel,
				map[string]any{"duration": 5, "ratio": "9:16", "mode": "i2v"}, "i2v")),
			mockNode("n6", "video", 800, 270, videoData("镜头二", "猫抬头的特写", mockVideoModel,
				map[string]any{"duration": 5, "ratio": "9:16", "mode": "i2v"}, "i2v")),
			mockNode("n7", "video", 800, 540, videoData("镜头三", "主角表情变化", mockVideoModel,
				map[string]any{"duration": 5, "ratio": "9:16", "mode": "i2v"}, "i2v")),
		},
		Edges: []map[string]any{
			mockEdge("n1", "n2"), mockEdge("n2", "n5"), mockEdge("n3", "n6"), mockEdge("n4", "n7"),
		},
	}
}

// graphFirstLastFrame：首帧在上、尾帧在下（网格吸附后 row 0 < row 1），不触发 FRAME_ORDER。
func graphFirstLastFrame() mockGraph {
	return mockGraph{Version: 1,
		Nodes: []map[string]any{
			mockNode("n1", "image", 0, 0, map[string]any{
				"label": "首帧", "prompt": "开场：空荡的客厅", "model": mockImageModel,
				"params": map[string]any{"ratio": "16:9"}, "frameRole": "first",
			}),
			mockNode("n2", "image", 0, 270, map[string]any{
				"label": "尾帧", "prompt": "收尾：客厅里坐满了人", "model": mockImageModel,
				"params": map[string]any{"ratio": "16:9"}, "frameRole": "last",
			}),
			mockNode("n3", "video", 400, 0, videoData("过渡镜头", "从空荡到满座的平滑过渡",
				mockVideoModel, map[string]any{"duration": 5, "ratio": "16:9", "mode": "fl2v"}, "fl2v")),
		},
		Edges: []map[string]any{mockEdge("n1", "n3"), mockEdge("n2", "n3")},
	}
}

// graphVoiceOver 刻意不给封面图连线（用例期望 text→image 与 text→audio 两条）→ 连线项 1/2。
func graphVoiceOver() mockGraph {
	return mockGraph{Version: 1,
		Nodes: []map[string]any{
			mockNode("n1", "text", 0, 0, textData("旁白文案", "深夜的城市，总有一盏灯为你亮着")),
			mockNode("n2", "audio", 400, 0, genData("女声配音", "温柔、语速偏慢", mockAudioModel, nil)),
			mockNode("n3", "image", 0, 270, genData("封面图", "夜景城市，一盏暖色窗灯", mockImageModel,
				map[string]any{"ratio": "9:16"})),
		},
		Edges: []map[string]any{mockEdge("n1", "n2")},
	}
}

func graphPanorama() mockGraph {
	return mockGraph{Version: 1,
		Nodes: []map[string]any{
			mockNode("n1", "text", 0, 0, textData("场景描述", "雪山脚下的湖泊，清晨薄雾，360 度")),
			mockNode("n2", "panorama", 400, 0, genData("全景图", "360 度全景：雪山、湖面、松林",
				mockImageModel, map[string]any{"ratio": "2:1", "clarity": "2K"})),
		},
		Edges: []map[string]any{mockEdge("n1", "n2")},
	}
}

// graphDirector：director 用默认的 reference 模式，没有上游图（用例期望首帧）→ 首帧项 0。
func graphDirector() mockGraph {
	return mockGraph{Version: 1,
		Nodes: []map[string]any{
			mockNode("n1", "text", 0, 0, textData("镜头描述", "3D 导演台：绕人物半圈推近，落地窗自然光")),
			mockNode("n2", "director", 400, 0, genData("运镜镜头", "环绕运镜，从远景推到中景",
				mockVideoModel, map[string]any{"duration": 5, "ratio": "16:9", "mode": "reference"})),
		},
		Edges: []map[string]any{mockEdge("n1", "n2")},
	}
}

func graphViralClone() mockGraph {
	return mockGraph{Version: 1,
		Nodes: []map[string]any{
			mockNode("n1", "text", 0, 0, textData("钩子文案", "前 3 秒：你还在这样擦眼镜吗？")),
			mockNode("n2", "image", 400, 0, genData("封面图", "手拿眼镜的特写，逆光", mockImageModel,
				map[string]any{"ratio": "9:16"})),
			mockNode("n3", "video", 800, 0, videoData("主镜头", "快速对比演示：擦前 / 擦后",
				mockVideoModel, map[string]any{"duration": 5, "ratio": "9:16", "mode": "i2v"}, "i2v")),
			mockNode("n4", "audio", 800, 270, genData("口播配音", "口语化、有节奏、句尾上扬",
				mockAudioModel, nil)),
		},
		Edges: []map[string]any{mockEdge("n1", "n2"), mockEdge("n2", "n3"), mockEdge("n1", "n4")},
	}
}

// graphNoChange：纯提问用例的模型输出——只解释原因，changes 给空数组。
func graphNoChange() mockGraph {
	return mockGraph{Version: 1}
}

// graphBudget 刻意超预算：图片 8 + 视频 12×10 = 128 > 用例的 100 → 结构分打对折。
func graphBudget() mockGraph {
	return mockGraph{Version: 1,
		Nodes: []map[string]any{
			mockNode("n1", "image", 0, 0, genData("产品图", "竖屏产品正面图", mockImageModel,
				map[string]any{"ratio": "9:16"})),
			mockNode("n2", "video", 400, 0, videoData("10 秒产品视频", "产品展示 + 使用场景",
				mockVideoModel, map[string]any{"duration": 10, "ratio": "9:16", "mode": "i2v"}, "i2v")),
		},
		Edges: []map[string]any{mockEdge("n1", "n2")},
	}
}

// mockRule 一条关键词规则。**关键词必须互斥**：先匹配到的胜出，所以更具体的关键词不能是
// 另一条 brief 的子串（例如 c07 用「旁白配音」而不是「配音」，否则 c10 的「口播配音」会被它抢走）。
// 自测会逐条断言 MockRuleID(brief) == 用例 id，加规则时不用靠人眼盯。
type mockRule struct {
	id       string
	keyword  string
	reply    string
	noChange bool
	build    func() mockGraph
}

var mockRules = []mockRule{
	{id: "c01", keyword: "竖屏短视频", reply: "先给一条 15 秒竖屏短视频骨架（文案 → 产品图 → 5 秒主镜头）。", build: graphShortVertical},
	{id: "c02", keyword: "人物口播", reply: "人物口播：文案 + 出镜图 + 5 秒口播镜头，另挂一段背景音乐。", build: graphTalkingHead},
	{id: "c03", keyword: "产品广告", reply: "产品广告：卖点文案配产品图，出 5 秒 / 10 秒两版，加女声旁白。", build: graphProductAd},
	{id: "c04", keyword: "音乐卡点", reply: "音乐卡点 MV：三张场景图各接一个 5 秒镜头，音乐由文本描述生成。", build: graphBeatMV},
	{id: "c05", keyword: "短剧", reply: "短剧分镜：剧本拆三个分镜画面，各出一个 5 秒镜头。", build: graphShortDrama},
	{id: "c06", keyword: "首尾帧", reply: "首尾帧视频：第一张图当首帧、第二张当尾帧，中间做 5 秒过渡。", build: graphFirstLastFrame},
	{id: "c07", keyword: "旁白配音", reply: "旁白配音：文案接女声配音，另配一张封面图。", build: graphVoiceOver},
	{id: "c08", keyword: "全景", reply: "全景图：场景描述接 2:1 的 360 全景图。", build: graphPanorama},
	{id: "c09", keyword: "导演台", reply: "3D 导演台：镜头描述接一段 5 秒环绕运镜。", build: graphDirector},
	{id: "c10", keyword: "复刻", reply: "按爆款结构复刻：钩子文案 + 封面图 + 5 秒主镜头 + 口播配音。", build: graphViralClone},
	{id: "c11", keyword: "别改", reply: "从画布看，主镜头是首尾帧模式但没有上游图片节点，提交时会被拦下；补一张图连到主镜头即可。按你说的，我没有改动任何节点。", noChange: true, build: graphNoChange},
	{id: "c12", keyword: "预算", reply: "预算 100 积分：产品图接 10 秒产品视频。", build: graphBudget},
}

// MockRuleID 关键词映射到的用例 id；没有命中返回 ""。
func MockRuleID(brief string) string {
	for _, r := range mockRules {
		if strings.Contains(brief, r.keyword) {
			return r.id
		}
	}
	return ""
}

// MockReplyForBrief 确定性 mock 的模型回复（一个 JSON 对象字符串）。
// 没命中关键词时返回 false，让调用方知道 mock 需要补规则。
func MockReplyForBrief(brief string) (string, bool) {
	for _, r := range mockRules {
		if !strings.Contains(brief, r.keyword) {
			continue
		}
		if r.noChange {
			body, err := json.Marshal(map[string]any{"reply": r.reply, "changes": []map[string]any{}})
			if err != nil {
				return "", false
			}
			return string(body), true
		}
		body, err := json.Marshal(map[string]any{
			"reply": r.reply,
			"changes": []map[string]any{
				{"action": "apply_graph", "reason": "按需求从零搭一条骨架", "graph": r.build()},
			},
		})
		if err != nil {
			return "", false
		}
		return string(body), true
	}
	return "", false
}

// MockReplyForCase 用例的固定回复。
func MockReplyForCase(c Case) (string, bool) {
	return MockReplyForBrief(c.Brief)
}

// ===== 确定性 mock chatter =====

// MockChatter 按脚本顺序返回固定回复的文本调用实现（满足 service.AgentChatter）。
// 脚本用尽后重复最后一条，保证循环一定能终止（不会因为 mock 空转而耗尽轮数）。
type MockChatter struct {
	mu      sync.Mutex
	replies []string
	idx     int
	calls   []string
}

// NewMockChatter 用给定脚本构造；至少要给一条回复。
func NewMockChatter(replies ...string) *MockChatter {
	cp := make([]string, 0, len(replies))
	cp = append(cp, replies...)
	return &MockChatter{replies: cp}
}

// NewMockChatterForCase 关键词映射出的固定回复；没命中时返回一条明确的错误 JSON
// （而不是空串），这样自测失败时能一眼看出是 mock 缺规则。
func NewMockChatterForCase(c Case) *MockChatter {
	reply, ok := MockReplyForCase(c)
	if !ok {
		reply = `{"reply":"mock 缺少这条用例的关键词规则","changes":[]}`
	}
	return NewMockChatter(reply)
}

// Chat 实现 service.AgentChatter 的旧入口（保留给直接调用方）。
func (m *MockChatter) Chat(modelName, prompt string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, prompt)
	if len(m.replies) == 0 {
		return "", nil
	}
	reply := m.replies[m.idx]
	if m.idx < len(m.replies)-1 {
		m.idx++
	}
	return reply, nil
}

// ChatWithOptions 实现 service.AgentChatter（评测台不关心 max_tokens / thinking）。
func (m *MockChatter) ChatWithOptions(modelName, prompt string, _ service.ChatParams) (string, error) {
	return m.Chat(modelName, prompt)
}

// Calls 收到的全部提示词（按调用顺序），供断言「回灌进提示词」这类不变量。
func (m *MockChatter) Calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.calls))
	out = append(out, m.calls...)
	return out
}

// ===== 画布上下文（patch 用例用）=====

// CanvasNodesFor 用例的画布上下文。只有 patch 用例给上下文（纯提问必须看到画布才能答原因）；
// generate 用例给空画布。这些节点只进提示词，不查库、不代表任何真实画布。
func CanvasNodesFor(c Case) []service.AssistNode {
	if c.Mode != "patch" {
		return nil
	}
	return []service.AssistNode{
		{Id: "image-2", Kind: "image", Label: "产品首帧", Prompt: "竖屏产品图", Selected: true, Downstream: []string{"video-3"}},
		{Id: "video-3", Kind: "video", Label: "主镜头", Prompt: "产品旋转展示", Selected: true, Upstream: []string{"image-2"}},
	}
}
