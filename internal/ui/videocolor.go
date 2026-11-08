package ui

import (
	"strings"
	"time"
)

// Video colour: what depth the TV lounge / video picture uses.
//
// The user's setting (/settings, stored per user) is "auto", "truecolor" or
// "16". Auto paints truecolor and steps down to 16 colours while the caller's
// link cannot drain truecolor video frames at videoMinFPS; it retries
// truecolor after a backoff. A link that never pushes back never steps down,
// so fast callers see no change. tvColorTrue() (the terminal's capability,
// from tv_color / the startup probe / Ctrl-T) is always the ceiling.

const (
	videoColorAuto = "auto"
	videoColorTrue = "truecolor"
	videoColor16   = "16"

	videoSlowFrame  = 250 * time.Millisecond // slower drain than this = under 4 fps
	videoSlowFor    = 3 * time.Second        // ... sustained this long steps down
	videoRetryFirst = time.Minute
	videoRetryMax   = 10 * time.Minute
	videoSettled    = 5 * time.Minute // truecolor held this long forgets the backoff
	videoMinSample  = 4096            // smaller frames (chat echo) say nothing about the link
)

// normVideoColor maps a stored or typed value to one of the three settings.
func normVideoColor(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case videoColorTrue, "24", "24bit", "true":
		return videoColorTrue
	case videoColor16, "cga":
		return videoColor16
	}
	return videoColorAuto
}

// nextVideoColor cycles the setting (settings panel v/V).
func nextVideoColor(v string, dir int) string {
	order := []string{videoColorAuto, videoColorTrue, videoColor16}
	i := 0
	for j, o := range order {
		if o == normVideoColor(v) {
			i = j
		}
	}
	return order[(i+dir+len(order))%len(order)]
}

// videoGovernor is Auto's state.
type videoGovernor struct {
	reduced   bool
	slowSince time.Time
	retryAt   time.Time
	backoff   time.Duration
	trueSince time.Time
}

// frameDone records one truecolor video frame that took `drain` to write;
// bound = the link pushed back while writing it. Reports a step down.
func (g *videoGovernor) frameDone(now time.Time, drain time.Duration, bound bool) bool {
	if g.reduced {
		return false
	}
	if g.trueSince.IsZero() {
		g.trueSince = now
	}
	if !bound || drain <= videoSlowFrame {
		g.slowSince = time.Time{}
		if g.backoff > 0 && now.Sub(g.trueSince) >= videoSettled {
			g.backoff = 0
		}
		return false
	}
	if g.slowSince.IsZero() {
		g.slowSince = now.Add(-drain)
	}
	if now.Sub(g.slowSince) < videoSlowFor {
		return false
	}
	g.reduced = true
	g.slowSince = time.Time{}
	if g.backoff == 0 {
		g.backoff = videoRetryFirst
	} else if g.backoff *= 2; g.backoff > videoRetryMax {
		g.backoff = videoRetryMax
	}
	g.retryAt = now.Add(g.backoff)
	return true
}

// tick reports a step back up to truecolor once the backoff has passed.
func (g *videoGovernor) tick(now time.Time) bool {
	if !g.reduced || now.Before(g.retryAt) {
		return false
	}
	g.reduced = false
	g.trueSince = now
	return true
}

// videoTrue: paint the picture in truecolor right now?
func (a *App) videoTrue() bool {
	if !a.tvColorTrue() {
		return false
	}
	switch normVideoColor(a.VideoColor) {
	case videoColorTrue:
		return true
	case videoColor16:
		return false
	}
	return !a.vgov.reduced
}

// videoColorStatus is the settings panel's line for the current setting.
func (a *App) videoColorStatus(setting string) string {
	switch normVideoColor(setting) {
	case videoColorTrue:
		return "Truecolor"
	case videoColor16:
		return "16 colors"
	}
	if !a.tvColorTrue() {
		return "Auto (now 16 colors: terminal)"
	}
	if a.vgov.reduced {
		return "Auto (now 16 colors: slow link)"
	}
	return "Auto (now truecolor)"
}
