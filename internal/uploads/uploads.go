// Package uploads validates and persists user-supplied image files (icons
// and background images). Caller supplies the maximum byte cap; the package
// enforces MIME sniffing on the bytes themselves so a renamed .exe can't
// pose as a .png. Filenames on disk are content-derived so users can't
// influence the path.
package uploads

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

// Kind picks the allowed MIME set and the on-disk extension defaults.
type Kind int

const (
	KindIcon Kind = iota
	KindBackground
)

var (
	ErrTooLarge      = errors.New("upload exceeds size limit")
	ErrUnsupportedMIME = errors.New("unsupported upload type")
	ErrEmpty         = errors.New("empty upload")
)

// allowedMIMEs lists the types we sniff for. Anything else is rejected.
// SVG is allowed only for icons — backgrounds use raster only because we
// render them via CSS background-image and don't want hostile <svg> JS.
var allowedMIMEs = map[Kind]map[string]string{
	KindIcon: {
		"image/png":      ".png",
		"image/jpeg":     ".jpg",
		"image/webp":     ".webp",
		"image/gif":      ".gif",
		"image/svg+xml":  ".svg",
		"text/xml":       ".svg", // http.DetectContentType reports text/xml for some SVGs
	},
	KindBackground: {
		"image/png":  ".png",
		"image/jpeg": ".jpg",
		"image/webp": ".webp",
	},
}

// Save reads the multipart file, enforces size limit and MIME, and writes the
// bytes under dir using a SHA256-derived filename. Returns the path *relative
// to dir* — callers store that as the icon_path, and combine with cfg.IconsDir
// when serving.
func Save(file multipart.File, header *multipart.FileHeader, kind Kind, dir string, maxBytes int64) (string, error) {
	if header == nil {
		return "", ErrEmpty
	}
	if header.Size <= 0 {
		return "", ErrEmpty
	}
	if header.Size > maxBytes {
		return "", fmt.Errorf("%w: %d > %d", ErrTooLarge, header.Size, maxBytes)
	}

	// Read into memory bounded by maxBytes+1 so a lying Content-Length can't
	// blow past the cap.
	buf, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read upload: %w", err)
	}
	if int64(len(buf)) > maxBytes {
		return "", ErrTooLarge
	}
	if len(buf) == 0 {
		return "", ErrEmpty
	}

	mime := http.DetectContentType(buf)
	ext, ok := allowedMIMEs[kind][mime]
	if !ok {
		// SVG sniffer sometimes returns "text/plain; charset=utf-8" for tiny
		// SVGs. Accept SVG only when the filename also claims .svg and the
		// content begins with `<svg` or `<?xml`.
		if kind == KindIcon && looksLikeSVG(buf, header.Filename) {
			ext = ".svg"
		} else {
			return "", fmt.Errorf("%w: %s", ErrUnsupportedMIME, mime)
		}
	}

	sum := sha256.Sum256(buf)
	name := hex.EncodeToString(sum[:]) + ext

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("creating uploads dir: %w", err)
	}
	full := filepath.Join(dir, name)
	if _, err := os.Stat(full); err == nil {
		// Already on disk — same bytes always yield the same hash, so reuse.
		return name, nil
	}
	if err := os.WriteFile(full, buf, 0o640); err != nil {
		return "", fmt.Errorf("writing upload: %w", err)
	}
	return name, nil
}

func looksLikeSVG(buf []byte, filename string) bool {
	if filepath.Ext(filename) != ".svg" {
		return false
	}
	// Trim leading whitespace.
	i := 0
	for i < len(buf) && (buf[i] == ' ' || buf[i] == '\t' || buf[i] == '\n' || buf[i] == '\r') {
		i++
	}
	rest := buf[i:]
	return hasPrefix(rest, "<svg") || hasPrefix(rest, "<?xml")
}

func hasPrefix(b []byte, p string) bool {
	if len(b) < len(p) {
		return false
	}
	return string(b[:len(p)]) == p
}
