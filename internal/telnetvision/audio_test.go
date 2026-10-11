package telnetvision

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func audioMsg(seq uint32) []byte {
	p := []byte{msgAudio, 0, 1}
	p = binary.BigEndian.AppendUint32(p, 22050)
	p = binary.BigEndian.AppendUint32(p, seq)
	p = binary.BigEndian.AppendUint64(p, 1234)
	p = binary.BigEndian.AppendUint32(p, 80)
	return append(p, 1, 2, 3, 4)
}

func send(t *testing.T, c net.Conn, p []byte) {
	t.Helper()
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(p)))
	if _, err := c.Write(append(hdr[:], p...)); err != nil {
		t.Fatal(err)
	}
}

// fakeRelay accepts one consumer, returns its hello, and sends msgs.
func fakeRelay(t *testing.T, msgs ...[]byte) (addr string, hello chan []byte) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	hello = make(chan []byte, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var hdr [4]byte
		if _, err := io.ReadFull(c, hdr[:]); err != nil {
			return
		}
		h := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		io.ReadFull(c, h)
		hello <- h
		for _, m := range msgs {
			send(t, c, m)
		}
		time.Sleep(time.Second)
	}()
	return l.Addr().String(), hello
}

func dial(addr string, want bool) *Consumer {
	host, port, _ := net.SplitHostPort(addr)
	var p int
	for _, ch := range port {
		p = p*10 + int(ch-'0')
	}
	c := NewConsumer(host, p, "main")
	c.WantAudio = want
	c.Start()
	return c
}

func TestConsumerAudio(t *testing.T) {
	addr, hello := fakeRelay(t, audioMsg(1), audioMsg(2))
	c := dial(addr, true)
	defer c.Close()
	h := <-hello
	if want := append([]byte{msgHelloConsumer, 4}, append([]byte("main"), capAudio)...); string(h) != string(want) {
		t.Fatalf("hello % x, want % x", h, want)
	}
	var got int
	for i := 0; i < 50 && got < 2; i++ {
		for _, m := range c.TakeAudio() {
			got++
			if m.SampleRate != 22050 || m.DurMs != 80 || m.PtsMs != 1234 || len(m.Payload) != 4 || m.Seq != uint32(got) {
				t.Fatalf("chunk %d = %+v", got, m)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got != 2 {
		t.Fatalf("got %d chunks, want 2", got)
	}
}

func TestConsumerWithoutAudioSendsV1Hello(t *testing.T) {
	addr, hello := fakeRelay(t, audioMsg(1))
	c := dial(addr, false)
	defer c.Close()
	if h := <-hello; len(h) != 2+len("main") {
		t.Fatalf("hello % x should carry no caps byte", h)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(c.TakeAudio()); n != 0 {
		t.Fatalf("queued %d chunks nobody asked for", n)
	}
}
