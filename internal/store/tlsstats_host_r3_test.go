package store

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// The host-filtered TLS reads return what a plain scan of flows returns, for
// substrings that match no host, one host, a pinned host with only failures,
// many hosts, and with both plans (host index and success index) exercised.
func TestR3HostFilteredTLSStatsMatchAFullScan(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rng := rand.New(rand.NewSource(11))
	hosts := []string{"api.example.com", "API.Example.com", "cdn.example.com", "pinned.example.org", "login.example.org", "", "big.example.net"}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ex := &txStmts{tx: tx}
	const n = 6000
	for i := 0; i < n; i++ {
		host := hosts[rng.Intn(len(hosts)-1)]
		if i%2 == 0 {
			host = "big.example.net" // holds more flows than the host-index plan allows below
		}
		f := &Flow{TS: time.UnixMilli(int64(1_700_000_000_000 + rng.Intn(1_000_000))), Method: "GET",
			Scheme: []string{"https", "https", "http"}[rng.Intn(3)], Host: host, Port: 443,
			Path: fmt.Sprintf("/p/%d", i), Status: []int{0, 200, 404}[rng.Intn(3)]}
		if host == "pinned.example.org" || rng.Intn(15) == 0 {
			f.Flags, f.Status = FlagTLSFailed, 0
		}
		if err := insertFlowRowExec(ex, f, nil, int64(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s.noteFlowID(n)
	if _, err := s.db.Exec(`UPDATE flows SET host = NULL WHERE id % 97 = 0`); err != nil {
		t.Fatal(err)
	}

	// big.example.net holds half the flows: over the bound, so it keeps the
	// walk of the success index; every other host goes through the host index.
	defer func(old int64) { httpsOKHostFlowsMax = old }(httpsOKHostFlowsMax)
	httpsOKHostFlowsMax = 1500
	for sub, wantHostIndex := range map[string]bool{"pinned": true, "cdn": true, "big": false, "example": false} {
		from, _, _, err := s.httpsOKHostSource(sub)
		if err != nil || strings.Contains(from, "idx_flows_host") != wantHostIndex {
			t.Fatalf("%q: source %q (err %v), want host-index plan = %v", sub, from, err, wantHostIndex)
		}
	}

	const scanOK = `scheme = 'https' AND +status > 0 AND (flags & 16) = 0 AND instr(lower(host), lower(?)) > 0`
	for _, sub := range []string{"nosuchhost", "pinned", "PINNED.example", "api", "example", "example.org", "big", ".", "cdn.example.com", "org"} {
		var wantCount, wantNewest, wantFailNewest, wantFailRecent int64
		if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(ts),0) FROM flows NOT INDEXED WHERE `+scanOK, sub).Scan(&wantCount, &wantNewest); err != nil {
			t.Fatal(err)
		}
		since := int64(1_700_000_500_000)
		if err := s.db.QueryRow(`SELECT COALESCE(MAX(ts),0), COALESCE(SUM(ts >= ?),0) FROM flows NOT INDEXED
			WHERE (flags & 16) != 0 AND instr(lower(host), lower(?)) > 0`, since, sub).Scan(&wantFailNewest, &wantFailRecent); err != nil {
			t.Fatal(err)
		}
		_, _, none, err := s.httpsOKHostSource(sub)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.CountSuccessfulHTTPS(sub)
		if err != nil || got != wantCount {
			t.Fatalf("%q: CountSuccessfulHTTPS = %d, %v; want %d", sub, got, err, wantCount)
		}
		for _, limit := range []int64{1, 7, 100000} {
			got, err := s.CountSuccessfulHTTPSUpTo(sub, limit)
			if err != nil || got != min(wantCount, limit) {
				t.Fatalf("%q: CountSuccessfulHTTPSUpTo(%d) = %d, %v; want %d", sub, limit, got, err, min(wantCount, limit))
			}
		}
		failNewest, failRecent, okNewest, err := s.TLSFailureStats(sub, since)
		if err != nil || failNewest != wantFailNewest || failRecent != wantFailRecent || okNewest != wantNewest {
			t.Fatalf("%q: TLSFailureStats = %d, %d, %d, %v; want %d, %d, %d", sub, failNewest, failRecent, okNewest, err, wantFailNewest, wantFailRecent, wantNewest)
		}
		if sub == "nosuchhost" && !none {
			t.Fatal("a substring matching no host was not short-circuited")
		}
	}
}
