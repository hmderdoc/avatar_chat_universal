package audiolink

import (
	"io"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/apcaudio"
)

const (
	// clipMs is one queued clip. It must stay under apcaudio's 300 ms resume
	// gap: clips go out about one clip apart, and a longer spacing would
	// read as a stream gap and pull in silence primes.
	clipMs = 200
	// leadMs is how far ahead of the speaker the player keeps the queue.
	leadMs = 1000
	// Channel is the terminal audio channel links play on; the TV uses 2.
	Channel = 3
)

// Player streams one Source to the terminal, paced by wall time.
type Player struct {
	Title string
	src   Source
	sess  *apcaudio.Session
	seq   uint32
	pts   uint64
	eof   bool
	err   error
	buf   []byte
}

// NewPlayer returns a player for src on the links channel. Blind resyncs
// are off: the door is the clock for a file, so there is no producer drift
// to cap, and a music track should not skip every minute.
func NewPlayer(title string, src Source) *Player {
	cfg := apcaudio.Defaults(Channel, "al")
	cfg.ResyncSecs = 0
	cfg.MaxQueued = 10
	return &Player{Title: title, src: src, sess: apcaudio.NewSession(cfg),
		buf: make([]byte, src.Rate()*2*clipMs/1000)}
}

// Start sets the channel volume.
func (p *Player) Start(w io.Writer) error { return p.sess.Start(w) }

// Tick queues clips until the lead is back at its target. Done reports the
// track has been fully queued and played out (by the ledger).
func (p *Player) Tick(w io.Writer, now time.Time) error {
	for !p.eof && p.sess.LeadMs(now) < leadMs {
		n, err := io.ReadFull(p.src, p.buf)
		if n > 0 {
			pcm := append([]byte(nil), p.buf[:n&^1]...)
			dur := uint32(len(pcm) * 1000 / (2 * p.src.Rate()))
			if dur == 0 {
				dur = 1
			}
			if perr := p.sess.Push(w, apcaudio.Chunk{Codec: apcaudio.CodecRaw, Channels: 1,
				SampleRate: uint32(p.src.Rate()), Seq: p.seq, PtsMs: p.pts, DurMs: dur, Payload: pcm}, now); perr != nil {
				return perr
			}
			p.seq++
			p.pts += uint64(dur)
		}
		if err != nil {
			p.eof = true
			if err != io.EOF && err != io.ErrUnexpectedEOF {
				p.err = err
			}
		}
	}
	return p.sess.Tick(w, now)
}

// Done reports that everything was queued and the ledger says it played.
func (p *Player) Done(now time.Time) bool { return p.eof && p.sess.LeadMs(now) == 0 }

// Err is a decode error that ended the track early, if any.
func (p *Player) Err() error { return p.err }

// Stop silences the channel now.
func (p *Player) Stop(w io.Writer) error { return p.sess.Stop(w) }
