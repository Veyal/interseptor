package collrun

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/capture"
	"github.com/Veyal/interseptor/internal/sender"
)

// cookieProject: /login sets a session cookie, /me answers 200 only with it.
func cookieProject(t *testing.T) (*e2e, *atomic.Int32) {
	x := newE2E(t)
	var meOK atomic.Int32
	x.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "session-cookie-12345", Path: "/", HttpOnly: true})
			w.WriteHeader(200)
		case "/me":
			if c, err := r.Cookie("sid"); err == nil && c.Value == "session-cookie-12345" {
				meOK.Add(1)
				w.WriteHeader(200)
				return
			}
			w.WriteHeader(401)
		}
	})
	return x, &meOK
}

func (x *e2e) freshBackend() *StoreBackend {
	snd := sender.New(x.st, capture.New(x.st))
	host := strings.TrimPrefix(x.srv.URL, "http://")
	host = host[:strings.LastIndex(host, ":")]
	return NewStoreBackend(StoreConfig{Store: x.st, Sender: snd, Scope: allowScope{map[string]bool{host: true}}})
}

// Cookies follow the persist policy like variable writes: keep writes the jar
// to the project (a new backend, i.e. a restart, sees it); discard leaves no
// trace, in the store or in the live jar.
func TestRunCookiesFollowPersistPolicy(t *testing.T) {
	for _, mode := range []string{PersistKeep, PersistDiscard} {
		t.Run(mode, func(t *testing.T) {
			x, meOK := cookieProject(t)
			x.add("login", "GET", "/login", nil)
			b := x.freshBackend()
			rep, err := New(b, x.st).Run(context.Background(), Options{CollectionUID: x.coll.UID, EnvUID: x.env.UID, Persist: mode})
			if err != nil || rep.Items[0].Outcome != "sent" {
				t.Fatalf("login run: %v %+v", err, rep)
			}
			stored, _ := x.st.ListCookies(partitionKey(x.coll.UID, x.env.UID, ""))
			wantStored := mode == PersistKeep
			if (len(stored) == 1) != wantStored {
				t.Fatalf("mode %s: stored cookies = %+v", mode, stored)
			}
			// Same live backend: discard must also have reverted the in-memory jar.
			x.add("me", "GET", "/me", nil)
			rep, _ = New(b, x.st).Run(context.Background(), Options{CollectionUID: x.coll.UID, EnvUID: x.env.UID, ItemUIDs: []string{x.lastItem(t, "me")}})
			gotCookie := meOK.Load() == 1
			if gotCookie != wantStored {
				t.Fatalf("mode %s: /me authenticated = %v", mode, gotCookie)
			}
			// A new backend (restart) hydrates from the store.
			meOK.Store(0)
			rep, _ = New(x.freshBackend(), x.st).Run(context.Background(), Options{CollectionUID: x.coll.UID, EnvUID: x.env.UID, ItemUIDs: []string{x.lastItem(t, "me")}})
			if (meOK.Load() == 1) != wantStored {
				t.Fatalf("mode %s after restart: /me authenticated = %v", mode, meOK.Load() == 1)
			}
		})
	}
}

func (x *e2e) lastItem(t *testing.T, name string) string {
	items, _ := x.st.ListItems(x.coll.UID)
	for _, it := range items {
		if it.Name == name {
			return it.UID
		}
	}
	t.Fatalf("no item %q", name)
	return ""
}
