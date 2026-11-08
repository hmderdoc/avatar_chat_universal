package ui

import (
	"testing"
	"time"
)

func TestVideoGovernorStepsDownAndRetries(t *testing.T) {
	var g videoGovernor
	t0 := time.Unix(1000, 0)
	slow := 400 * time.Millisecond
	// Fast frames, or slow frames the link never pushed back on, never step down.
	for i := 0; i < 50; i++ {
		now := t0.Add(time.Duration(i) * slow)
		if g.frameDone(now, 10*time.Millisecond, true) || g.frameDone(now, slow, false) {
			t.Fatal("stepped down on a link that keeps up")
		}
	}
	// Sustained slow, link-bound frames step down after videoSlowFor.
	now := t0.Add(time.Minute)
	down := false
	for i := 0; i < 20 && !down; i++ {
		now = now.Add(slow)
		down = g.frameDone(now, slow, true)
	}
	if !down || !g.reduced {
		t.Fatal("never stepped down on a slow link")
	}
	if g.tick(now.Add(videoRetryFirst - time.Second)) {
		t.Fatal("retried before the backoff")
	}
	if !g.tick(now.Add(videoRetryFirst)) || g.reduced {
		t.Fatal("no retry after the backoff")
	}
	// A second step-down doubles the wait.
	now = now.Add(videoRetryFirst)
	for i := 0; i < 20 && !g.reduced; i++ {
		now = now.Add(slow)
		g.frameDone(now, slow, true)
	}
	if g.retryAt.Sub(now) != 2*videoRetryFirst {
		t.Fatalf("backoff %v, want %v", g.retryAt.Sub(now), 2*videoRetryFirst)
	}
}

func TestVideoColorSetting(t *testing.T) {
	a := &App{TVColor: "truecolor"}
	a.vgov.reduced = true
	if a.videoTrue() {
		t.Fatal("auto ignored the step-down")
	}
	a.VideoColor = "truecolor"
	if !a.videoTrue() {
		t.Fatal("truecolor setting ignored")
	}
	a.TVColor = "16"
	if a.videoTrue() {
		t.Fatal("truecolor on a 16-colour terminal")
	}
	if nextVideoColor("auto", 1) != "truecolor" || nextVideoColor("auto", -1) != "16" {
		t.Fatal("cycle order")
	}
}
