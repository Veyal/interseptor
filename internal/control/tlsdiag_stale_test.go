package control

import (
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// A rejection captured hours ago must not keep tls_intercept blocked once a fresh
// HTTPS flow has been intercepted: a fixed environment that reads as broken hides
// the real regression next time.
func TestStaleTLSRejectionsDoNotBlockAfterAFreshSuccess(t *testing.T) {
	h, st, _ := newHub(t)

	old := time.Now().Add(-3 * time.Hour)
	if _, err := st.InsertFlow(&store.Flow{
		TS: old, Method: "CONNECT", Scheme: "https", Host: "api.example", Port: 443,
		Path: "", Status: 0, Flags: store.FlagTLSFailed,
	}); err != nil {
		t.Fatalf("insert failure: %v", err)
	}
	if _, err := st.InsertFlow(&store.Flow{
		TS: time.Now().Add(-time.Minute), Method: "GET", Scheme: "https", Host: "api.example", Port: 443,
		Path: "/me", Status: 200, Flags: 0, Mime: "application/json",
	}); err != nil {
		t.Fatalf("insert success: %v", err)
	}

	rep := (&authzAPI{h}).buildTLSDiagnosis("")
	if rep.Verdict != "ok" {
		t.Fatalf("verdict = %q (%s), want ok: a newer success means interception works", rep.Verdict, rep.Detail)
	}
	if rep.NewestFailureAge == "" {
		t.Fatal("the detail must still state the age of the last rejection")
	}
	if rep.NewestFailureTS == 0 || rep.NewestFailureTS > time.Now().Add(-2*time.Hour).UnixMilli() {
		t.Fatalf("newest failure ts = %d, want the old one", rep.NewestFailureTS)
	}
}

// A rejection newer than the last success is current, so it still blocks.
func TestFreshTLSRejectionStillBlocks(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.InsertFlow(&store.Flow{
		TS: time.Now().Add(-2 * time.Minute), Method: "GET", Scheme: "https", Host: "api.example", Port: 443,
		Path: "/me", Status: 200,
	}); err != nil {
		t.Fatalf("insert success: %v", err)
	}
	if _, err := st.InsertFlow(&store.Flow{
		TS: time.Now().Add(-time.Minute), Method: "CONNECT", Scheme: "https", Host: "api.example", Port: 443,
		Flags: store.FlagTLSFailed,
	}); err != nil {
		t.Fatalf("insert failure: %v", err)
	}
	if rep := (&authzAPI{h}).buildTLSDiagnosis(""); rep.Verdict != "tls_blocked" {
		t.Fatalf("verdict = %q, want tls_blocked", rep.Verdict)
	}
}
