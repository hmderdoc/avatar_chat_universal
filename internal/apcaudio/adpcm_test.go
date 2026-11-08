package apcaudio

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// Ported from the telnetvision door (door/adpcm_test.go, commit e7493ab).

// imaDecode is a reference IMA ADPCM WAV decoder for the tests (the same
// algorithm libsndfile uses), returning interleaved samples.
func imaDecode(t *testing.T, file []byte) (out []int16, rate, channels, spb int) {
	t.Helper()
	if string(file[0:4]) != "RIFF" || string(file[8:12]) != "WAVE" {
		t.Fatal("not RIFF/WAVE")
	}
	var blockAlign int
	p := 12
	for p+8 <= len(file) {
		id, n := string(file[p:p+4]), int(binary.LittleEndian.Uint32(file[p+4:]))
		body := file[p+8 : p+8+n]
		switch id {
		case "fmt ":
			if binary.LittleEndian.Uint16(body) != 0x11 {
				t.Fatalf("format tag %#x", binary.LittleEndian.Uint16(body))
			}
			channels = int(binary.LittleEndian.Uint16(body[2:]))
			rate = int(binary.LittleEndian.Uint32(body[4:]))
			blockAlign = int(binary.LittleEndian.Uint16(body[12:]))
			spb = int(binary.LittleEndian.Uint16(body[18:]))
		case "data":
			for b := 0; b+blockAlign <= len(body); b += blockAlign {
				blk := body[b : b+blockAlign]
				st := make([]imaState, channels)
				frames := make([][]int16, channels)
				for c := 0; c < channels; c++ {
					st[c].pred = int32(int16(binary.LittleEndian.Uint16(blk[c*4:])))
					st[c].index = int32(blk[c*4+2])
					frames[c] = append(frames[c], int16(st[c].pred))
				}
				d := blk[4*channels:]
				for w := 0; w+4*channels <= len(d); w += 4 * channels {
					for c := 0; c < channels; c++ {
						for j := 0; j < 8; j++ {
							code := d[w+c*4+j/2] >> (4 * (j & 1)) & 0xF
							frames[c] = append(frames[c], int16(imaStep(&st[c], code)))
						}
					}
				}
				for i := range frames[0] {
					for c := 0; c < channels; c++ {
						out = append(out, frames[c][i])
					}
				}
			}
		}
		p += 8 + n + n&1
	}
	return out, rate, channels, spb
}

func imaStep(st *imaState, code byte) int32 {
	step := imaStepTable[st.index]
	diff := step >> 3
	if code&4 != 0 {
		diff += step
	}
	if code&2 != 0 {
		diff += step >> 1
	}
	if code&1 != 0 {
		diff += step >> 2
	}
	if code&8 != 0 {
		st.pred -= diff
	} else {
		st.pred += diff
	}
	if st.pred > 32767 {
		st.pred = 32767
	} else if st.pred < -32768 {
		st.pred = -32768
	}
	st.index += imaIndexTable[code]
	if st.index < 0 {
		st.index = 0
	} else if st.index > 88 {
		st.index = 88
	}
	return st.pred
}

func sinePCM(rate, channels, ms int) []byte {
	n := rate * ms / 1000
	pcm := make([]byte, 0, n*channels*2)
	for i := 0; i < n; i++ {
		for c := 0; c < channels; c++ {
			v := int16(8000 * math.Sin(2*math.Pi*float64(440*(c+1))*float64(i)/float64(rate)))
			pcm = binary.LittleEndian.AppendUint16(pcm, uint16(v))
		}
	}
	return pcm
}

// The IMA encoder must round-trip through a standard decoder: right length
// (an exact block fit for the producer's 22050 Hz x 80 ms chunks), close to
// the input, and about a quarter the size of 16-bit PCM.
func TestEncodeWAVIMARoundTrip(t *testing.T) {
	for _, tc := range []struct{ rate, ch, ms int }{{22050, 1, 80}, {44100, 2, 80}, {48000, 2, 80}, {8000, 1, 40}} {
		pcm := sinePCM(tc.rate, tc.ch, tc.ms)
		file := encodeWAVIMA(pcm, tc.rate, tc.ch)
		dec, rate, ch, spb := imaDecode(t, file)
		if rate != tc.rate || ch != tc.ch || (spb-1)%8 != 0 {
			t.Fatalf("%v: header rate=%d ch=%d spb=%d", tc, rate, ch, spb)
		}
		frames := len(pcm) / (2 * tc.ch)
		got := len(dec) / tc.ch
		if got < frames || got-frames > spb/4 {
			t.Errorf("%v: decoded %d frames for %d in (pad %d)", tc, got, frames, got-frames)
		}
		if tc.rate == 22050 && got != frames {
			t.Errorf("22050 Hz x 80 ms should fit blocks exactly, padded %d", got-frames)
		}
		var sig, noise float64
		for i := 0; i < frames*tc.ch; i++ {
			s := float64(int16(binary.LittleEndian.Uint16(pcm[2*i:])))
			sig += s * s
			d := s - float64(dec[i])
			noise += d * d
		}
		if snr := 10 * math.Log10(sig/noise); snr < 20 {
			t.Errorf("%v: SNR %.1f dB", tc, snr)
		}
		if frames >= 1000 && len(file)*3 > len(pcm) {
			t.Errorf("%v: %d bytes for %d of PCM, want under a third", tc, len(file), len(pcm))
		}
	}
}

func TestULaw(t *testing.T) {
	for _, c := range []struct {
		in   int16
		want byte
	}{{0, 0xFF}, {-1, 0x7F}, {32767, 0x80}, {-32768, 0x00}, {1000, 0xCE}} {
		if got := linearToULaw(c.in); got != c.want {
			t.Errorf("ulaw(%d) = %#x, want %#x", c.in, got, c.want)
		}
	}
	pcm := sinePCM(22050, 1, 80)
	f := encodeWAVULaw(pcm, 22050, 1)
	if binary.LittleEndian.Uint16(f[20:]) != 7 || len(f) != len(pcm)/2+58 {
		t.Errorf("ulaw wav: tag %d len %d", binary.LittleEndian.Uint16(f[20:]), len(f))
	}
}

func TestWavPCM16(t *testing.T) {
	pcm := sinePCM(22050, 2, 20)
	got, rate, ch, ok := wavPCM16(EncodeWAV(pcm, 22050, 2))
	if !ok || rate != 22050 || ch != 2 || !bytes.Equal(got, pcm) {
		t.Fatalf("PCM WAV round trip: ok=%v rate=%d ch=%d", ok, rate, ch)
	}
	for name, f := range map[string][]byte{
		"ima":  encodeWAVIMA(pcm, 22050, 2),
		"flac": []byte("fLaC\x00\x00\x00\x22"),
		"tiny": []byte("RIFF"),
	} {
		if _, _, _, ok := wavPCM16(f); ok {
			t.Errorf("%s accepted as PCM16 WAV", name)
		}
	}
}
