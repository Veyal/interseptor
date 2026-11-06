package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

// Editing a held response must keep the origin's headers and body as evidence,
// exactly as an edited held *request* does (OriginalReqHeaders/OriginalReqBodyHash).
// History's "original" view reads OriginalResHeaders/OriginalResBodyHash and
// falls back to the edited data when they are missing, so losing them silently
// rewrites captured evidence.
func TestInterceptEditedResponseKeepsOriginalEvidence(t *testing.T) {
	const originBody = "origin-response-body"
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Origin-Marker", "keep-me")
		io.WriteString(w, originBody)
	}))
	defer origin.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	eng := intercept.New()
	eng.SetResponseEnabled(true)
	srv := New(st, capture.New(st), nil, eng, nil)
	pl := httptest.NewServer(srv)
	defer pl.Close()
	pu, _ := url.Parse(pl.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}, Timeout: 10 * time.Second}

	type result struct {
		body string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := client.Get(origin.URL + "/thing")
		if err != nil {
			ch <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		ch <- result{body: string(b), err: err}
	}()

	var held intercept.HeldResponse
	deadline := time.Now().Add(5 * time.Second)
	for {
		if q := eng.ResponseQueue(); len(q) > 0 {
			held = q[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("response never held")
		}
		time.Sleep(5 * time.Millisecond)
	}
	raw := string(held.Raw)
	head, _, ok := strings.Cut(raw, "\r\n\r\n")
	if !ok {
		t.Fatalf("held raw has no head/body boundary: %q", raw)
	}
	if err := eng.ForwardResponse(held.ID, []byte(head+"\r\n\r\n"+"EDITED-BODY")); err != nil {
		t.Fatalf("forward edited response: %v", err)
	}
	got := <-ch
	if got.err != nil {
		t.Fatalf("client: %v", got.err)
	}
	if got.body != "EDITED-BODY" {
		t.Fatalf("client body = %q, want the edit", got.body)
	}

	flows := waitFlows(t, st, 1)
	f := flows[0]
	if f.Flags&store.FlagResponseEdited == 0 {
		t.Fatalf("flow not flagged as response-edited: flags=%v", f.Flags)
	}
	if http.Header(f.OriginalResHeaders).Get("X-Origin-Marker") != "keep-me" {
		t.Fatalf("original response headers lost: %v", f.OriginalResHeaders)
	}
	if f.OriginalResBodyHash == "" {
		t.Fatal("original response body hash lost — History's original view will show the edited body")
	}
	if f.OriginalResBodyHash == f.ResBodyHash {
		t.Fatalf("original body hash equals the edited one (%q)", f.ResBodyHash)
	}
	rc, err := st.OpenBody(f.OriginalResBodyHash)
	if err != nil {
		t.Fatalf("open original body: %v", err)
	}
	defer rc.Close()
	orig, _ := io.ReadAll(rc)
	if string(orig) != originBody {
		t.Fatalf("original body = %q, want %q", orig, originBody)
	}
}
