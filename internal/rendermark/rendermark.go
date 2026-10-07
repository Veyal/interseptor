// Package rendermark stamps generated evidence renders with a PNG tEXt chunk
// so that, wherever the bytes travel, they can still be recognised as an
// Interseptor render rather than a browser or device screenshot. It uses only
// the standard library; both the renderer and the finding store depend on it.
package rendermark

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"strings"
)

// Keyword is the tEXt keyword of the marker chunk.
const Keyword = "Interseptor-Render"

var pngSig = []byte("\x89PNG\r\n\x1a\n")

// sanitize makes text valid Latin-1 tEXt content: printable ASCII only.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			r = '?'
		}
		b.WriteRune(r)
	}
	if b.Len() > 200 {
		return b.String()[:200]
	}
	return b.String()
}

func chunk(typ string, data []byte) []byte {
	out := make([]byte, 8, 12+len(data))
	binary.BigEndian.PutUint32(out, uint32(len(data)))
	copy(out[4:], typ)
	out = append(out, data...)
	crc := crc32.ChecksumIEEE(out[4:])
	return binary.BigEndian.AppendUint32(out, crc)
}

// Embed inserts the marker chunk right after IHDR. Input that is not a PNG is
// returned unchanged.
func Embed(png []byte, text string) []byte {
	const ihdrEnd = 8 + 25 // signature + IHDR chunk (13 data bytes)
	if len(png) < ihdrEnd || !bytes.HasPrefix(png, pngSig) {
		return png
	}
	c := chunk("tEXt", append([]byte(Keyword+"\x00"), sanitize(text)...))
	out := make([]byte, 0, len(png)+len(c))
	out = append(out, png[:ihdrEnd]...)
	out = append(out, c...)
	return append(out, png[ihdrEnd:]...)
}

// Find reports whether png carries the marker and returns its text.
func Find(png []byte) (string, bool) {
	if !bytes.HasPrefix(png, pngSig) {
		return "", false
	}
	for off := len(pngSig); off+12 <= len(png); {
		n := int(binary.BigEndian.Uint32(png[off:]))
		typ := string(png[off+4 : off+8])
		end := off + 8 + n + 4
		if n < 0 || end > len(png) {
			return "", false
		}
		if typ == "tEXt" {
			data := png[off+8 : off+8+n]
			if k, v, ok := bytes.Cut(data, []byte{0}); ok && string(k) == Keyword {
				return string(v), true
			}
		}
		if typ == "IDAT" || typ == "IEND" {
			return "", false // the marker, if present, precedes the image data
		}
		off = end
	}
	return "", false
}
