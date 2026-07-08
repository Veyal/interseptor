package version

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func TestCheckLatestGitHubHeaders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GITHUB_TOKEN", "test-token")
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/repos/"+Repo+"/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v9.9.9"})
	}))
	defer srv.Close()

	old := githubAPIRoot
	t.Cleanup(func() { githubAPIRoot = old })
	githubAPIRoot = srv.URL + "/repos/" + Repo

	latest, newer, err := CheckLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if latest != "9.9.9" || !newer {
		t.Fatalf("latest=%q newer=%v", latest, newer)
	}
	if !strings.HasPrefix(ua, "interseptor/") {
		t.Fatalf("User-Agent = %q, want interseptor/… prefix", ua)
	}
}

func TestCheckLatestGitHub403(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("INTERSEPTOR_GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer api.Close()

	web := httptest.NewServer(nil)
	web.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.Redirect(w, r, web.URL+"/"+Repo+"/releases/tag/v8.8.8", http.StatusFound)
			return
		}
		if strings.Contains(r.URL.Path, "/releases/tag/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	})
	defer web.Close()

	oldAPI := githubAPIRoot
	oldWeb := githubReleasesLatest
	t.Cleanup(func() {
		githubAPIRoot = oldAPI
		githubReleasesLatest = oldWeb
	})
	githubAPIRoot = api.URL + "/repos/" + Repo
	githubReleasesLatest = web.URL + "/" + Repo + "/releases/latest"

	latest, newer, err := CheckLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if latest != "8.8.8" || !newer {
		t.Fatalf("fallback latest=%q newer=%v", latest, newer)
	}
}

func TestCheckLatestRedirect(t *testing.T) {
	srv := httptest.NewServer(nil)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.Redirect(w, r, srv.URL+"/"+Repo+"/releases/tag/v7.1.0", http.StatusFound)
			return
		}
		if strings.Contains(r.URL.Path, "/releases/tag/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	})
	defer srv.Close()

	old := githubReleasesLatest
	t.Cleanup(func() { githubReleasesLatest = old })
	githubReleasesLatest = srv.URL + "/" + Repo + "/releases/latest"

	got, err := checkLatestRedirect(context.Background())
	if err != nil || got != "7.1.0" {
		t.Fatalf("checkLatestRedirect = %q err=%v", got, err)
	}
}

func TestCheckLatestSkipsAPIWithoutToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("INTERSEPTOR_GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	apiHits := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits++
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0"})
	}))
	defer api.Close()

	web := httptest.NewServer(nil)
	web.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.Redirect(w, r, web.URL+"/"+Repo+"/releases/tag/v2.0.0", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	defer web.Close()

	oldAPI := githubAPIRoot
	oldWeb := githubReleasesLatest
	t.Cleanup(func() {
		githubAPIRoot = oldAPI
		githubReleasesLatest = oldWeb
	})
	githubAPIRoot = api.URL + "/repos/" + Repo
	githubReleasesLatest = web.URL + "/" + Repo + "/releases/latest"

	latest, _, err := CheckLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if latest != "2.0.0" {
		t.Fatalf("latest=%q", latest)
	}
	if apiHits != 0 {
		t.Fatalf("expected no API calls without token, got %d", apiHits)
	}
}

func TestFetchReleaseAPI403Fallback(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	tag := "v1.2.3"
	osToken, archToken := platformTokens(runtime.GOOS, runtime.GOARCH)
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	asset := fmt.Sprintf("interseptor_1.2.3_%s_%s%s", osToken, archToken, ext)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Header().Set("Retry-After", "60")
	}))
	defer api.Close()

	dl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Header.Get("Range") != "" || r.Method == http.MethodGet {
			if strings.HasSuffix(r.URL.Path, asset) {
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		http.NotFound(w, r)
	}))
	defer dl.Close()

	oldAPI := githubAPIRoot
	oldDL := githubReleasesDownload
	t.Cleanup(func() {
		githubAPIRoot = oldAPI
		githubReleasesDownload = oldDL
	})
	githubAPIRoot = api.URL + "/repos/" + Repo
	githubReleasesDownload = dl.URL

	rel, err := fetchRelease(context.Background(), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != tag {
		t.Fatalf("tag=%q", rel.Tag)
	}
	name, url := pickAssetFor(rel, "1.2.3", runtime.GOOS, runtime.GOARCH)
	if name != asset || !strings.Contains(url, asset) {
		t.Fatalf("pickAsset=%q %q", name, url)
	}
}

func TestRetryAfterHint(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"90"}},
	}
	if got := retryAfterHint(resp); got != "90s" {
		t.Fatalf("retry-after=%q", got)
	}
}
