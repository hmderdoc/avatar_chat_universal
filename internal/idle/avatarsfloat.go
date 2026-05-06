package idle

import (
	"math"
	"math/rand"

	"github.com/hmderdoc/avatar_chat_universal/internal/ansi"
	"github.com/hmderdoc/avatar_chat_universal/internal/avatar"
)

// AvatarPick pairs an avatar with the user's name so the animation can
// reference them when generating greetings.
type AvatarPick struct {
	Name   string
	Avatar avatar.Avatar
}

// AvatarsFloat is a foreground animation: 4-12 user avatars bouncing
// around the screen as 10x6 sprites. When two sprites collide, they swap
// velocities and exchange short greetings that float above each one for
// a few seconds. Cells outside a sprite (and outside an active greeting)
// are left transparent so the chat shows through.
//
// Port of /sbbs/xtrn/avatar_chat/lib/avatars-float.js, including the
// _avoidAndGreet collision logic at lines 244-265.
type AvatarsFloat struct {
	entities         []avatarEntity
	tickCount        int
	greetingDuration int
	rng              *rand.Rand
}

type avatarEntity struct {
	name       string
	a          avatar.Avatar
	x, y       float64
	dx, dy     float64
	greet      string // active greeting text; "" if no bubble
	greetUntil int    // tickCount at which the bubble expires
}

// NewAvatarsFloat builds the animation with named avatar entries. Falls
// back to a single anonymous identicon if pool is empty.
func NewAvatarsFloat(w, h int, pool []AvatarPick) *AvatarsFloat {
	af := &AvatarsFloat{
		greetingDuration: 60, // ~3.3 seconds at 18 fps
		rng:              rngFor(),
	}

	minEnts := 4
	maxEnts := (w * h) / (avatar.Width * avatar.Height) / 8
	if maxEnts < minEnts {
		maxEnts = minEnts
	}
	if maxEnts > 12 {
		maxEnts = 12
	}

	candidates := pool
	if len(candidates) == 0 {
		candidates = []AvatarPick{{Name: "anonymous", Avatar: avatar.Identicon("anonymous")}}
	}
	count := len(candidates)
	if count > maxEnts {
		count = maxEnts
	}

	maxX := float64(w - avatar.Width)
	maxY := float64(h - avatar.Height)
	if maxX < 0 {
		maxX = 0
	}
	if maxY < 0 {
		maxY = 0
	}
	for i := 0; i < count; i++ {
		c := candidates[i%len(candidates)]
		af.entities = append(af.entities, avatarEntity{
			name: c.Name,
			a:    c.Avatar,
			x:    af.rng.Float64() * maxX,
			y:    af.rng.Float64() * maxY,
			dx:   (af.rng.Float64()*2 - 1) * 0.6,
			dy:   (af.rng.Float64()*2 - 1) * 0.4,
		})
	}
	return af
}

func (af *AvatarsFloat) Name() string       { return "avatars_float" }
func (af *AvatarsFloat) Category() Category { return Foreground }
func (af *AvatarsFloat) PreferredFPS() int  { return 18 }

var greetings = []string{"Hello %s!", "Hey %s", "Hi %s", "Yo %s", "Greetings %s"}
var responses = []string{"Hi %s", "Hey there %s", "Yo %s", "Howdy %s", "Hey hey"}

func (af *AvatarsFloat) makeGreeting(other string) string {
	t := greetings[af.rng.Intn(len(greetings))]
	return formatName(t, other)
}

func (af *AvatarsFloat) makeResponse(other string) string {
	t := responses[af.rng.Intn(len(responses))]
	return formatName(t, other)
}

func formatName(template, name string) string {
	out := []byte(template)
	idx := -1
	for i := 0; i+1 < len(out); i++ {
		if out[i] == '%' && out[i+1] == 's' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return template
	}
	return string(out[:idx]) + name + string(out[idx+2:])
}

func (af *AvatarsFloat) Tick(f *ansi.Frame) {
	f.Clear()
	af.tickCount++

	maxX := float64(f.W - avatar.Width)
	maxY := float64(f.H - avatar.Height)
	if maxX < 0 {
		maxX = 0
	}
	if maxY < 0 {
		maxY = 0
	}

	// Update positions, bounce off walls, expire greetings.
	for i := range af.entities {
		e := &af.entities[i]
		if af.rng.Float64() < 0.05 {
			e.dx += (af.rng.Float64()*2 - 1) * 0.1
			e.dy += (af.rng.Float64()*2 - 1) * 0.06
		}
		e.x += e.dx
		e.y += e.dy
		if e.x < 0 {
			e.x = 0
			e.dx = math.Abs(e.dx)
		}
		if e.x > maxX {
			e.x = maxX
			e.dx = -math.Abs(e.dx)
		}
		if e.y < 0 {
			e.y = 0
			e.dy = math.Abs(e.dy)
		}
		if e.y > maxY {
			e.y = maxY
			e.dy = -math.Abs(e.dy)
		}
		if e.greet != "" && af.tickCount >= e.greetUntil {
			e.greet = ""
		}
	}

	// Pairwise collision check: avatars whose centers are close swap
	// velocities and exchange greetings if neither is already greeting.
	const halfW = float64(avatar.Width) / 2
	const halfH = float64(avatar.Height) / 2
	nearDist := (avatar.Width + avatar.Width) / 2 // ~10 cells
	nearDistSq := float64(nearDist * nearDist)
	for i := 0; i < len(af.entities); i++ {
		for j := i + 1; j < len(af.entities); j++ {
			a := &af.entities[i]
			b := &af.entities[j]
			ax := a.x + halfW
			ay := a.y + halfH
			bx := b.x + halfW
			by := b.y + halfH
			ddx := ax - bx
			ddy := ay - by
			distSq := ddx*ddx + ddy*ddy
			if distSq > nearDistSq {
				continue
			}
			// Velocity swap (elastic-ish).
			a.dx, b.dx = b.dx, a.dx
			a.dy, b.dy = b.dy, a.dy
			// Nudge them apart so they don't immediately re-collide.
			if ddx == 0 && ddy == 0 {
				ddx = 0.5
			}
			mag := math.Sqrt(distSq)
			if mag < 0.01 {
				mag = 0.01
			}
			push := 1.0
			a.x += ddx / mag * push
			a.y += ddy / mag * push
			b.x -= ddx / mag * push
			b.y -= ddy / mag * push

			if a.greet == "" && b.greet == "" {
				a.greet = af.makeGreeting(b.name)
				b.greet = af.makeResponse(a.name)
				a.greetUntil = af.tickCount + af.greetingDuration
				b.greetUntil = af.tickCount + af.greetingDuration
			}
		}
	}

	// Render avatars and any active greeting bubbles.
	for i := range af.entities {
		e := &af.entities[i]
		e.a.RenderTo(f, int(e.x), int(e.y))
		if e.greet != "" {
			af.drawGreeting(f, int(e.x), int(e.y), e.greet)
		}
	}
}

// drawGreeting paints a one-line greeting bubble above the avatar at
// (avatarX, avatarY). The bubble is `[ text ]` clipped to the frame
// width; cells are written as opaque so they overlay the chat below.
func (af *AvatarsFloat) drawGreeting(f *ansi.Frame, ax, ay int, text string) {
	if text == "" {
		return
	}
	row := ay - 1
	if row < 0 {
		row = 0
	}
	bubble := "[" + text + "]"
	// Center the bubble over the avatar's middle.
	centerX := ax + avatar.Width/2
	startX := centerX - len(bubble)/2
	if startX < 0 {
		startX = 0
	}
	if startX+len(bubble) > f.W {
		startX = f.W - len(bubble)
		if startX < 0 {
			startX = 0
		}
	}
	for i := 0; i < len(bubble); i++ {
		col := startX + i
		if col < 0 || col >= f.W {
			continue
		}
		f.SetCell(col, row, bubble[i], ansi.White|ansi.BgMagenta)
	}
}
