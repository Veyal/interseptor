package collrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// OAuth2 through the shared backend: the token is fetched once, stored per
// collection (local only), reused by a fresh backend (a restart) and the
// stored form never reaches a scrubbed export.
func TestStoreBackendOAuth2TokenIsPersistedAndReused(t *testing.T) {
	x := newE2E(t)
	var tokenCalls atomic.Int32
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "canary-access-AAAA1111BBBB", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer idp.Close()
	if _, err := x.st.UpdateCollection(store.Collection{UID: x.coll.UID, Name: x.coll.Name, Rev: x.coll.Rev, Caps: x.coll.Caps,
		Auth: js(map[string]any{"type": "oauth2", "oauth2": []map[string]any{
			{"key": "grant_type", "value": "client_credentials"}, {"key": "accessTokenUrl", "value": idp.URL + "/token"},
			{"key": "clientId", "value": "cid"}, {"key": "clientSecret", "value": "csecret-value-123456"}}})}); err != nil {
		t.Fatal(err)
	}
	var sawAuth atomic.Value
	x.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(200)
	})
	x.add("data", "GET", "/data", nil)

	run := func() *Report {
		b := x.backend()
		rep, err := New(b, x.st).Run(context.Background(), Options{CollectionUID: x.coll.UID, EnvUID: x.env.UID})
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	rep := run()
	if rep.Items[0].Outcome != "sent" || sawAuth.Load() != "Bearer canary-access-AAAA1111BBBB" {
		t.Fatalf("first run: %+v auth=%v", rep.Items[0], sawAuth.Load())
	}
	run() // a new backend (as after a restart) reuses the persisted token
	if tokenCalls.Load() != 1 {
		t.Fatalf("token endpoint called %d times, want 1", tokenCalls.Load())
	}
	toks, _ := x.st.ListTokens(x.coll.UID)
	if len(toks) != 1 {
		t.Fatalf("stored tokens = %d", len(toks))
	}
	snap := t.TempDir() + "/s.db"
	if _, err := x.st.BackupToScrubbed(snap, store.ScrubOptions{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := readFile(snap)
	if strings.Contains(string(raw), "csecret-value-123456") {
		t.Fatal("the OAuth2 client secret survived the scrubbed snapshot")
	}
	// Captured flows are evidence and keep their wire bytes; only the token
	// cache rows must be gone.
	db, err := sql.Open("sqlite", snap)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ix_tokens`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("token cache rows in scrubbed snapshot = %d (%v)", n, err)
	}
}

func readFile(p string) ([]byte, error) { return os.ReadFile(p) }
