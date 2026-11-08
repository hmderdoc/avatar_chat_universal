package apcaudio

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEncodeWAV(t *testing.T) {
	pcm := []byte{1, 0, 2, 0, 3, 0, 4, 0}
	w := EncodeWAV(pcm, 22050, 2)
	if len(w) != 44+len(pcm) || string(w[0:4]) != "RIFF" || string(w[8:16]) != "WAVEfmt " || string(w[36:40]) != "data" {
		t.Fatalf("bad header % x", w[:44])
	}
	if binary.LittleEndian.Uint32(w[24:]) != 22050 || binary.LittleEndian.Uint16(w[22:]) != 2 ||
		binary.LittleEndian.Uint32(w[28:]) != 22050*4 || binary.LittleEndian.Uint32(w[40:]) != 8 {
		t.Fatal("bad fmt fields")
	}
}

func TestFeatureReplyWithNoise(t *testing.T) {
	buf := []byte("ab\x1b[24;80R\x1b[=7;100;1ncd")
	avail, s, e, found := FeatureReply(buf, 100)
	if !found || !avail || string(buf[:s])+string(buf[e:]) != "ab\x1b[24;80Rcd" {
		t.Fatalf("found=%v avail=%v rest=%q", found, avail, string(buf[:s])+string(buf[e:]))
	}
	if avail, _, _, found := FeatureReply([]byte("\x1b[=7;100;0n"), 100); !found || avail {
		t.Fatal("a 0 reply means libsndfile is missing")
	}
	if _, _, _, found := FeatureReply([]byte("\x1b[=7;2;1n"), 100); found {
		t.Fatal("a channel report is not the feature reply")
	}
}

type script struct{ chunks [][]byte }

func (s *script) ReadTimeout(p []byte, d time.Duration) (int, error) {
	if len(s.chunks) == 0 {
		time.Sleep(d)
		return 0, nil
	}
	c := s.chunks[0]
	s.chunks = s.chunks[1:]
	return copy(p, c), nil
}

type errReader struct{}

func (errReader) ReadTimeout(p []byte, d time.Duration) (int, error) { return 0, errors.New("closed") }

func TestDetect(t *testing.T) {
	var out bytes.Buffer
	ok, f, rest := Detect(&out, &script{chunks: [][]byte{[]byte("x\x1b[=7;"), []byte("100;1ny")}}, time.Second)
	if !ok || f != (Formats{}) || string(rest) != "xy" || out.String() != FormatProbe+Probe {
		t.Fatalf("ok=%v formats=%+v rest=%q wrote %q", ok, f, rest, out.String())
	}
	ok, _, rest = Detect(&out, &script{chunks: [][]byte{[]byte("typed")}}, 120*time.Millisecond)
	if ok || string(rest) != "typed" {
		t.Fatalf("silent terminal: ok=%v rest=%q", ok, rest)
	}
	if ok, _, _ := Detect(&out, errReader{}, time.Second); ok {
		t.Fatal("a read error means no audio")
	}
}

// A SyncTERM that knows libsndfileFormat answers the format probes first
// (they were sent first); the replies are consumed, never replayed as keys.
func TestDetectFormats(t *testing.T) {
	var out bytes.Buffer
	ok, f, rest := Detect(&out, &script{chunks: [][]byte{
		[]byte("a\x1b[=7;101;1;18;1n\x1b[=7;101;"), []byte("1;16;0nb\x1b[=7;100;1n"),
	}}, time.Second)
	if !ok || !f.IMAADPCM || f.ULaw || string(rest) != "ab" {
		t.Fatalf("ok=%v formats=%+v rest=%q", ok, f, rest)
	}
}

// Clips go out in the best format the terminal decodes and the config
// allows; durations (the ledger) are untouched.
func TestSessionEncodesForTheTerminal(t *testing.T) {
	for _, tc := range []struct {
		codec   string
		formats Formats
		tag     uint16
	}{
		{"adpcm", Formats{IMAADPCM: true, ULaw: true}, 0x11},
		{"adpcm", Formats{ULaw: true}, 7},
		{"ulaw", Formats{IMAADPCM: true, ULaw: true}, 7},
		{"pcm", Formats{IMAADPCM: true, ULaw: true}, 1},
		{"adpcm", Formats{}, 1},
	} {
		cfg := Defaults(2, "tv")
		cfg.Codec, cfg.Formats, cfg.Prebuffer = tc.codec, tc.formats, 1
		s := NewSession(cfg)
		var w bytes.Buffer
		now := time.Unix(1000, 0)
		if err := s.Push(&w, chunk(80), now); err != nil {
			t.Fatal(err)
		}
		file := storedFile(t, w.Bytes())
		if got := binary.LittleEndian.Uint16(file[20:]); got != tc.tag {
			t.Errorf("%s %+v: format tag %#x, want %#x", tc.codec, tc.formats, got, tc.tag)
		}
		if s.LeadMs(now) != 80 {
			t.Errorf("%s %+v: lead %d, want 80", tc.codec, tc.formats, s.LeadMs(now))
		}
	}
}

// storedFile decodes the first cached clip (ESC _ SyncTERM:C;S;name;<b64>).
func storedFile(t *testing.T, out []byte) []byte {
	t.Helper()
	s := string(out)
	i := strings.Index(s, "SyncTERM:C;S;")
	if i < 0 {
		t.Fatalf("no store in %q", s)
	}
	s = s[i+len("SyncTERM:C;S;"):]
	s = s[strings.Index(s, ";")+1:]
	s = s[:strings.Index(s, "\x1b")]
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func chunk(ms uint32) Chunk {
	n := 22050 * 2 * int(ms) / 1000
	return Chunk{Codec: CodecRaw, Channels: 1, SampleRate: 22050, DurMs: ms, Payload: make([]byte, n)}
}

func TestCushionThenSteady(t *testing.T) {
	s := NewSession(Defaults(2, "tv"))
	var w bytes.Buffer
	t0 := time.Unix(1000, 0)
	for i := 0; i < 2; i++ {
		if err := s.Push(&w, chunk(80), t0.Add(time.Duration(i)*80*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	if w.Len() != 0 {
		t.Fatal("chunks were sent before the cushion filled")
	}
	s.Push(&w, chunk(80), t0.Add(160*time.Millisecond))
	if n := strings.Count(w.String(), "A;Queue;C=2;"); n != 3 {
		t.Fatalf("cushion burst queued %d clips, want 3", n)
	}
	if !strings.Contains(w.String(), "C;S;tv_100.wav;") || !strings.Contains(w.String(), "A;Load;S=102;tv_102.wav") {
		t.Fatal("slots/names not rotating from 100")
	}
	w.Reset()
	s.Push(&w, chunk(80), t0.Add(240*time.Millisecond))
	if strings.Count(w.String(), "A;Queue") != 1 || strings.Contains(w.String(), "A;Flush") {
		t.Fatalf("steady state should queue exactly one clip: %q", w.String()[:40])
	}
}

func TestBudgetFlushesStaleTail(t *testing.T) {
	cfg := Defaults(3, "ac")
	cfg.Prebuffer = 1
	s := NewSession(cfg)
	var w bytes.Buffer
	t0 := time.Unix(1000, 0)
	// A source far faster than real time piles up lead.
	for i := 0; i < 10; i++ {
		s.Push(&w, chunk(80), t0)
	}
	if !strings.Contains(w.String(), "A;Flush;C=3") {
		t.Fatal("lead past the budget was never flushed")
	}
	if s.LeadMs(t0) > int64(cfg.MaxQueued+1)*80+400 {
		t.Fatalf("lead %d ms still unbounded", s.LeadMs(t0))
	}
}

func TestGapPrimesSilence(t *testing.T) {
	cfg := Defaults(2, "tv")
	cfg.Prebuffer = 1
	s := NewSession(cfg)
	var w bytes.Buffer
	t0 := time.Unix(1000, 0)
	s.Push(&w, chunk(80), t0)
	w.Reset()
	s.Push(&w, chunk(80), t0.Add(2*time.Second))
	if strings.Count(w.String(), "A;Queue") != 2 {
		t.Fatal("a resumed stream should lead with a silence prime")
	}
}

func TestMuteAndStop(t *testing.T) {
	cfg := Defaults(2, "tv")
	cfg.Prebuffer = 1
	s := NewSession(cfg)
	var w bytes.Buffer
	t0 := time.Unix(1000, 0)
	s.Start(&w)
	if !strings.Contains(w.String(), "A;Volume;C=2;V=-6dB") {
		t.Fatalf("volume = %q", w.String())
	}
	s.Push(&w, chunk(80), t0)
	if !s.ToggleMute() {
		t.Fatal("mute did not engage")
	}
	w.Reset()
	s.Tick(&w, t0)
	if !strings.Contains(w.String(), "A;Flush;C=2") {
		t.Fatal("mute should flush the channel")
	}
	w.Reset()
	s.Push(&w, chunk(80), t0.Add(100*time.Millisecond))
	if w.Len() != 0 {
		t.Fatal("audio sent while muted")
	}
	s.ToggleMute()
	w.Reset()
	s.Stop(&w)
	if !strings.Contains(w.String(), "A;Flush;C=2") {
		t.Fatal("Stop should flush")
	}
}

func TestStalledCushionGoesOut(t *testing.T) {
	s := NewSession(Defaults(2, "tv"))
	var w bytes.Buffer
	t0 := time.Unix(1000, 0)
	s.Push(&w, chunk(80), t0)
	s.Tick(&w, t0.Add(100*time.Millisecond))
	if w.Len() != 0 {
		t.Fatal("cushion flushed too early")
	}
	s.Tick(&w, t0.Add(time.Second))
	if strings.Count(w.String(), "A;Queue") != 1 {
		t.Fatal("a stalled partial cushion must still play")
	}
}

func TestChannelClamp(t *testing.T) {
	s := NewSession(Config{Channel: 1})
	if s.cfg.Channel != 2 || s.cfg.Prefix != "ac" {
		t.Fatalf("cfg = %+v", s.cfg)
	}
}
