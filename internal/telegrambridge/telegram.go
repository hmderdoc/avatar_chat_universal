package telegrambridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// tgClient is a minimal Telegram Bot API client: long-poll getUpdates plus the
// few send methods the bridge needs. Full bot SDKs are overkill for forwarding
// one channel, and hand-rolling keeps the binary dependency-free (mirroring the
// IRC bridge's own client).
type tgClient struct {
	token string
	http  *http.Client
}

func newTGClient(token string) *tgClient {
	return &tgClient{
		token: token,
		// No client-wide timeout: getUpdates long-polls. Per-call timeouts come
		// from the request context instead.
		http: &http.Client{},
	}
}

func (c *tgClient) method(name string) string {
	return "https://api.telegram.org/bot" + c.token + "/" + name
}

type tgResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
}

type tgUpdate struct {
	UpdateID    int64      `json:"update_id"`
	Message     *tgMessage `json:"message"`
	ChannelPost *tgMessage `json:"channel_post"`
}

// effective returns the message carried by the update, whether it arrived as a
// direct message or a channel post.
func (u tgUpdate) effective() *tgMessage {
	if u.Message != nil {
		return u.Message
	}
	return u.ChannelPost
}

type tgMessage struct {
	MessageID int64         `json:"message_id"`
	From      *tgUser       `json:"from"`
	Chat      *tgChat       `json:"chat"`
	Text      string        `json:"text"`
	Caption   string        `json:"caption"`
	Photo     []tgPhotoSize `json:"photo"`
	Document  *tgDocument   `json:"document"`
	Audio     *tgAudio      `json:"audio"`
	Voice     *tgVoice      `json:"voice"`
	Sticker   *tgSticker    `json:"sticker"`
}

type tgUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type tgChat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Username string `json:"username"`
}

type tgPhotoSize struct {
	FileID string `json:"file_id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type tgDocument struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
}

type tgAudio struct {
	FileID    string `json:"file_id"`
	Title     string `json:"title"`
	Performer string `json:"performer"`
}

type tgVoice struct {
	FileID string `json:"file_id"`
}

type tgSticker struct {
	Emoji string `json:"emoji"`
}

// getUpdates long-polls for new updates starting at offset. The context bounds
// the call; on ctx cancellation it returns ctx.Err().
func (c *tgClient) getUpdates(ctx context.Context, offset int64, timeoutSecs int) ([]tgUpdate, error) {
	q := url.Values{}
	q.Set("offset", strconv.FormatInt(offset, 10))
	q.Set("timeout", strconv.Itoa(timeoutSecs))
	q.Set("allowed_updates", `["message","channel_post"]`)

	// Give the HTTP read a little slack beyond the long-poll window.
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs+15)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, c.method("getUpdates")+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var updates []tgUpdate
	if err := c.do(req, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// sendMessage posts text. parseMode may be "HTML" (or "") -- when set, callers
// must HTML-escape any dynamic content they interpolate.
func (c *tgClient) sendMessage(ctx context.Context, chatID, text, parseMode string) error {
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", text)
	form.Set("disable_web_page_preview", "true")
	if parseMode != "" {
		form.Set("parse_mode", parseMode)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.method("sendMessage"), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req, nil)
}

// fileUpload describes a multipart media send (sendPhoto/sendAudio/...).
type fileUpload struct {
	method    string // API method, e.g. "sendPhoto"
	fileField string // primary file's form field, e.g. "photo"
	filename  string
	data      []byte
	caption   string
	parseMode string            // caption parse mode, e.g. "HTML"
	thumb     []byte            // optional JPEG thumbnail (<=320px, <200KB)
	extra     map[string]string // method-specific fields (title, performer, ...)
}

// sendFile uploads bytes (and an optional thumbnail) as a native attachment.
func (c *tgClient) sendFile(ctx context.Context, chatID string, up fileUpload) error {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("chat_id", chatID)
	if up.caption != "" {
		_ = w.WriteField("caption", up.caption)
	}
	if up.parseMode != "" {
		_ = w.WriteField("parse_mode", up.parseMode)
	}
	for k, v := range up.extra {
		if v != "" {
			_ = w.WriteField(k, v)
		}
	}
	part, err := w.CreateFormFile(up.fileField, up.filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(up.data); err != nil {
		return err
	}
	// Telegram references an attached thumbnail by its multipart field name via
	// the "attach://" scheme. Field name "thumb" <- form value "attach://thumb".
	if len(up.thumb) > 0 {
		_ = w.WriteField("thumbnail", "attach://thumb")
		tp, err := w.CreateFormFile("thumb", "thumb.jpg")
		if err != nil {
			return err
		}
		if _, err := tp.Write(up.thumb); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.method(up.method), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return c.do(req, nil)
}

func (c *tgClient) sendPhoto(ctx context.Context, chatID, filename string, data []byte, caption, parseMode string) error {
	return c.sendFile(ctx, chatID, fileUpload{
		method: "sendPhoto", fileField: "photo",
		filename: filename, data: data,
		caption: caption, parseMode: parseMode,
	})
}

func (c *tgClient) sendAudio(ctx context.Context, chatID, filename string, data []byte, caption, parseMode, title, performer string, thumb []byte) error {
	return c.sendFile(ctx, chatID, fileUpload{
		method: "sendAudio", fileField: "audio",
		filename: filename, data: data,
		caption: caption, parseMode: parseMode,
		thumb: thumb,
		extra: map[string]string{"title": title, "performer": performer},
	})
}

// do executes req, decodes the Bot API envelope, and unmarshals result into out
// (which may be nil to ignore the result body).
func (c *tgClient) do(req *http.Request, out interface{}) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	var env tgResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("telegram: decode response (status %d): %v", resp.StatusCode, err)
	}
	if !env.OK {
		return fmt.Errorf("telegram: api error (status %d): %s", resp.StatusCode, env.Description)
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// matchChat reports whether msg's chat is the one the bridge is bound to.
// configured may be a numeric id or an @username.
func matchChat(configured string, chat *tgChat) bool {
	if chat == nil {
		return false
	}
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return false
	}
	if strings.HasPrefix(configured, "@") {
		return strings.EqualFold(configured[1:], chat.Username)
	}
	if id, err := strconv.ParseInt(configured, 10, 64); err == nil {
		return id == chat.ID
	}
	return false
}
