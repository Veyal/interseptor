package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// C5: the peer URL is caller-supplied and fetched server-side with a
// caller-supplied key. Nothing stopped it from naming this instance's own control
// listener, so a pull could make the control API call itself.
func TestMergePullRefusesOwnListener(t *testing.T) {
	h, _, _ := newHub(t)
	var selfExports atomic.Int32
	inner := h.Handler()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/export/full" {
			selfExports.Add(1)
		}
		inner.ServeHTTP(w, r)
	}))
	defer ts.Close()
	h.SetSelfAddr(strings.TrimPrefix(ts.URL, "http://"))

	for _, peer := range []string{ts.URL, strings.Replace(ts.URL, "127.0.0.1", "localhost", 1)} {
		resp, err := http.Post(ts.URL+"/api/merge/pull", "application/json", strings.NewReader(`{"peerUrl":"`+peer+`","key":"k"}`))
		if err != nil {
			t.Fatal(err)
		}
		body := readAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "this Interseptor instance") {
			t.Fatalf("pull from own listener %s: status %d %s, want a 400 refusal", peer, resp.StatusCode, body)
		}
	}
	if n := selfExports.Load(); n != 0 {
		t.Fatalf("the control API fetched its own /api/export/full %d time(s)", n)
	}
}

// A peer that redirects the fetch back to this instance is refused too.
func TestMergePullRefusesRedirectToOwnListener(t *testing.T) {
	h, _, _ := newHub(t)
	var selfExports atomic.Int32
	inner := h.Handler()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/export/full" {
			selfExports.Add(1)
		}
		inner.ServeHTTP(w, r)
	}))
	defer ts.Close()
	h.SetSelfAddr(strings.TrimPrefix(ts.URL, "http://"))
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, ts.URL+"/api/export/full", http.StatusFound)
	}))
	defer peer.Close()

	resp, err := http.Post(ts.URL+"/api/merge/pull", "application/json", strings.NewReader(`{"peerUrl":"`+peer.URL+`","key":"k"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK || selfExports.Load() != 0 {
		t.Fatalf("redirect back to own listener: status %d, self fetches %d", resp.StatusCode, selfExports.Load())
	}
}

// Only http/https peers are fetched.
func TestMergePullRejectsNonHTTPPeerURL(t *testing.T) {
	h, _, _ := newHub(t)
	for _, peer := range []string{"file:///etc/hosts", "gopher://peer.example.com/", "peer.example.com"} {
		rec := httptest.NewRecorder()
		h.mergePull(rec, httptest.NewRequest(http.MethodPost, "/api/merge/pull", strings.NewReader(`{"peerUrl":"`+peer+`","key":"k"}`)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("peerUrl %q: status %d %s, want 400", peer, rec.Code, rec.Body.String())
		}
	}
}

// The peer fetch ignored the request context: after the caller went away the
// handler kept waiting on the peer (up to the 10-minute client timeout).
func TestMergePullStopsWhenCallerGoesAway(t *testing.T) {
	h, _, _ := newHub(t)
	release := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select { // a peer that never answers
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer peer.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/merge/pull", strings.NewReader(`{"peerUrl":"`+peer.URL+`","key":"k"}`)).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.mergePull(httptest.NewRecorder(), req)
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("mergePull kept waiting on the peer 3s after its caller's context was cancelled")
	}
}

// vault fetches share the guard.
func TestVaultListRefusesOwnListener(t *testing.T) {
	h, _, _ := newHub(t)
	h.GlobalDir = t.TempDir()
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	h.SetSelfAddr(strings.TrimPrefix(ts.URL, "http://"))
	if err := h.saveVaultClient(vaultClientConfig{URL: ts.URL, Key: "k"}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.vaultList(rec, httptest.NewRequest(http.MethodGet, "/api/vault/remote", nil))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "this Interseptor instance") {
		t.Fatalf("vault list against own listener: %d %s", rec.Code, rec.Body.String())
	}
}
