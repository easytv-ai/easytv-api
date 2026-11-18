package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math"
	"testing"
)

func livePeersHave(peers []canvasLivePeer, id int) bool {
	for _, item := range peers {
		if item.Id == id {
			return true
		}
	}
	return false
}

func takeLive(t *testing.T, ch <-chan []byte) canvasLiveWire {
	t.Helper()
	select {
	case raw := <-ch:
		var msg canvasLiveWire
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("json: %s", raw)
		}
		return msg
	default:
		t.Fatal("missing message")
	}
	return canvasLiveWire{}
}

func TestCanvasLiveRelay(t *testing.T) {
	hub := NewCanvasLiveHub()
	ada, hello := hub.Join(7, CanvasLiveUser{Id: 1, Name: "Ada"}, true)
	if ada == nil || len(hello) == 0 {
		t.Fatal("join ada")
	}
	bo, _ := hub.Join(7, CanvasLiveUser{Id: 2, Name: "Bo"}, true)
	if bo == nil {
		t.Fatal("join bo")
	}
	joined := takeLive(t, ada.send)
	if joined.Op != "join" || joined.User == nil || joined.User.Id != 2 {
		t.Fatalf("join notice: %+v", joined)
	}
	hub.Relay(7, ada, "cursor", 0, 34, nil)
	cursor := takeLive(t, bo.send)
	if cursor.Op != "cursor" || cursor.Id != 1 || cursor.X != 0 || cursor.Y != 34 {
		t.Fatalf("cursor: %+v", cursor)
	}
	select {
	case raw := <-ada.send:
		t.Fatalf("cursor echoed to sender: %s", raw)
	default:
	}
	adaAgain, helloAgain := hub.Join(7, CanvasLiveUser{Id: 1, Name: "Ada"}, true)
	if adaAgain == nil {
		t.Fatal("second conn")
	}
	var again canvasLiveWire
	if json.Unmarshal(helloAgain, &again) != nil || !livePeersHave(again.Peers, 1) {
		t.Fatalf("second window hello: %s", helloAgain)
	}
	for _, item := range again.Peers {
		if item.Id != 1 {
			continue
		}
		if item.X == nil || item.Y == nil || *item.X != 0 || *item.Y != 34 {
			t.Fatalf("second window missing the earlier cursor: %+v", item)
		}
	}
	selfJoin := takeLive(t, ada.send)
	if selfJoin.Op != "join" || selfJoin.User == nil || selfJoin.User.Id != 1 {
		t.Fatalf("other window was not told: %+v", selfJoin)
	}
	select {
	case raw := <-bo.send:
		t.Fatalf("second conn should not join again: %s", raw)
	default:
	}
	hub.Relay(7, ada, "cursor", 3, 4, nil)
	var followed cursorWire
	select {
	case raw := <-adaAgain.send:
		if json.Unmarshal(raw, &followed) != nil || followed.Name != "Ada" || followed.X != 3 || followed.Y != 4 {
			t.Fatalf("cursor to other window: %s", raw)
		}
	default:
		t.Fatal("other window missed cursor")
	}
	_ = takeLive(t, bo.send)
	view, _ := hub.Join(7, CanvasLiveUser{Id: 3, Name: "Vi"}, false)
	if view == nil {
		t.Fatal("viewer")
	}
	_ = takeLive(t, ada.send)
	_ = takeLive(t, bo.send)
	hub.Relay(7, view, "nodes", 0, 0, []CanvasLiveNode{{Id: "n1", X: 1, Y: 2}})
	select {
	case raw := <-ada.send:
		t.Fatalf("viewer drag was relayed: %s", raw)
	default:
	}
	hub.Saved(7, 1, 9)
	select {
	case raw := <-ada.send:
		t.Fatalf("saver got own saved: %s", raw)
	default:
	}
	saved := takeLive(t, bo.send)
	if saved.Op != "saved" || saved.Version != 9 {
		t.Fatalf("saved: %+v", saved)
	}
	hub.Leave(7, ada)
	select {
	case raw := <-bo.send:
		t.Fatalf("leave while another conn remains: %s", raw)
	default:
	}
	hub.Leave(7, adaAgain)
	left := takeLive(t, bo.send)
	if left.Op != "leave" || left.Id != 1 {
		t.Fatalf("leave: %+v", left)
	}
}

func TestCanvasLiveRoomCap(t *testing.T) {
	hub := NewCanvasLiveHub()
	for i := 1; i <= canvasLiveRoomCap; i++ {
		peer, _ := hub.Join(3, CanvasLiveUser{Id: i, Name: "P"}, true)
		if peer == nil {
			t.Fatalf("join %d", i)
		}
	}
	extra, _ := hub.Join(3, CanvasLiveUser{Id: 99, Name: "X"}, true)
	if extra != nil {
		t.Fatal("room should be full")
	}
}

func TestValidCanvasLiveNodes(t *testing.T) {
	if !ValidCanvasLivePoint(0, -3) || ValidCanvasLivePoint(math.NaN(), 1) || ValidCanvasLivePoint(1e7, 0) {
		t.Fatal("point")
	}
	if !ValidCanvasLiveNodes([]CanvasLiveNode{{Id: "image-1", X: 1, Y: 2}}) {
		t.Fatal("nodes")
	}
	if ValidCanvasLiveNodes(nil) || ValidCanvasLiveNodes([]CanvasLiveNode{{Id: "", X: 1, Y: 2}}) {
		t.Fatal("reject empty id")
	}
}

func TestWsTextRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	payload := []byte(`{"op":"cursor","x":1}`)
	if err := writeWsText(w, payload); err != nil {
		t.Fatal(err)
	}
	frame := buf.Bytes()
	if frame[0] != 0x81 {
		t.Fatalf("opcode header %x", frame[0])
	}
	masked := []byte{0x81, 0x80 | byte(len(payload)), 1, 2, 3, 4}
	body := append([]byte(nil), payload...)
	for i := range body {
		body[i] ^= masked[2+i%4]
	}
	masked = append(masked, body...)
	opcode, got, err := readWsFrame(bufio.NewReader(bytes.NewReader(masked)))
	if err != nil {
		t.Fatal(err)
	}
	if opcode != 1 || string(got) != string(payload) {
		t.Fatalf("got %d %s", opcode, got)
	}
	if wsAcceptKey("dGhlIHNhbXBsZSBub25jZQ==") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatal("accept key")
	}
}
