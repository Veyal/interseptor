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
