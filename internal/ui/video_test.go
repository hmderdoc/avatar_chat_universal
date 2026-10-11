package ui

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/chat"
	"github.com/hmderdoc/avatar_chat_universal/internal/video"
)

// clipFetcher stands in for the guarded downloader by copying a local
// clip, so the UI flow is tested without loosening the address guard.
type clipFetcher struct{ src string }

func (c clipFetcher) FetchFile(_ context.Context, _ string, path string, max int64) (int64, error) {
	in, err := os.Open(c.src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	return io.Copy(out, io.LimitReader(in, max))
}

func videoTools(t *testing.T) (ffmpeg, ffprobe string) {
	ff, e1 := exec.LookPath("ffmpeg")
	fp, e2 := exec.LookPath("ffprobe")
	if e1 != nil || e2 != nil {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	return ff, fp
}

func testClip(t *testing.T, ffmpeg string, seconds string) string {
	clip := filepath.Join(t.TempDir(), "clip.mp4")
	out, err := exec.Command(ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=25",
		"-t", seconds, "-c:v", "libx264", "-pix_fmt", "yuv420p", clip).CombinedOutput()
	if err != nil {
		t.Fatalf("make clip: %v %s", err, out)
	}
	return clip
}

func enableTestVideo(t *testing.T, app *App, clip string) string {
	ff, fp := videoTools(t)
	dir := t.TempDir()
	app.EnableVideo(VideoConfig{
		Video: video.Config{FFmpeg: ff, FFprobe: fp, Dir: dir, MaxConcurrent: 2, MaxSeconds: 60,
			Sites: video.DefaultSites, Extractors: video.DefaultExtractors},
		Fetcher: clipFetcher{clip},
	})
	t.Cleanup(app.shutdownVideo)
	return dir
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func workDirs(dir string) int {
	n := 0
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "v-") {
			n++
		}
	}
	return n
}

// A video link gets a "/play" card line; /play downloads and plays it on
// the lounge screen with a caption; Esc stops it (without quitting the
// door) and removes the download.
func TestVideoPlayAndEscStops(t *testing.T) {
	addr := startUITestServer(t)
	sess := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "alice"}, "vidroom")
	if err := sess.Connect(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	app := NewApp(io.Discard, ansi.NewInput(strings.NewReader("")), sess, 80, 25, ansi.CharsetCP437)
	ff, _ := videoTools(t)
	dir := enableTestVideo(t, app, testClip(t, ff, "6"))

	link := "https://cdn.example/clip.mp4"
	if err := sess.Send("watch " + link); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the message", func() bool { sess.Cycle(); return len(app.audioURLs()) == 1 })
	if l := app.mediaLabel(link); !strings.Contains(l, "clip.mp4") || !strings.HasSuffix(l, "/play") {
		t.Fatalf("card line = %q", l)
	}
	if app.mediaLabel("https://example.com/page") != "" {
		t.Fatal("a plain page link got a play line")
	}

	app.handlePlay(nil)
	if !strings.Contains(app.mediaLabel(link), "loading") {
		t.Fatalf("card line while loading = %q", app.mediaLabel(link))
	}
	waitFor(t, "playback", func() bool { app.videoTick(); return app.videoPlaying() })
	if !app.loungeActive() || !strings.Contains(app.mediaLabel(link), "playing") {
		t.Fatal("video not on the lounge screen")
	}
	waitFor(t, "a frame", func() bool { return app.feed().Latest() != nil })
	app.drawLounge()
	if c := app.bgAnimFrame.CellAt(app.bgAnimFrame.W/2, app.bgAnimFrame.H/2); c.Char == 0 || c.Char == ' ' && c.Attr == 0 {
		t.Fatalf("no picture drawn: %+v", c)
	}
	if cap := app.videoCaption(); !strings.HasPrefix(cap, "clip.mp4") || !strings.Contains(cap, "0:06") {
		t.Fatalf("caption = %q", cap)
	}
	if workDirs(dir) != 1 {
		t.Fatalf("work dirs while playing = %d", workDirs(dir))
	}

	quit, err := app.handleKey(ansi.Key{Type: ansi.KeyEsc})
	if err != nil || quit {
		t.Fatalf("Esc during video: quit=%v err=%v", quit, err)
	}
	if app.videoBusy() || app.loungeActive() {
		t.Fatal("video still running after Esc")
	}
	if workDirs(dir) != 0 {
		t.Fatal("download left behind after Esc")
	}
	quit, _ = app.handleKey(ansi.Key{Type: ansi.KeyEsc})
	if !quit {
		t.Fatal("Esc with no video should still exit the door")
	}
}

// In a tuned room the video takes the screen from the TV feed and hands
// it back when it ends.
func TestVideoTakesOverTVAndHandsBack(t *testing.T) {
	app := loungeApp(t)
	ff, _ := videoTools(t)
	enableTestVideo(t, app, testClip(t, ff, "1"))
	if app.tvConsumer == nil {
		t.Fatal("TV feed not open")
	}
	app.playVideo("https://cdn.example/clip.mp4", video.Direct, "https://cdn.example/clip.mp4", "clip.mp4")
	waitFor(t, "playback", func() bool { app.videoTick(); return app.videoPlaying() })
	app.tvSync()
	if app.tvConsumer != nil {
		t.Fatal("TV feed still open under the video")
	}
	waitFor(t, "the end", func() bool { app.videoTick(); return !app.videoBusy() })
	app.tvSync()
	if app.tvConsumer == nil || !app.loungeActive() {
		t.Fatal("TV feed not back after the video")
	}
}

// NSFW-looking video links stay unplayable until /nsfw.
func TestVideoNSFWVeil(t *testing.T) {
	addr := startUITestServer(t)
	sess := chat.NewSession(chat.NewClient(addr), &chat.Nick{Name: "alice"}, "nsfwroom")
	if err := sess.Connect(context.Background(), 50); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Close() })
	app := NewApp(io.Discard, ansi.NewInput(strings.NewReader("")), sess, 80, 25, ansi.CharsetCP437)
	ff, _ := videoTools(t)
	enableTestVideo(t, app, testClip(t, ff, "1"))
	app.images = readyManager(ImageConfig{NSFWKeywords: []string{"nsfw"}})
	if _, _, _, ok := app.videoTrack("https://cdn.example/nsfw-clip.mp4"); ok {
		t.Fatal("NSFW video playable while veiled")
	}
	app.images.revealed = true
	if _, _, _, ok := app.videoTrack("https://cdn.example/nsfw-clip.mp4"); !ok {
		t.Fatal("NSFW video not playable after /nsfw")
	}
	app.videos.cfg.Video.YtDlp = "/x/yt-dlp"
	if k, _, _, ok := app.videoTrack("https://www.youtube.com/watch?v=x"); !ok || k != video.Site {
		t.Fatal("allowlisted site link not offered")
	}
}
