// Package video plays video links on the caller's terminal with the host's
// ffmpeg, through the same renderer and audio path as the TV lounge.
//
// A chat message is written by whoever is on the other end, so neither
// ffmpeg nor yt-dlp is ever handed a URL from one:
//
//   - a direct link (.mp4 .m4v .webm .mov .mkv, or a page's og:video) is
//     downloaded by the door's guarded fetcher (public addresses only,
//     ports 80/443, every redirect re-checked, size capped) into a private
//     directory under the cache dir;
//   - a page link goes to yt-dlp only when its host is on the sysop's
//     allowlist (video_sites), with yt-dlp limited to the matching
//     extractors, no config files, no plugins, no cookies, no --exec, no
//     playlists, a size cap and a time limit, writing into the same kind
//     of private directory;
//   - ffprobe and ffmpeg then read only that local file: protocol
//     whitelist "file", demuxer whitelist mov/matroska (so a "video" that
//     is really an HLS or concat playlist naming other files is refused),
//     the demuxer pinned to the probed one, no stdin.
//
// HLS (.m3u8) and DASH links are not played: their segments are fetched by
// ffmpeg itself, which the guards cannot see.
//
// Children run in their own process group (killed whole on stop, on
// hangup through Pdeathsig on Linux), at a lower priority, under a
// duration cap; a host-wide slot lock caps concurrent playbacks across all
// door processes.
package video

import (
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Kind is how a link would be played.
type Kind int

const (
	None   Kind = iota
	Direct      // a video file, downloaded by the guarded fetcher
	Site        // a page on an allowlisted site, downloaded by yt-dlp
)

// Config is the sysop's video setup, resolved at startup.
type Config struct {
	FFmpeg  string // resolved path; "" means video is off
	FFprobe string // resolved path; required with FFmpeg
	YtDlp   string // resolved path; "" means page links are not played

	// Sites are the hosts whose page links go to yt-dlp (a host or any
	// subdomain of it); Extractors are the yt-dlp extractors it may use.
	Sites      []string
	Extractors []string

	MaxBytes        int64         // per download
	MaxSeconds      int           // per playback
	DownloadTimeout time.Duration // per download
	MaxConcurrent   int           // playbacks at once, host wide
	Dir             string        // private work dirs live under it
}

// Defaults for the keys the sysop leaves unset.
const (
	DefaultMaxMB           = 100
	DefaultMaxSeconds      = 600
	DefaultDownloadSeconds = 180
	DefaultMaxConcurrent   = 2
)

// DefaultSites and DefaultExtractors are the yt-dlp allowlist out of the
// box.
var (
	DefaultSites      = []string{"youtube.com", "youtu.be", "vimeo.com"}
	DefaultExtractors = []string{"youtube", "vimeo"}
)

// Enabled reports whether ffmpeg and ffprobe were found.
func (c Config) Enabled() bool { return c.FFmpeg != "" && c.FFprobe != "" }

var fileExt = regexp.MustCompile(`(?i)\.(mp4|m4v|webm|mov|mkv)$`)

// Ext is the lower-case video extension of a link's path ("" if none).
func Ext(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	m := fileExt.FindStringSubmatch(u.Path)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}

// SiteAllowed reports whether rawURL is an http(s) link, on the default
// port, without credentials, to one of the allowlisted hosts.
func (c Config) SiteAllowed(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return false
	}
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	for _, s := range c.Sites {
		s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), ".")
		if s != "" && (host == s || strings.HasSuffix(host, "."+s)) {
			return true
		}
	}
	return false
}

// Classify says how rawURL would be played under c.
func (c Config) Classify(rawURL string) Kind {
	if !c.Enabled() {
		return None
	}
	if Ext(rawURL) != "" {
		return Direct
	}
	if c.YtDlp != "" && len(c.Extractors) > 0 && c.SiteAllowed(rawURL) {
		return Site
	}
	return None
}
