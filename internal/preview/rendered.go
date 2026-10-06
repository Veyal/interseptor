package preview

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Kind identifies which evidence render produced a Rendered value.
const (
	KindIntruderTimeline     = "intruder_timeline"
	KindIntruderDistribution = "intruder_distribution"
	KindIntruderRace         = "intruder_race"
	KindIntruderStrip        = "intruder_strip"
	KindAuthzMatrix          = "authz_matrix"
	KindFlowDiff             = "flow_diff"
	KindFlowWaterfall        = "flow_waterfall"
	KindFindingChain         = "finding_chain"
)

// Rendered is the shared result of every evidence renderer.
type Rendered struct {
	PNG     []byte
	Alt     string // generated, factual alt text
	Summary string // one-line summary for captions
	Kind    string // one of the Kind* constants
	Width   int
	Height  int
}

// Opts controls evidence render size and theme. Reports use the light theme.
type Opts struct {
	Width   int  // default 1100, clamped to 640..1600
	Dark    bool // dark theme for UI preview only
	MaxRows int  // plotted rows before aggregation (default 400)
	// Deadline aborts a render with ErrRenderTimeout once passed (checked
	// between draw attempts); the zero value means no deadline.
	Deadline time.Time
}

// ErrRenderTimeout is returned when Opts.Deadline passes before a render ends.
var ErrRenderTimeout = errors.New("preview: render deadline exceeded")

const (
	defaultRenderWidth = 1100
	minRenderWidth     = 640
	maxRenderWidth     = 1600
	maxRenderHeight    = 1800
	defaultMaxRows     = 400
)

func (o Opts) width() int {
	switch {
	case o.Width <= 0:
		return defaultRenderWidth
	case o.Width < minRenderWidth:
		return minRenderWidth
	case o.Width > maxRenderWidth:
		return maxRenderWidth
	}
	return o.Width
}

func (o Opts) maxRows() int {
	if o.MaxRows <= 0 || o.MaxRows > defaultMaxRows {
		return defaultMaxRows
	}
	return o.MaxRows
}

// AltFromParts joins non-empty sentence fragments into alt text, each ending
// in a period, with whitespace collapsed.
func AltFromParts(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Join(strings.Fields(p), " ")
		if p == "" {
			continue
		}
		if !strings.HasSuffix(p, ".") && !strings.HasSuffix(p, "!") && !strings.HasSuffix(p, "?") {
			p += "."
		}
		out = append(out, p)
	}
	return strings.Join(out, " ")
}

// countOf formats "1 cluster" / "2 clusters" (regular -s plurals only).
func countOf(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
