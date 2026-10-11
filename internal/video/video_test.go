package video

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{FFmpeg: "/x/ffmpeg", FFprobe: "/x/ffprobe", YtDlp: "/x/yt-dlp",
		Sites: DefaultSites, Extractors: DefaultExtractors}
}

func TestClassify(t *testing.T) {
	c := testConfig()
	cases := map[string]Kind{
		"https://cdn.example/clip.mp4":                 Direct,
		"https://cdn.example/clip.WEBM?x=1":            Direct,
		"http://cdn.example/a/b.mkv#t=3":               Direct,
		"https://cdn.example/clip.m3u8":                None, // HLS: ffmpeg would fetch segments itself
		"https://cdn.example/clip.mp4.html":            None,
		"ftp://cdn.example/clip.mp4":                   None,
		"https://www.youtube.com/watch?v=abc":          Site,
		"https://youtu.be/abc":                         Site,
		"https://m.youtube.com/watch?v=abc":            Site,
		"https://player.vimeo.com/video/1":             Site,
		"https://notyoutube.com/watch?v=abc":           None,
		"https://youtube.com.evil.example/watch?v=abc": None,
		"https://user:pw@youtube.com/watch?v=abc":      None,
		"https://youtube.com:8443/watch?v=abc":         None,
		"https://example.com/page":                     None,
	}
	for u, want := range cases {
		if got := c.Classify(u); got != want {
			t.Errorf("Classify(%q) = %d, want %d", u, got, want)
		}
	}
	c.YtDlp = ""
	if c.Classify("https://youtu.be/abc") != None {
		t.Error("a site link was playable without yt-dlp")
	}
	if (Config{}).Classify("https://cdn.example/clip.mp4") != None {
		t.Error("a direct link was playable without ffmpeg")
	}
}

// No URL ever reaches ffprobe or ffmpeg: they get a file: path, the file
// protocol only, and the mov/matroska demuxers only.
func TestFFmpegArgvLocalOnly(t *testing.T) {
	path := "/data/videocache/v-1/in"
	for name, argv := range map[string][]string{
		"probe": ProbeArgs(path),
		"play":  PlayArgs(path, Probe{Demuxer: "mov", HasVideo: true, HasAudio: true}, 80, 22, 600, true),
	} {
		joined := strings.Join(argv, " ")
		if strings.Contains(joined, "http") {
			t.Errorf("%s argv mentions http: %s", name, joined)
		}
		if !hasPair(argv, "-protocol_whitelist", "file") || !hasPair(argv, "-format_whitelist", "mov,matroska") {
			t.Errorf("%s argv lacks the whitelists: %s", name, joined)
		}
		if argv[len(argv)-1] != "file:"+path && !strings.Contains(joined, "-i file:"+path) {
			t.Errorf("%s argv does not read the local file as file:: %s", name, joined)
		}
	}
	play := PlayArgs(path, Probe{Demuxer: "matroska", HasVideo: true}, 80, 22, 600, true)
	if !hasPair(play, "-f", "matroska") || !contains(play, "-nostdin") || !hasPair(play, "-t", "600") {
		t.Errorf("play argv: %v", play)
	}
	if contains(play, "pipe:3") {
		t.Error("audio output asked of a file with no audio track")
	}
}

func TestYtDlpArgs(t *testing.T) {
	c := testConfig()
	c.MaxBytes = 50 << 20
	argv := YtDlpArgs(c, "https://youtu.be/abc", "/d/v.%(ext)s")
	for _, flag := range []string{"--ignore-config", "--no-plugin-dirs", "--no-playlist", "--no-exec",
		"--no-cookies", "--no-cookies-from-browser", "--no-cache-dir"} {
		if !contains(argv, flag) {
			t.Errorf("yt-dlp argv lacks %s", flag)
		}
	}
	if !hasPair(argv, "--use-extractors", "youtube,vimeo") || !hasPair(argv, "--max-filesize", "50M") {
		t.Errorf("yt-dlp argv: %v", argv)
	}
	if n := len(argv); argv[n-2] != "--" || argv[n-1] != "https://youtu.be/abc" {
		t.Errorf("URL is not last after --: %v", argv[n-2:])
	}
	for _, a := range argv {
		if strings.HasPrefix(a, "--exec") && a != "--no-exec" || a == "--cookies" {
			t.Errorf("forbidden yt-dlp option %s", a)
		}
	}
}

func TestParseProbe(t *testing.T) {
	p, err := ParseProbe([]byte("stream,video\nstream,audio\nformat,\"mov,mp4,m4a,3gp,3g2,mj2\",2.000000\n"))
	if err != nil || p.Demuxer != "mov" || !p.HasVideo || !p.HasAudio || p.Seconds != 2 {
		t.Fatalf("mp4: %+v %v", p, err)
	}
	p, err = ParseProbe([]byte("stream,video\nformat,\"matroska,webm\",N/A\n"))
	if err != nil || p.Demuxer != "matroska" || p.HasAudio || p.Seconds != 0 {
		t.Fatalf("webm: %+v %v", p, err)
	}
	for _, out := range []string{"", "stream,audio\nformat,\"mov,mp4\",3\n", "stream,video\nformat,hls,3\n"} {
		if _, err := ParseProbe([]byte(out)); err == nil {
			t.Errorf("%q: accepted", out)
		}
	}
}

func TestSlotsCapAcrossHolders(t *testing.T) {
	dir := t.TempDir()
	r1, ok1, err := acquireSlot(dir, 2)
	r2, ok2, _ := acquireSlot(dir, 2)
	_, ok3, _ := acquireSlot(dir, 2)
	if err != nil || !ok1 || !ok2 || ok3 {
		t.Fatalf("slots: %v %v %v %v", ok1, ok2, ok3, err)
	}
	r1()
	r4, ok4, _ := acquireSlot(dir, 2)
	if !ok4 {
		t.Fatal("a released slot was not reusable")
	}
	r2()
	r4()
}

func TestPurgeKeepsYoungDirs(t *testing.T) {
	dir := t.TempDir()
	old, young := filepath.Join(dir, "v-old"), filepath.Join(dir, "v-young")
	os.Mkdir(old, 0o700)
	os.Mkdir(young, 0o700)
	os.Mkdir(filepath.Join(dir, "slots"), 0o700)
	past := time.Now().Add(-3 * time.Hour)
	os.Chtimes(old, past, past)
	Purge(dir, time.Hour)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("old work dir kept")
	}
	if _, err := os.Stat(young); err != nil {
		t.Error("young work dir removed (another door may be playing it)")
	}
	if _, err := os.Stat(filepath.Join(dir, "slots")); err != nil {
		t.Error("slots dir removed")
	}
}

// copyFetcher stands in for the guarded fetcher: it copies a local file,
// so the pipeline can be tested without weakening the address guard.
type copyFetcher struct{ src string }

func (c copyFetcher) FetchFile(_ context.Context, _ string, path string, max int64) (int64, error) {
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

func realTools(t *testing.T) Config {
	ff, err1 := exec.LookPath("ffmpeg")
	fp, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	return Config{FFmpeg: ff, FFprobe: fp, Dir: t.TempDir(), MaxConcurrent: 2, MaxSeconds: 30}
}

func makeClip(t *testing.T, c Config, name string, args ...string) string {
	path := filepath.Join(t.TempDir(), name)
	cmd := exec.Command(c.FFmpeg, append([]string{"-v", "error", "-y"}, append(args, path)...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make %s: %v %s", name, err, out)
	}
	return path
}

// A 2 s test clip goes through the real pipeline: download (copied),
// probe, ffmpeg at 12 fps into lounge-shaped frames plus PCM; Close
// removes the download and frees the slot.
func TestPlayRealClip(t *testing.T) {
	c := realTools(t)
	clip := makeClip(t, c, "t.mp4", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest")
	m, err := Prepare(context.Background(), c, copyFetcher{clip}, Direct, "https://cdn.example/t.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if m.Probe.Demuxer != "mov" || !m.Probe.HasAudio || m.Probe.Seconds < 1.5 {
		t.Fatalf("probe: %+v", m.Probe)
	}
	dir := m.Dir
	p, err := Play(c, m, "t", 40, 12, true)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	var chunks int
	for !p.Done() && time.Now().Before(deadline) {
		chunks += len(p.TakeAudio())
		time.Sleep(50 * time.Millisecond)
	}
	chunks += len(p.TakeAudio())
	fr := p.Latest()
	if !p.Done() || fr == nil || fr.Cols != 40 || fr.Rows != 12 || len(fr.Pixels) != 40*24*3 {
		t.Fatalf("done=%v frame=%+v", p.Done(), fr)
	}
	if p.frames < 18 || chunks < 12 {
		t.Fatalf("frames=%d audio chunks=%d for a 2 s clip", p.frames, chunks)
	}
	p.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("download not removed after Close")
	}
	for i := 0; i < 2; i++ {
		r, ok, _ := acquireSlot(filepath.Join(c.Dir, "slots"), 2)
		if !ok {
			t.Fatal("slot leaked after Close")
		}
		defer r()
	}
}

// Stopping mid-play kills ffmpeg and cleans up at once.
func TestStopMidPlay(t *testing.T) {
	c := realTools(t)
	clip := makeClip(t, c, "long.webm", "-f", "lavfi", "-i", "testsrc=size=64x48:rate=10", "-t", "20",
		"-c:v", "libvpx", "-b:v", "50k")
	m, err := Prepare(context.Background(), c, copyFetcher{clip}, Direct, "https://cdn.example/long.webm")
	if err != nil {
		t.Fatal(err)
	}
	dir := m.Dir
	p, err := Play(c, m, "long", 20, 6, false)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond)
	start := time.Now()
	p.Close()
	if time.Since(start) > 3*time.Second {
		t.Fatal("Close took too long")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("download not removed after a stop")
	}
}

// A file that is not mp4/mkv/webm (here an HLS playlist dressed as .mp4,
// which would make ffmpeg open what it names) is refused at the probe and
// its download removed.
func TestPrepareRefusesPlaylistDisguise(t *testing.T) {
	c := realTools(t)
	evil := filepath.Join(t.TempDir(), "evil.mp4")
	os.WriteFile(evil, []byte("#EXTM3U\n#EXTINF:1,\nfile:/etc/hostname\n"), 0o600)
	_, err := Prepare(context.Background(), c, copyFetcher{evil}, Direct, "https://cdn.example/evil.mp4")
	if err == nil {
		t.Fatal("playlist accepted")
	}
	entries, _ := os.ReadDir(c.Dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), workDirPrefix) {
			t.Fatalf("download dir %s left behind", e.Name())
		}
	}
	avi := makeClip(t, c, "t.avi", "-f", "lavfi", "-i", "testsrc=size=64x48", "-t", "1")
	if _, err := Prepare(context.Background(), c, copyFetcher{avi}, Direct, "https://cdn.example/t.mp4"); err == nil {
		t.Fatal("an avi (not on the demuxer whitelist) was accepted")
	}
}

func TestPrepareBusy(t *testing.T) {
	c := realTools(t)
	c.MaxConcurrent = 1
	r, ok, _ := acquireSlot(filepath.Join(c.Dir, "slots"), 1)
	if !ok {
		t.Fatal("no slot")
	}
	defer r()
	if _, err := Prepare(context.Background(), c, copyFetcher{"/nonexistent"}, Direct, "https://x/a.mp4"); err != ErrBusy {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func contains(argv []string, s string) bool {
	for _, a := range argv {
		if a == s {
			return true
		}
	}
	return false
}

func hasPair(argv []string, k, v string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == k && argv[i+1] == v {
			return true
		}
	}
	return false
}
