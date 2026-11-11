package store

import (
	"database/sql"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The readiness check and the TLS diagnosis ask, on every call, whether an
// intercepted HTTPS flow exists and when the newest one was captured. Both
// must be answered from idx_flows_https_ok, never by reading the flows table:
// a project with no intercepted HTTPS otherwise pays a full scan per call.
func TestR2HTTPSSuccessReadsUseThePartialIndex(t *testing.T) {
	s := reviewOpen(t)
	for name, q := range map[string]string{
		"newest": newestHTTPSOKSQL,
		"capped": cappedHTTPSOKCountSQL,
		"exact":  exactHTTPSOKCountSQL,
	} {
		args := []any{}
		if strings.Contains(q, "?") {
			args = append(args, 10)
		}
		plan := explainPlan(t, s, q, args...)
		if !strings.Contains(plan, "idx_flows_https_ok") || strings.Contains(plan, "TEMP B-TREE") {
			t.Errorf("%s: plan does not stay on the HTTPS-success index:\n%s\n%s", name, q, plan)
		}
	}
}

// The index-backed reads must report exactly what a full scan reports, with
// and without a host filter, on a randomised capture.
func TestR2HTTPSSuccessStatsMatchAFullScan(t *testing.T) {
	s := reviewOpen(t)
	rng := rand.New(rand.NewSource(7))
	hosts := []string{"api.example.com", "API.Example.com", "cdn.example.net", "pinned.example.org"}
	schemes := []string{"https", "https", "http"}
	statuses := []int{0, 0, 200, 204, 404, 500}
	flagSets := []int64{0, 0, 0, FlagTLSFailed, FlagAI, FlagTLSFailed | FlagAI}
	base := int64(1_700_000_000_000)
	for i := 0; i < 600; i++ {
		reviewFlow(t, s, Flow{
			TS:     time.UnixMilli(base + int64(rng.Intn(100000))),
			Scheme: schemes[rng.Intn(len(schemes))],
			Host:   hosts[rng.Intn(len(hosts))],
			Status: statuses[rng.Intn(len(statuses))],
			Flags:  flagSets[rng.Intn(len(flagSets))],
		})
	}
	for _, host := range []string{"", "example.com", "PINNED", "cdn.", "nomatch"} {
		var wantN, wantNewest int64
		if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(ts),0) FROM flows NOT INDEXED
			WHERE scheme='https' AND status > 0 AND (flags & 16) = 0 AND (? = '' OR instr(lower(host), lower(?)) > 0)`,
			host, host).Scan(&wantN, &wantNewest); err != nil {
			t.Fatalf("brute force: %v", err)
		}
		if got, err := s.CountSuccessfulHTTPS(host); err != nil || got != wantN {
			t.Errorf("CountSuccessfulHTTPS(%q) = %d, %v; want %d", host, got, err, wantN)
		}
		for _, limit := range []int64{1, 5, 100000} {
			got, err := s.CountSuccessfulHTTPSUpTo(host, limit)
			if want := min(wantN, limit); err != nil || got != want {
				t.Errorf("CountSuccessfulHTTPSUpTo(%q, %d) = %d, %v; want %d", host, limit, got, err, want)
			}
		}
		if _, _, got, err := s.TLSFailureStats(host, 0); err != nil || got != wantNewest {
			t.Errorf("TLSFailureStats(%q) newest success = %d, %v; want %d", host, got, err, wantNewest)
		}
	}
}

// A flow becomes an interception success when its response status is written,
// and stops being one when a TLS failure is recorded on it or it is deleted.
func TestR2HTTPSSuccessIndexFollowsUpdatesAndDeletes(t *testing.T) {
	s := reviewOpen(t)
	f := &Flow{TS: time.UnixMilli(1_700_000_000_500), Method: "GET", Scheme: "https", Host: "api.example.com", Path: "/"}
	id, err := s.InsertFlow(f)
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	want := func(step string, n, newest int64) {
		t.Helper()
		got, err := s.CountSuccessfulHTTPSUpTo("", 10)
		_, _, gotNewest, err2 := s.TLSFailureStats("", 0)
		if err != nil || err2 != nil || got != n || gotNewest != newest {
			t.Fatalf("%s: count=%d newest=%d (%v, %v); want %d, %d", step, got, gotNewest, err, err2, n, newest)
		}
	}
	want("pending", 0, 0)
	f.Status = 200
	if err := s.UpdateFlow(f); err != nil {
		t.Fatalf("UpdateFlow: %v", err)
	}
	want("answered", 1, 1_700_000_000_500)
	f.Flags |= FlagTLSFailed
	if err := s.UpdateFlow(f); err != nil {
		t.Fatalf("UpdateFlow: %v", err)
	}
	want("failed", 0, 0)
	f.Flags = 0
	if err := s.UpdateFlow(f); err != nil {
		t.Fatalf("UpdateFlow: %v", err)
	}
	if _, err := s.DeleteFlows([]int64{id}); err != nil {
		t.Fatalf("DeleteFlows: %v", err)
	}
	want("deleted", 0, 0)
}

// A database written by an earlier release has no idx_flows_https_ok; opening
// it must add the index and index the rows already captured.
func TestR2HTTPSSuccessIndexIsAddedToAnExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	reviewFlow(t, s, Flow{TS: time.UnixMilli(1_700_000_000_900), Scheme: "https", Host: "api.example.com", Status: 200})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	db, err := sql.Open("sqlite", storeDSN(filepath.Join(dir, currentDBName)))
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := db.Exec(`DROP INDEX idx_flows_https_ok`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	db.Close()

	s, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	if n, err := s.CountSuccessfulHTTPSUpTo("", 10); err != nil || n != 1 {
		t.Fatalf("CountSuccessfulHTTPSUpTo after reopen = %d, %v; want 1", n, err)
	}
	if _, _, newest, err := s.TLSFailureStats("", 0); err != nil || newest != 1_700_000_000_900 {
		t.Fatalf("newest success after reopen = %d, %v", newest, err)
	}
}
