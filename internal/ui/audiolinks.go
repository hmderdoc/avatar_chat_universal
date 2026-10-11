package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/audiolink"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
)

// AudioLinkConfig turns on /play for audio links (mp3, Ogg Vorbis, FLAC,
// WAV). Playback only ever starts on /play.
type AudioLinkConfig struct {
	Fetcher *inlineimg.Fetcher
}

// audioNote is the one-line card under an audio link.
const audioNote = 0x0E // CP437 double note

type audioResult struct {
	url string
	src audiolink.Source
	err error
}

type audioLinks struct {
	cfg      AudioLinkConfig
	player   *audiolink.Player
	playing  string // URL of the current track
	loading  string // URL being fetched
	cancel   context.CancelFunc
	results  chan audioResult
	finished string // last track that played to the end, for the card
}

// EnableAudioLinks turns on audio-link cards and /play.
func (a *App) EnableAudioLinks(cfg AudioLinkConfig) {
	if cfg.Fetcher == nil {
		return
	}
	a.alinks = &audioLinks{cfg: cfg, results: make(chan audioResult, 4)}
	a.transcript.AudioLabel = a.audioLabel
}

// audioTrack resolves a link in chat to something /play can play: the link
// itself when it names an audio file, else the og:audio stream its preview
// card announced (a track page, such as a record label's). ok is false for
// any other link.
func (a *App) audioTrack(u string) (stream, ext, name string, ok bool) {
	if ext := audiolink.Ext(u); ext != "" {
		return u, ext, audiolink.Name(u), true
	}
	if a.cards != nil {
		return a.cards.audio(u)
	}
	return "", "", "", false
}

// audioLabel is the card text for an audio link, "" when u is not one.
func (a *App) audioLabel(u string) string {
	_, ext, name, ok := a.audioTrack(u)
	if !ok {
		return ""
	}
	note := string(rune(audioNote)) + " "
	switch {
	case !audiolink.Playable(ext):
		return note + name + " (." + ext + " can't play here)"
	case a.alinks.playing == u:
		return note + "playing " + name + "  /stop"
	case a.alinks.loading == u:
		return note + "loading " + name + "..."
	}
	return note + name + "  /play"
}

// audioURLs lists the audio links in the transcript, newest first.
func (a *App) audioURLs() []string {
	var out []string
	seen := map[string]bool{}
	msgs := a.Session.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m == nil || m.Nick == nil {
			continue
		}
		for _, u := range inlineimg.FindURLs(m.Str) {
			if _, _, _, ok := a.audioTrack(u); ok && !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
	}
	return out
}

// handlePlay runs "/play [N|list]".
func (a *App) handlePlay(args []string) {
	if a.alinks == nil {
		if !a.AudioOK {
			a.Session.Notice("/play: your terminal does not play audio (SyncTERM does)")
		} else {
			a.Session.Notice("/play: audio links are turned off here")
		}
		return
	}
	urls := a.audioURLs()
	if len(urls) == 0 {
		a.Session.Notice("/play: no audio links in this channel yet")
		return
	}
	n := 1
	if len(args) > 0 {
		if strings.EqualFold(args[0], "list") {
			for i, u := range urls {
				if i == 9 {
					break
				}
				_, _, name, _ := a.audioTrack(u)
				a.Session.Notice(fmt.Sprintf("  /play %d  %s", i+1, name))
			}
			return
		}
		v, err := strconv.Atoi(args[0])
		if err != nil || v < 1 || v > len(urls) {
			a.Session.Notice(fmt.Sprintf("/play: pick 1-%d (newest is 1), or /play list", len(urls)))
			return
		}
		n = v
	}
	u := urls[n-1]
	stream, ext, name, _ := a.audioTrack(u)
	if !audiolink.Playable(ext) {
		a.Session.Notice("/play: ." + ext + " files can't be played here (mp3, ogg, flac and wav can)")
		return
	}
	a.stopAudio()
	ctx, cancel := context.WithCancel(context.Background())
	a.alinks.cancel = cancel
	a.alinks.loading = u
	a.Session.Notice("loading " + name + "...")
	a.dirty = true
	go func(f *inlineimg.Fetcher, results chan<- audioResult) {
		data, err := f.Fetch(ctx, stream)
		var src audiolink.Source
		if err == nil {
			src, err = audiolink.Open(data, ext)
		}
		select {
		case results <- audioResult{url: u, src: src, err: err}:
		case <-ctx.Done():
		}
	}(a.alinks.cfg.Fetcher, a.alinks.results)
}

// stopAudio cancels a fetch and silences a playing track.
func (a *App) stopAudio() bool {
	if a.alinks == nil {
		return false
	}
	was := a.alinks.playing != "" || a.alinks.loading != ""
	if a.alinks.cancel != nil {
		a.alinks.cancel()
		a.alinks.cancel = nil
	}
	if a.alinks.player != nil {
		_ = a.alinks.player.Stop(&a.audioOut)
		a.alinks.player = nil
	}
	a.alinks.playing, a.alinks.loading = "", ""
	a.dirty = true
	return was
}

// audioTick starts fetched tracks and keeps the playing one fed.
func (a *App) audioTick(now time.Time) {
	al := a.alinks
	if al == nil {
		return
	}
	select {
	case r := <-al.results:
		if r.url != al.loading {
			break // superseded by a later /play or /stop
		}
		al.loading = ""
		al.cancel = nil
		a.dirty = true
		if r.err != nil {
			a.Session.Notice(fmt.Sprintf("/play: %v", r.err))
			break
		}
		al.player = audiolink.NewPlayer(audiolink.Name(r.url), r.src)
		al.playing = r.url
		_ = al.player.Start(&a.audioOut)
		a.Session.Notice("playing " + al.player.Title + " (/stop to stop)")
	default:
	}
	if al.player == nil {
		return
	}
	if a.audioOut.Len() > audioOutMax {
		return // the link is behind; let it drain before queueing more
	}
	if err := al.player.Tick(&a.audioOut, now); err != nil {
		a.stopAudio()
		return
	}
	if al.player.Done(now) {
		if err := al.player.Err(); err != nil {
			a.Session.Notice(fmt.Sprintf("/play: stopped early: %v", err))
		}
		al.player = nil
		al.playing = ""
		a.dirty = true
	}
}
