package botapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const supportFileMax = 10 << 20

type File struct {
	ID   string `json:"file_id"`
	Path string `json:"file_path"`
	Size int64  `json:"file_size"`
}

func filePath(path string) bool {
	if len(path) == 0 || len(path) > 512 {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, r := range segment {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
				return false
			}
		}
	}
	return true
}

func (c *Client) GetFile(ctx context.Context, id string) (File, error) {
	var out File
	if id == "" || len(id) > 4096 || !utf8.ValidString(id) || strings.ContainsAny(id, "\x00\r\n") {
		return out, &APIError{Code: "INVALID_INPUT"}
	}
	err := c.call(ctx, "getFile", map[string]string{"file_id": id}, &out, 10*time.Second)
	if err != nil {
		return File{}, err
	}
	if out.ID != id || out.Size < 0 || out.Size > 1<<52-1 || !filePath(out.Path) {
		return File{}, invalid()
	}
	if out.Size > supportFileMax {
		return File{}, &APIError{Code: "FILE_TOO_LARGE"}
	}
	return out, nil
}

func (c *Client) DownloadFile(ctx context.Context, path string) ([]byte, error) {
	if !filePath(path) {
		return nil, &APIError{Code: "INVALID_INPUT"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.telegram.org/file/bot"+c.token+"/"+path, nil)
	if err != nil {
		return nil, &APIError{Code: "INVALID_INPUT"}
	}
	resp, err := c.http.Do(r)
	if err != nil {
		return nil, &APIError{Code: "UNAVAILABLE"}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, invalid()
	}
	if resp.StatusCode != 200 {
		return nil, &APIError{Code: "UNAVAILABLE"}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, supportFileMax+1))
	if err != nil {
		return nil, &APIError{Code: "UNAVAILABLE"}
	}
	if len(b) > supportFileMax {
		return nil, &APIError{Code: "FILE_TOO_LARGE"}
	}
	if len(b) == 0 {
		return nil, invalid()
	}
	return b, nil
}

func (c *Client) SendSupportDocument(ctx context.Context, chat, thread int64, name string, file []byte, caption string) (int64, error) {
	if !supportDestination(chat, thread) || len(file) == 0 || len(file) > supportFileMax || !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 128 || strings.ContainsAny(name, "/\\") || name == "." || name == ".." || !utf8.ValidString(caption) || strings.ContainsRune(caption, '\x00') || utf8.RuneCountInString(caption) > 1024 {
		return 0, &APIError{Code: "INVALID_INPUT"}
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return 0, &APIError{Code: "INVALID_INPUT"}
		}
	}
	// ponytail: multipart is buffered within the 10 MiB ceiling; stream if that limit grows.
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fields := map[string]string{"chat_id": strconv.FormatInt(chat, 10), "caption": caption}
	if thread != 0 {
		fields["message_thread_id"] = strconv.FormatInt(thread, 10)
	}
	for key, value := range fields {
		if w.WriteField(key, value) != nil {
			return 0, &APIError{Code: "INVALID_INPUT"}
		}
	}
	p, err := w.CreateFormFile("document", name)
	if err != nil {
		return 0, &APIError{Code: "INVALID_INPUT"}
	}
	if _, err = p.Write(file); err != nil {
		return 0, &APIError{Code: "INVALID_INPUT"}
	}
	if w.Close() != nil {
		return 0, &APIError{Code: "INVALID_INPUT"}
	}
	var out Message
	err = c.request(ctx, "sendDocument", w.FormDataContentType(), &body, &out, 10*time.Second)
	if err == nil && (out.ID <= 0 || out.ID > 1<<52-1 || out.Chat.ID != chat || out.ThreadID != thread || chat < 0 && out.Chat.Type != "supergroup" || chat > 0 && out.Chat.Type != "private") {
		return 0, invalid()
	}
	return out.ID, err
}

func (m *Message) UnmarshalJSON(raw []byte) error {
	if len(raw) > 1<<20 {
		return invalid()
	}
	type message Message
	var out message
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	*m = Message(out)
	m.Raw = append(json.RawMessage(nil), raw...)
	return nil
}

func (m Message) SupportDigest() ([32]byte, error) {
	raw := m.Raw
	if len(raw) == 0 {
		var err error
		raw, err = json.Marshal(m)
		if err != nil {
			return [32]byte{}, invalid()
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var content map[string]any
	if d.Decode(&content) != nil || content == nil {
		return [32]byte{}, invalid()
	}
	delete(content, "from")
	delete(content, "chat")
	delete(content, "date")
	content["chat_id"], content["message_id"], content["thread_id"] = m.Chat.ID, m.ID, m.ThreadID
	if m.From != nil {
		content["actor_id"] = m.From.ID
	}
	canonical, err := json.Marshal(content)
	if err != nil {
		return [32]byte{}, invalid()
	}
	return sha256.Sum256(canonical), nil
}
