package control

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Veyal/interseptor/internal/msgcodec"
	"github.com/Veyal/interseptor/internal/store"
)

// Body decoding for flow images. In decode=auto each side's body (already
// decompressed by bodyForDisplayLimit) is shown readable when a message codec,
// a form-urlencoded layout or a whole-body base64 text applies, in that order.
// decode=raw draws the captured bytes. Request line and headers are never
// rewritten.
const (
	decodeAuto = "auto"
	decodeRaw  = "raw"

	hdrDecodedBody = "X-Interseptor-Decoded-Body"

	// maxPreviewCodecBody is the largest stored body handed to a message codec;
	// flowForCodec reads bodies whole, so bigger ones are drawn undecoded.
	maxPreviewCodecBody = 4 << 20
	maxBase64Printable  = 16 // minimum base64 body length treated as encoded
	maxDecodeNoteErr    = 80
)

// parseDecodeMode normalizes the decode parameter ("" means auto).
func parseDecodeMode(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", decodeAuto:
		return decodeAuto, nil
	case decodeRaw:
		return decodeRaw, nil
	}
	return "", fmt.Errorf("decode must be auto or raw")
}

// sideDecode is what happened to one side: Tag is the short header token
// (codec:<id>, form, base64; empty when the body was not decoded) and Notice
// the band text (also set when a codec failed).
type sideDecode struct {
	Tag    string
	Notice string
}

type previewDecodeInfo struct{ Req, Res sideDecode }

// Header is the X-Interseptor-Decoded-Body value ("req=form,res=base64"); empty
// when nothing was decoded.
func (i previewDecodeInfo) Header() string {
	var parts []string
	if i.Req.Tag != "" {
		parts = append(parts, "req="+i.Req.Tag)
	}
	if i.Res.Tag != "" {
		parts = append(parts, "res="+i.Res.Tag)
	}
	return strings.Join(parts, ",")
}

func (i previewDecodeInfo) Notices() []string {
	var out []string
	for _, n := range []string{i.Req.Notice, i.Res.Notice} {
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

func writeDecodeHeader(w http.ResponseWriter, i previewDecodeInfo) {
	if v := i.Header(); v != "" {
		w.Header().Set(hdrDecodedBody, v)
	}
}

// previewDecoder decodes both sides of one flow, loading the project codecs and
// the codec view of the flow at most once.
type previewDecoder struct {
	h      *Hub
	f      *store.Flow
	mode   string
	loaded bool
	codecs []*msgcodec.Codec
	flow   msgcodec.Flow
	flowOK bool
}

// newPreviewDecoder returns nil in raw mode.
func (h *Hub) newPreviewDecoder(f *store.Flow, mode string) *previewDecoder {
	if mode == decodeRaw {
		return nil
	}
	return &previewDecoder{h: h, f: f, mode: mode}
}

func (d *previewDecoder) codecFlow() (msgcodec.Flow, bool) {
	if d.loaded {
		return d.flow, d.flowOK
	}
	d.loaded = true
	d.codecs = d.h.loadCodecs()
	if len(d.codecs) == 0 || d.f.ReqLen > maxPreviewCodecBody || d.f.ResLen > maxPreviewCodecBody {
		return msgcodec.Flow{}, false
	}
	flow, err := d.h.flowForCodec(d.f)
	if err != nil {
		return msgcodec.Flow{}, false
	}
	d.flow, d.flowOK = flow, true
	return d.flow, true
}

// decodeSide returns the readable text for one side's body, or ok=false. body
// is the displayed body (decompressed); truncated means it was cut at the read
// limit, which rules out the whole-body decoders.
func (d *previewDecoder) decodeSide(request bool, headers map[string][]string, body []byte, truncated bool) (string, sideDecode, bool) {
	label := "Response"
	side := "res"
	if request {
		label, side = "Request", "req"
	}
	var failed string
	if flow, ok := d.codecFlow(); ok {
		if r, matched := msgcodec.TryDecode(d.codecs, flow, side); matched {
			title := strings.TrimSpace(r.Title)
			if title == "" {
				title = r.CodecID
			}
			switch {
			case r.Error != "":
				failed = fmt.Sprintf("codec “%s” failed: %s", title, decodeNoteLine(r.Error, maxDecodeNoteErr))
			case r.Plaintext != "":
				return r.Plaintext, sideDecode{Tag: "codec:" + r.CodecID, Notice: fmt.Sprintf("%s decoded: codec “%s”", label, title)}, true
			}
		}
	}
	text, kind := "", ""
	if !truncated {
		if s, ok := decodeFormBody(body, firstHeader(headers, "Content-Type"), request); ok {
			text, kind = s, "form"
		} else if s, ok := decodeBase64Body(body); ok {
			text, kind = s, "base64"
		}
	}
	switch {
	case kind != "":
		name := kind
		if kind == "form" {
			name = "form-urlencoded"
		}
		notice := fmt.Sprintf("%s decoded: %s", label, name)
		if failed != "" {
			notice += "; " + failed
		}
		return text, sideDecode{Tag: kind, Notice: notice}, true
	case failed != "":
		return "", sideDecode{Notice: fmt.Sprintf("%s %s; showing the raw body", label, failed)}, false
	}
	return "", sideDecode{}, false
}

func decodeNoteLine(s string, n int) string {
	// Script errors carry a traceback; the last line is the message.
	if lines := strings.Split(strings.TrimSpace(s), "\n"); len(lines) > 1 {
		s = lines[len(lines)-1]
	}
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// decodeFormBody renders an application/x-www-form-urlencoded body as one
// "key = value" line per pair, percent- and plus-decoded. With a Content-Type
// only form types qualify; without one a request body must parse cleanly as
// k=v&k=v (non-empty keys and values, no whitespace, not JSON/XML).
func decodeFormBody(body []byte, contentType string, request bool) (string, bool) {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	explicit := ct != ""
	if explicit && !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		return "", false
	}
	if !explicit && !request {
		return "", false
	}
	s := string(body)
	if explicit {
		s = strings.TrimSpace(s)
	}
	if s == "" {
		return "", false
	}
	if !explicit {
		if strings.IndexFunc(s, unicode.IsSpace) >= 0 || strings.ContainsAny(s[:1], "{[<") {
			return "", false
		}
	}
	var lines []string
	for _, piece := range strings.Split(s, "&") {
		if piece == "" {
			if explicit {
				continue
			}
			return "", false
		}
		k, v, hasEq := strings.Cut(piece, "=")
		if !explicit && (!hasEq || k == "" || v == "" || v[0] == '=' || !formKeyOK(k)) {
			return "", false
		}
		lines = append(lines, formUnescape(k)+" = "+formUnescape(v))
	}
	if len(lines) == 0 {
		return "", false
	}
	return strings.ToValidUTF8(strings.Join(lines, "\n"), "�"), true
}

func formKeyOK(k string) bool {
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.-[]%+:", r)) {
			return false
		}
	}
	return true
}

func formUnescape(s string) string {
	if u, err := url.QueryUnescape(s); err == nil {
		return u
	}
	return s
}

// decodeBase64Body decodes a body that is entirely base64 (standard or URL
// alphabet, padding optional, surrounding whitespace ignored, at least 16
// characters) into valid UTF-8 printable text. Anything else stays raw.
func decodeBase64Body(body []byte) (string, bool) {
	s := strings.TrimSpace(string(body))
	if len(s) < maxBase64Printable {
		return "", false
	}
	std, urlAlpha := false, false
	data := strings.TrimRight(s, "=")
	pad := len(s) - len(data)
	if pad > 2 || (pad > 0 && len(s)%4 != 0) || len(data)%4 == 1 {
		return "", false
	}
	for i := 0; i < len(data); i++ {
		switch c := data[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '+' || c == '/':
			std = true
		case c == '-' || c == '_':
			urlAlpha = true
		default:
			return "", false
		}
	}
	if std && urlAlpha {
		return "", false
	}
	enc := base64.RawStdEncoding
	if urlAlpha {
		enc = base64.RawURLEncoding
	}
	dec, err := enc.DecodeString(data)
	if err != nil || len(dec) == 0 || !utf8.Valid(dec) {
		return "", false
	}
	for _, r := range string(dec) {
		if r == utf8.RuneError || !(unicode.IsPrint(r) || r == '\n' || r == '\r' || r == '\t') {
			return "", false
		}
	}
	return string(dec), true
}
