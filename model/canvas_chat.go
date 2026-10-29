package model

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"easytv-api/common"

	"gorm.io/gorm"
)

// CanvasChat 画布右侧对话的一段会话（API.md 2.1）。
// 消息正文是前端对话气泡的 JSON，后端只校验形状和大小，不解释确认卡。
// 挂在用户和画布上，换一台电脑登录同一账号还能打开。
type CanvasChat struct {
	Id           int    `json:"id"`
	UserId       int    `json:"user_id" gorm:"index:idx_canvas_chat_canvas,priority:1;comment:所有者用户ID"`
	CanvasId     int    `json:"canvas_id" gorm:"index:idx_canvas_chat_canvas,priority:2;comment:所属画布ID"`
	Title        string `json:"title" gorm:"type:varchar(80);comment:会话标题"`
	Messages     string `json:"-" gorm:"type:longtext;comment:消息 JSON 数组"`
	MessageCount int    `json:"message_count" gorm:"default:0;comment:消息条数"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint;comment:创建时间（Unix 秒）"`
	UpdatedAt    int64  `json:"updated_at" gorm:"bigint;comment:更新时间（Unix 秒）"`
}

func (CanvasChat) TableName() string { return "canvas_chat" }

const (
	// CanvasChatTitleMax 标题按字数截断，和对话栏标签同一量级。
	CanvasChatTitleMax = 80
	// CanvasChatMessagesMax 单段对话 JSON 上限（2 MB），与画布 graph 同级。
	CanvasChatMessagesMax = 2 * 1024 * 1024
	// CanvasChatMessageMaxN 单段最多保留的气泡条数。
	CanvasChatMessageMaxN = 200
	// CanvasChatKeep 一张画布最多留多少段历史，多出来的按更新时间删最旧的。
	CanvasChatKeep = 30
)

var (
	ErrCanvasChatEmpty   = errors.New("对话内容为空")
	ErrCanvasChatInvalid = errors.New("对话内容无效")
	ErrCanvasChatTooLong = errors.New("对话内容过长")
	ErrCanvasChatTooMany = errors.New("对话条数过多")
)

// NormalizeCanvasChatTitle 去掉空白，空标题用「新对话」，超长按字截断。
func NormalizeCanvasChatTitle(title string) string {
	title = strings.TrimSpace(title)
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return "新对话"
	}
	if utf8.RuneCountInString(title) <= CanvasChatTitleMax {
		return title
	}
	runes := []rune(title)
	return string(runes[:CanvasChatTitleMax])
}

// NormalizeCanvasChatMessages 校验并收成可入库的 JSON。
// 丢掉进行中的气泡和只在当次打开有效的撤销凭据。
func NormalizeCanvasChatMessages(raw json.RawMessage) (string, int, error) {
	if len(bytesTrim(raw)) == 0 || string(raw) == "null" {
		return "", 0, ErrCanvasChatEmpty
	}
	if len(raw) > CanvasChatMessagesMax {
		return "", 0, ErrCanvasChatTooLong
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return "", 0, ErrCanvasChatInvalid
	}
	if len(list) == 0 {
		return "", 0, ErrCanvasChatEmpty
	}
	if len(list) > CanvasChatMessageMaxN {
		return "", 0, ErrCanvasChatTooMany
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		var row map[string]any
		if err := json.Unmarshal(item, &row); err != nil || row == nil {
			return "", 0, ErrCanvasChatInvalid
		}
		if pending, ok := row["pending"].(bool); ok && pending {
			continue
		}
		role, _ := row["role"].(string)
		if role != "user" && role != "assistant" {
			return "", 0, ErrCanvasChatInvalid
		}
		if text, ok := row["text"]; ok && text != nil {
			if _, ok := text.(string); !ok {
				return "", 0, ErrCanvasChatInvalid
			}
		}
		delete(row, "undoToken")
		delete(row, "pending")
		out = append(out, row)
	}
	if len(out) == 0 {
		return "", 0, ErrCanvasChatEmpty
	}
	buf, err := json.Marshal(out)
	if err != nil {
		return "", 0, ErrCanvasChatInvalid
	}
	if len(buf) > CanvasChatMessagesMax {
		return "", 0, ErrCanvasChatTooLong
	}
	return string(buf), len(out), nil
}

func bytesTrim(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}

// Summary 历史列表用：不含消息正文。
func (row *CanvasChat) Summary() map[string]any {
	return map[string]any{
		"id":            row.Id,
		"title":         row.Title,
		"message_count": row.MessageCount,
		"created_at":    row.CreatedAt,
		"updated_at":    row.UpdatedAt,
	}
}

// Detail 单段对话：带消息正文。
func (row *CanvasChat) Detail() map[string]any {
	view := row.Summary()
	messages := row.Messages
	if messages == "" {
		messages = "[]"
	}
	view["messages"] = json.RawMessage(messages)
	return view
}

// ListCanvasChats 一张画布的对话历史，最近更新的在前，不含正文。
func ListCanvasChats(userId, canvasId int) ([]*CanvasChat, error) {
	var list []*CanvasChat
	err := DB.Select("id", "user_id", "canvas_id", "title", "message_count", "created_at", "updated_at").
		Where("user_id = ? AND canvas_id = ?", userId, canvasId).
		Order("updated_at desc, id desc").
		Limit(CanvasChatKeep).
		Find(&list).Error
	return list, err
}

// GetCanvasChat 读取一段属于该用户、该画布的对话。
func GetCanvasChat(userId, canvasId, id int) (*CanvasChat, error) {
	row := &CanvasChat{}
	err := DB.Where("id = ? AND user_id = ? AND canvas_id = ?", id, userId, canvasId).First(row).Error
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return row, nil
}

// CreateCanvasChat 新建一段对话。超过保留条数时删掉最旧的。
func CreateCanvasChat(userId, canvasId int, title, messages string, count int) (*CanvasChat, error) {
	now := common.GetTimestamp()
	row := &CanvasChat{
		UserId:       userId,
		CanvasId:     canvasId,
		Title:        title,
		Messages:     messages,
		MessageCount: count,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := pruneCanvasChats(tx, userId, canvasId); err != nil {
			return err
		}
		return tx.Create(row).Error
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

// SaveCanvasChat 覆盖一段已有对话的标题和消息。
func SaveCanvasChat(userId, canvasId, id int, title, messages string, count int) (*CanvasChat, error) {
	row, err := GetCanvasChat(userId, canvasId, id)
	if err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	err = DB.Model(&CanvasChat{}).Where("id = ? AND user_id = ? AND canvas_id = ?", row.Id, userId, canvasId).
		Updates(map[string]any{
			"title":         title,
			"messages":      messages,
			"message_count": count,
			"updated_at":    now,
		}).Error
	if err != nil {
		return nil, err
	}
	row.Title = title
	row.Messages = messages
	row.MessageCount = count
	row.UpdatedAt = now
	return row, nil
}

// DeleteCanvasChat 删除一段对话；不是这条画布的返回 ErrNotFound。
func DeleteCanvasChat(userId, canvasId, id int) error {
	res := DB.Where("id = ? AND user_id = ? AND canvas_id = ?", id, userId, canvasId).Delete(&CanvasChat{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearCanvasChats 清空一张画布上当前用户的全部对话，返回删掉的条数。
func ClearCanvasChats(userId, canvasId int) (int64, error) {
	res := DB.Where("user_id = ? AND canvas_id = ?", userId, canvasId).Delete(&CanvasChat{})
	return res.RowsAffected, res.Error
}

// pruneCanvasChats 给即将插入的新对话腾位置：已有 Keep 条时删掉最旧的，插入后仍是 Keep 条。
func pruneCanvasChats(tx *gorm.DB, userId, canvasId int) error {
	var count int64
	if err := tx.Model(&CanvasChat{}).Where("user_id = ? AND canvas_id = ?", userId, canvasId).Count(&count).Error; err != nil {
		return err
	}
	extra := int(count) - (CanvasChatKeep - 1)
	if extra <= 0 {
		return nil
	}
	var ids []int
	if err := tx.Model(&CanvasChat{}).Where("user_id = ? AND canvas_id = ?", userId, canvasId).
		Order("updated_at asc, id asc").Limit(extra).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return tx.Where("user_id = ? AND canvas_id = ? AND id IN ?", userId, canvasId, ids).Delete(&CanvasChat{}).Error
}
