package upload

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// socketpair returns the two ends of an AF_UNIX SOCK_STREAM pair as a
// net.Conn (for our side) and an *os.File (for the subprocess side).
// SOCK_STREAM gives us back-pressure and a clean EOF when the child exits;
// crucially, net.UnixConn supports SetReadDeadline, which our zmodem reader
// relies on.
func socketpair(t *testing.T) (net.Conn, *os.File) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	usFile := os.NewFile(uintptr(fds[0]), "us")
	childFile := os.NewFile(uintptr(fds[1]), "child")
	conn, err := net.FileConn(usFile)
	if err != nil {
		usFile.Close()
		childFile.Close()
		t.Fatalf("FileConn: %v", err)
	}
	usFile.Close() // FileConn dup'd it.
	return conn, childFile
}

// TestReceiveZMODEM_AgainstLrzsz drives lrzsz `sz` against ReceiveZMODEM
// over a Unix socketpair. If our wire format is right, this should succeed
// without modification; if `sz` rejects our ZRINIT (capabilities in the
// wrong byte, bad CRC, etc.) the test fails.
//
// Skipped if `sz` isn't on PATH.
func TestReceiveZMODEM_AgainstLrzsz(t *testing.T) {
	szPath, err := exec.LookPath("sz")
	if err != nil {
		t.Skip("sz (lrzsz) not installed; skipping end-to-end Zmodem test")
	}

	want := make([]byte, 120)
	for i := range want {
		want[i] = byte(i % 251) // avoid all-zeros / all-ff
	}

	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "avatar.bin")
	if err := os.WriteFile(srcPath, want, 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	ourEnd, childEnd := socketpair(t)
	defer ourEnd.Close()

	// `sz -b -q <file>` = binary mode, quiet. The subprocess inherits the
	// child end as its stdin/stdout, so its protocol read/write talks to
	// our side of the socketpair.
	cmd := exec.Command(szPath, "-b", "-q", srcPath)
	cmd.Stdin = childEnd
	cmd.Stdout = childEnd
	cmd.Stderr = nil // sz prints status to stderr; suppress it for clean test output

	if err := cmd.Start(); err != nil {
		t.Fatalf("start sz: %v", err)
	}
	childEnd.Close() // child has its own dup'd fd
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	type result struct {
		data []byte
		name string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, name, err := ReceiveZMODEM(ourEnd, 4096)
		done <- result{data, name, err}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("ReceiveZMODEM: %v", res.err)
		}
		if !bytes.Equal(res.data, want) {
			t.Errorf("payload mismatch: got %d bytes, want %d", len(res.data), len(want))
			if len(res.data) <= 256 {
				t.Logf("got:  % x", res.data)
				t.Logf("want: % x", want)
			}
		}
		if filepath.Base(res.name) != "avatar.bin" {
			t.Errorf("filename: got %q want %q", res.name, "avatar.bin")
		}
	case <-time.After(45 * time.Second):
		t.Fatalf("timeout: ReceiveZMODEM did not complete within 45s")
	}
}

// TestReceiveZMODEM_AgainstLrzsz_LargerFile exercises the multi-subpacket
// path with a payload bigger than the default 1024-byte ZDATA chunk.
func TestReceiveZMODEM_AgainstLrzsz_LargerFile(t *testing.T) {
	szPath, err := exec.LookPath("sz")
	if err != nil {
		t.Skip("sz (lrzsz) not installed; skipping")
	}

	want := make([]byte, 8192)
	for i := range want {
		want[i] = byte(i ^ (i >> 7))
	}

	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "blob.dat")
	if err := os.WriteFile(srcPath, want, 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	ourEnd, childEnd := socketpair(t)
	defer ourEnd.Close()

	cmd := exec.Command(szPath, "-b", "-q", srcPath)
	cmd.Stdin = childEnd
	cmd.Stdout = childEnd

	if err := cmd.Start(); err != nil {
		t.Fatalf("start sz: %v", err)
	}
	childEnd.Close()
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	var got []byte
	var rerr error
	go func() {
		defer wg.Done()
		got, _, rerr = ReceiveZMODEM(ourEnd, 65536)
	}()
	doneCh := make(chan struct{})
	go func() { wg.Wait(); close(doneCh) }()
	select {
	case <-doneCh:
	case <-time.After(60 * time.Second):
		t.Fatalf("timeout")
	}
	if rerr != nil {
		t.Fatalf("ReceiveZMODEM: %v", rerr)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("payload mismatch: got %d bytes, want %d", len(got), len(want))
	}
}
