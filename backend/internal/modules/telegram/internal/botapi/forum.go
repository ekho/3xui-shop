package botapi

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

type ChatMember struct {
	User            User   `json:"user"`
	Status          string `json:"status"`
	CanManageTopics bool   `json:"can_manage_topics"`
}

func forumID(id int64) bool { return id < 0 && id >= -(1<<52-1) }

func (c *Client) SetForumTopicClosed(ctx context.Context, group, thread int64, closed bool) (bool, error) {
	if !forumID(group) || thread <= 1 || thread > 1<<52-1 {
		return false, &APIError{Code: "INVALID_INPUT"}
	}
	method := "reopenForumTopic"
	if closed {
		method = "closeForumTopic"
	}
	var ok bool
	err := c.call(ctx, method, map[string]any{"chat_id": group, "message_thread_id": thread}, &ok, 10*time.Second)
	if err == nil && !ok {
		return false, invalid()
	}
	return ok, err
}
func (c *Client) SendSupportCard(ctx context.Context, group, thread int64, text string, keyboard *InlineKeyboard) (int64, error) {
	if !forumID(group) || thread < 0 || thread > 1<<52-1 {
		return 0, &APIError{Code: "INVALID_INPUT"}
	}
	return c.supportText(ctx, group, thread, text, keyboard, thread <= 1)
}

func supportDestination(chatID, threadID int64) bool {
	return chatID > 0 && chatID <= 1<<52-1 && threadID == 0 || forumID(chatID) && threadID > 1 && threadID <= 1<<52-1
}

func (c *Client) GetChat(ctx context.Context, id int64) (Chat, error) {
	var out Chat
	if !forumID(id) {
		return out, &APIError{Code: "INVALID_INPUT"}
	}
	err := c.call(ctx, "getChat", map[string]any{"chat_id": id}, &out, 10*time.Second)
	if err == nil && out.ID != id {
		return Chat{}, invalid()
	}
	return out, err
}
func (c *Client) GetChatMember(ctx context.Context, groupID, userID int64) (ChatMember, error) {
	var out ChatMember
	if !forumID(groupID) || userID <= 0 || userID > 1<<52-1 {
		return out, &APIError{Code: "INVALID_INPUT"}
	}
	err := c.call(ctx, "getChatMember", map[string]any{"chat_id": groupID, "user_id": userID}, &out, 10*time.Second)
	if err == nil && (out.User.ID != userID || !out.User.IsBot) {
		return ChatMember{}, invalid()
	}
	return out, err
}
func (c *Client) CreateForumTopic(ctx context.Context, groupID int64, name string) (int64, error) {
	if !forumID(groupID) || !utf8.ValidString(name) || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 128 || strings.ContainsRune(name, '\x00') {
		return 0, &APIError{Code: "INVALID_INPUT"}
	}
	var out struct {
		ID int64 `json:"message_thread_id"`
	}
	err := c.call(ctx, "createForumTopic", map[string]any{"chat_id": groupID, "name": name}, &out, 10*time.Second)
	if err == nil && (out.ID <= 1 || out.ID > 1<<52-1) {
		return 0, invalid()
	}
	return out.ID, err
}
func (c *Client) SendSupportText(ctx context.Context, chatID, threadID int64, text string) (int64, error) {
	return c.supportText(ctx, chatID, threadID, text, nil, false)
}
func (c *Client) supportText(ctx context.Context, chatID, threadID int64, text string, keyboard *InlineKeyboard, general bool) (int64, error) {
	if (!general && !supportDestination(chatID, threadID) || general && (!forumID(chatID) || threadID > 1 || threadID < 0)) || !utf8.ValidString(text) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 4096 || strings.ContainsRune(text, '\x00') || !validKeyboard(keyboard) {
		return 0, &APIError{Code: "INVALID_INPUT"}
	}
	in := map[string]any{"chat_id": chatID, "text": text, "link_preview_options": map[string]bool{"is_disabled": true}}
	if keyboard != nil {
		in["reply_markup"] = keyboard
	}
	if !general && threadID != 0 {
		in["message_thread_id"] = threadID
	}
	var out Message
	err := c.call(ctx, "sendMessage", in, &out, 10*time.Second)
	if err == nil && (out.ID <= 0 || out.ID > 1<<52-1 || out.Chat.ID != chatID || !general && out.ThreadID != threadID || general && out.ThreadID != 0 && out.ThreadID != 1 || chatID < 0 && out.Chat.Type != "supergroup" || chatID > 0 && out.Chat.Type != "private") {
		return 0, invalid()
	}
	return out.ID, err
}
func (c *Client) CopySupportMessage(ctx context.Context, chatID, threadID, sourceChatID, sourceMessageID int64) (int64, error) {
	if !supportDestination(chatID, threadID) || sourceChatID == 0 || sourceChatID < -(1<<52-1) || sourceChatID > 1<<52-1 || sourceMessageID <= 0 || sourceMessageID > 1<<52-1 {
		return 0, &APIError{Code: "INVALID_INPUT"}
	}
	in := map[string]any{"chat_id": chatID, "from_chat_id": sourceChatID, "message_id": sourceMessageID}
	if threadID != 0 {
		in["message_thread_id"] = threadID
	}
	var out struct {
		ID int64 `json:"message_id"`
	}
	err := c.call(ctx, "copyMessage", in, &out, 10*time.Second)
	if err == nil && (out.ID <= 0 || out.ID > 1<<52-1) {
		return 0, invalid()
	}
	return out.ID, err
}
