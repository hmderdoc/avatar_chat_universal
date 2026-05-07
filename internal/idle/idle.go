// Package idle implements idle-state background animations for the chat
// transcript. When the user has been inactive for a while, the App swaps
// the transcript frame's contents for an Animation, which paints procedural
// pixels each tick. Any keypress or new message exits idle and restores the
// chat.
//
// Ports a small subset of /sbbs/xtrn/avatar_chat/lib/canvas-animations.js;
// initial set is Starfield + MatrixRain. Each animation owns its own state
// and writes directly into a Frame on Tick().
package idle

import (
	"math/rand"
	"time"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
)

// Category indicates whether an animation is meant to render BEHIND the
// chat content (background — opaque, replaces the transcript area) or
// ABOVE it (foreground — transparent, overlays specific cells while the
// chat shows through everywhere else). The compositor in ui/app.go
// layers bg < transcript < fg so foreground animations naturally see the
// chat under them where they don't paint. Current foreground animations:
// avatars_float, figlet_message.
type Category int

const (
	Background Category = iota
	Foreground
)

func (c Category) String() string {
	if c == Foreground {
		return "fg"
	}
	return "bg"
}

// Animation is the contract for idle animations: render one frame.
type Animation interface {
	Name() string
	Category() Category
	Tick(f *ansi.Frame)
}

// FPSPreferring is an optional interface an Animation can implement to
// request a tick rate different from the global IdleFPS. The App's idle
// loop type-asserts and uses this when present. Useful for sprite-based
// animations (avatars_float, comets) that look choppy at the default 8 fps.
type FPSPreferring interface {
	PreferredFPS() int
}

// Registry is the catalog of available animations the App can rotate
// through. Keys are the canonical names that appear in the JS door's
// idle_sequence / idle_disable config (matrix_rain, starfield, etc.).
type Registry struct {
	factories map[string]func(w, h int) Animation
}

// NewRegistry builds the registry pre-populated with every animation
// shipping in this package. Categories follow the JS door's grouping at
// /sbbs/xtrn/avatar_chat/avatar_chat.ini:46-51.
func NewRegistry() *Registry {
	r := &Registry{factories: map[string]func(w, h int) Animation{}}
	r.factories["tv_static"] = func(w, h int) Animation { return NewTvStatic(w, h) }
	r.factories["matrix_rain"] = func(w, h int) Animation { return NewMatrixRain(w, h) }
	r.factories["life"] = func(w, h int) Animation { return NewLife(w, h) }
	r.factories["starfield"] = func(w, h int) Animation { return NewStarfield(w, h) }
	r.factories["fireflies"] = func(w, h int) Animation { return NewFireflies(w, h) }
	r.factories["sine_wave"] = func(w, h int) Animation { return NewSineWave(w, h) }
	r.factories["comet_trails"] = func(w, h int) Animation { return NewCometTrails(w, h) }
	r.factories["plasma"] = func(w, h int) Animation { return NewPlasma(w, h) }
	r.factories["fireworks"] = func(w, h int) Animation { return NewFireworks(w, h) }
	r.factories["aurora"] = func(w, h int) Animation { return NewAurora(w, h) }
	r.factories["fire_smoke"] = func(w, h int) Animation { return NewFireSmoke(w, h) }
	r.factories["ocean_ripple"] = func(w, h int) Animation { return NewOceanRipple(w, h) }
	r.factories["lissajous"] = func(w, h int) Animation { return NewLissajous(w, h) }
	r.factories["lightning"] = func(w, h int) Animation { return NewLightning(w, h) }
	r.factories["tunnel"] = func(w, h int) Animation { return NewTunnel(w, h) }
	return r
}

// Names returns every registered animation key in stable alphabetical order.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.factories))
	for k := range r.factories {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

// Build instantiates a fresh animation by name sized for (w, h). Returns
// nil if the name isn't registered.
func (r *Registry) Build(name string, w, h int) Animation {
	if f, ok := r.factories[name]; ok {
		return f(w, h)
	}
	return nil
}

// sortStrings is a local insertion sort to avoid importing sort; the
// registry is small enough that the algorithmic difference doesn't matter.
func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// rngFor returns a per-animation RNG seeded from the current time.
func rngFor() *rand.Rand {
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}
