package control

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// Every seeded route must answer a malformed body with a 4xx, never a panic or
// a 500, and must not expose the wrong method. This swept the routes once by
// hand; keeping it catches a new handler that forgets to decode defensively.
func TestControlRoutesRejectMalformedBodiesWithout5xx(t *testing.T) {
	h, st, _ := newHub(t)
	reqHash := putTestBody(t, st, []byte(`{"a":1}`))
	resHash := putTestBody(t, st, []byte(`{"b":2}`))
	flowID, err := st.InsertFlow(&store.Flow{
		TS: time.Now(), Method: "GET", Scheme: "https", Host: "example.com", Path: "/api/x?y=1",
		Status: 200, ReqHeaders: map[string][]string{"Accept": {"*/*"}},
		ResHeaders:  map[string][]string{"Content-Type": {"application/json"}},
		ReqBodyHash: reqHash, ResBodyHash: resHash, ReqLen: 7, ResLen: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	findID, err := st.CreateFinding(&store.Finding{Title: "T", Severity: "medium", Status: "confirmed", Target: "example.com", Detail: "d"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("flowID=%d findingID=%d", flowID, findID)

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	paths := []string{
		fmt.Sprintf("/api/flows/%d", flowID),
		fmt.Sprintf("/api/flows/%d/raw", flowID),
		fmt.Sprintf("/api/flows/%d/body", flowID),
		fmt.Sprintf("/api/flows/%d/decoded", flowID),
		fmt.Sprintf("/api/flows/%d/analyze", flowID),
		fmt.Sprintf("/api/flows/%d/curl", flowID),
		fmt.Sprintf("/api/flows/%d/replay", flowID),
		fmt.Sprintf("/api/flows/%d/ws", flowID),
		fmt.Sprintf("/api/flows/%d/auth-timeline", flowID),
		fmt.Sprintf("/api/replay/%d", flowID),
		fmt.Sprintf("/api/findings/%d", findID),
		fmt.Sprintf("/api/findings/%d/retest", findID),
		fmt.Sprintf("/api/findings/%d/flow-preview", findID),
		fmt.Sprintf("/api/findings/%d/evidence-render", findID),
		fmt.Sprintf("/api/findings/%d/images/aaaa", findID),
		fmt.Sprintf("/api/finding-revisions/%d", findID),
		fmt.Sprintf("/api/finding-quality/%d", findID),
		fmt.Sprintf("/api/finding-export/%d", findID),
		fmt.Sprintf("/api/finding-revisions/%d/1/restore", findID),
		fmt.Sprintf("/api/findings/%d/normalize-targets", findID),
		fmt.Sprintf("/api/authz/flow-auth/%d", flowID),
	}
	bodies := []string{``, `{}`, `null`, `[]`, `"x"`, `123`, `{"flowId":"abc"}`, `{"flowIds":[-1]}`,
		`{"side":"nope"}`, `{"mask":123}`, `{"position":-99}`, `{"width":99999}`, `{"kind":"nope"}`}
	var bad []string
	for _, p := range paths {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			for _, b := range bodies {
				var rd *bytes.Reader
				if b == "" {
					rd = bytes.NewReader(nil)
				} else {
					rd = bytes.NewReader([]byte(b))
				}
				req, _ := http.NewRequest(method, ts.URL+p, rd)
				req.Header.Set("Content-Type", "application/json")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("%s %s: %v", method, p, err)
				}
				resp.Body.Close()
				if resp.StatusCode >= 500 || resp.StatusCode == http.StatusMethodNotAllowed {
					bad = append(bad, fmt.Sprintf("%s %s body=%q -> %d", method, p, b, resp.StatusCode))
				}
			}
		}
	}
	for _, b := range bad {
		t.Log("BAD:", b)
	}
	if len(bad) > 0 {
		t.Errorf("%d seeded probes returned 5xx/405", len(bad))
	}
}
