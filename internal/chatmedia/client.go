package chatmedia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Upload posts data to uploadURL (a media endpoint's /upload) under its
// original name and returns the URL to send in chat and how many days the
// server keeps it.
func Upload(ctx context.Context, uploadURL, name string, data []byte) (string, int, error) {
	u, err := url.Parse(uploadURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", 0, fmt.Errorf("upload_url %q is not an http(s) URL", uploadURL)
	}
	q := u.Query()
	q.Set("name", name)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(data))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("media server unreachable: %v", err)
	}
	defer resp.Body.Close()
	var r uploadReply
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&r); err != nil {
		return "", 0, fmt.Errorf("media server answered %s", resp.Status)
	}
	if r.Error != "" {
		return "", 0, errors.New(r.Error)
	}
	if resp.StatusCode != http.StatusOK || r.URL == "" {
		return "", 0, fmt.Errorf("media server answered %s", resp.Status)
	}
	return r.URL, r.ExpiresDays, nil
}
