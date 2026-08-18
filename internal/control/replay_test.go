package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

func TestReplayPageClassifiesLookupFailures(t *testing.T) {
	h, st, _ := newHub(t)
	api := &toolsAPI{h}

	missingReq := httptest.NewRequest(http.MethodGet, "/replay/1", nil)
	missingReq.SetPathValue("id", "1")
	missingRec := httptest.NewRecorder()
	api.replayPage(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d, want 404", missingRec.Code)
	}

	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	failureReq := httptest.NewRequest(http.MethodGet, "/replay/1", nil)
	failureReq.SetPathValue("id", "1")
	failureRec := httptest.NewRecorder()
	api.replayPage(failureRec, failureReq)
	if failureRec.Code != http.StatusInternalServerError || !bytes.Contains(failureRec.Body.Bytes(), []byte("internal server error")) {
		t.Fatalf("storage failure status=%d body=%q, want scrubbed 500", failureRec.Code, failureRec.Body.String())
	}
}

// A replay link re-sends a captured flow's request. session="flow" replays it
// exactly as captured; session="current" lets the configured session headers
// override the captured ones.
func TestReplayFlowSessionModes(t *testing.T) {
	type rec struct {
		method, path, body, auth string
	}
	var mu sync.Mutex
	var got []rec
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, rec{r.Method, r.URL.Path, string(b), r.Header.Get("X-Auth")})
		mu.Unlock()
		io.WriteString(w, "ok")
	}))
	defer target.Close()

	h, s, _ := newHub(t)
	// Session headers only apply to in-session-scope targets (the cross-domain
	// leak guard); allow all here so the current-session override can engage.
	h.snd.SetSessionScope(func(string, string, int, string) bool { return true })

	bw, err := s.NewBodyWriter()
	if err != nil {
		t.Fatalf("NewBodyWriter: %v", err)
	}
	bw.Write([]byte("payload=1"))
	bh, _, err := bw.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	u, _ := url.Parse(target.URL)
	port, _ := strconv.Atoi(u.Port())
	id, err := s.InsertFlow(&store.Flow{
		Method: "POST", Host: u.Hostname(), Path: "/submit", Scheme: "http", Port: port,
		ReqHeaders:  map[string][]string{"X-Auth": {"orig"}},
		ReqBodyHash: bh, Status: 200,
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	replay := func(session string) {
		body, _ := json.Marshal(map[string]string{"session": session})
		resp, err := http.Post(fmt.Sprintf("%s/api/flows/%d/replay", ts.URL, id), "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("replay %s: %v", session, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("replay %s: status %d", session, resp.StatusCode)
		}
	}

	replay("flow")
	h.snd.SetSession(true, []sender.Header{{Key: "X-Auth", Value: "current"}})
	replay("current")

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("target received %d requests, want 2", len(got))
	}
	for i, g := range got {
		if g.method != "POST" || g.path != "/submit" || g.body != "payload=1" {
			t.Fatalf("replay %d reconstructed wrong: %+v", i, g)
		}
	}
	if got[0].auth != "orig" {
		t.Fatalf("flow-session replay must send the captured X-Auth, got %q", got[0].auth)
	}
	if got[1].auth != "current" {
		t.Fatalf("current-session replay must override X-Auth with the session value, got %q", got[1].auth)
	}
}

func TestReplayFlowRejectsInvalidOptionsBeforeSending(t *testing.T) {
	var mu sync.Mutex
	var hits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		io.WriteString(w, "ok")
	}))
	defer target.Close()

	h, s, _ := newHub(t)
	u, _ := url.Parse(target.URL)
	port, _ := strconv.Atoi(u.Port())
	id, err := s.InsertFlow(&store.Flow{
		Method: "GET", Host: u.Hostname(), Path: "/replay-target", Scheme: "http", Port: port,
		Status: 200,
	})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	for _, tc := range []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "malformed JSON", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "unknown session", body: `{"session":"currnet"}`, wantStatus: http.StatusBadRequest},
		{name: "trailing JSON", body: `{"session":"flow"}{}`, wantStatus: http.StatusBadRequest},
		{name: "oversized JSON", body: `{"session":"flow","padding":"` + string(bytes.Repeat([]byte("x"), maxReplayRequestBytes)) + `"}`, wantStatus: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Post(
				fmt.Sprintf("%s/api/flows/%d/replay", ts.URL, id),
				"application/json",
				bytes.NewBufferString(tc.body),
			)
			if err != nil {
				t.Fatalf("POST replay: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
		})
	}

	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatalf("invalid replay options sent %d requests, want 0", hits)
	}
}

// The confirm page (GET /replay/{id}) is a safe, side-effect-free preview: it
// renders without sending anything and references the flow.
func TestReplayPageIsSideEffectFree(t *testing.T) {
	var hits int
	var mu sync.Mutex
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
	}))
	defer target.Close()

	h, s, _ := newHub(t)
	u, _ := url.Parse(target.URL)
	port, _ := strconv.Atoi(u.Port())
	id, _ := s.InsertFlow(&store.Flow{Method: "POST", Host: u.Hostname(), Path: "/x", Scheme: "http", Port: port, Status: 200})

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(fmt.Sprintf("%s/replay/%d?session=flow", ts.URL, id))
	if err != nil {
		t.Fatalf("GET replay page: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replay page status %d", resp.StatusCode)
	}
	page, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(page, []byte("/api/flows/"+strconv.FormatInt(id, 10)+"/replay")) {
		t.Fatalf("replay page does not reference the replay endpoint")
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatalf("GET replay page must NOT send the request; target got %d hits", hits)
	}
}
