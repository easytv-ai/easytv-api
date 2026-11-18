package service

import (
	"encoding/json"
	"math"
	"sync"
	"unicode/utf8"
)

// 同一张画布上的在线会话。进程内广播：光标、正在拖的卡片、保存后的版本号。
// 查看者能看见别人，不能把卡片坐标发出去。多进程部署时，连到不同进程的人互相看不见。

const (
	canvasLiveRoomCap      = 12
	canvasLiveConnsPerUser = 4
)

var canvasLiveColors = []string{
	"#f59e0b",
	"#38bdf8",
	"#a78bfa",
	"#34d399",
	"#fb7185",
	"#facc15",
}

type CanvasLiveUser struct {
	Id    int    `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type CanvasLiveNode struct {
	Id string  `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
}

type canvasLiveWire struct {
	Op      string           `json:"op"`
	X       float64          `json:"x,omitempty"`
	Y       float64          `json:"y,omitempty"`
	Items   []CanvasLiveNode `json:"items,omitempty"`
	Version int              `json:"version,omitempty"`
	User    *CanvasLiveUser  `json:"user,omitempty"`
	Peers   []canvasLivePeer `json:"peers,omitempty"`
	Id      int              `json:"id,omitempty"`
}

// canvasLivePeer 进房时带上这个人最后一次光标。坐标可以是 0，所以用指针区分「没动过」。
type canvasLivePeer struct {
	Id    int      `json:"id"`
	Name  string   `json:"name"`
	Color string   `json:"color"`
	X     *float64 `json:"x,omitempty"`
	Y     *float64 `json:"y,omitempty"`
}

// cursorWire 光标坐标可以是 0，不能用 omitempty。
// 名字和颜色一起带上：对方如果漏掉了 join，仍能凭这一帧把人画出来。
type cursorWire struct {
	Op    string  `json:"op"`
	Id    int     `json:"id"`
	Name  string  `json:"name,omitempty"`
	Color string  `json:"color,omitempty"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

type CanvasLivePeer struct {
	User     CanvasLiveUser
	Writable bool
	send     chan []byte
	done     chan struct{}
	once     sync.Once
}

func (p *CanvasLivePeer) TrySend(b []byte) {
	select {
	case <-p.done:
	case p.send <- b:
	default:
	}
}

func (p *CanvasLivePeer) closeSend() {
	p.once.Do(func() { close(p.done) })
}

type canvasLiveSlot struct {
	user      CanvasLiveUser
	writable  bool
	conns     []*CanvasLivePeer
	hasCursor bool
	cursorX   float64
	cursorY   float64
}

func (s *canvasLiveSlot) presence() canvasLivePeer {
	view := canvasLivePeer{Id: s.user.Id, Name: s.user.Name, Color: s.user.Color}
	if s.hasCursor {
		x, y := s.cursorX, s.cursorY
		view.X = &x
		view.Y = &y
	}
	return view
}

type canvasLiveRoom struct {
	slots map[int]*canvasLiveSlot
}

// CanvasLiveHub 按画布 id 分组。单进程内有效。
type CanvasLiveHub struct {
	mu    sync.Mutex
	rooms map[int]*canvasLiveRoom
}

func NewCanvasLiveHub() *CanvasLiveHub {
	return &CanvasLiveHub{rooms: map[int]*canvasLiveRoom{}}
}

var defaultCanvasLive = NewCanvasLiveHub()

func CanvasLive() *CanvasLiveHub { return defaultCanvasLive }

func canvasLiveColor(userId int) string {
	if userId < 0 {
		userId = -userId
	}
	return canvasLiveColors[userId%len(canvasLiveColors)]
}

// ValidCanvasLivePoint 拒绝非数字和离谱坐标。
func ValidCanvasLivePoint(x, y float64) bool {
	return liveFinite(x) && liveFinite(y)
}

// ValidCanvasLiveNodes 一批正在拖的卡片。空批次、超长 id、坏坐标都丢掉。
func ValidCanvasLiveNodes(items []CanvasLiveNode) bool {
	if len(items) == 0 || len(items) > 64 {
		return false
	}
	for _, item := range items {
		if !validLiveNodeId(item.Id) || !ValidCanvasLivePoint(item.X, item.Y) {
			return false
		}
	}
	return true
}

func liveFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > -1e7 && v < 1e7
}

func validLiveNodeId(id string) bool {
	if id == "" || len(id) > 40 || !utf8.ValidString(id) {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x20 || id[i] == 0x7f {
			return false
		}
	}
	return true
}

// Join 把一条连接放进房间。同一用户可有多条连接，只在第一条时通知别人。
// 房间满或这个人的连接数满时返回 nil。
func (h *CanvasLiveHub) Join(canvasId int, user CanvasLiveUser, writable bool) (*CanvasLivePeer, []byte) {
	if user.Id <= 0 {
		return nil, nil
	}
	if user.Color == "" {
		user.Color = canvasLiveColor(user.Id)
	}
	if user.Name == "" {
		user.Name = "成员"
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[canvasId]
	if room == nil {
		room = &canvasLiveRoom{slots: map[int]*canvasLiveSlot{}}
		h.rooms[canvasId] = room
	}
	slot := room.slots[user.Id]
	if slot == nil && len(room.slots) >= canvasLiveRoomCap {
		return nil, nil
	}
	if slot != nil && len(slot.conns) >= canvasLiveConnsPerUser {
		return nil, nil
	}
	peer := &CanvasLivePeer{
		User:     user,
		Writable: writable,
		send:     make(chan []byte, 32),
		done:     make(chan struct{}),
	}
	first := slot == nil
	if first {
		slot = &canvasLiveSlot{user: user, writable: writable}
		room.slots[user.Id] = slot
	}
	if writable {
		slot.writable = true
	}
	slot.conns = append(slot.conns, peer)
	others := make([]canvasLivePeer, 0, len(room.slots))
	for id, item := range room.slots {
		if id == user.Id {
			continue
		}
		others = append(others, item.presence())
	}
	if first {
		h.broadcastOthersLocked(room, user.Id, canvasLiveWire{Op: "join", User: &slot.user})
	} else {
		// 同一个账号又开了一个窗口。别的人已经认识他，只告诉他自己的其他窗口。
		// 新窗口的名单里也放一份自己，并带上另一个窗口最后的光标。
		others = append(others, slot.presence())
		notice := mustLiveJSON(canvasLiveWire{Op: "join", User: &slot.user})
		for _, item := range slot.conns {
			if item != peer {
				item.TrySend(notice)
			}
		}
	}
	return peer, canvasLiveHello(slot.user, others)
}

// Leave 关掉一条连接。这个人的最后一条连接断开时才告诉别人。
func (h *CanvasLiveHub) Leave(canvasId int, peer *CanvasLivePeer) {
	if peer == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	peer.closeSend()
	room := h.rooms[canvasId]
	if room == nil {
		return
	}
	slot := room.slots[peer.User.Id]
	if slot == nil {
		return
	}
	next := slot.conns[:0]
	for _, item := range slot.conns {
		if item != peer {
			next = append(next, item)
		}
	}
	slot.conns = next
	if len(slot.conns) > 0 {
		return
	}
	delete(room.slots, peer.User.Id)
	if len(room.slots) == 0 {
		delete(h.rooms, canvasId)
		return
	}
	h.broadcastOthersLocked(room, 0, canvasLiveWire{Op: "leave", Id: peer.User.Id})
}

// Relay 把光标或拖动转给房间里的其他连接。查看者的拖动丢掉。不回给发送的那条连接。
func (h *CanvasLiveHub) Relay(canvasId int, peer *CanvasLivePeer, op string, x, y float64, items []CanvasLiveNode) {
	if peer == nil {
		return
	}
	if op == "nodes" && !peer.Writable {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[canvasId]
	if room == nil {
		return
	}
	var raw []byte
	if op == "cursor" {
		if slot := room.slots[peer.User.Id]; slot != nil {
			slot.cursorX = x
			slot.cursorY = y
			slot.hasCursor = true
		}
		raw = mustLiveJSON(cursorWire{Op: "cursor", Id: peer.User.Id, Name: peer.User.Name, Color: peer.User.Color, X: x, Y: y})
	} else if op == "nodes" {
		raw = mustLiveJSON(canvasLiveWire{Op: "nodes", Id: peer.User.Id, Items: items})
	} else {
		return
	}
	for _, slot := range room.slots {
		for _, item := range slot.conns {
			if item == peer {
				continue
			}
			item.TrySend(raw)
		}
	}
}

// Saved 某人把画布存上了。不回给保存者本人的连接，避免他自己再拉一次。
func (h *CanvasLiveHub) Saved(canvasId, userId, version int) {
	if version <= 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[canvasId]
	if room == nil {
		return
	}
	raw := mustLiveJSON(canvasLiveWire{Op: "saved", Id: userId, Version: version})
	for _, slot := range room.slots {
		if slot.user.Id == userId {
			continue
		}
		for _, item := range slot.conns {
			item.TrySend(raw)
		}
	}
}

func (h *CanvasLiveHub) broadcastOthersLocked(room *canvasLiveRoom, exceptUserId int, msg canvasLiveWire) {
	raw := mustLiveJSON(msg)
	for _, slot := range room.slots {
		if slot.user.Id == exceptUserId {
			continue
		}
		for _, item := range slot.conns {
			item.TrySend(raw)
		}
	}
}

func mustLiveJSON(msg any) []byte {
	raw, err := json.Marshal(msg)
	if err != nil {
		return []byte(`{"op":"ping"}`)
	}
	return raw
}

func canvasLiveHello(self CanvasLiveUser, peers []canvasLivePeer) []byte {
	return mustLiveJSON(canvasLiveWire{Op: "hello", User: &self, Peers: peers})
}
