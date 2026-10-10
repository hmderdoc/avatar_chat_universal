package termprobe

import (
	"errors"
	"io"
	"net"
	"os"
	"time"
)

// DeadlineReader adapts anything with SetReadDeadline (a net.Conn, or a
// door's own connection type) to TimedReader. A deadline error is reported
// as a timeout so the probe simply ends.
func DeadlineReader(rw interface {
	io.Reader
	SetReadDeadline(time.Time) error
}) TimedReader {
	return deadlineReader{rw}
}

type deadlineReader struct {
	rw interface {
		io.Reader
		SetReadDeadline(time.Time) error
	}
}

func (d deadlineReader) ReadTimeout(p []byte, timeout time.Duration) (int, error) {
	if err := d.rw.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return 0, err
	}
	defer d.rw.SetReadDeadline(time.Time{})
	n, err := d.rw.Read(p)
	if n > 0 {
		return n, nil
	}
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, os.ErrDeadlineExceeded) {
			return 0, nil
		}
		return 0, err
	}
	return 0, nil
}

// PollReader adapts a non-blocking read function to TimedReader. read must
// return again=true when no input is available right now (EAGAIN); the
// adapter sleeps interval between attempts until timeout elapses. This suits
// a door that already drives a non-blocking stdin or inherited socket.
func PollReader(read func(p []byte) (n int, again bool, err error), interval time.Duration) TimedReader {
	if interval <= 0 {
		interval = 5 * time.Millisecond
	}
	return pollReader{read: read, interval: interval}
}

type pollReader struct {
	read     func(p []byte) (int, bool, error)
	interval time.Duration
}

func (r pollReader) ReadTimeout(p []byte, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for {
		n, again, err := r.read(p)
		if n > 0 {
			return n, nil
		}
		if err != nil {
			return 0, err
		}
		if !again {
			return 0, io.EOF
		}
		if time.Now().After(deadline) {
			return 0, nil
		}
		time.Sleep(r.interval)
	}
}

// ChanReader adapts a channel of input chunks to TimedReader. The door
// starts one goroutine that reads its terminal and sends every chunk on ch;
// the probe consumes from ch with timeouts, and afterwards the door's key
// handler consumes the same channel, so no goroutine is ever left blocked on
// a read that would swallow the first keystroke. A closed channel reads as
// io.EOF. Partial copies keep the remainder for the next call.
func ChanReader(ch <-chan []byte) TimedReader {
	return &chanReader{ch: ch}
}

type chanReader struct {
	ch      <-chan []byte
	pending []byte
}

func (c *chanReader) ReadTimeout(p []byte, timeout time.Duration) (int, error) {
	if len(c.pending) == 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		select {
		case b, ok := <-c.ch:
			if !ok {
				return 0, io.EOF
			}
			c.pending = b
		case <-t.C:
			return 0, nil
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}
