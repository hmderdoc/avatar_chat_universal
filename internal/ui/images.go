package ui

import (
	"context"
	"image"
	"sort"
	"time"

	"github.com/hmderdoc/shadecell"

	"github.com/hmderdoc/avatar_chat_universal/internal/inlineimg"
)

// ImageConfig turns on inline pictures for image links in chat. The App
// does nothing with links while it is nil.
type ImageConfig struct {
	Fetcher *inlineimg.Fetcher
	// Sixel is set when the terminal said it draws DEC sixel; CellW/CellH
	// are its cell size in pixels (0 = assume 8x16).
	Sixel        bool
	CellW, CellH int
	// NSFWKeywords veils pictures whose link or line matches, until /nsfw.
	NSFWKeywords []string
}

// imageState is where one link is in its life.
type imageState int

const (
	imgPending imageState = iota // queued or downloading
	imgReady
	imgFailed
)

type imageEntry struct {
	url    string
	state  imageState
	queued bool // waiting for or holding a download worker
	img    image.Image
	nsfw   bool
	at     time.Time // when it became ready (orders the /img list)
	from   string    // sender, for the /img header
	// pics caches layouts by box and colour mode.
	pics map[picKey]*inlineimg.Picture
}

type picKey struct {
	cols, rows int
	truecolor  bool
	sixel      bool
}

type imageResult struct {
	url string
	img image.Image
	err error
}

// imageManager owns every inline picture: it decides which links are
// fetched, runs at most two downloads at once off the UI goroutine, and
// lays pictures out on demand (on the UI goroutine, cached per box).
type imageManager struct {
	cfg      ImageConfig
	q        *shadecell.Quantizer
	entries  map[string]*imageEntry
	queue    []string
	inflight int
	results  chan imageResult
	revealed bool // /nsfw: show veiled pictures this session
}

const (
	imageWorkers  = 2
	imageMaxQueue = 16
	// inlineMaxRows is the tallest inline picture, as in the shell.
	inlineMaxRows = 10
)

func newImageManager(cfg ImageConfig) *imageManager {
	return &imageManager{
		cfg:     cfg,
		q:       shadecell.New(),
		entries: map[string]*imageEntry{},
		results: make(chan imageResult, imageMaxQueue),
	}
}

// imageView is what the transcript draws for one link.
type imageView struct {
	pending bool
	veiled  bool
	pic     *inlineimg.Picture
}

// lookup returns how to show url (found in a line from sender), queueing
// its download the first time it is seen. ok is false when nothing should
// be drawn (a failed download).
func (m *imageManager) lookup(url, line, sender string, maxCols int, truecolor bool) (imageView, bool) {
	e := m.entries[url]
	if e == nil {
		e = &imageEntry{
			url:  url,
			from: sender,
			nsfw: inlineimg.LooksNSFW(url, m.cfg.NSFWKeywords) || inlineimg.LooksNSFW(line, m.cfg.NSFWKeywords),
			pics: map[picKey]*inlineimg.Picture{},
		}
		m.entries[url] = e
	}
	if e.nsfw && !m.revealed {
		return imageView{veiled: true}, true
	}
	if e.state == imgPending && !e.queued {
		m.enqueue(e)
	}
	switch e.state {
	case imgPending:
		return imageView{pending: true}, true
	case imgReady:
		return imageView{pic: m.layout(e, maxCols, inlineMaxRows, truecolor, m.cfg.Sixel)}, true
	}
	return imageView{}, false
}

// layout renders e for a box, caching the result.
func (m *imageManager) layout(e *imageEntry, cols, rows int, truecolor, sixel bool) *inlineimg.Picture {
	k := picKey{cols, rows, truecolor, sixel}
	if p, ok := e.pics[k]; ok {
		return p
	}
	p := inlineimg.Render(e.img, m.q, inlineimg.Options{
		MaxCols: cols, MaxRows: rows, Truecolor: truecolor,
		Sixel: sixel, CellW: m.cfg.CellW, CellH: m.cfg.CellH,
	})
	e.pics[k] = p
	return p
}

// enqueue queues e's download. A full queue leaves it unqueued; the next
// lookup (every redraw) tries again.
func (m *imageManager) enqueue(e *imageEntry) {
	if len(m.queue) >= imageMaxQueue {
		return
	}
	e.queued = true
	m.queue = append(m.queue, e.url)
	m.pump()
}

// pump starts downloads while there are free workers.
func (m *imageManager) pump() {
	for m.inflight < imageWorkers && len(m.queue) > 0 {
		url := m.queue[0]
		m.queue = m.queue[1:]
		m.inflight++
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*m.cfg.Fetcher.Timeout)
			defer cancel()
			body, err := m.cfg.Fetcher.Fetch(ctx, url)
			var img image.Image
			if err == nil {
				img, err = inlineimg.Decode(body)
			}
			m.results <- imageResult{url: url, img: img, err: err}
		}()
	}
}

// drain applies finished downloads; true when anything changed on screen.
func (m *imageManager) drain() bool {
	changed := false
	for {
		select {
		case r := <-m.results:
			m.inflight--
			if e := m.entries[r.url]; e != nil {
				e.queued = false
				if r.err != nil {
					e.state = imgFailed
				} else {
					e.state = imgReady
					e.img = r.img
					e.at = time.Now()
				}
			}
			changed = true
		default:
			m.pump()
			return changed
		}
	}
}

// toggleReveal flips /nsfw and returns the new state plus how many veiled
// links it affects. Revealed links download on their next lookup.
func (m *imageManager) toggleReveal() (bool, int) {
	m.revealed = !m.revealed
	n := 0
	for _, e := range m.entries {
		if e.nsfw {
			n++
		}
	}
	return m.revealed, n
}

// ready lists the downloaded pictures the user may see, oldest first, for
// the /img viewer.
func (m *imageManager) ready() []*imageEntry {
	var out []*imageEntry
	for _, e := range m.entries {
		if e.state == imgReady && (!e.nsfw || m.revealed) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].at.Before(out[j].at) })
	return out
}

// readyCount is len(ready()) without the sort.
func (m *imageManager) readyCount() int {
	n := 0
	for _, e := range m.entries {
		if e.state == imgReady && (!e.nsfw || m.revealed) {
			n++
		}
	}
	return n
}
