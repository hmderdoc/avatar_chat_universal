package webchat

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
)

// The avatar library the page's Avatar modal picks from: the collections
// the server was given (the door's bundled ones, plus a sysop's), read
// only. A pick lives in the browser and rides on each message as
// nick.avatar, like the door's.
//
//	/avatars/index.json        [{id, name, title, count}]
//	/avatars/<id>.json         {id, name, avatars: [base64, ...]}

// maxCollectionAvatars caps one collection's reply.
const maxCollectionAvatars = 500

var collectionID = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

type collectionInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
	Count int    `json:"count"`
}

// avatarIndex lists the collections with at least one valid avatar, each
// with a URL-safe id (repeats get a numeric suffix).
func (s *Server) avatarIndex() ([]collectionInfo, map[string]*avatar.Collection) {
	var out []collectionInfo
	byID := map[string]*avatar.Collection{}
	for _, c := range s.Avatars {
		if c == nil || len(c.Avatars) == 0 {
			continue
		}
		id := strings.Trim(collectionID.ReplaceAllString(c.Name, "-"), "-.")
		if id == "" {
			id = "collection"
		}
		base := id
		for n := 2; byID[id] != nil; n++ {
			id = base + "-" + itoa(n)
		}
		byID[id] = c
		n := len(c.Avatars)
		if n > maxCollectionAvatars {
			n = maxCollectionAvatars
		}
		out = append(out, collectionInfo{ID: id, Name: c.Name, Title: c.Title, Count: n})
	}
	return out, byID
}

func (s *Server) handleAvatars(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/avatars/")
	if !strings.HasSuffix(name, ".json") || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSuffix(name, ".json")
	index, byID := s.avatarIndex()
	var body interface{}
	if id == "index" {
		if index == nil {
			index = []collectionInfo{}
		}
		body = index
	} else {
		c := byID[id]
		if c == nil {
			http.NotFound(w, r)
			return
		}
		list := make([]string, 0, len(c.Avatars))
		for _, a := range c.Avatars {
			if len(list) >= maxCollectionAvatars {
				break
			}
			list = append(list, a.Base64())
		}
		body = struct {
			ID      string   `json:"id"`
			Name    string   `json:"name"`
			Avatars []string `json:"avatars"`
		}{id, c.Name, list}
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=3600")
	h.Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(body)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
