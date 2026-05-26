package matrixbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// mxClient is a minimal Matrix Client-Server API client: a long-poll sync loop
// plus the few send/upload methods the bridge needs. A full Matrix SDK is
// overkill for forwarding one room, and hand-rolling keeps the binary
// dependency-free (mirroring the Telegram bridge's own client).
type mxClient struct {
	homeserver string // base URL, no trailing slash
	token      string
	http       *http.Client

	txn uint64 // monotonic counter feeding unique transaction ids

	dnMu    sync.Mutex
	dnCache map[string]string // mxid -> display name
}

func newMXClient(homeserver, token string) *mxClient {
	return &mxClient{
		homeserver: strings.TrimRight(homeserver, "/"),
		token:      token,
		// No client-wide timeout: sync long-polls. Per-call timeouts come from
		// the request context instead.
		http:    &http.Client{},
		dnCache: map[string]string{},
	}
}

// mxEvent is one timeline event. Content is left raw so it can be decoded based
// on msgtype.
type mxEvent struct {
	Type     string          `json:"type"`
	Sender   string          `json:"sender"`
	EventID  string          `json:"event_id"`
	Content  json.RawMessage `json:"content"`
	OriginTs int64           `json:"origin_server_ts"`
}

// mxMessageContent is the body of an m.room.message event.
type mxMessageContent struct {
	MsgType  string  `json:"msgtype"`
	Body     string  `json:"body"`
	URL      string  `json:"url"`
	Info     *mxInfo `json:"info"`
	FileName string  `json:"filename"`
}

type mxInfo struct {
	MimeType string `json:"mimetype"`
}

type mxSyncResponse struct {
	NextBatch string `json:"next_batch"`
	Rooms     struct {
		Join map[string]struct {
			Timeline struct {
				Events []mxEvent `json:"events"`
			} `json:"timeline"`
		} `json:"join"`
	} `json:"rooms"`
}

// sync long-polls /sync starting at the given batch token. The context bounds
// the call; on ctx cancellation it returns ctx.Err(). It returns the timeline
// events for roomID and the next batch token to pass on the following call.
func (c *mxClient) sync(ctx context.Context, since, roomID string, timeoutMs int) ([]mxEvent, string, error) {
	q := url.Values{}
	q.Set("timeout", strconv.Itoa(timeoutMs))
	if since != "" {
		q.Set("since", since)
	}

	// Give the HTTP read a little slack beyond the long-poll window.
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs/1000+15)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, c.homeserver+"/_matrix/client/v3/sync?"+q.Encode(), nil)
	if err != nil {
		return nil, since, err
	}
	c.auth(req)
	var resp mxSyncResponse
	if err := c.do(req, &resp); err != nil {
		return nil, since, err
	}
	next := resp.NextBatch
	if next == "" {
		next = since
	}
	if room, ok := resp.Rooms.Join[roomID]; ok {
		return room.Timeline.Events, next, nil
	}
	return nil, next, nil
}

// sendText sends a plain text message into roomID.
func (c *mxClient) sendText(ctx context.Context, roomID, text string) error {
	body := map[string]interface{}{
		"msgtype": "m.text",
		"body":    text,
	}
	return c.sendEvent(ctx, roomID, body)
}

// sendHTML sends a formatted (org.matrix.custom.html) message: clients that
// render HTML show formatted; others fall back to the plain body.
func (c *mxClient) sendHTML(ctx context.Context, roomID, plain, formatted string) error {
	body := map[string]interface{}{
		"msgtype":        "m.text",
		"body":           plain,
		"format":         "org.matrix.custom.html",
		"formatted_body": formatted,
	}
	return c.sendEvent(ctx, roomID, body)
}

// sendMedia uploads bytes to the content repository and posts a message event
// referencing the resulting mxc URL. msgtype is m.image/m.audio/m.file. On
// upload failure it returns the error so the caller can fall back to text.
func (c *mxClient) sendMedia(ctx context.Context, roomID, msgtype, filename, contentType string, data []byte) error {
	mxc, err := c.upload(ctx, filename, contentType, data)
	if err != nil {
		return err
	}
	body := map[string]interface{}{
		"msgtype": msgtype,
		"body":    filename,
		"url":     mxc,
		"info": map[string]interface{}{
			"mimetype": contentType,
		},
	}
	return c.sendEvent(ctx, roomID, body)
}

// sendEvent PUTs a message event with a unique transaction id.
func (c *mxClient) sendEvent(ctx context.Context, roomID string, content map[string]interface{}) error {
	payload, err := json.Marshal(content)
	if err != nil {
		return err
	}
	txnID := c.nextTxnID()
	u := c.homeserver + "/_matrix/client/v3/rooms/" + url.PathEscape(roomID) + "/send/m.room.message/" + url.PathEscape(txnID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	c.auth(req)
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, nil)
}

// upload sends raw bytes to the media repository and returns the mxc:// URI.
func (c *mxClient) upload(ctx context.Context, filename, contentType string, data []byte) (string, error) {
	q := url.Values{}
	if filename != "" {
		q.Set("filename", filename)
	}
	u := c.homeserver + "/_matrix/media/v3/upload"
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	c.auth(req)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	req.Header.Set("Content-Type", contentType)
	var resp struct {
		ContentURI string `json:"content_uri"`
	}
	if err := c.do(req, &resp); err != nil {
		return "", err
	}
	if resp.ContentURI == "" {
		return "", fmt.Errorf("matrix: upload returned no content_uri")
	}
	return resp.ContentURI, nil
}

// displayName resolves a sender's display name with a small in-memory cache,
// falling back to the mxid localpart. It is best-effort and resilient: any
// error yields the localpart fallback.
func (c *mxClient) displayName(ctx context.Context, userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "matrix"
	}
	c.dnMu.Lock()
	if name, ok := c.dnCache[userID]; ok {
		c.dnMu.Unlock()
		return name
	}
	c.dnMu.Unlock()

	name := localpart(userID)
	u := c.homeserver + "/_matrix/client/v3/profile/" + url.PathEscape(userID) + "/displayname"
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil); err == nil {
		c.auth(req)
		var resp struct {
			DisplayName string `json:"displayname"`
		}
		if err := c.do(req, &resp); err == nil {
			if dn := strings.TrimSpace(resp.DisplayName); dn != "" {
				name = dn
			}
		}
	}
	c.dnMu.Lock()
	c.dnCache[userID] = name
	c.dnMu.Unlock()
	return name
}

func (c *mxClient) auth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
}

func (c *mxClient) nextTxnID() string {
	n := atomic.AddUint64(&c.txn, 1)
	return strconv.FormatInt(time.Now().UnixNano(), 10) + "." + strconv.FormatUint(n, 10)
}

// do executes req and unmarshals the JSON body into out (which may be nil to
// ignore the body). Non-2xx responses surface the Matrix error payload.
func (c *mxClient) do(req *http.Request, out interface{}) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			ErrCode string `json:"errcode"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(body, &apiErr)
		if apiErr.Error != "" {
			return fmt.Errorf("matrix: api error (status %d): %s %s", resp.StatusCode, apiErr.ErrCode, apiErr.Error)
		}
		return fmt.Errorf("matrix: api error (status %d)", resp.StatusCode)
	}
	if out != nil && len(body) > 0 {
		return json.Unmarshal(body, out)
	}
	return nil
}

// localpart turns @bot:server into "bot"; it drops the leading @ and the
// :server suffix. Used as a display-name fallback.
func localpart(mxid string) string {
	s := strings.TrimPrefix(strings.TrimSpace(mxid), "@")
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "matrix"
	}
	return s
}
