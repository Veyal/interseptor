package version

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubLatestRelease points the release-page redirect at a server whose latest
// tag is `tag`, with an empty HOME and no token so the API path is skipped.
func stubLatestRelease(t *testing.T, tag string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("INTERSEPTOR_GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	web := httptest.NewServer(nil)
	web.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.Redirect(w, r, web.URL+"/"+Repo+"/releases/tag/v"+tag, http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	t.Cleanup(web.Close)
	old := githubReleasesLatest
	t.Cleanup(func() { githubReleasesLatest = old })
	githubReleasesLatest = web.URL + "/" + Repo + "/releases/latest"
}

func TestCheckLatestFreshBypassesCache(t *testing.T) {
	stubLatestRelease(t, "99.0.0")
	writeLatestCache("1.0.0", "") // a fresh-looking, stale-in-fact cache entry

	got, _, err := CheckLatest(context.Background())
	if err != nil || got != "1.0.0" {
		t.Fatalf("passive CheckLatest should keep using the cache: got %q err %v", got, err)
	}
	got, newer, err := CheckLatestFresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "99.0.0" || !newer {
		t.Fatalf("CheckLatestFresh must bypass the cache: latest=%q newer=%v", got, newer)
	}
	if c, ok := readLatestCache(); !ok || c.Latest != "99.0.0" {
		t.Fatalf("a fresh check should refresh the cache, got %+v ok=%v", c, ok)
	}
}

func TestUpdateCheckIgnoresCachedLatest(t *testing.T) {
	stubLatestRelease(t, "99.0.0")
	writeLatestCache(String(), "") // cache says "you are on the latest"

	var out bytes.Buffer
	if err := Update(context.Background(), UpdateOptions{Check: true, Out: &out}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "update available: v99.0.0") {
		t.Fatalf("`update --check` trusted the cache; output: %q", out.String())
	}
}
