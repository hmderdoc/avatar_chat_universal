package chatserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The store's snapshot on disk (Server.StorePath): every scalar and array
// (history, the public_channels registry, TV tuner state) and the names
// doors and bridges have used. Subscriptions are not kept; clients make
// them again when they reconnect. One JSON object, written to a temp file
// and renamed over the old one, so a crash mid-save leaves the last good
// snapshot.

// snapshotVersion is the "version" written; LoadStore refuses newer ones.
const snapshotVersion = 1

type snapshot struct {
	Version int                          `json:"version"`
	Saved   int64                        `json:"saved"` // unix seconds
	Scalars map[string]json.RawMessage   `json:"scalars"`
	Arrays  map[string][]json.RawMessage `json:"arrays"`
	Names   map[string]int64             `json:"bbs_names,omitempty"`
}

// LoadStore reads the snapshot at s.StorePath into the store. A missing
// file is an empty store. A file that can't be parsed (or is from a newer
// server) is logged, renamed aside to <path>.bad-<time> and the server
// starts empty, so one bad write never keeps the server from starting.
// An error means the file is there but couldn't be read or moved; the
// caller should then not save over it. Call it before serving.
func (s *Server) LoadStore() error {
	if s.StorePath == "" {
		return nil
	}
	b, err := os.ReadFile(s.StorePath)
	if errors.Is(err, os.ErrNotExist) {
		s.Logger.Printf("chatserver: store %s: none yet, starting empty", s.StorePath)
		return nil
	}
	if err != nil {
		return fmt.Errorf("chatserver: store %s: %v", s.StorePath, err)
	}
	var snap snapshot
	perr := json.Unmarshal(b, &snap)
	if perr == nil && snap.Version == 0 {
		perr = errors.New("no version")
	}
	if perr == nil && snap.Version > snapshotVersion {
		perr = fmt.Errorf("version %d is newer than this server's %d", snap.Version, snapshotVersion)
	}
	if perr != nil {
		bad := s.StorePath + ".bad-" + s.now().UTC().Format("20060102T150405Z")
		if err := os.Rename(s.StorePath, bad); err != nil {
			return fmt.Errorf("chatserver: store %s is unreadable (%v) and can't be moved aside: %v", s.StorePath, perr, err)
		}
		s.Logger.Printf("chatserver: STORE %s IS UNREADABLE (%v); moved it to %s and starting with an EMPTY store", s.StorePath, perr, bad)
		return nil
	}

	st := s.store
	st.mu.Lock()
	defer st.mu.Unlock()
	for k, v := range snap.Scalars {
		st.scalars[k] = v
	}
	for k, a := range snap.Arrays {
		if s.MaxHistory > 0 && len(a) > s.MaxHistory {
			a = a[len(a)-s.MaxHistory:]
		}
		st.arrays[k] = a
	}
	for k, t := range snap.Names {
		st.names[k] = t
	}
	s.Logger.Printf("chatserver: store %s: loaded %d values, %d lists, %d door names", s.StorePath, len(st.scalars), len(st.arrays), len(st.names))
	return nil
}

// persistLoop saves the store s.saveEvery after a change, so a burst of
// messages is one write, until ctx ends. The final save is
// ListenAndServe's.
func (s *Server) persistLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.store.changed:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.saveEvery):
		}
		if err := s.saveStore(); err != nil {
			s.Logger.Printf("chatserver: %v", err)
		}
	}
}

// saveStore writes the snapshot when the store changed since the last
// save. Door names older than NameRetention are dropped first.
func (s *Server) saveStore() error {
	if s.StorePath == "" {
		return nil
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()

	st := s.store
	st.mu.Lock()
	if st.gen == s.savedGen {
		st.mu.Unlock()
		return nil
	}
	cutoff := s.now().Add(-s.NameRetention).Unix()
	for k, t := range st.names {
		if t < cutoff {
			delete(st.names, k)
		}
	}
	gen := st.gen
	b, err := json.Marshal(snapshot{
		Version: snapshotVersion,
		Saved:   s.now().Unix(),
		Scalars: st.scalars,
		Arrays:  st.arrays,
		Names:   st.names,
	})
	st.mu.Unlock()
	if err != nil {
		return fmt.Errorf("store %s: %v", s.StorePath, err)
	}

	if dir := filepath.Dir(s.StorePath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("store %s: %v", s.StorePath, err)
		}
	}
	tmp := s.StorePath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("store %s: %v", s.StorePath, err)
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, s.StorePath)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("store %s: %v", s.StorePath, err)
	}
	s.savedGen = gen
	return nil
}
