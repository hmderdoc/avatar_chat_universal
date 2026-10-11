// Package audiolink plays audio links from chat on the caller's terminal:
// it decodes the file in pure Go, mixes it to mono at about 22 kHz (the
// telnetvision rate, a quarter of CD bandwidth on the wire) and feeds it to
// an apcaudio.Session in real time.
//
// Formats: mp3 (hajimehoshi/go-mp3), Ogg Vorbis (jfreymuth/oggvorbis), FLAC
// (mewkiz/flac) and PCM WAV. Opus, AAC and m4a have no pure-Go decoder, so
// those links are listed as unplayable.
package audiolink

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"
	"github.com/mewkiz/flac"
)

// TargetRate is the rate the player aims for: rates of 32 kHz and up are
// decimated by a whole factor toward it.
const TargetRate = 22050

// ErrUnsupported is returned for formats the door cannot decode.
var ErrUnsupported = errors.New("audiolink: format not supported")

var audioURL = regexp.MustCompile(`(?i)\.(mp3|ogg|oga|flac|wav|opus|m4a|aac)(?:[?#]|$)`)

// Ext returns the lower-case audio extension of a link ("" if it is not an
// audio link).
func Ext(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	m := audioURL.FindStringSubmatch(u.Path)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}

// Playable reports whether the door can decode ext.
func Playable(ext string) bool {
	switch ext {
	case "mp3", "ogg", "oga", "flac", "wav":
		return true
	}
	return false
}

// Name is a link's file name, for the card.
func Name(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if n := path.Base(u.Path); n != "." && n != "/" {
		if un, err := url.PathUnescape(n); err == nil {
			return un
		}
		return n
	}
	return u.Host
}

// Source is decoded audio as mono S16LE PCM at Rate.
type Source interface {
	Rate() int
	// Read fills p with whole 16-bit samples; io.EOF at the end.
	Read(p []byte) (int, error)
}

// blockFunc returns the next block of mono samples (16-bit range) at the
// native rate, or io.EOF.
type blockFunc func() ([]int32, error)

// Open decodes data, whose format is named by ext.
func Open(data []byte, ext string) (Source, error) {
	r := bytes.NewReader(data)
	switch ext {
	case "mp3":
		d, err := mp3.NewDecoder(r)
		if err != nil {
			return nil, fmt.Errorf("mp3: %w", err)
		}
		buf := make([]byte, 4096) // go-mp3 always gives 16-bit stereo
		return newResampled(d.SampleRate(), func() ([]int32, error) {
			n, err := io.ReadFull(d, buf)
			if n == 0 {
				if err == nil || err == io.ErrUnexpectedEOF {
					err = io.EOF
				}
				return nil, err
			}
			out := make([]int32, n/4)
			for i := range out {
				l := int16(binary.LittleEndian.Uint16(buf[i*4:]))
				rr := int16(binary.LittleEndian.Uint16(buf[i*4+2:]))
				out[i] = (int32(l) + int32(rr)) / 2
			}
			return out, nil
		}), nil
	case "ogg", "oga":
		d, err := oggvorbis.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("ogg: %w", err)
		}
		ch := d.Channels()
		if ch < 1 {
			return nil, ErrUnsupported
		}
		buf := make([]float32, 1024*ch)
		return newResampled(d.SampleRate(), func() ([]int32, error) {
			n, err := d.Read(buf)
			if n == 0 {
				if err == nil {
					err = io.EOF
				}
				return nil, err
			}
			out := make([]int32, n/ch)
			for i := range out {
				var sum float32
				for c := 0; c < ch; c++ {
					sum += buf[i*ch+c]
				}
				out[i] = clamp16(int32(sum / float32(ch) * 32767))
			}
			return out, nil
		}), nil
	case "flac":
		s, err := flac.New(r)
		if err != nil {
			return nil, fmt.Errorf("flac: %w", err)
		}
		shift := int(s.Info.BitsPerSample) - 16
		return newResampled(int(s.Info.SampleRate), func() ([]int32, error) {
			f, err := s.ParseNext()
			if err != nil {
				return nil, err // io.EOF at the end
			}
			ch := len(f.Subframes)
			if ch == 0 {
				return nil, io.EOF
			}
			n := len(f.Subframes[0].Samples)
			out := make([]int32, n)
			for i := 0; i < n; i++ {
				var sum int64
				for c := 0; c < ch; c++ {
					sum += int64(f.Subframes[c].Samples[i])
				}
				v := int32(sum / int64(ch))
				if shift > 0 {
					v >>= uint(shift)
				} else if shift < 0 {
					v <<= uint(-shift)
				}
				out[i] = clamp16(v)
			}
			return out, nil
		}), nil
	case "wav":
		return openWAV(data)
	}
	return nil, ErrUnsupported
}

// openWAV reads an integer PCM WAV (8, 16, 24 or 32 bits, any channels).
func openWAV(data []byte) (Source, error) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("wav: not a RIFF/WAVE file")
	}
	var rate, ch, bits int
	var pcm []byte
	for off := 12; off+8 <= len(data); {
		id := string(data[off : off+4])
		size := int(binary.LittleEndian.Uint32(data[off+4:]))
		body := off + 8
		end := body + size
		if size < 0 || end > len(data) {
			end = len(data)
		}
		switch id {
		case "fmt ":
			if end-body < 16 {
				return nil, errors.New("wav: short fmt chunk")
			}
			format := binary.LittleEndian.Uint16(data[body:])
			ch = int(binary.LittleEndian.Uint16(data[body+2:]))
			rate = int(binary.LittleEndian.Uint32(data[body+4:]))
			bits = int(binary.LittleEndian.Uint16(data[body+14:]))
			if format != 1 && format != 0xFFFE {
				return nil, fmt.Errorf("wav: format %d is not integer PCM: %w", format, ErrUnsupported)
			}
		case "data":
			pcm = data[body:end]
		}
		off = end + size%2 // chunks are word aligned
		if end == len(data) {
			break
		}
	}
	bps := bits / 8
	if rate <= 0 || ch < 1 || pcm == nil || (bits != 8 && bits != 16 && bits != 24 && bits != 32) {
		return nil, errors.New("wav: missing or unsupported fmt/data")
	}
	frame := bps * ch
	pos := 0
	return newResampled(rate, func() ([]int32, error) {
		if pos+frame > len(pcm) {
			return nil, io.EOF
		}
		n := (len(pcm) - pos) / frame
		if n > 1024 {
			n = 1024
		}
		out := make([]int32, n)
		for i := 0; i < n; i++ {
			var sum int64
			for c := 0; c < ch; c++ {
				p := pcm[pos+i*frame+c*bps:]
				var v int32
				switch bits {
				case 8:
					v = (int32(p[0]) - 128) << 8
				case 16:
					v = int32(int16(binary.LittleEndian.Uint16(p)))
				case 24:
					v = int32(uint32(p[0])<<8|uint32(p[1])<<16|uint32(p[2])<<24) >> 16
				case 32:
					v = int32(binary.LittleEndian.Uint32(p)) >> 16
				}
				sum += int64(v)
			}
			out[i] = int32(sum / int64(ch))
		}
		pos += n * frame
		return out, nil
	}), nil
}

func clamp16(v int32) int32 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return v
}

// resampled decimates a native-rate mono block stream by a whole factor
// (box average, which doubles as a crude low-pass) and serves S16LE.
type resampled struct {
	next   blockFunc
	factor int
	rate   int
	acc    int64
	accN   int
	out    []byte
	err    error
}

func newResampled(rate int, next blockFunc) *resampled {
	f := 1
	if rate >= 32000 {
		f = rate / TargetRate
		if f < 1 {
			f = 1
		}
	}
	return &resampled{next: next, factor: f, rate: rate / f}
}

func (r *resampled) Rate() int { return r.rate }

func (r *resampled) Read(p []byte) (int, error) {
	for len(r.out) < len(p) && r.err == nil {
		blk, err := r.next()
		if err != nil {
			r.err = err
			break
		}
		for _, s := range blk {
			r.acc += int64(s)
			r.accN++
			if r.accN == r.factor {
				v := int16(r.acc / int64(r.factor))
				r.out = append(r.out, byte(v), byte(uint16(v)>>8))
				r.acc, r.accN = 0, 0
			}
		}
	}
	n := copy(p[:len(p)&^1], r.out)
	r.out = r.out[n:]
	if n == 0 && r.err != nil {
		if r.err == io.EOF {
			return 0, io.EOF
		}
		return 0, r.err
	}
	return n, nil
}
