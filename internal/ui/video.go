package ui

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/apcaudio"
	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
	"github.com/hmderdoc/avatar_chat_universal/internal/telnetvision"
	"github.com/hmderdoc/avatar_chat_universal/internal/video"
)

// VideoConfig turns on /play for video links: the sysop's ffmpeg (and
// optionally yt-dlp) setup and the guarded downloader. Playback only ever
// starts on /play, and takes over the screen like the TV lounge.
type VideoConfig struct {
	Video   video.Config
	Fetcher video.FileFetcher
}

// videoMark leads the one-line card under a video link.
const videoMark = 0x10 // CP437 right-pointing triangle

type videoResult struct {
	url   string
	title string
	media *video.Media
	err   error
}

type videoState struct {
	cfg     VideoConfig
	loading string // URL being downloaded
	playing string // URL on screen
	cancel  context.CancelFunc
	results chan videoResult
	player  *video.Player
}

// videoPurgeAge is how old a leftover work dir must be before a starting
// door removes it: older than any download plus playback another door
// could still be running.
func videoPurgeAge(c video.Config) time.Duration {
	age := time.Duration(c.MaxSeconds)*time.Second + c.DownloadTimeout + time.Hour
	if age < 2*time.Hour {
		age = 2 * time.Hour
	}
	return age
}

// EnableVideo turns on video-link cards and /play for them.
func (a *App) EnableVideo(cfg VideoConfig) {
	if !cfg.Video.Enabled() || cfg.Fetcher == nil {
		return
	}
	a.videos = &videoState{cfg: cfg, results: make(chan videoResult, 4)}
	a.transcript.AudioLabel = a.mediaLabel
	go video.Purge(cfg.Video.Dir, videoPurgeAge(cfg.Video))
}

// videoTrack resolves a chat link to a playable video: a link naming a
// video file, a page whose card announced og:video, or a page on an
// allowlisted site (yt-dlp). NSFW links stay unplayable until /nsfw.
func (a *App) videoTrack(u string) (kind video.Kind, stream, name string, ok bool) {
	if a.videos == nil {
		return video.None, "", "", false
	}
	c := a.videos.cfg.Video
	if video.Ext(u) != "" {
		if a.nsfwHidden(u) {
			return video.None, "", "", false
		}
		return video.Direct, u, linkName(u), true
	}
	if a.cards != nil {
		if s, n, ok := a.cards.video(u); ok {
			return video.Direct, s, n, true
		}
	}
	if c.Classify(u) == video.Site {
		if a.nsfwHidden(u) {
			return video.None, "", "", false
		}
		name = linkName(u)
		if a.cards != nil {
			if t := a.cards.title(u); t != "" {
				name = t
			}
		}
		return video.Site, u, name, true
	}
	return video.None, "", "", false
}

// nsfwHidden reports that u trips the NSFW keywords and /nsfw is off.
func (a *App) nsfwHidden(u string) bool {
	switch {
	case a.images != nil:
		return !a.images.revealed && inlineimg.LooksNSFW(u, a.images.cfg.NSFWKeywords)
	case a.cards != nil:
		return !a.cards.revealed && inlineimg.LooksNSFW(u, a.cards.cfg.NSFWKeywords)
	}
	return inlineimg.LooksNSFW(u, inlineimg.DefaultNSFWKeywords)
}

// linkName is a link's file name, or its host for a page.
func linkName(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	if b := path.Base(p.Path); b != "." && b != "/" && b != "" && strings.Contains(b, ".") {
		if un, err := url.PathUnescape(b); err == nil {
			return un
		}
		return b
	}
	return strings.TrimPrefix(p.Hostname(), "www.")
}

// videoLabel is the card line under a video link, "" when u is not one.
func (a *App) videoLabel(u string) string {
	_, _, name, ok := a.videoTrack(u)
	if !ok {
		return ""
	}
	mark := string(rune(videoMark)) + " "
	switch u {
	case a.videos.playing:
		return mark + "playing " + name + "  Esc or /stop"
	case a.videos.loading:
		return mark + "loading " + name + "..."
	}
	return mark + name + "  /play"
}

// mediaLabel is the card line for any playable link: audio first (when the
// terminal plays it), then video.
func (a *App) mediaLabel(u string) string {
	if a.alinks != nil {
		if l := a.audioLabel(u); l != "" {
			return l
		}
	}
	if a.videos != nil {
		return a.videoLabel(u)
	}
	return ""
}

// playVideo downloads u in the background; videoTick starts it.
func (a *App) playVideo(u string, kind video.Kind, stream, name string) {
	vs := a.videos
	a.stopAudio()
	a.stopVideo()
	ctx, cancel := context.WithCancel(context.Background())
	vs.cancel = cancel
	vs.loading = u
	how := "downloading"
	if kind == video.Site {
		how = "fetching from " + linkName(u)
	}
	a.Session.Notice("loading " + name + " (" + how + " first; Esc or /stop cancels)...")
	a.dirty = true
	go func(cfg VideoConfig, results chan<- videoResult) {
		m, err := video.Prepare(ctx, cfg.Video, cfg.Fetcher, kind, stream)
		if ctx.Err() != nil {
			m.Close() // cancelled while downloading
			return
		}
		results <- videoResult{url: u, title: name, media: m, err: err}
	}(vs.cfg, vs.results)
}

// videoPlaying reports a video on screen.
func (a *App) videoPlaying() bool { return a.videos != nil && a.videos.player != nil }

// videoBusy reports a video downloading or playing.
func (a *App) videoBusy() bool {
	return a.videos != nil && (a.videos.player != nil || a.videos.loading != "")
}

// stopVideo cancels a download or ends playback, handing the screen back
// (to the TV feed, when the room is tuned). It reports whether anything
// was running.
func (a *App) stopVideo() bool {
	vs := a.videos
	if vs == nil {
		return false
	}
	was := vs.player != nil || vs.loading != ""
	if vs.cancel != nil {
		vs.cancel()
		vs.cancel = nil
	}
	vs.loading = ""
	if vs.player != nil {
		vs.player.Close()
		vs.player = nil
		vs.playing = ""
		if a.tvAudio != nil { // the video's own audio session
			_ = a.tvAudio.Stop(&a.audioOut)
			a.tvAudio = nil
		}
		a.clearLoungeResidue()
	}
	a.dirty = true
	return was
}

// videoTick starts downloaded videos and ends finished ones.
func (a *App) videoTick() {
	vs := a.videos
	if vs == nil {
		return
	}
	select {
	case r := <-vs.results:
		if r.url != vs.loading {
			r.media.Close() // superseded by a later /play or /stop
			break
		}
		vs.loading = ""
		vs.cancel = nil
		a.dirty = true
		if r.err != nil {
			a.Session.Notice(fmt.Sprintf("/play: %v", r.err))
			break
		}
		a.startVideo(r)
	default:
	}
	if vs.player != nil && vs.player.Done() {
		err := vs.player.Err()
		a.stopVideo()
		if err != nil {
			a.Session.Notice(fmt.Sprintf("/play: %v", err))
		} else {
			a.Session.Notice("video ended")
		}
	}
}

func (a *App) startVideo(r videoResult) {
	vs := a.videos
	if a.tvConsumer != nil { // the video takes the screen from the TV feed
		a.closeTVFeed()
	}
	hasAudio := r.media.Probe.HasAudio
	wantAudio := a.AudioOK && a.TVAudio
	p, err := video.Play(vs.cfg.Video, r.media, r.title, a.bgAnimFrame.W, a.bgAnimFrame.H, wantAudio)
	if err != nil {
		a.Session.Notice(fmt.Sprintf("/play: %v", err))
		return
	}
	vs.player = p
	vs.playing = r.url
	if wantAudio && hasAudio {
		a.tvAudio = apcaudio.NewSession(apcaudio.Defaults(2, "vd"))
		_ = a.tvAudio.Start(&a.audioOut)
	}
	a.tvShowTranscript = false
	a.clearLoungeResidue()
	note := "playing " + r.title + " (Esc or /stop ends it"
	if hasAudio && !wantAudio {
		note += "; sound needs SyncTERM"
	}
	a.Session.Notice(note + ")")
}

// shutdownVideo stops everything video on exit, including downloads that
// finish after it.
func (a *App) shutdownVideo() {
	if a.videos == nil {
		return
	}
	a.stopVideo()
	for {
		select {
		case r := <-a.videos.results:
			r.media.Close()
		default:
			return
		}
	}
}

// loungeFeed is what the lounge screen plays: a telnetvision channel or a
// video link.
type loungeFeed interface {
	Latest() *telnetvision.Frame
	TakeAudio() []apcaudio.Chunk
}

// feed is the lounge's current source, nil when there is none.
func (a *App) feed() loungeFeed {
	if a.videoPlaying() {
		return a.videos.player
	}
	if a.tvConsumer != nil {
		return a.tvConsumer
	}
	return nil
}

// videoCaption is the caption bar while a video plays.
func (a *App) videoCaption() string {
	p := a.videos.player
	t := clock(p.Elapsed())
	if p.Seconds > 0 {
		t += " / " + clock(time.Duration(p.Seconds*float64(time.Second)))
	}
	return p.Title + "   " + t
}

func clock(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// videoHints replace the lounge's command strip while a video plays.
var videoHints = []string{"Esc or /stop end", "PgUp history", "/tvmute", "/tvcolor"}
