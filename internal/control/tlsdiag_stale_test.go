package control

import (
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/intercept"
	"github.com/Veyal/interseptor/internal/store"
)

// tlsFixture stores one TLS rejection and, optionally, one intercepted HTTPS
// flow, at the given ages.
func tlsFixture(t *testing.T, failureAge, successAge time.Duration) *tlsDiagnosisReport {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	st.InsertFlow(&store.Flow{
		TS: time.Now().Add(-failureAge), Method: "CONNECT", Scheme: "https", Host: "api.bank.example", Port: 443,
		Path: "(tls handshake)", Error: "tls: client closed during handshake", Flags: store.FlagTLSFailed,
	})
	if successAge >= 0 {
		st.InsertFlow(&store.Flow{
			TS: time.Now().Add(-successAge), Method: "GET", Scheme: "https", Host: "api.bank.example", Port: 443,
			Path: "/v1/me", Status: 200,
		})
	}
	hub := New(st, intercept.New(), nil, nil, nil)
	rep := (&authzAPI{hub}).buildTLSDiagnosis("")
	return &rep
}

// A pinning bypass that worked leaves old rejections behind. They must not keep
// the check red once fresh traffic is being intercepted.
func TestStaleTLSFailureDoesNotBlockWhenInterceptionWorks(t *testing.T) {
	rep := tlsFixture(t, 3*time.Hour, time.Minute)
	if rep.Verdict != "ok" {
		t.Fatalf("verdict = %q, want ok: %s", rep.Verdict, rep.Detail)
	}
	if rep.RecentFailureCount != 0 {
		t.Fatalf("recentFailureCount = %d, want 0", rep.RecentFailureCount)
	}
	if rep.NewestFailureAge == "" || !strings.Contains(rep.Detail, "newest "+rep.NewestFailureAge+" ago") {
		t.Fatalf("detail must state the newest failure's age: %q", rep.Detail)
	}
	if !strings.Contains(rep.NewestFailureAge, "3h") {
		t.Fatalf("age = %q, want it to describe 3 hours", rep.NewestFailureAge)
	}
}

func TestFreshTLSFailureStillBlocks(t *testing.T) {
	rep := tlsFixture(t, 30*time.Second, time.Minute)
	if rep.Verdict != "tls_blocked" {
		t.Fatalf("verdict = %q, want tls_blocked: %s", rep.Verdict, rep.Detail)
	}
	if rep.RecentFailureCount != 1 {
		t.Fatalf("recentFailureCount = %d, want 1", rep.RecentFailureCount)
	}
}

func TestStaleTLSFailureWithoutAnySuccessStillBlocks(t *testing.T) {
	rep := tlsFixture(t, 3*time.Hour, -1)
	if rep.Verdict != "tls_blocked" {
		t.Fatalf("verdict = %q, want tls_blocked when nothing was ever intercepted", rep.Verdict)
	}
	if !strings.Contains(rep.Detail, "no HTTPS flow has been intercepted yet") {
		t.Fatalf("detail should say interception never worked: %q", rep.Detail)
	}
}

// The readiness row follows the verdict, so a fixed environment reports ready.
func TestReadinessTLSCheckClearsAfterStaleFailures(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	st.InsertFlow(&store.Flow{TS: time.Now().Add(-4 * time.Hour), Method: "CONNECT", Scheme: "https", Host: "x.example", Port: 443, Flags: store.FlagTLSFailed, Error: "tls fail"})
	st.InsertFlow(&store.Flow{TS: time.Now(), Method: "GET", Scheme: "https", Host: "x.example", Port: 443, Path: "/", Status: 200})
	if _, err := st.CreateScopeRule(&store.ScopeRule{Action: "include", Host: "x.example", Enabled: true}); err != nil {
		t.Fatalf("scope: %v", err)
	}

	hub := New(st, intercept.New(), nil, nil, nil)
	rep := (&authzAPI{hub}).buildReadiness()
	for _, c := range rep.Checks {
		if c.ID != "tls_intercept" {
			continue
		}
		if !c.OK {
			t.Fatalf("tls_intercept should be ok with stale failures and fresh success: %+v", c)
		}
		if !strings.Contains(c.Detail, "older TLS rejection") {
			t.Fatalf("detail should distinguish historical rejections: %q", c.Detail)
		}
		return
	}
	t.Fatal("tls_intercept row missing")
}

func TestHumanAge(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{12 * time.Minute, "12m"},
		{3*time.Hour + 12*time.Minute, "3h12m"},
		{50 * time.Hour, "2d02h"},
		{-time.Minute, "0s"},
	} {
		if got := humanAge(tc.d); got != tc.want {
			t.Errorf("humanAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
