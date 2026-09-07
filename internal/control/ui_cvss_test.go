package control

import (
	"strings"
	"testing"
)

func TestUICVSSUsesExplicitApplyAndIgnoresStalePreview(t *testing.T) {
	cvss := executableJS(readUIAsset(t, "js/cvss.js"))
	findings := executableJS(readUIAsset(t, "js/findings.js"))
	for _, want := range []string{
		"/api/finding-cvss",
		"data-cvss-preview",
		"data-cvss-apply",
		"generation++",
		"if (token !== generation) return",
		"latestVector !== input.value.trim()",
		"input.value",
		"severity: latest.rating === 'INFO' ? 'Info'",
	} {
		if !strings.Contains(cvss, want) {
			t.Errorf("CVSS editor missing compatibility guard %q", want)
		}
	}
	if strings.Contains(cvss, "onblur") || strings.Contains(findings, "blurPatch('#findCvss'") {
		t.Error("CVSS input must not persist a partially typed vector on blur")
	}
	for _, want := range []string{"renderCvssEditor", "bindCvssEditor", "{ cvss: vector, severity }"} {
		if !strings.Contains(findings, want) {
			t.Errorf("findings editor must wire explicit CVSS Apply: missing %q", want)
		}
	}
}

func TestUICVSSUsesCustomMetricSelects(t *testing.T) {
	cvss := executableJS(readUIAsset(t, "js/cvss.js"))
	for _, metric := range []string{"AV", "AC", "AT", "PR", "UI", "VC", "VI", "VA", "SC", "SI", "SA"} {
		if !strings.Contains(cvss, `['`+metric+`',`) {
			t.Errorf("CVSS editor missing metric %s", metric)
		}
	}
	if !strings.Contains(cvss, "<select data-cvss-metric=") {
		t.Error("CVSS metrics must use the existing select enhancement path")
	}
}
