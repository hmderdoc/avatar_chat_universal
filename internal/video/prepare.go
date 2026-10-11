package video

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// FileFetcher is the guarded downloader (inlineimg.Fetcher.FetchFile).
type FileFetcher interface {
	FetchFile(ctx context.Context, rawURL, path string, maxBytes int64) (int64, error)
}

// Media is a downloaded, probed video in its own private directory. Close
// removes the directory.
type Media struct {
	Dir   string
	File  string
	Probe Probe
	slot  func()
}

// Close releases the playback slot and deletes the download.
func (m *Media) Close() {
	if m == nil {
		return
	}
	if m.slot != nil {
		m.slot()
		m.slot = nil
	}
	if m.Dir != "" {
		os.RemoveAll(m.Dir)
		m.Dir = ""
	}
}

// ErrBusy means every host-wide playback slot is taken.
var ErrBusy = errors.New("video: the BBS is already playing as many videos as it allows; try again shortly")

// workDirPrefix names the private per-playback directories.
const workDirPrefix = "v-"

// Prepare takes a playback slot, downloads the link into a private
// directory and probes it. kind and stream come from Classify (stream is
// the URL to download: the link itself, or a page's og:video). The caller
// owns the returned Media and must Close it.
func Prepare(ctx context.Context, c Config, f FileFetcher, kind Kind, stream string) (*Media, error) {
	if !c.Enabled() {
		return nil, errors.New("video: ffmpeg is not installed here")
	}
	release, ok, err := acquireSlot(filepath.Join(c.Dir, "slots"), c.MaxConcurrent)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrBusy
	}
	m := &Media{slot: release}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		m.Close()
		return nil, err
	}
	if m.Dir, err = os.MkdirTemp(c.Dir, workDirPrefix); err != nil {
		m.Close()
		return nil, err
	}
	timeout := c.DownloadTimeout
	if timeout <= 0 {
		timeout = DefaultDownloadSeconds * time.Second
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	maxBytes := c.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxMB << 20
	}
	switch kind {
	case Direct:
		m.File = filepath.Join(m.Dir, "in")
		if _, err = f.FetchFile(dctx, stream, m.File, maxBytes); err != nil {
			m.Close()
			return nil, err
		}
	case Site:
		if c.YtDlp == "" || !c.SiteAllowed(stream) {
			m.Close()
			return nil, errors.New("video: that site is not on this BBS's video list")
		}
		if m.File, err = ytdlp(dctx, c, stream, m.Dir, maxBytes); err != nil {
			m.Close()
			return nil, err
		}
	default:
		m.Close()
		return nil, errors.New("video: not a video link")
	}
	if m.Probe, err = probe(dctx, c, m.File); err != nil {
		m.Close()
		return nil, err
	}
	return m, nil
}

// childEnv is the environment children run with: enough to find their
// own libraries and a home, nothing else from the door's environment.
func childEnv(dir string) []string {
	env := []string{"HOME=" + dir, "TMPDIR=" + dir, "LANG=C.UTF-8"}
	if p := os.Getenv("PATH"); p != "" {
		env = append(env, "PATH="+p)
	}
	if r := os.Getenv("SYSTEMROOT"); r != "" { // Windows needs it to start anything
		env = append(env, "SYSTEMROOT="+r)
	}
	return env
}

// command builds a child that is isolated from the door: own process
// group, minimal environment, working directory dir, no stdin.
func command(ctx context.Context, dir, bin string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = childEnv(dir)
	cmd.SysProcAttr = sysProcAttr()
	cmd.Cancel = func() error { killTree(cmd); return nil }
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

// run starts cmd at a lower priority and waits, returning stderr's head
// with the error.
func run(cmd *exec.Cmd) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd.Stdout = &limitWriter{w: &out, n: 64 << 10}
	cmd.Stderr = &limitWriter{w: &errb, n: 4 << 10}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	lowerPriority(cmd)
	err := cmd.Wait()
	if err != nil {
		if msg := firstLine(errb.String()); msg != "" {
			return out.Bytes(), fmt.Errorf("%s: %s", filepath.Base(cmd.Path), msg)
		}
		return out.Bytes(), err
	}
	return out.Bytes(), nil
}

func ytdlp(ctx context.Context, c Config, rawURL, dir string, maxBytes int64) (string, error) {
	if _, err := run(command(ctx, dir, c.YtDlp, YtDlpArgs(c, rawURL, filepath.Join(dir, "v.%(ext)s")))); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("video: download took too long")
		}
		return "", err
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "v.*"))
	if len(matches) != 1 {
		return "", errors.New("video: the site gave no single video file")
	}
	st, err := os.Lstat(matches[0])
	if err != nil || !st.Mode().IsRegular() {
		return "", errors.New("video: the site gave no single video file")
	}
	if st.Size() > maxBytes {
		return "", fmt.Errorf("video: over the %d MB cap", maxBytes>>20)
	}
	return matches[0], nil
}

func probe(ctx context.Context, c Config, path string) (Probe, error) {
	pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := run(command(pctx, filepath.Dir(path), c.FFprobe, ProbeArgs(path)))
	if err != nil {
		return Probe{}, ErrNotVideo
	}
	return ParseProbe(out)
}

// Purge removes work directories older than age under dir: leftovers of a
// door that was killed mid-play. Younger ones may belong to another door
// process playing right now.
func Purge(dir string, age time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-age)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), workDirPrefix) {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

type limitWriter struct {
	w interface{ Write([]byte) (int, error) }
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := len(p)
		if k > l.n {
			k = l.n
		}
		l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}
