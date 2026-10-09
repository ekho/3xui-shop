package telegram

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/modules/support"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

type supportFile struct {
	ID   string `json:"file_id"`
	Name string `json:"file_name"`
	Size int64  `json:"file_size"`
}

func supportFileName(name, fallback string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '_'
		}
		return r
	}, name)
	if !utf8.ValidString(name) || name == "" || name == "." || name == ".." {
		name = path.Base(fallback)
	}
	runes := []rune(name)
	if len(runes) > 128 {
		name = string(runes[:128])
	}
	return name
}

func (b *supportBridge) messageInput(ctx context.Context, m botapi.Message, update int64, captureFile bool) (support.TelegramInput, error) {
	var in support.TelegramInput
	digest, err := m.SupportDigest()
	if err != nil || m.From == nil {
		return in, &ActionError{Code: "INVALID_INPUT"}
	}
	in.Source = support.TelegramSource{BotID: b.botID, GroupID: b.cfg.GroupID, ChatID: m.Chat.ID, MessageID: m.ID, UpdateID: update, ActorID: m.From.ID, ThreadID: m.ThreadID, Action: "message", Digest: digest}
	in.Text = m.Text
	var fields map[string]json.RawMessage
	if len(m.Raw) > 0 && json.Unmarshal(m.Raw, &fields) != nil {
		return in, &ActionError{Code: "INVALID_INPUT"}
	}
	if in.Text == "" && fields["caption"] != nil {
		if json.Unmarshal(fields["caption"], &in.Text) != nil {
			return in, &ActionError{Code: "INVALID_INPUT"}
		}
	}
	var file supportFile
	if raw, ok := fields["photo"]; ok {
		in.MediaKind = "photo"
		var photos []supportFile
		if json.Unmarshal(raw, &photos) == nil {
			for _, photo := range photos {
				if photo.ID != "" && photo.Size >= 0 && photo.Size <= 10<<20 && (file.ID == "" || photo.Size >= file.Size) {
					file = photo
				}
			}
		}
	} else {
		for _, kind := range []string{"document", "video", "animation", "audio", "voice", "video_note", "sticker"} {
			if raw, ok := fields[kind]; ok {
				in.MediaKind = kind
				if json.Unmarshal(raw, &file) != nil {
					file = supportFile{}
				}
				break
			}
		}
	}
	if in.MediaKind != "" {
		in.TelegramOnly = true
		if captureFile && file.ID != "" && file.Size >= 0 && file.Size <= 10<<20 {
			metadata, e := b.api.GetFile(ctx, file.ID)
			if e == nil {
				data, e := b.api.DownloadFile(ctx, metadata.Path)
				if e == nil {
					in.Bytes, in.Name, in.TelegramOnly = data, supportFileName(file.Name, metadata.Path), false
				}
			}
		}
		return in, nil
	}
	for _, kind := range []string{"contact", "location", "venue", "poll", "dice"} {
		if raw, ok := fields[kind]; ok {
			text, valid := supportPlainContent(kind, raw)
			if valid && utf8.ValidString(text) && !strings.ContainsRune(text, '\x00') && utf8.RuneCountInString(text) <= 4000 {
				in.Text = text
			} else {
				in.MediaKind, in.TelegramOnly = "unknown", true
			}
			return in, nil
		}
	}
	// Preserve future copyable content without interpreting it or exposing raw IDs.
	for key := range fields {
		switch key {
		case "message_id", "message_thread_id", "from", "chat", "date", "text", "entities", "caption", "caption_entities", "reply_markup", "sender_chat", "sender_boost_count", "sender_business_bot", "business_connection_id", "forward_origin", "is_topic_message", "is_automatic_forward", "reply_to_message", "external_reply", "quote", "reply_to_story", "via_bot", "edit_date", "has_protected_content", "is_from_offline", "media_group_id", "author_signature", "link_preview_options", "effect_id":
		default:
			in.MediaKind, in.TelegramOnly = "unknown", true
		}
	}
	return in, nil
}

type supportLocation struct {
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
}

func (l supportLocation) plain() (string, bool) {
	if l.Latitude == nil || l.Longitude == nil || *l.Latitude < -90 || *l.Latitude > 90 || *l.Longitude < -180 || *l.Longitude > 180 {
		return "", false
	}
	return fmt.Sprintf("Location: %g, %g", *l.Latitude, *l.Longitude), true
}
func supportPlainContent(kind string, raw json.RawMessage) (string, bool) {
	switch kind {
	case "contact":
		var c struct {
			Phone string `json:"phone_number"`
			First string `json:"first_name"`
			Last  string `json:"last_name"`
		}
		if json.Unmarshal(raw, &c) != nil || c.Phone == "" {
			return "", false
		}
		return "Contact: " + strings.TrimSpace(c.First+" "+c.Last) + "\nPhone: " + c.Phone, true
	case "location":
		var l supportLocation
		if json.Unmarshal(raw, &l) != nil {
			return "", false
		}
		return l.plain()
	case "venue":
		var v struct {
			Title, Address string
			Location       supportLocation
		}
		if json.Unmarshal(raw, &v) != nil || v.Title == "" {
			return "", false
		}
		location, valid := v.Location.plain()
		return "Venue: " + v.Title + "\n" + v.Address + "\n" + location, valid
	case "poll":
		var p struct {
			Question string
			Options  []struct{ Text string }
		}
		if json.Unmarshal(raw, &p) != nil || p.Question == "" || len(p.Options) == 0 {
			return "", false
		}
		text := "Poll: " + p.Question
		for _, option := range p.Options {
			text += "\n- " + option.Text
		}
		return text, true
	case "dice":
		var d struct {
			Emoji string
			Value int
		}
		if json.Unmarshal(raw, &d) != nil || d.Emoji == "" || d.Value < 1 || d.Value > 64 {
			return "", false
		}
		return fmt.Sprintf("Dice: %s %d", d.Emoji, d.Value), true
	}
	return "", false
}
