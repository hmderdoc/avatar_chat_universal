package bridgemedia

import (
	"strings"
	"testing"
)

func TestParseID3NoTag(t *testing.T) {
	if _, ok := ParseID3([]byte("not an id3 tag, just audio")); ok {
		t.Fatal("should report nothing when there is no ID3v2 tag")
	}
}

func TestParseID3ReadsTextAndArt(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	mp3 := buildMP3(map[string][]byte{
		"TIT2": append([]byte{0x00}, []byte("Fast Fart Philosopher")...),
		"TPE1": append([]byte{0x00}, []byte("mrodroid")...),
		"TALB": append([]byte{0x00}, []byte("Methane Suite")...),
		"APIC": buildAPIC(png),
	})
	meta, ok := ParseID3(mp3)
	if !ok {
		t.Fatal("expected to parse the tag")
	}
	if meta.Title != "Fast Fart Philosopher" || meta.Artist != "mrodroid" || meta.Album != "Methane Suite" {
		t.Fatalf("bad metadata: %+v", meta)
	}
	if len(meta.Art) == 0 || meta.Art[0] != 0x89 {
		t.Fatalf("cover art not extracted: %v", meta.Art)
	}
}

func TestScanRefsStripsAndClassifies(t *testing.T) {
	text := "look ![Cinder](https://x/cinder.png) and a track https://x/song.mp3 ok"
	clean, refs := ScanRefs(text, nil)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %#v", len(refs), refs)
	}
	if refs[0].Kind != KindImage || refs[0].Label != "Cinder" || refs[0].URL != "https://x/cinder.png" {
		t.Fatalf("bad image ref: %#v", refs[0])
	}
	if refs[1].Kind != KindAudio || refs[1].URL != "https://x/song.mp3" {
		t.Fatalf("bad audio ref: %#v", refs[1])
	}
	// Markdown collapses to its label; the bare URL is removed entirely.
	if strings.Contains(clean, "https://x/song.mp3") || strings.Contains(clean, "](") {
		t.Fatalf("media URLs should be stripped from text: %q", clean)
	}
	if !strings.Contains(clean, "Cinder") {
		t.Fatalf("markdown label should remain: %q", clean)
	}
}

func TestScanRefsResolveAndDedup(t *testing.T) {
	resolve := func(s string) string {
		if strings.HasPrefix(s, "/") {
			return "https://base" + s
		}
		return s
	}
	_, refs := ScanRefs("/a/pic.png again /a/pic.png", resolve)
	if len(refs) != 1 {
		t.Fatalf("duplicate resolved URLs should de-dupe, got %#v", refs)
	}
	if refs[0].URL != "https://base/a/pic.png" {
		t.Fatalf("resolve not applied: %#v", refs[0])
	}
}

func TestAudioFileNameFromMetadata(t *testing.T) {
	got := AudioFileName(Meta{Title: "Song Two", Artist: "DJ"}, "https://x/whatever.mp3", "mp3")
	if got != "DJ - Song Two.mp3" {
		t.Fatalf("metadata filename = %q", got)
	}
	got = AudioFileName(Meta{}, "https://x/api?file=Cool_Track.mp3", "mp3")
	if got != "Cool_Track.mp3" {
		t.Fatalf("fallback filename = %q", got)
	}
}

// buildAPIC returns a minimal front-cover APIC frame body wrapping pic.
func buildAPIC(pic []byte) []byte {
	body := []byte{0x00}
	body = append(body, []byte("image/png")...)
	body = append(body, 0x00, 0x03, 0x00)
	body = append(body, pic...)
	return body
}

// buildMP3 returns bytes beginning with a minimal ID3v2.3 tag containing the
// given frames (id -> frame body), followed by junk "audio".
func buildMP3(frames map[string][]byte) []byte {
	var all []byte
	for id, body := range frames {
		sz := len(body)
		f := []byte(id)
		f = append(f, byte(sz>>24), byte(sz>>16), byte(sz>>8), byte(sz))
		f = append(f, 0x00, 0x00)
		f = append(f, body...)
		all = append(all, f...)
	}
	ts := len(all)
	tag := []byte{'I', 'D', '3', 0x03, 0x00, 0x00}
	tag = append(tag,
		byte((ts>>21)&0x7f), byte((ts>>14)&0x7f), byte((ts>>7)&0x7f), byte(ts&0x7f))
	tag = append(tag, all...)
	tag = append(tag, []byte("....fake audio frames....")...)
	return tag
}
