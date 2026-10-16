package pdfexport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// EnvLiveTest opts into the one test here that starts a real browser. It is off
// by default: CI has no Chrome, and a test that launches the operator's browser
// should never be a side effect of `go test ./...`.
const EnvLiveTest = "INTERSEPTOR_PDF_LIVE_TEST"

// TestLiveRenderWithRealBrowser prints a page with a real headless Chrome. Run
// it with INTERSEPTOR_PDF_LIVE_TEST=1 after changing the command line, because
// the flags are the part the fakes cannot check.
func TestLiveRenderWithRealBrowser(t *testing.T) {
	if os.Getenv(EnvLiveTest) == "" {
		t.Skipf("set %s=1 to print with a real browser", EnvLiveTest)
	}
	r := New()
	bin, err := r.Browser()
	if err != nil {
		t.Skipf("no browser: %v", err)
	}
	t.Logf("browser: %s", bin)

	html := `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Interseptor — Engagement Report</title>` +
		`<style>body{font-family:system-ui}pre{background:#f4f4f4;padding:8px}</style></head><body>` +
		`<h1>Interseptor — Engagement Report</h1><h2>High</h2><h3>1. IDOR on /invoices/{id}</h3>` +
		`<pre>GET /invoices/8 HTTP/1.1\nHost: example.com</pre></body></html>`

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	pdf, err := r.Render(ctx, []byte(html))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	t.Logf("printed %d bytes in %s", len(pdf), time.Since(start).Round(time.Millisecond))
	if !strings.HasPrefix(string(pdf), "%PDF-") {
		t.Fatalf("not a PDF: %q", pdf[:16])
	}
	if !strings.Contains(string(pdf[max(0, len(pdf)-1024):]), "%%EOF") {
		t.Fatal("PDF has no trailer")
	}
	// A real print of this page is tens of kilobytes; a few hundred bytes would
	// mean Chrome produced an empty document.
	if len(pdf) < 2000 {
		t.Fatalf("suspiciously small PDF: %d bytes", len(pdf))
	}
	out := filepath.Join(t.TempDir(), "live.pdf")
	if err := os.WriteFile(out, pdf, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", out)
}
