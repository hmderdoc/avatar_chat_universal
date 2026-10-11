package inlineimg

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenRetriesWithoutRangeAndKeepsGuards(t *testing.T) {
	ranged := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			ranged++
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if r.URL.Path == "/hop" {
			http.Redirect(w, r, "http://[::1]:"+strings.Split(r.Host, ":")[1]+"/song", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		io.WriteString(w, "ID3 song bytes")
	}))
	defer srv.Close()
	f := testFetcher(t, srv)
	resp, err := f.Open(context.Background(), srv.URL+"/song", "bytes=0-")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "ID3 song bytes" || ranged != 1 {
		t.Fatalf("status %d body %q ranged %d", resp.StatusCode, body, ranged)
	}
	if _, err := f.Open(context.Background(), srv.URL+"/hop", ""); err == nil {
		t.Fatal("a redirect to a blocked address was followed")
	}
	if _, err := f.Open(context.Background(), "ftp://example.com/x.mp3", ""); err == nil {
		t.Fatal("a non-http scheme was opened")
	}
	// Without the test's loopback allowance the real rules refuse it.
	if _, err := NewFetcher("").Open(context.Background(), srv.URL+"/song", ""); err == nil {
		t.Fatal("a loopback address was opened by a default fetcher")
	}
}
