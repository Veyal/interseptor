package mcp

import (
	"fmt"
	"strings"
	"testing"
)

func essay(n int) string {
	return strings.Repeat("x", n)
}

func TestWallOfTextErrorNamesFieldAndPublishesLimit(t *testing.T) {
	err, _ := validateFindingFormat(findingFormatInput{Severity: "High", Detail: essay(narrativeCharLimit + 20)})
	if err == nil {
		t.Fatal("expected rejection")
	}
	msg := err.Error()
	for _, want := range []string{"error:", "detail", fmt.Sprint(narrativeCharLimit), fmt.Sprint(narrativeCharLimit + 20), "impact"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q: %s", want, msg)
		}
	}
	body := fmt.Sprintf(`[{"type":"text","role":"observation","md":%q}]`, essay(narrativeCharLimit+5))
	err, _ = validateFindingFormat(findingFormatInput{Severity: "High", Body: body})
	if err == nil || !strings.Contains(err.Error(), "blocks") || !strings.Contains(err.Error(), fmt.Sprint(narrativeCharLimit)) {
		t.Fatalf("blocks narrative must hit the same published limit and name the field: %v", err)
	}
}

func TestNarrativeLimitAppliesToEachFieldSeparately(t *testing.T) {
	half := essay(narrativeCharLimit/2 + 10)
	body := fmt.Sprintf(`[{"type":"text","role":"observation","md":%q}]`, half)
	if err, _ := validateFindingFormat(findingFormatInput{Severity: "High", Detail: half, Body: body}); err != nil {
		t.Fatalf("two within-limit fields must pass: %v", err)
	}
	if err, _ := validateFindingFormat(findingFormatInput{Severity: "High", Detail: essay(narrativeCharLimit + 1), Impact: "stated", Why: "stated"}); err != nil {
		t.Fatalf("structured fields present, essay detail is allowed: %v", err)
	}
}

func TestHardErrorsNameFieldAndExpectedShape(t *testing.T) {
	err, _ := validateFindingFormat(findingFormatInput{Body: "not json"})
	if err == nil || !strings.HasPrefix(err.Error(), "error:") || !strings.Contains(err.Error(), "body") || !strings.Contains(err.Error(), "JSON array") {
		t.Fatalf("body error: %v", err)
	}
	err, _ = validateFindingFormat(findingFormatInput{Confidence: "maybe"})
	if err == nil || !strings.HasPrefix(err.Error(), "error:") || !strings.Contains(err.Error(), "confidence") || !strings.Contains(err.Error(), "maybe") {
		t.Fatalf("confidence error: %v", err)
	}
}

func TestFormatWarningsBlockIsExplicitlyNonBlocking(t *testing.T) {
	out := formatWarningsBlock([]string{"missing impact — set impact"})
	if !strings.Contains(out, "non-blocking") || !strings.Contains(out, "warning: missing impact") || strings.Contains(out, "error:") {
		t.Fatalf("warnings block must read as advisory: %q", out)
	}
	if formatWarningsBlock(nil) != "" {
		t.Fatal("no warnings, no block")
	}
}

func TestDetailIsDeprecatedInSchemaAndGuideStatesLimit(t *testing.T) {
	if !strings.Contains(detailFieldDescription, "DEPRECATED") || !strings.Contains(detailFieldDescription, fmt.Sprint(narrativeCharLimit)) {
		t.Fatalf("detail description must mark deprecation and limit: %s", detailFieldDescription)
	}
	if !strings.Contains(findingFormatGuide, fmt.Sprint(narrativeCharLimit)) || !strings.Contains(findingFormatGuide, "error:") || !strings.Contains(findingFormatGuide, "warning:") {
		t.Fatal("guide must publish the narrative limit and the error/warning convention")
	}
}
