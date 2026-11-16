package control

import (
	"strings"
	"testing"
)

func TestUICodecsAndDecoderWorkbenchesArePolished(t *testing.T) {
	css := readUIAsset(t, "app.css")
	surfaces := readUIAsset(t, "surfaces.css")
	index := readUIAsset(t, "index.html")
	codecs := executableJS(readUIAsset(t, "js/codecs.js"))
	decoder := executableJS(readUIAsset(t, "js/decoder.js"))

	// Codecs editor must fill the vertical code pane rather than shrinking to 3 lines.
	requireUIContains(t, css, "#codecSrc{", "flex:1", ".codecs-id-field", ".codecs-out-head", ".codecs-tag")
	requireUIContains(t, index, `id="codecTestSideSeg"`, `for="codecId"`, `class="codecs-out-head"`)
	requireUIContains(t, codecs, "codecTestSide", "side: codecTestSide", "syncCodecDirtyState(")
	for _, inlineBadge := range []string{`style="color:var(--red);border-color:var(--red)"`, `style="color:var(--accent);border-color:var(--accent)"`, `style="white-space:pre-wrap;margin:0;font-family:var(--mono);font-size:var(--fs-xs)"`} {
		if strings.Contains(codecs, inlineBadge) {
			t.Errorf("codecs.js still embeds inline style %s; use semantic CSS classes", inlineBadge)
		}
	}

	// Decoder drawer uses a responsive two-pane workbench with grouped ops and semantic tones.
	requireUIContains(t, index, `id="decClear"`, `id="decInMeta"`, `id="decOutMeta"`, `id="decChain"`)
	requireUIContains(t, surfaces, ".dec-panes", ".dec-pane", ".dec-ops", ".dec-op-group")
	requireUIContains(t, decoder, "DEC_GROUPS", "decSetActiveOp(", "dataset.tone")
	if strings.Contains(decoder, "err.style.color=") {
		t.Error("decoder.js still mutates err.style.color inline; use data-tone tokens")
	}
}
