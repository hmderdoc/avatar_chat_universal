package slackbridge

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
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// slackClient is a minimal Slack Socket Mode + Web API client. Socket Mode is
// opened with the app-level token; Web API calls (postMessage, users.info, file
// uploads) use the bot token. A full Slack SDK is overkill for forwarding one
// channel, and hand-rolling keeps the dependency surface to the already-vendored
// gorilla/websocket (mirroring the Telegram bridge's own tgClient).
type slackClient struct {
	appToken string
	botToken string
	http     *http.Client

	conn *websocket.Conn

	nameMu    sync.Mutex
	nameCache map[string]string
}

func newSlackClient(appToken, botToken string) *slackClient {
	return &slackClient{
		appToken:  appToken,
		botToken:  botToken,
		http:      &http.Client{},
		nameCache: make(map[string]string),
	}
}

// --- Socket Mode envelope and event shapes ---------------------------------

type slackEnvelope struct {
	Type       string          `json:"type"`
	EnvelopeID string          `json:"envelope_id"`
	Payload    json.RawMessage `json:"payload"`
	Reason     string          `json:"reason"`
}

type slackEventPayload struct {
	TeamID string          `json:"team_id"`
	Event  slackEventInner `json:"event"`
}

type slackEventInner struct {
	Type    string      `json:"type"`
	Subtype string      `json:"subtype"`
	Channel string      `json:"channel"`
	User    string      `json:"user"`
	Text    string      `json:"text"`
	BotID   string      `json:"bot_id"`
	Team    string      `json:"team"`
	Files   []slackFile `json:"files"`
}

type slackFile struct {
	Name       string `json:"name"`
	Title      string `json:"title"`
	Mimetype   string `json:"mimetype"`
	Filetype   string `json:"filetype"`
	URLPrivate string `json:"url_private"`
}

// --- Socket Mode connection -------------------------------------------------

type connectionsOpenResponse struct {
	OK    bool   `json:"ok"`
	URL   string `json:"url"`
	Error string `json:"error"`
}

// open requests a Socket Mode websocket URL and dials it. The returned URL is a
// wss:// endpoint scoped to the app-level token.
func (c *slackClient) open(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/apps.connections.open", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.appToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var out connectionsOpenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("slack: decode connections.open (status %d): %v", resp.StatusCode, err)
	}
	if !out.OK || out.URL == "" {
		return fmt.Errorf("slack: connections.open failed (status %d): %s", resp.StatusCode, out.Error)
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, out.URL, nil)
	if err != nil {
		return fmt.Errorf("slack: dial socket: %v", err)
	}
	c.conn = conn
	return nil
}

func (c *slackClient) close() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

// readEnvelope reads one Socket Mode envelope from the websocket.
func (c *slackClient) readEnvelope() (*slackEnvelope, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("slack: socket not open")
	}
	_, data, err := c.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	var env slackEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("slack: decode envelope: %v", err)
	}
	return &env, nil
}

// ack acknowledges a Socket Mode envelope. Slack disconnects clients that don't
// ack within ~3s, so this must be sent immediately after reading any envelope
// that carries an envelope_id.
func (c *slackClient) ack(envelopeID string) error {
	if c.conn == nil {
		return fmt.Errorf("slack: socket not open")
	}
	payload, err := json.Marshal(map[string]string{"envelope_id": envelopeID})
	if err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.TextMessage, payload)
}

// --- Web API ----------------------------------------------------------------

type slackAPIResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// postMessage sends plain text to a channel via the bot token.
func (c *slackClient) postMessage(ctx context.Context, channel, text string) error {
	payload, err := json.Marshal(map[string]string{"channel": channel, "text": text})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/chat.postMessage", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.botToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	var out slackAPIResponse
	if err := c.do(req, &out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("slack: chat.postMessage failed: %s", out.Error)
	}
	return nil
}

// postBlocks posts a Block Kit message (blocks) with a plain-text fallback for
// notifications and non-rendering clients.
func (c *slackClient) postBlocks(ctx context.Context, channel string, blocks []map[string]interface{}, fallback string) error {
	payload, err := json.Marshal(map[string]interface{}{
		"channel": channel,
		"text":    fallback,
		"blocks":  blocks,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/chat.postMessage", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.botToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	var out slackAPIResponse
	if err := c.do(req, &out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("slack: chat.postMessage(blocks) failed: %s", out.Error)
	}
	return nil
}

// uploadFileRef uploads bytes and returns the new file's id WITHOUT sharing it
// to a channel, so it can be referenced from a Block Kit slack_file image
// element (e.g. an inline avatar) rather than posted as its own file message.
func (c *slackClient) uploadFileRef(ctx context.Context, filename string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("slack: empty upload")
	}
	if filename == "" {
		filename = "file"
	}
	form := url.Values{}
	form.Set("filename", filename)
	form.Set("length", strconv.Itoa(len(data)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/files.getUploadURLExternal", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.botToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var reserved getUploadURLResponse
	if err := c.do(req, &reserved); err != nil {
		return "", err
	}
	if !reserved.OK || reserved.UploadURL == "" || reserved.FileID == "" {
		return "", fmt.Errorf("slack: files.getUploadURLExternal failed: %s", reserved.Error)
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reserved.UploadURL, &body)
	if err != nil {
		return "", err
	}
	upReq.Header.Set("Content-Type", w.FormDataContentType())
	upResp, err := c.http.Do(upReq)
	if err != nil {
		return "", err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(upResp.Body, 1<<20))
	_ = upResp.Body.Close()
	if upResp.StatusCode < 200 || upResp.StatusCode >= 300 {
		return "", fmt.Errorf("slack: upload POST status %d", upResp.StatusCode)
	}

	// Complete WITHOUT channel_id so the file is created but not posted anywhere.
	payload, err := json.Marshal(map[string]interface{}{
		"files": []map[string]string{{"id": reserved.FileID}},
	})
	if err != nil {
		return "", err
	}
	compReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/files.completeUploadExternal", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	compReq.Header.Set("Authorization", "Bearer "+c.botToken)
	compReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	var done slackAPIResponse
	if err := c.do(compReq, &done); err != nil {
		return "", err
	}
	if !done.OK {
		return "", fmt.Errorf("slack: files.completeUploadExternal failed: %s", done.Error)
	}
	return reserved.FileID, nil
}

type usersInfoResponse struct {
	OK   bool `json:"ok"`
	User struct {
		Name     string `json:"name"`
		RealName string `json:"real_name"`
		Profile  struct {
			DisplayName string `json:"display_name"`
			RealName    string `json:"real_name"`
		} `json:"profile"`
	} `json:"user"`
	Error string `json:"error"`
}

// userName resolves a Slack user id to a human-friendly display name, with a
// small in-memory cache. On any error it falls back to the raw id.
func (c *slackClient) userName(ctx context.Context, userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "slack"
	}
	c.nameMu.Lock()
	if name, ok := c.nameCache[userID]; ok {
		c.nameMu.Unlock()
		return name
	}
	c.nameMu.Unlock()

	name := c.fetchUserName(ctx, userID)
	if name == "" {
		name = userID
	}
	c.nameMu.Lock()
	c.nameCache[userID] = name
	c.nameMu.Unlock()
	return name
}

func (c *slackClient) fetchUserName(ctx context.Context, userID string) string {
	q := url.Values{}
	q.Set("user", userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://slack.com/api/users.info?"+q.Encode(), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+c.botToken)
	var out usersInfoResponse
	if err := c.do(req, &out); err != nil || !out.OK {
		return ""
	}
	if n := strings.TrimSpace(out.User.Profile.DisplayName); n != "" {
		return n
	}
	if n := strings.TrimSpace(out.User.Profile.RealName); n != "" {
		return n
	}
	if n := strings.TrimSpace(out.User.RealName); n != "" {
		return n
	}
	if n := strings.TrimSpace(out.User.Name); n != "" {
		return n
	}
	return ""
}

// --- External file upload flow ----------------------------------------------

type getUploadURLResponse struct {
	OK        bool   `json:"ok"`
	UploadURL string `json:"upload_url"`
	FileID    string `json:"file_id"`
	Error     string `json:"error"`
}

// uploadFile performs Slack's external upload flow: reserve an upload URL, POST
// the bytes, then complete the upload into the channel with an optional caption.
// Any failure returns an error so the caller can fall back to a text post.
func (c *slackClient) uploadFile(ctx context.Context, channel, filename, title string, data []byte, caption string) error {
	if len(data) == 0 {
		return fmt.Errorf("slack: empty upload")
	}
	if filename == "" {
		filename = "file"
	}

	// 1. Reserve an upload URL.
	form := url.Values{}
	form.Set("filename", filename)
	form.Set("length", strconv.Itoa(len(data)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/files.getUploadURLExternal", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.botToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var reserved getUploadURLResponse
	if err := c.do(req, &reserved); err != nil {
		return err
	}
	if !reserved.OK || reserved.UploadURL == "" || reserved.FileID == "" {
		return fmt.Errorf("slack: files.getUploadURLExternal failed: %s", reserved.Error)
	}

	// 2. POST the bytes to the upload URL as multipart form field "file".
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reserved.UploadURL, &body)
	if err != nil {
		return err
	}
	upReq.Header.Set("Content-Type", w.FormDataContentType())
	upResp, err := c.http.Do(upReq)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(upResp.Body, 1<<20))
	_ = upResp.Body.Close()
	if upResp.StatusCode < 200 || upResp.StatusCode >= 300 {
		return fmt.Errorf("slack: upload POST status %d", upResp.StatusCode)
	}

	// 3. Complete the upload into the channel.
	type completeFile struct {
		ID    string `json:"id"`
		Title string `json:"title,omitempty"`
	}
	complete := map[string]interface{}{
		"files":      []completeFile{{ID: reserved.FileID, Title: title}},
		"channel_id": channel,
	}
	if caption != "" {
		complete["initial_comment"] = caption
	}
	payload, err := json.Marshal(complete)
	if err != nil {
		return err
	}
	compReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/files.completeUploadExternal", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	compReq.Header.Set("Authorization", "Bearer "+c.botToken)
	compReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	var done slackAPIResponse
	if err := c.do(compReq, &done); err != nil {
		return err
	}
	if !done.OK {
		return fmt.Errorf("slack: files.completeUploadExternal failed: %s", done.Error)
	}
	return nil
}

// do executes req and decodes the JSON body into out (which may be nil).
func (c *slackClient) do(req *http.Request, out interface{}) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("slack: decode response (status %d): %v", resp.StatusCode, err)
	}
	return nil
}

// callTimeout bounds a single Web API call.
func callTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 30*time.Second)
}
