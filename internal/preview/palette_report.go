package preview

import (
	"fmt"
	"image/color"
)

// reportPalette holds semantic colours for evidence renders.
type reportPalette struct {
	paper, grid, ink, muted, panel color.RGBA
	success, redirect, client      color.RGBA // 2xx, 3xx, 4xx
	blocked, server, errc          color.RGBA // 429/403, 5xx, error
	accent                         color.RGBA
}

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: 0xff} }

// Light palette. Names: Paper White, Mist Grey, Ink Navy, Success Teal,
// Slate Blue, Amber, Signal Red, Deep Plum, Graphite.
func lightReportPalette() reportPalette {
	return reportPalette{
		paper:    rgb(0xff, 0xff, 0xff),
		grid:     rgb(0xe3, 0xe6, 0xea),
		ink:      rgb(0x1a, 0x23, 0x32),
		muted:    rgb(0x55, 0x5b, 0x66),
		panel:    rgb(0xf5, 0xf6, 0xf8),
		success:  rgb(0x1b, 0x7f, 0x6b),
		redirect: rgb(0x3b, 0x6f, 0xa8),
		client:   rgb(0xa8, 0x63, 0x00), // Amber #a86300 (spec #b36b00 is 4.18:1; darkened for AA text)
		blocked:  rgb(0xb3, 0x26, 0x1e),
		server:   rgb(0x6b, 0x2c, 0x91),
		errc:     rgb(0x55, 0x5b, 0x66),
		accent:   rgb(0x1a, 0x73, 0xb8),
	}
}

// Dark palette for UI previews; status colours are lightened for contrast.
func darkReportPalette() reportPalette {
	return reportPalette{
		paper:    rgb(0x16, 0x18, 0x1e),
		grid:     rgb(0x2e, 0x34, 0x40),
		ink:      rgb(0xe6, 0xea, 0xf0),
		muted:    rgb(0x9a, 0xa4, 0xb5),
		panel:    rgb(0x1e, 0x22, 0x2c),
		success:  rgb(0x3f, 0xc2, 0xa6),
		redirect: rgb(0x7c, 0xac, 0xe0),
		client:   rgb(0xe8, 0xa3, 0x3d),
		blocked:  rgb(0xf0, 0x7a, 0x70),
		server:   rgb(0xc0, 0x8c, 0xe6),
		errc:     rgb(0xa8, 0xb0, 0xbd),
		accent:   rgb(0x5b, 0x9f, 0xd4),
	}
}

func paletteFor(dark bool) reportPalette {
	if dark {
		return darkReportPalette()
	}
	return lightReportPalette()
}

// isBlockedStatus reports lockout-like statuses (rate limit / forbidden / locked).
func isBlockedStatus(status int) bool {
	return status == 429 || status == 403 || status == 423
}

// statusColor maps an HTTP status to its semantic colour. status <= 0 is an error.
func (p reportPalette) statusColor(status int) color.RGBA {
	switch {
	case status <= 0 || status >= 600:
		return p.errc
	case isBlockedStatus(status):
		return p.blocked
	case status >= 500:
		return p.server
	case status >= 400:
		return p.client
	case status >= 300:
		return p.redirect
	case status >= 200:
		return p.success
	}
	return p.redirect
}

// statusGlyph is the non-colour channel for a status: a short ASCII mark.
// 2xx "ok", blocked "X", other classes their digit class, error "E".
func statusGlyph(status int) string {
	switch {
	case status <= 0 || status >= 600:
		return "E"
	case isBlockedStatus(status):
		return "X"
	case status >= 500:
		return "5"
	case status >= 400:
		return "4"
	case status >= 300:
		return "3"
	case status >= 200:
		return "ok"
	}
	return "1"
}

func linear(c uint8) float64 {
	v := float64(c) / 255
	if v <= 0.03928 {
		return v / 12.92
	}
	return pow((v+0.055)/1.055, 2.4)
}

func relLuminance(c color.RGBA) float64 {
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

// contrastRatio is the WCAG 2.x contrast ratio between two colours.
func contrastRatio(a, b color.RGBA) float64 {
	la, lb := relLuminance(a), relLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// statusChipText spells the status class out so a chip is readable alone
// ("502 5xx", "429 throttle", "err") instead of a cryptic suffix glyph.
func statusChipText(status int) string {
	switch {
	case status <= 0 || status >= 600:
		return "err"
	case isBlockedStatus(status):
		return fmt.Sprintf("%d throttle", status)
	}
	return fmt.Sprintf("%d %dxx", status, status/100)
}
