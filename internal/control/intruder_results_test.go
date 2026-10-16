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
