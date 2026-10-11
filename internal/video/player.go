package video

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/apcaudio"
	"github.com/hmderdoc/avatar_chat_universal/internal/telnetvision"
)

// audioChunkMs is one PCM chunk handed to the audio session; well under
// apcaudio's 300 ms clips.
const audioChunkMs = 120

// audioQueueMax bounds chunks waiting for the UI (about 6 s).
const audioQueueMax = 50

// Player is one playback: ffmpeg decoding a Media in real time. It offers
// the same Latest/TakeAudio surface as the TV lounge's consumer, so the
// lounge renderer and audio path play it unchanged.
type Player struct {
	Title   string
	Seconds float64 // probed duration (0 = unknown)

	media *Media
	cmd   *proc
	start time.Time

	mu     sync.Mutex
	latest *telnetvision.Frame
	audio  []apcaudio.Chunk
	frames int
	done   bool
	err    error

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// proc is the running ffmpeg.
type proc struct {
	wait func() error
	kill func()
}

// Play starts ffmpeg on m at cols x rows cells, with audio when wantAudio
// and the file has a track. The Player owns m from here: Close releases
// both.
func Play(c Config, m *Media, title string, cols, rows int, wantAudio bool) (*Player, error) {
	if cols <= 0 || rows <= 0 {
		m.Close()
		return nil, errors.New("video: no room on screen")
	}
	maxSec := c.MaxSeconds
	if maxSec <= 0 {
		maxSec = DefaultMaxSeconds
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(maxSec+30)*time.Second)
	cmd := command(ctx, m.Dir, c.FFmpeg, PlayArgs(m.File, m.Probe, cols, rows, maxSec, wantAudio))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		m.Close()
		return nil, err
	}
	var ar, aw *os.File
	audio := wantAudio && m.Probe.HasAudio
	if audio {
		if ar, aw, err = os.Pipe(); err != nil {
			cancel()
			m.Close()
			return nil, err
		}
		cmd.ExtraFiles = []*os.File{aw} // fd 3 in the child
	}
	if err := cmd.Start(); err != nil {
		cancel()
		if ar != nil {
			ar.Close()
			aw.Close()
		}
		m.Close()
		return nil, err
	}
	lowerPriority(cmd)
	if aw != nil {
		aw.Close() // the child holds its own copy
	}
	p := &Player{Title: title, Seconds: m.Probe.Seconds, media: m, start: time.Now(), cancel: cancel,
		cmd: &proc{wait: cmd.Wait, kill: func() { killTree(cmd) }}}
	p.wg.Add(1)
	go p.readFrames(stdout, cols, rows)
	if ar != nil {
		p.wg.Add(1)
		go p.readAudio(ar)
	}
	go func() {
		p.wg.Wait()
		err := p.cmd.wait()
		p.mu.Lock()
		p.done = true
		if err != nil && p.frames == 0 {
			p.err = errors.New("video: ffmpeg could not play that file")
		}
		p.mu.Unlock()
	}()
	return p, nil
}

func (p *Player) readFrames(r io.ReadCloser, cols, rows int) {
	defer p.wg.Done()
	defer r.Close()
	size := cols * 2 * rows * 3
	for {
		buf := make([]byte, size)
		if _, err := io.ReadFull(r, buf); err != nil {
			return
		}
		fr := &telnetvision.Frame{Cols: cols, Rows: rows, Pixels: buf}
		p.mu.Lock()
		p.latest = fr // drop-to-latest: a slow link never builds a backlog
		p.frames++
		p.mu.Unlock()
	}
}

func (p *Player) readAudio(r *os.File) {
	defer p.wg.Done()
	defer r.Close()
	size := AudioRate * audioChunkMs / 1000 * 2
	var seq uint32
	var pts uint64
	for {
		buf := make([]byte, size)
		n, err := io.ReadFull(r, buf)
		n &^= 1
		if n > 0 {
			dur := uint32(n * 1000 / (2 * AudioRate))
			if dur == 0 {
				dur = 1
			}
			ch := apcaudio.Chunk{Codec: apcaudio.CodecRaw, Channels: 1, SampleRate: AudioRate,
				Seq: seq, PtsMs: pts, DurMs: dur, Payload: buf[:n]}
			seq++
			pts += uint64(dur)
			p.mu.Lock()
			p.audio = append(p.audio, ch)
			if len(p.audio) > audioQueueMax {
				p.audio = p.audio[len(p.audio)-audioQueueMax:]
			}
			p.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// Latest is the newest decoded frame (nil before the first).
func (p *Player) Latest() *telnetvision.Frame {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.latest
}

// TakeAudio returns the PCM chunks decoded since the last call.
func (p *Player) TakeAudio() []apcaudio.Chunk {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.audio
	p.audio = nil
	return out
}

// Done reports that ffmpeg has finished (end of file, the duration cap,
// or a failure; see Err).
func (p *Player) Done() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}

// Err is why playback failed, when it produced nothing.
func (p *Player) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Elapsed is wall time since the start.
func (p *Player) Elapsed() time.Duration { return time.Since(p.start) }

// Close stops ffmpeg (its whole process group), waits for the readers,
// releases the slot and deletes the download. Safe to call more than once.
func (p *Player) Close() {
	if p == nil || p.media == nil {
		return
	}
	p.cmd.kill()
	p.cancel()
	p.wg.Wait()
	p.media.Close()
	p.media = nil
}
