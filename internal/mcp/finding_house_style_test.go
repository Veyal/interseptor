package mcp

import (
	"strings"
	"testing"
)

// The house style is documented guidance read by agents at call time. The
// hard reject (narrativeCharLimit) must not move with it.
func TestFindingFormatGuideCarriesHouseStyle(t *testing.T) {
	for _, want := range []string{
		"HOUSE STYLE",
		"working pentester",
		"title <=70", "summary: ONE sentence <=140", "impact: ONE sentence <=120",
		"why <=100", "blocks[].text <=100", "fix <=160", "retest: ONE sentence <=100",
		"NEVER WRITE",
		"background or theory", "restating the title", "hedging", "narration",
		"severity adjectives", "padding", "already visible",
	} {
		if !strings.Contains(findingFormatGuide, want) {
			t.Errorf("findingFormatGuide missing %q", want)
		}
	}
}

func TestNarrativeCharLimitStaysHardReject(t *testing.T) {
	if narrativeCharLimit != 180 {
		t.Fatalf("narrativeCharLimit = %d; the house-style budgets are guidance, the reject stays 180", narrativeCharLimit)
	}
}

// Evidence is not optional. The guide used to require a flow or image only
// before report-readiness, and create_finding advertised a title-only stub as
// an acceptable result, so agents filed claims with nothing backing them.
func TestFindingGuideAlwaysRequiresEvidence(t *testing.T) {
	for _, want := range []string{
		"ALWAYS attach evidence",
		"at least one captured flow or image",
		"same run",
		"never a finished finding",
	} {
		if !strings.Contains(findingFormatGuide, want) {
			t.Errorf("findingFormatGuide missing %q", want)
		}
	}
	if strings.Contains(findingFormatGuide, "every report-ready finding needs a captured flow and/or image") {
		t.Error("findingFormatGuide still scopes the evidence requirement to report-ready only")
	}
}

// The create_finding description must not advertise a bare stub as a finished
// result; a title-only create is an intermediate step that still owes evidence.
func TestCreateFindingDescriptionDoesNotBlessBareStubs(t *testing.T) {
	s := New("http://127.0.0.1:1") // control plane never contacted: we only read the tool description
	tool, ok := s.tools["create_finding"]
	if !ok {
		t.Fatal("create_finding tool missing")
	}
	if strings.Contains(tool.description, "Stub create with title only is OK.") {
		t.Error("create_finding still advertises a title-only stub as OK with no evidence follow-up")
	}
	if !strings.Contains(tool.description, "attach evidence in the same run") {
		t.Error("create_finding description must tell the agent to attach evidence in the same run")
	}
}
