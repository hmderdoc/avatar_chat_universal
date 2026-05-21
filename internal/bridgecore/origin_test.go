package bridgecore

import "testing"

func TestOriginMatchesExactEndpointCaseInsensitive(t *testing.T) {
	a := NewOrigin("IRC", "irc.Libera.Chat", "#Avatar")
	b := NewOrigin("irc", "IRC.LIBERA.CHAT", "#avatar")
	if !a.Matches(b) {
		t.Fatalf("%#v should match %#v", a, b)
	}
	c := NewOrigin("irc", "irc.efnet.org", "#avatar")
	if a.Matches(c) {
		t.Fatalf("%#v should not match %#v", a, c)
	}
}

func TestHostMarkers(t *testing.T) {
	o := NewOrigin("irc", "irc.libera.chat", "#avatar")
	host := FormatHostMarker(o)
	got, ok := ParseHostMarker(host)
	if !ok || !got.Matches(o) {
		t.Fatalf("ParseHostMarker(%q) = %#v, %v", host, got, ok)
	}

	legacy, ok := ParseHostMarker("IRC:irc.efnet.org/#bbs")
	if !ok {
		t.Fatal("legacy IRC marker did not parse")
	}
	if legacy.Key() != "irc:irc.efnet.org/#bbs" {
		t.Fatalf("legacy key = %q", legacy.Key())
	}
}
