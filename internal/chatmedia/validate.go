// Package chatmedia is the bundled chat server's optional file drop: callers
// upload a picture or a piece of ANSI art, the server stores it for a few
// days and answers with a URL, and the caller sends that URL in chat like any
// other link. Every client already renders such links (inline pictures, the
// web's ANSI viewer), so nothing on the chat wire changes.
//
// Files are stored and served as /chatmedia/<YYYY-MM-DD>/<24 hex>.<ext>, the
// same shape the Synchronet web chat uses, because clients recognise an
// attachment (and its "expired" state) by that path.
package chatmedia

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"path"
	"strings"

	_ "golang.org/x/image/bmp"  // decode only; stored as png
	_ "golang.org/x/image/webp" // decode only; stored as png
)

// Kind is what an upload is.
type Kind string

const (
	KindImage Kind = "image"
	KindANSI  Kind = "ansi"
)

// ErrUnsupported is returned for anything that is not an accepted picture or
// ANSI art, whatever its name says.
var ErrUnsupported = errors.New("unsupported file type: send a png, jpeg, gif, webp or bmp picture, or .ans/.bin ANSI art")

// Detect names a file by its bytes: "png", "jpg", "gif", "webp", "bmp" for
// pictures (the extension is ignored), or "ans" / "bin" for ANSI art, which
// has no magic number and is judged by the offered name plus its content.
// It does not decode the whole file; Normalize does.
func Detect(name string, data []byte) (string, Kind, error) {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "png", KindImage, nil
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "jpg", KindImage, nil
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "gif", KindImage, nil
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "webp", KindImage, nil
	case bytes.HasPrefix(data, []byte("BM")) && len(data) > 26:
		return "bmp", KindImage, nil
	}
	if looksMarkup(data) {
		return "", "", ErrUnsupported
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".ans", ".asc", ".diz", ".nfo":
		if err := checkANSI(data); err != nil {
			return "", "", err
		}
		return "ans", KindANSI, nil
	case ".bin":
		if err := checkBin(data); err != nil {
			return "", "", err
		}
		return "bin", KindANSI, nil
	}
	return "", "", ErrUnsupported
}

// Normalize validates data fully and returns the bytes to store with their
// extension. Pictures are decoded and re-encoded, which drops metadata (EXIF,
// GPS, comments) and anything appended after the image; bmp and webp are
// stored as png. Animated GIFs keep their frames and timing. ANSI art is
// stored as sent, SAUCE record included.
func Normalize(name string, data []byte) (ext string, kind Kind, out []byte, err error) {
	ext, kind, err = Detect(name, data)
	if err != nil {
		return "", "", nil, err
	}
	if kind == KindANSI {
		return ext, kind, data, nil
	}
	var buf bytes.Buffer
	switch ext {
	case "gif":
		g, derr := gif.DecodeAll(bytes.NewReader(data))
		if derr != nil {
			return "", "", nil, fmt.Errorf("not a readable gif: %v", derr)
		}
		if err := checkSize(g.Config.Width, g.Config.Height); err != nil {
			return "", "", nil, err
		}
		if err := gif.EncodeAll(&buf, g); err != nil {
			return "", "", nil, err
		}
	case "jpg":
		img, derr := jpeg.Decode(bytes.NewReader(data))
		if derr != nil {
			return "", "", nil, fmt.Errorf("not a readable jpeg: %v", derr)
		}
		if err := checkBounds(img); err != nil {
			return "", "", nil, err
		}
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			return "", "", nil, err
		}
	default: // png, bmp, webp -> png
		cfg, _, cerr := image.DecodeConfig(bytes.NewReader(data))
		if cerr != nil {
			return "", "", nil, fmt.Errorf("not a readable %s: %v", ext, cerr)
		}
		if err := checkSize(cfg.Width, cfg.Height); err != nil {
			return "", "", nil, err
		}
		img, _, derr := image.Decode(bytes.NewReader(data))
		if derr != nil {
			return "", "", nil, fmt.Errorf("not a readable %s: %v", ext, derr)
		}
		if err := png.Encode(&buf, img); err != nil {
			return "", "", nil, err
		}
		ext = "png"
	}
	return ext, kind, buf.Bytes(), nil
}

// maxPixels bounds a decoded picture so a tiny file that claims a huge
// canvas cannot exhaust memory (40 megapixels, about 160 MB as RGBA).
const maxPixels = 40 << 20

func checkSize(w, h int) error {
	if w <= 0 || h <= 0 {
		return errors.New("picture has no pixels")
	}
	if int64(w)*int64(h) > maxPixels {
		return fmt.Errorf("picture is %dx%d, too large", w, h)
	}
	return nil
}

func checkBounds(img image.Image) error {
	b := img.Bounds()
	return checkSize(b.Dx(), b.Dy())
}

// looksMarkup refuses HTML, SVG and XML outright, whatever they are named,
// so a page can never be stored as "art".
func looksMarkup(data []byte) bool {
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	h := strings.ToLower(strings.TrimLeft(string(head), " \t\r\n\ufeff"))
	for _, p := range []string{"<!doctype", "<html", "<svg", "<?xml", "<script", "<head", "<body", "<iframe"} {
		if strings.HasPrefix(h, p) || strings.Contains(h, p+" ") || strings.Contains(h, p+">") {
			return true
		}
	}
	return false
}

// stripSAUCE returns the art without its trailing SAUCE record and comment
// block, if it has one, for the content checks. The stored file keeps it.
func stripSAUCE(data []byte) []byte {
	if len(data) >= 128 && string(data[len(data)-128:len(data)-123]) == "SAUCE" {
		data = data[:len(data)-128]
		if i := bytes.LastIndex(data, []byte("COMNT")); i >= 0 && len(data)-i < 64*255+5 {
			data = data[:i]
		}
		data = bytes.TrimRight(data, "\x1a")
	}
	return data
}

// checkANSI accepts CP437 text art: escape sequences and high bytes are
// fine, but a file that is mostly NULs or other control bytes is a binary
// in disguise.
func checkANSI(data []byte) error {
	body := stripSAUCE(data)
	if len(body) == 0 {
		return errors.New("empty art file")
	}
	bad := 0
	for _, c := range body {
		switch {
		case c == 0:
			bad++
		case c < 0x20 && c != '\r' && c != '\n' && c != '\t' && c != 0x1b && c != 0x1a && c != 0x0c && c != 0x08:
			bad++
		}
	}
	if bad*100 > len(body) {
		return errors.New("not ANSI art: too many control bytes")
	}
	return nil
}

// checkBin accepts BinaryText: character/attribute pairs, a whole number of
// 80-column rows (160 bytes each).
func checkBin(data []byte) error {
	body := stripSAUCE(data)
	if len(body) == 0 || len(body)%160 != 0 {
		return errors.New("not BinaryText art: length is not a whole number of 80-column rows")
	}
	return nil
}
