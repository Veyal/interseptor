package control

import "testing"

// Session section layout: the per-host overrides disclosure sits inside the
// headers section (it must not draw a second section rule), and the Save row
// aligns with the section content instead of being padded inward.
func TestUISettingsSessionLayout(t *testing.T) {
	index := readUIAsset(t, "index.html")
	requireUIContains(t, index,
		`<details class="set-disc u-mt-3">`+"\n"+`            <summary>Per-host overrides</summary>`,
		`<div class="row u-gap-2 u-wrap u-mt-4"><button class="btn btn-primary" id="saveSessionBtn">`,
		`<div class="row u-gap-3 u-wrap u-mb-2"><button type="button" class="uisw" id="autoBypassToggle"`,
	)
}
