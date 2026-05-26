package bridgemedia

import (
	"bytes"
	"image"
	_ "image/gif"  // register GIF decoder for image.Decode
	"image/jpeg"
	_ "image/png" // register PNG decoder for image.Decode

	xdraw "golang.org/x/image/draw"
)

// ThumbnailJPEG decodes art bytes (JPEG/PNG/GIF), scales them down to fit within
// maxDim on the longest edge (never upscales), and JPEG-encodes the result under
// maxBytes by stepping quality down. It returns false if the bytes are not a
// decodable image or can't be squeezed under the size cap.
//
// It exists so chat bridges can turn embedded ID3 cover art (or an avatar PNG)
// into something a platform will accept as a small thumbnail -- Telegram audio
// thumbnails, for instance, must be JPEG, <=320px, and <200KB.
func ThumbnailJPEG(data []byte, maxDim, maxBytes int) ([]byte, bool) {
	if len(data) == 0 {
		return nil, false
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, false
	}
	if maxDim > 0 && (w > maxDim || h > maxDim) {
		longest := w
		if h > longest {
			longest = h
		}
		nw := w * maxDim / longest
		nh := h * maxDim / longest
		if nw < 1 {
			nw = 1
		}
		if nh < 1 {
			nh = 1
		}
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
		src = dst
	}
	for _, q := range []int{85, 70, 55, 40} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: q}); err != nil {
			return nil, false
		}
		if maxBytes <= 0 || buf.Len() <= maxBytes {
			return buf.Bytes(), true
		}
	}
	return nil, false
}
