package collreport

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
)

// Response data, script console output and names come from the target and from
// imported files. No renderer may let them forge report lines, break the
// document or emit bytes the format cannot carry.
func hostileReport() *collrun.Report {
	evil := "x\n[PASS] GET forged line\r\x1b[2J\x1b]0;title\x07 ]]> </pre><script>alert(1)</script> \xff\xfe \u2028 &amp; <!--"
	long := strings.Repeat("é", 200_000)
	return &collrun.Report{
		RunUID: "RUN1", CollectionName: evil, EnvName: evil, Source: "cli", Status: collrun.StatusDone, Iterations: 1,
		Quarantined: []collrun.QuarantinedScript{{Owner: evil, Name: evil, Listen: evil, Hash: "abc"}},
		Items: []collrun.ItemResult{{Seq: 1, ItemUID: "i1", Name: evil, Path: evil, Method: evil, URL: "https://example.com/" + evil,
			Outcome: collexec.OutcomeSent, HTTPStatus: 200, StatusText: evil, Error: evil,
			Tests:   []collexec.TestResult{{Name: evil, Status: collexec.TestFail, Message: evil, Expected: evil, Actual: evil, Source: evil}},
			Console: []collexec.ConsoleLine{{Level: evil, Text: evil}, {Level: "log", Text: long}}}},
		Totals: collrun.Totals{Requests: 1, Sent: 1, Fail: 1},
	}
}

func renderHostile(t *testing.T, format string) string {
	t.Helper()
	b, err := Render(format, hostileReport(), Options{Scrub: func(s string) string { return s }})
	if err != nil {
		t.Fatalf("%s: %v", format, err)
	}
	return string(b)
}

func TestTextReportCannotBeForgedByReportedStrings(t *testing.T) {
	out := renderHostile(t, FormatCLI)
	if !utf8.ValidString(out) {
		t.Error("text report is not valid UTF-8")
	}
	for _, r := range out {
		if r != '\n' && (r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == '\u2028' || r == '\u2029') {
			t.Errorf("text report carries control character %U", r)
			break
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "[PASS]") {
			t.Errorf("a reported string forged a result line: %.80q", line)
		}
	}
}

func TestJUnitReportStaysWellFormedWithHostileStrings(t *testing.T) {
	out := renderHostile(t, FormatJUnit)
	if !utf8.ValidString(out) {
		t.Error("junit report is not valid UTF-8")
	}
	dec := xml.NewDecoder(strings.NewReader(out))
	for {
		if _, err := dec.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("junit report is not well-formed XML: %v", err)
		}
	}
	if strings.Contains(out, "<script>") {
		t.Error("junit report carries raw markup")
	}
}

func TestHTMLReportEscapesHostileStrings(t *testing.T) {
	out := renderHostile(t, FormatHTML)
	if !utf8.ValidString(out) {
		t.Error("html report is not valid UTF-8")
	}
	if strings.Contains(out, "<script") || strings.Contains(out, "</pre><") || strings.Contains(out, "<!--") {
		t.Error("html report carries raw markup from a reported string")
	}
}

func TestJSONReportIsValidWithHostileStrings(t *testing.T) {
	if out := renderHostile(t, FormatJSON); !utf8.ValidString(out) {
		t.Error("json report is not valid UTF-8")
	}
}
