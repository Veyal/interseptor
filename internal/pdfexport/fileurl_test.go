package pdfexport

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A temp directory containing '#' or '%' must still reach the browser as the
// exact source file: unescaped, '#' starts a fragment and '%' an escape.
func TestRenderEscapesSourceFileURL(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "odd#dir 100%")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	var src string
	var seen []byte
	f := &fakeLaunch{}
	f.behave = func(out, logPath string) {
		for _, a := range f.args {
			if strings.HasPrefix(a, "file:") {
				u, err := url.Parse(a)
				if err != nil {
					t.Errorf("source URL %q does not parse: %v", a, err)
					return
				}
				src = u.Path
				if runtime.GOOS == "windows" {
					src = strings.TrimPrefix(src, "/") // /C:/... -> C:/...
				}
				seen, _ = os.ReadFile(filepath.FromSlash(src))
			}
		}
		writeFakePDF(out, logPath)
	}
	r := newFake(t, f)
	html := "<html><body>escape-marker</body></html>"
	if _, err := r.Render(context.Background(), []byte(html)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.ToSlash(src), filepath.ToSlash(tmp)) || string(seen) != html {
		t.Fatalf("browser URL resolves to %q (read %q), want the HTML under %q", src, seen, tmp)
	}
}

func TestFileURL(t *testing.T) {
	got := fileURL("/tmp/a#b/c%d e.html")
	if got != "file:///tmp/a%23b/c%25d%20e.html" {
		t.Fatalf("fileURL = %q", got)
	}
	if got := fileURL(`C:/Users/x/report.html`); got != "file:///C:/Users/x/report.html" {
		t.Fatalf("windows fileURL = %q", got)
	}
}
