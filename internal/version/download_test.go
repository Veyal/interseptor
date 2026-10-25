package version

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestDownloadToStreamsBodyAndReportsProgress(t *testing.T) {
	body := strings.Repeat("x", 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	var lastDone int64
	n, total, err := DownloadTo(context.Background(), srv.Client(), srv.URL+"/a.zip", &buf, 1<<20, func(done, size int64) { lastDone = done })
	if err != nil {
		t.Fatalf("DownloadTo: %v", err)
	}
	if n != int64(len(body)) || total != int64(len(body)) || buf.String() != body {
		t.Fatalf("got n=%d total=%d len=%d", n, total, buf.Len())
	}
	if lastDone != int64(len(body)) {
		t.Fatalf("final progress = %d, want %d", lastDone, len(body))
	}
}

func TestDownloadToRejectsOversizeAndHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(bytes.Repeat([]byte("y"), 100))
	}))
	defer srv.Close()

	if _, _, err := DownloadTo(context.Background(), srv.Client(), srv.URL+"/big", &bytes.Buffer{}, 10, nil); err == nil {
		t.Fatal("expected size-limit error")
	}
	_, _, err := DownloadTo(context.Background(), srv.Client(), srv.URL+"/missing", &bytes.Buffer{}, 10, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("expected HTTP 404 error, got %v", err)
	}
}

func TestParseChecksumsExported(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	data := sum + "  x-Interseptor-v1.0.0-unsigned.zip\n" + strings.Repeat("cd", 32) + "  Interseptor-v1.0.0-unsigned.zip\n"
	got, ok := ParseChecksums(data, "Interseptor-v1.0.0-unsigned.zip")
	if !ok || got != strings.Repeat("cd", 32) {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	if _, ok := ParseChecksums(sum+"  other.zip\n", "Interseptor-v1.0.0-unsigned.zip"); ok {
		t.Fatal("expected no match")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"2.19.0", "v2.19.0", 0, true},
		{"2.19", "2.19.0", 0, true},
		{"2.20.0", "2.19.9", 1, true},
		{"2.9.0", "2.19.0", -1, true},
		{"garbage", "2.19.0", 0, false},
	}
	for _, c := range cases {
		got, ok := CompareVersions(c.a, c.b)
		if got != c.want || ok != c.ok {
			t.Errorf("CompareVersions(%q,%q) = %d,%v want %d,%v", c.a, c.b, got, ok, c.want, c.ok)
		}
	}
}

func TestReleaseAssetURL(t *testing.T) {
	got := ReleaseAssetURL("v2.19.0", "macos-checksums.txt")
	want := "https://github.com/" + Repo + "/releases/download/v2.19.0/macos-checksums.txt"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
