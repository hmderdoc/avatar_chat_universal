package nickstyle

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNormalizeMirrorsChatStyleLib(t *testing.T) {
	in := Style{
		Colors: []string{" #F00 ", "nope", "#00FF00", "", ""},
		Tag:    Tag{Text: "  so|p\x01é  ", Fg: "#FFF", Bg: "red"},
	}
	got := Normalize(in)
	want := Style{
		Colors: []string{"#ff0000", "", "#00ff00"},
		Tag:    Tag{Text: "sop", Fg: "#ffffff", Bg: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Normalize = %+v, want %+v", got, want)
	}
}

func TestNormalizeClearsTagColoursWithoutText(t *testing.T) {
	got := Normalize(Style{Tag: Tag{Text: " | ", Fg: "#ffffff", Bg: "#000000"}})
	if got.Tag != (Tag{}) {
		t.Fatalf("tag = %+v, want empty", got.Tag)
	}
}

func TestNormalizeCaps(t *testing.T) {
	colors := make([]string, 80)
	for i := range colors {
		colors[i] = "#123456"
	}
	got := Normalize(Style{Colors: colors, Tag: Tag{Text: "abcdefghijklmnop"}})
	if len(got.Colors) != MaxColors {
		t.Errorf("colors = %d, want %d", len(got.Colors), MaxColors)
	}
	if got.Tag.Text != "abcdefghijkl" {
		t.Errorf("tag = %q", got.Tag.Text)
	}
}

func TestUnmarshalToleratesJunk(t *testing.T) {
	var msg struct {
		Style *Style `json:"style"`
		Str   string `json:"str"`
	}
	raw := `{"style":{"colors":[1,"#abc",null,{}],"tag":{"text":42,"fg":"#fff"}},"str":"hi"}`
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Str != "hi" {
		t.Fatal("message body lost")
	}
	want := Style{Colors: []string{"", "#aabbcc"}, Tag: Tag{Text: "42", Fg: "#ffffff"}}
	if !reflect.DeepEqual(*msg.Style, want) {
		t.Fatalf("style = %+v, want %+v", *msg.Style, want)
	}
	if err := json.Unmarshal([]byte(`{"style":"bogus","str":"x"}`), &msg); err != nil {
		t.Fatalf("string style broke the message: %v", err)
	}
	if !msg.Style.IsEmpty() {
		t.Fatal("bogus style should decode empty")
	}
}

func TestAlign(t *testing.T) {
	s := &Style{Colors: []string{"#ff0000", "", "#00ff00"}}
	if got := s.Align("ab"); !reflect.DeepEqual(got, []string{"#ff0000", ""}) {
		t.Errorf("short = %v", got)
	}
	if got := s.Align("abcd"); !reflect.DeepEqual(got, []string{"#ff0000", "", "#00ff00", ""}) {
		t.Errorf("long = %v", got)
	}
}

func TestPresets(t *testing.T) {
	if got := Preset("gradient", 3, "#000000", "#ffffff"); !reflect.DeepEqual(got, []string{"#000000", "#808080", "#ffffff"}) {
		t.Errorf("gradient = %v", got)
	}
	if got := Preset("solid", 2, "#ABC", ""); !reflect.DeepEqual(got, []string{"#aabbcc", "#aabbcc"}) {
		t.Errorf("solid = %v", got)
	}
	if got := Preset("gradient", 2, "", "#ff0000"); !reflect.DeepEqual(got, []string{"#ff0000", "#ff0000"}) {
		t.Errorf("gradient fallback = %v", got)
	}
	// hsvHex(0, .85, 1) in chat_style_lib.js is #ff2626.
	if got := Preset("rainbow", 1, "", ""); got[0] != "#ff2626" {
		t.Errorf("rainbow[0] = %v", got)
	}
	if got := Preset("clear", 2, "", ""); !reflect.DeepEqual(got, []string{"", ""}) {
		t.Errorf("clear = %v", got)
	}
}

func TestIsEmpty(t *testing.T) {
	if !(&Style{Colors: []string{"", ""}}).IsEmpty() {
		t.Error("all-default colours should be empty")
	}
	if (&Style{Tag: Tag{Text: "x"}}).IsEmpty() {
		t.Error("tag only is not empty")
	}
}
