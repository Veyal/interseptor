package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/intercept"
	"github.com/Veyal/interceptor/internal/store"
)

func TestTLSDiagnosisEndpoint(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	st.InsertFlow(&store.Flow{
		TS: time.Now(), Method: "CONNECT", Scheme: "https", Host: "api.bank.com", Port: 443,
		Path: "(tls handshake)", Error: "tls: client closed during handshake",
		Flags: store.FlagTLSFailed,
	})

	hub := New(st, intercept.New(), nil, nil, nil)
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/tls-diagnosis")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var rep tlsDiagnosisReport
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.Verdict != "tls_blocked" {
		t.Fatalf("verdict %q, want tls_blocked", rep.Verdict)
	}
	if rep.TLSFailureCount != 1 {
		t.Fatalf("tlsFailureCount %d, want 1", rep.TLSFailureCount)
	}
}

func TestReadinessIncludesTLSIntercept(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	st.InsertFlow(&store.Flow{
		TS: time.Now(), Method: "CONNECT", Scheme: "https", Host: "x.com", Port: 443,
		Flags: store.FlagTLSFailed, Error: "tls fail",
	})

	hub := New(st, intercept.New(), nil, nil, nil)
	ts := httptest.NewServer(hub.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/readiness")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var rep readinessReport
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, c := range rep.Checks {
		if c.ID == "tls_intercept" {
			found = true
			if c.OK {
				t.Fatal("tls_intercept should be not OK when failures exist")
			}
		}
	}
	if !found {
		t.Fatal("readiness missing tls_intercept check")
	}
}
