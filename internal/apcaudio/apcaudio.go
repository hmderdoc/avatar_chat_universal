// Package apcaudio plays sound on a caller's terminal through SyncTERM's
// Audio APCs: each clip is cached, loaded into a slot and queued on a
// channel, and the terminal decodes it with libsndfile. No native audio
// library is involved, so the door builds the same everywhere.
//
//	ESC _ SyncTERM:C;S;<name>;<base64>       ESC \   (cache the clip)
//	ESC _ SyncTERM:A;Load;S=<slot>;<name>    ESC \   (decode into a slot)
//	ESC _ SyncTERM:A;Queue;C=<ch>;S=<slot>   ESC \   (play; slot empties)
//
// Feature probe: ESC _ SyncTERM:Q;libsndfile ESC \ -> ESC [ = 7 ; 100 ; 0|1 n.
// Only a terminal that answers it gets audio: SyncTERM, and the futureland
// connection shim and fTelnet fork that also answer it.
//
// Ported from the telnetvision door (door/audio.go, whose sequencing comes
// from lameboy's apc_audio.rs) and run open-loop, like the futureland
// shell's port (fshell_ts src/chat/apc_audio.ts): this door reads keys
// through one input decoder, and the terminal's unsolicited audio status
// reports would arrive there as stray keystrokes. So there are no drain
// notifications and no state polls. The queue is a local ledger: every
// queued clip adds its duration; lead = queued - wall time since the
// anchor. A gap in the stream re-anchors with a short silent clip, a lead
// past the budget flushes and skips to the present, and a blind periodic
// resync caps clock-drift tails. Sample rates are never stretched: a gap is
// better than a time-warp.
package apcaudio

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

const (
	// Channels 0 and 1 belong to cterm (ANSI music / SFX); Queue on them is
	// refused by the terminal.
	chanMin = 2
	chanMax = 15

	featureID = 100 // DSR id of the libsndfile feature reply

	// Probe is the feature query.
	Probe = "\x1b_SyncTERM:Q;libsndfile\x1b\\"

	// A gap longer than this means the terminal ran (or will run) dry; the
	// next clip is led by a silence prime so the cushion rebuilds.
	resumeGap = 300 * time.Millisecond
	// A partly filled cushion that stopped growing goes out as-is.
	fillStale = 300 * time.Millisecond
)

// Codecs of a Chunk's payload (telnetvision wire ids).
const (
	CodecRaw  = 0 // interleaved S16LE PCM; wrapped in a WAV header here
	CodecFLAC = 1
	CodecWAV  = 2
)

// Config is the tunable surface; Defaults are telnetvision's.
type Config struct {
	Channel    int    // 2..15
	SlotStart  int    // first slot of the rotating pool
	SlotCount  int    // pool size
	MaxQueued  int    // latency budget: clips' worth of queued lead
	Prebuffer  int    // cushion: clips held and queued as one burst (>= 1)
	VolumeDB   int    // A;Volume set once at Start
	ResyncSecs int    // blind tail-cap interval (0 = off)
	Prefix     string // cache file name prefix, so two sessions never share names
}

// Defaults returns telnetvision's tuning on channel ch.
func Defaults(ch int, prefix string) Config {
	return Config{Channel: ch, SlotStart: 100, SlotCount: 100, MaxQueued: 6, Prebuffer: 3,
		VolumeDB: -6, ResyncSecs: 60, Prefix: prefix}
}

func (c Config) clamp() Config {
	if c.Channel < chanMin || c.Channel > chanMax {
		c.Channel = chanMin
	}
	if c.SlotStart < 0 || c.SlotStart > 255 {
		c.SlotStart = 100
	}
	if c.SlotCount < 1 || c.SlotStart+c.SlotCount > 256 {
		c.SlotCount = 256 - c.SlotStart
	}
	if c.MaxQueued < 1 {
		c.MaxQueued = 6
	}
	if c.Prebuffer < 1 {
		c.Prebuffer = 1
	}
	if c.Prebuffer > c.MaxQueued {
		c.Prebuffer = c.MaxQueued // a cushion larger than the budget deadlocks
	}
	if c.Prefix == "" {
		c.Prefix = "ac"
	}
	return c
}

// Chunk is one clip of audio with its producer timing.
type Chunk struct {
	Codec      byte
	Channels   byte
	SampleRate uint32
	Seq        uint32
	PtsMs      uint64
	DurMs      uint32
	Payload    []byte
}

// TimedReader is a reader with a per-call timeout (termprobe's interface).
type TimedReader interface {
	ReadTimeout(p []byte, d time.Duration) (int, error)
}

// Detect sends the feature probe and waits up to timeout for the reply.
// It must run before the door's input decoder starts reading. Bytes that
// were not part of the reply (keys typed meanwhile) come back in leftover
// for the caller to replay. No reply means no audio.
func Detect(w io.Writer, r TimedReader, timeout time.Duration) (supported bool, leftover []byte) {
	if _, err := io.WriteString(w, Probe); err != nil {
		return false, nil
	}
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 0, 64)
	scratch := make([]byte, 64)
	for time.Now().Before(deadline) && len(buf) < 512 {
		rem := time.Until(deadline)
		if rem > 50*time.Millisecond {
			rem = 50 * time.Millisecond
		}
		n, err := r.ReadTimeout(scratch, rem)
		if n > 0 {
			buf = append(buf, scratch[:n]...)
			if avail, start, end, found := FeatureReply(buf, featureID); found {
				rest := append(append([]byte{}, buf[:start]...), buf[end:]...)
				return avail, rest
			}
		}
		if err != nil {
			break
		}
	}
	return false, buf
}

// FeatureReply finds ESC [ = 7 ; <id> ; <state> n in buf, tolerating noise
// around it, and returns whether state != 0 and where the reply sits.
func FeatureReply(buf []byte, id int) (avail bool, start, end int, found bool) {
	for i := 0; i+4 < len(buf); i++ {
		if buf[i] != 0x1B || buf[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(buf) && buf[j] != 'n' && buf[j] != 0x1B && j-i < 64 {
			j++
		}
		if j >= len(buf) || buf[j] != 'n' {
			continue
		}
		if pairs, ok := dsrPairs(buf[i+2 : j]); ok {
			for _, p := range pairs {
				if p[0] == id {
					return p[1] != 0, i, j + 1, true
				}
			}
		}
	}
	return false, 0, 0, false
}

// dsrPairs decodes "=7;a;b;c;d" into [a b] [c d].
func dsrPairs(params []byte) ([][2]int, bool) {
	if len(params) < 2 || params[0] != '=' || params[1] != '7' {
		return nil, false
	}
	var vals []int
	for j := 2; j < len(params); {
		if params[j] != ';' {
			return nil, false
		}
		j++
		v := -1
		for j < len(params) && params[j] >= '0' && params[j] <= '9' {
			if v < 0 {
				v = 0
			}
			v = v*10 + int(params[j]-'0')
			j++
		}
		if v < 0 {
			return nil, false
		}
		vals = append(vals, v)
	}
	var pairs [][2]int
	for k := 0; k+1 < len(vals); k += 2 {
		pairs = append(pairs, [2]int{vals[k], vals[k+1]})
	}
	return pairs, true
}

// heldChunk is an encoded clip waiting in the cushion.
type heldChunk struct {
	file  []byte
	codec byte
	durMs int64
}

// Session streams one source (a TV channel's audio, a played link) to one
// caller. It is not safe for concurrent use: the door drives it from its
// render loop, which also owns the connection's output.
type Session struct {
	cfg  Config
	slot int

	started   bool
	primed    bool
	playStart time.Time
	emittedMs int64
	lastQueue time.Time
	lastEmpty time.Time
	chunkMs   int64

	fill     []heldChunk
	fillLast time.Time

	lastRate     uint32
	lastChannels byte

	muted     bool
	needFlush bool
}

// NewSession returns a session for cfg.
func NewSession(cfg Config) *Session {
	return &Session{cfg: cfg.clamp(), chunkMs: 80}
}

// Start sets the channel volume. The dB suffix matters: a bare number is
// read as 0..100 percent, so "V=-6" would be the -60 dB floor.
func (s *Session) Start(w io.Writer) error {
	s.started = true
	_, err := fmt.Fprintf(w, "\x1b_SyncTERM:A;Volume;C=%d;V=%ddB\x1b\\", s.cfg.Channel, s.cfg.VolumeDB)
	return err
}

// Muted reports the mute state.
func (s *Session) Muted() bool { return s.muted }

// ToggleMute flips mute. Muting flushes the channel on the next Tick;
// unmuting resumes through the normal gap prime.
func (s *Session) ToggleMute() bool {
	s.muted = !s.muted
	if s.muted {
		s.needFlush = s.primed
	}
	return s.muted
}

// LeadMs is the ledger's estimate of audio queued ahead of the speaker.
func (s *Session) LeadMs(now time.Time) int64 {
	if !s.primed {
		return 0
	}
	lead := s.emittedMs - now.Sub(s.playStart).Milliseconds()
	if lead < 0 {
		return 0
	}
	return lead
}

// Push queues one chunk: held while the cushion fills, then sent. A lead
// past the budget flushes the stale tail and keeps this fresh chunk.
func (s *Session) Push(w io.Writer, m Chunk, now time.Time) error {
	s.lastRate, s.lastChannels = m.SampleRate, m.Channels
	if s.muted {
		return nil
	}
	dur := int64(m.DurMs)
	if dur <= 0 {
		dur = s.chunkMs
	}
	if s.LeadMs(now) > int64(s.cfg.MaxQueued)*dur {
		if err := s.resync(w, now); err != nil {
			return err
		}
	}
	file := m.Payload
	if m.Codec == CodecRaw {
		file = EncodeWAV(m.Payload, int(m.SampleRate), int(m.Channels))
	}
	if !s.primed || s.fill != nil {
		s.fill = append(s.fill, heldChunk{file: file, codec: m.Codec, durMs: dur})
		s.fillLast = now
		if len(s.fill) < s.cfg.Prebuffer {
			return nil
		}
		return s.flushFill(w, now)
	}
	if !s.lastQueue.IsZero() && now.Sub(s.lastQueue) > resumeGap {
		s.playStart = now
		s.emittedMs = 0
		if err := s.primeSilence(w, now); err != nil {
			return err
		}
	}
	if err := s.emit(w, file, m.Codec); err != nil {
		return err
	}
	s.emittedMs += dur
	s.lastQueue = now
	s.chunkMs = dur
	return nil
}

// Tick does the periodic work: a mute flush, sending a stalled cushion, and
// the blind tail cap.
func (s *Session) Tick(w io.Writer, now time.Time) error {
	if s.needFlush {
		s.needFlush = false
		if err := s.flushChannel(w); err != nil {
			return err
		}
		s.playStart, s.emittedMs, s.lastEmpty = now, 0, now
	}
	if s.muted {
		return nil
	}
	if len(s.fill) > 0 && now.Sub(s.fillLast) > fillStale {
		if err := s.flushFill(w, now); err != nil {
			return err
		}
	}
	if s.primed && s.cfg.ResyncSecs > 0 && !s.lastEmpty.IsZero() &&
		now.Sub(s.lastEmpty) > time.Duration(s.cfg.ResyncSecs)*time.Second &&
		now.Sub(s.lastQueue) < resumeGap {
		return s.resync(w, now)
	}
	return nil
}

// Stop silences the channel so nothing plays on after the source ends.
func (s *Session) Stop(w io.Writer) error {
	if !s.started {
		return nil
	}
	s.primed, s.fill, s.emittedMs = false, nil, 0
	return s.flushChannel(w)
}

func (s *Session) flushChannel(w io.Writer) error {
	_, err := fmt.Fprintf(w, "\x1b_SyncTERM:A;Flush;C=%d\x1b\\", s.cfg.Channel)
	return err
}

// resync flushes the channel's unplayed tail, re-anchors the ledger and
// re-primes with silence: one brief skip instead of unbounded latency.
func (s *Session) resync(w io.Writer, now time.Time) error {
	if !s.primed {
		return nil
	}
	if err := s.flushChannel(w); err != nil {
		return err
	}
	s.playStart, s.emittedMs = now, 0
	return s.primeSilence(w, now)
}

func (s *Session) primeSilence(w io.Writer, now time.Time) error {
	if s.lastRate == 0 {
		return nil
	}
	silMs := int64(s.cfg.Prebuffer) * s.chunkMs
	if silMs > 400 {
		silMs = 400
	}
	ch := int(s.lastChannels)
	if ch < 1 {
		ch = 1
	}
	n := int(s.lastRate) * ch * 2 * int(silMs) / 1000
	n -= n % (2 * ch)
	if err := s.emit(w, EncodeWAV(make([]byte, n), int(s.lastRate), ch), CodecWAV); err != nil {
		return err
	}
	s.emittedMs += silMs
	s.lastQueue = now
	s.lastEmpty = now
	return nil
}

func (s *Session) flushFill(w io.Writer, now time.Time) error {
	s.primed = true
	s.playStart = now
	s.emittedMs = 0
	for _, c := range s.fill {
		if err := s.emit(w, c.file, c.codec); err != nil {
			return err
		}
		s.emittedMs += c.durMs
		s.chunkMs = c.durMs
	}
	s.lastQueue = now
	s.lastEmpty = now
	s.fill = nil
	return nil
}

// emit writes one Store/Load/Queue triple as a single contiguous write, so a
// slot is never queued before its data arrived, and rotates the slot pool.
// Names reuse the slot number, so the caller's cache holds at most SlotCount
// files however long the stream runs.
func (s *Session) emit(w io.Writer, file []byte, codec byte) error {
	slot := s.cfg.SlotStart + s.slot
	s.slot = (s.slot + 1) % s.cfg.SlotCount
	ext := "wav"
	if codec == CodecFLAC {
		ext = "flac"
	}
	name := fmt.Sprintf("%s_%d.%s", s.cfg.Prefix, slot, ext)
	var b bytes.Buffer
	b.Grow(base64.StdEncoding.EncodedLen(len(file)) + 128)
	b.WriteString("\x1b_SyncTERM:C;S;" + name + ";")
	b.WriteString(base64.StdEncoding.EncodeToString(file))
	b.WriteString("\x1b\\")
	fmt.Fprintf(&b, "\x1b_SyncTERM:A;Load;S=%d;%s\x1b\\", slot, name)
	fmt.Fprintf(&b, "\x1b_SyncTERM:A;Queue;C=%d;S=%d\x1b\\", s.cfg.Channel, slot)
	_, err := w.Write(b.Bytes())
	return err
}

// EncodeWAV wraps interleaved S16LE PCM in a canonical 44-byte RIFF/WAVE
// header so the terminal's libsndfile can decode it.
func EncodeWAV(pcm []byte, rate, channels int) []byte {
	if channels < 1 {
		channels = 1
	}
	const bits = 16
	v := make([]byte, 44, 44+len(pcm))
	copy(v[0:], "RIFF")
	binary.LittleEndian.PutUint32(v[4:], uint32(36+len(pcm)))
	copy(v[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(v[16:], 16)
	binary.LittleEndian.PutUint16(v[20:], 1) // PCM
	binary.LittleEndian.PutUint16(v[22:], uint16(channels))
	binary.LittleEndian.PutUint32(v[24:], uint32(rate))
	binary.LittleEndian.PutUint32(v[28:], uint32(rate*channels*bits/8))
	binary.LittleEndian.PutUint16(v[32:], uint16(channels*bits/8))
	binary.LittleEndian.PutUint16(v[34:], bits)
	copy(v[36:], "data")
	binary.LittleEndian.PutUint32(v[40:], uint32(len(pcm)))
	return append(v, pcm...)
}
