package chat

import "testing"

func TestTunerMarkerRoundTrip(t *testing.T) {
	m := FormatTuner("futureland.today", 7601, "cam")
	if !IsTunerMarker(m) {
		t.Fatalf("IsTunerMarker(%q) = false", m)
	}
	tn, off, ok := ParseTunerMarker(m)
	if !ok || off || tn == nil {
		t.Fatalf("parse failed: tn=%v off=%v ok=%v", tn, off, ok)
	}
	if tn.Host != "futureland.today" || tn.Port != 7601 || tn.Channel != "cam" {
		t.Fatalf("bad tuner: %+v", tn)
	}
}

func TestTunerOffMarker(t *testing.T) {
	_, off, ok := ParseTunerMarker(FormatTunerOff())
	if !ok || !off {
		t.Fatalf("off marker parse: off=%v ok=%v", off, ok)
	}
}

func TestTunerMarkerRejects(t *testing.T) {
	for _, s := range []string{
		"hello",
		"[TVTUNER|host]",            // missing port/channel
		"[TVTUNER|host|notaport|c]", // bad port
		"[TVTUNER|host|99999|c]",    // port out of range
		"[BITMAP|1|2|x|deadbeef]",   // different marker
		"[TVTUNER|host|7601|]",      // empty channel
	} {
		if _, _, ok := ParseTunerMarker(s); ok {
			t.Errorf("ParseTunerMarker(%q) unexpectedly ok", s)
		}
	}
}
