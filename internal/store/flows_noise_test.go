package store

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func TestFlowFilterHidesBrowserNoiseBeforeTheLimit(t *testing.T) {
	// Given: a target flow, then more browser background flows than the page holds.
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	insert := func(host, path string) int64 {
		t.Helper()
		id, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: host, Path: path, Status: 200})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	target := insert("app.example.com", "/login")
	keepAccounts := insert("accounts.google.com", "/o/oauth2/auth") // a sign-in flow under test stays
	keepSuffix := insert("notgvt1.com", "/")                        // suffix needs the dot
	for _, n := range [][2]string{
		{"clients2.google.com", "/service/update2/crx"},
		{"CONTENT-AUTOFILL.googleapis.com", "/v1/forms:vote"},
		{"rr7---sn-example.gvt1.com", "/edgedl/x"},
		{"accounts.google.com", "/ListAccounts?gpsia=1"},
		{"www.google.com", "/async/folae?async=_fmt:p"},
		{"www.googleapis.com", "/chromewebstore/v1.1/item"},
	} {
		insert(n[0], n[1])
	}

	// When
	got, err := s.QueryFlowsListFilter(FlowFilter{HideBrowserNoise: true, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.QueryFlowsListFilter(FlowFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if len(got) != 3 || got[0].ID != keepSuffix || got[1].ID != keepAccounts || got[2].ID != target {
		ids := []int64{}
		for _, f := range got {
			ids = append(ids, f.ID)
		}
		t.Fatalf("ids = %v, want [%d %d %d]", ids, keepSuffix, keepAccounts, target)
	}
	if len(all) != 9 {
		t.Fatalf("without the filter %d rows, want all 9 (display filter only)", len(all))
	}
}

// js/proxy-filters.js applies the same rules to live rows; the lists must match.
func TestBrowserNoiseRulesMatchTheUIMirror(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "control", "ui", "js", "proxy-filters.js"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	block := regexp.MustCompile(`(?s)export const BROWSER_NOISE = \{(.*?)\n\};`).FindStringSubmatch(src)
	if block == nil {
		t.Fatal("BROWSER_NOISE block not found in proxy-filters.js")
	}
	quoted := regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(block[1], -1)
	js := map[string]int{}
	for _, q := range quoted {
		js[q[1]]++
	}
	var want []string
	want = append(want, browserNoiseHosts...)
	want = append(want, browserNoiseSuffixes...)
	for _, hp := range browserNoisePaths {
		want = append(want, hp[0], hp[1])
	}
	goSet := map[string]int{}
	for _, w := range want {
		goSet[w]++
	}
	for k, n := range goSet {
		if js[k] != n {
			t.Errorf("%q appears %d times in Go rules but %d in the UI mirror", k, n, js[k])
		}
	}
	for k := range js {
		if goSet[k] == 0 {
			t.Errorf("UI mirror has %q, Go rules do not", k)
		}
	}
}
