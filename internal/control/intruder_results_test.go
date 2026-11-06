package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A finished attack's results are retrievable in pages from /api/intruder/results.
func TestIntruderResultsEndpointPaginates(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	start := `{"target":"` + upstream.URL + `","template":"GET / HTTP/1.1\r\nHost: h\r\n\r\n","attackType":"repeat","repeat":6,"threads":1}`
	resp, err := http.Post(ts.URL+"/api/intruder/start", "application/json", strings.NewReader(start))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start status=%d", resp.StatusCode)
	}

	// Wait for the attack to finish.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sr, _ := http.Get(ts.URL + "/api/intruder/state")
		var st struct {
			Running bool `json:"running"`
			Done    int  `json:"done"`
			Total   int  `json:"total"`
		}
		json.NewDecoder(sr.Body).Decode(&st)
		sr.Body.Close()
		if !st.Running && st.Done == st.Total && st.Total == 6 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	var page struct {
		Results []map[string]any `json:"results"`
		Total   int              `json:"total"`
		Offset  int              `json:"offset"`
		Limit   int              `json:"limit"`
	}
	pr, err := http.Get(ts.URL + "/api/intruder/results?offset=2&limit=3")
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	defer pr.Body.Close()
	json.NewDecoder(pr.Body).Decode(&page)
	if page.Total != 6 {
		t.Fatalf("total = %d, want 6", page.Total)
	}
	if len(page.Results) != 3 {
		t.Fatalf("page len = %d, want 3", len(page.Results))
	}
	if page.Offset != 2 || page.Limit != 3 {
		t.Fatalf("offset/limit echoed wrong: %d/%d", page.Offset, page.Limit)
	}

	// Invalid limit is rejected.
	br, _ := http.Get(ts.URL + "/api/intruder/results?limit=0")
	br.Body.Close()
	if br.StatusCode != http.StatusBadRequest {
		t.Fatalf("limit=0 status=%d, want 400", br.StatusCode)
	}
}

// A finished run longer than the engine's live head must still be fully
// reachable: /state stays a bounded response (partialResults=true) and every
// attempt pages out of /results. A 2000-row cap in the state JSON used to make
// a longer run look truncated in the UI.
func TestIntruderStateStaysBoundedAndResultsServeTheRest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	const n = 2100 // past the engine's 2000-row live head
	start := `{"target":"` + upstream.URL + `","template":"GET / HTTP/1.1\r\nHost: h\r\n\r\n","attackType":"repeat","repeat":` +
		itoa(n) + `,"threads":8}`
	resp, err := http.Post(ts.URL+"/api/intruder/start", "application/json", strings.NewReader(start))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	resp.Body.Close()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		sr, _ := http.Get(ts.URL + "/api/intruder/state")
		var st struct {
			Running bool `json:"running"`
			Done    int  `json:"done"`
		}
		json.NewDecoder(sr.Body).Decode(&st)
		sr.Body.Close()
		if !st.Running && st.Done == n {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	sr, err := http.Get(ts.URL + "/api/intruder/state")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	var st struct {
		Running        bool  `json:"running"`
		Done           int   `json:"done"`
		Results        []any `json:"results"`
		PartialResults bool  `json:"partialResults"`
	}
	json.NewDecoder(sr.Body).Decode(&st)
	sr.Body.Close()
	if st.Done != n {
		t.Fatalf("done = %d, want %d", st.Done, n)
	}
	if len(st.Results) != 2000 {
		t.Fatalf("state results = %d, want the 2000-row head", len(st.Results))
	}
	if !st.PartialResults {
		t.Fatal("a bounded state head must report partialResults")
	}

	// The tail pages out of the results API.
	pr, err := http.Get(ts.URL + "/api/intruder/results?offset=2000&limit=5000")
	if err != nil {
		t.Fatalf("results: %v", err)
	}
	var page struct {
		Results []any  `json:"results"`
		Total   int    `json:"total"`
		RunID   string `json:"runId"`
		Stale   bool   `json:"stale"`
	}
	json.NewDecoder(pr.Body).Decode(&page)
	pr.Body.Close()
	if page.Total != n {
		t.Fatalf("results total = %d, want %d", page.Total, n)
	}
	if len(page.Results) != n-2000 {
		t.Fatalf("tail = %d rows, want %d", len(page.Results), n-2000)
	}
	if page.RunID == "" || page.Stale {
		t.Fatalf("a page must name its run: runId=%q stale=%v", page.RunID, page.Stale)
	}

	// A page tagged with another run's id must not mix rows from this one.
	sr2, err := http.Get(ts.URL + "/api/intruder/results?offset=0&limit=100&runId=someotherrun")
	if err != nil {
		t.Fatalf("stale results: %v", err)
	}
	var stale struct {
		Results []any  `json:"results"`
		RunID   string `json:"runId"`
		Stale   bool   `json:"stale"`
	}
	json.NewDecoder(sr2.Body).Decode(&stale)
	sr2.Body.Close()
	if !stale.Stale || len(stale.Results) != 0 {
		t.Fatalf("stale page = %d rows, stale=%v; want 0 rows flagged stale", len(stale.Results), stale.Stale)
	}
	if stale.RunID != page.RunID {
		t.Fatalf("stale page names run %q, want the current %q", stale.RunID, page.RunID)
	}
}

// An oversized payload list is a transport limit, not an attack limit: the 413
// must say so (and point at splitting the list), because "request body too
// large" reads like the Intruder refusing a long list.
func TestIntruderStartExplainsOversizedPayloadBody(t *testing.T) {
	old := maxRequestBody
	maxRequestBody = 512
	defer func() { maxRequestBody = old }()

	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body := `{"target":"http://example.com","template":"GET / HTTP/1.1\r\nHost: h\r\n\r\n","attackType":"sniper","threads":1,"payloads":[["` +
		strings.Repeat("p", 2048) + `"]]}`
	resp, err := http.Post(ts.URL+"/api/intruder/start", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	msg := string(raw)
	if !strings.Contains(msg, "no request cap") || !strings.Contains(msg, "split") {
		t.Fatalf("413 must explain the transport limit and the way forward: %s", msg)
	}
}
