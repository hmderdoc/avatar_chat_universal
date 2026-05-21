package bridgemedia

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// Fetcher downloads a remote media URL so a bridge can re-upload it natively.
// Bridges inject one (defaulting to NewHTTPFetcher) and stub it in tests.
type Fetcher func(rawURL string) ([]byte, error)

// NewHTTPFetcher returns a Fetcher that GETs a URL with a timeout and refuses
// bodies larger than maxBytes (so an oversize file fails cleanly rather than
// buffering unboundedly).
func NewHTTPFetcher(maxBytes int64) Fetcher {
	client := &http.Client{Timeout: 20 * time.Second}
	return func(rawURL string) ([]byte, error) {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "avatar-chat-bridge")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("status %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > maxBytes {
			return nil, fmt.Errorf("media exceeds %d bytes", maxBytes)
		}
		return data, nil
	}
}
