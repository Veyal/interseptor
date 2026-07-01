package version

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckLatestGitHubHeaders(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
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
	if !strings.HasPrefix(ua, "interceptor/") {
		t.Fatalf("User-Agent = %q, want interceptor/… prefix", ua)
	}
}

func TestCheckLatestGitHub403(t *testing.T) {
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
