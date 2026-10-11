package audiolink

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// decodeAll reads a whole source and returns its samples.
func decodeAll(t *testing.T, s Source) []int16 {
	t.Helper()
	var out []int16
	buf := make([]byte, 999) // odd on purpose: Read must keep whole samples
	for {
		n, err := s.Read(buf)
		if n%2 != 0 {
			t.Fatalf("Read returned %d bytes, half a sample", n)
		}
		for i := 0; i+1 < n; i += 2 {
			out = append(out, int16(binary.LittleEndian.Uint16(buf[i:])))
		}
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Each fixture is a 440 Hz tone; check rate, length and that it is a tone
// (zero crossings near 2 x 440 per second), not silence or noise.
func TestDecodeFixtures(t *testing.T) {
	cases := []struct {
		file, ext string
		rate      int
		secs      float64
	}{
		{"tone.mp3", "mp3", 22050, 0.5},
		{"tone.ogg", "ogg", 22050, 0.5},
		{"tone.flac", "flac", 22050, 0.5},
		{"tone24.wav", "wav", 24000, 0.2},
	}
	for _, c := range cases {
		data, err := os.ReadFile("testdata/" + c.file)
		if err != nil {
			t.Fatal(err)
		}
		src, err := Open(data, c.ext)
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		if src.Rate() != c.rate {
			t.Errorf("%s: rate %d, want %d", c.file, src.Rate(), c.rate)
		}
		s := decodeAll(t, src)
		got := float64(len(s)) / float64(src.Rate())
		if math.Abs(got-c.secs) > 0.1 {
			t.Errorf("%s: %.3f s decoded, want about %.1f", c.file, got, c.secs)
		}
		var peak int16
		cross := 0
		for i := 1; i < len(s); i++ {
			if s[i] > peak {
				peak = s[i]
			}
			if (s[i-1] < 0) != (s[i] < 0) {
				cross++
			}
		}
		rate := float64(cross) / got
		if peak < 2000 || rate < 700 || rate > 1060 {
			t.Errorf("%s: peak %d, %.0f crossings/s: not a 440 Hz tone", c.file, peak, rate)
		}
	}
}

func TestOpenRejects(t *testing.T) {
	if _, err := Open([]byte("nope"), "opus"); err != ErrUnsupported {
		t.Fatalf("opus: %v", err)
	}
	if _, err := Open([]byte("RIFFxxxxWAVE"), "wav"); err == nil {
		t.Fatal("wav without fmt/data accepted")
	}
	if _, err := Open([]byte("garbage garbage garbage"), "flac"); err == nil {
		t.Fatal("garbage flac accepted")
	}
}

func TestExtAndName(t *testing.T) {
	if Ext("https://x.org/a/Song%20One.MP3?dl=1") != "mp3" || Ext("https://x.org/page.html") != "" || Ext("https://x.org/a.opus") != "opus" {
		t.Fatal("Ext")
	}
	if Name("https://x.org/a/Song%20One.mp3") != "Song One.mp3" {
		t.Fatalf("Name = %q", Name("https://x.org/a/Song%20One.mp3"))
	}
	if Playable("opus") || !Playable("flac") {
		t.Fatal("Playable")
	}
}

// constSource is n seconds of a constant sample.
type constSource struct{ left int }

func (c *constSource) Rate() int { return 22050 }
func (c *constSource) Read(p []byte) (int, error) {
	if c.left == 0 {
		return 0, io.EOF
	}
	n := len(p) &^ 1
	if n > c.left {
		n = c.left
	}
	c.left -= n
	return n, nil
}

// The player keeps about a second queued, paced by the clock, and finishes.
func TestPlayerPacesAndFinishes(t *testing.T) {
	p := NewPlayer("t", &constSource{left: 22050 * 2 * 3}) // 3 s
	var w bytes.Buffer
	t0 := time.Unix(1000, 0)
	p.Start(&w)
	p.Tick(&w, t0)
	first := strings.Count(w.String(), "A;Queue;C=3")
	if first < 3 || first > 6 {
		t.Fatalf("first tick queued %d clips, want about a second's worth", first)
	}
	for ms := 100; ms <= 6000 && !p.Done(t0.Add(time.Duration(ms)*time.Millisecond)); ms += 100 {
		p.Tick(&w, t0.Add(time.Duration(ms)*time.Millisecond))
		if lead := p.sess.LeadMs(t0.Add(time.Duration(ms) * time.Millisecond)); lead > 1600 {
			t.Fatalf("lead %d ms at %d ms: the player ran ahead", lead, ms)
		}
	}
	if !p.Done(t0.Add(6 * time.Second)) {
		t.Fatal("a 3 s track did not finish within 6 s")
	}
	if strings.Count(w.String(), "A;Flush") != 0 {
		t.Fatal("steady playback should never flush")
	}
	total := strings.Count(w.String(), "A;Queue;C=3")
	if total < 15 || total > 17 {
		t.Fatalf("queued %d clips for 3 s, want 15 (+1 if a gap was primed)", total)
	}
}
