package preview

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/rendermark"
)

func TestEveryRenderCarriesTheGeneratedMarker(t *testing.T) {
	r, err := RenderIntruderTimeline(tlFixture12(), Opts{SourceRef: "intruder:run-abc"})
	if err != nil {
		t.Fatal(err)
	}
	text, ok := rendermark.Find(r.PNG)
	if !ok || !strings.Contains(text, "ref=intruder:run-abc") || !strings.Contains(text, "not a browser screenshot") {
		t.Fatalf("marker = %q ok=%v", text, ok)
	}
	for name, fn := range map[string]func() (Rendered, error){
		"distribution": func() (Rendered, error) { return RenderIntruderDistribution(distFixture(), Opts{}) },
		"race":         func() (Rendered, error) { return RenderIntruderRace(raceFixture(), Opts{}) },
		"strip":        func() (Rendered, error) { return RenderIntruderStrip(StripInput{Rows: stripFixture(5)}, Opts{}) },
		"authz":        func() (Rendered, error) { return RenderAuthzMatrix(authzFixture(), Opts{}) },
		"chain":        func() (Rendered, error) { return RenderFindingChain(linearChain(), Opts{}) },
		"diff":         func() (Rendered, error) { return RenderFlowDiff(sampleFlowDiff(), Opts{}) },
		"waterfall": func() (Rendered, error) {
			return RenderFlowWaterfall(WaterfallInput{Rows: wfRows()}, Opts{})
		},
	} {
		r, err := fn()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := rendermark.Find(r.PNG); !ok {
			t.Errorf("%s render has no generated-render marker", name)
		}
	}
}

func TestFooterShowsSourceRef(t *testing.T) {
	a, _ := RenderIntruderTimeline(tlFixture12(), Opts{Width: 640, SourceRef: "intruder:run-abc"})
	b, _ := RenderIntruderTimeline(tlFixture12(), Opts{Width: 640})
	if len(a.PNG) == len(b.PNG) && string(a.PNG) == string(b.PNG) {
		t.Fatal("the source ref must be visible in the footer pixels")
	}
}
