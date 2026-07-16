package store

import (
	"testing"
	"time"
)

func TestDeleteFlowsOlderThan(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seed := func(host string, tsMillis int64) {
		if _, err := st.InsertFlow(&Flow{Method: "GET", Host: host, Path: "/", TS: time.UnixMilli(tsMillis)}); err != nil {
			t.Fatal(err)
		}
	}
	seed("old.test", 1_000_000)
	seed("old2.test", 2_000_000)
	seed("new.test", 9_000_000)

	n, err := st.DeleteFlowsOlderThan(5_000_000)
	if err != nil {
		t.Fatalf("DeleteFlowsOlderThan: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 deleted, got %d", n)
	}
	stats, _ := st.HostStats()
	var hosts []string
	for _, s := range stats {
		hosts = append(hosts, s.Host)
	}
	if !containsStr(hosts, "new.test") || containsStr(hosts, "old.test") {
		t.Fatalf("unexpected hosts remaining: %v", hosts)
	}
}

func TestDeleteFlowsKeepNewest(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := int64(0); i < 5; i++ {
		if _, err := st.InsertFlow(&Flow{Method: "GET", Host: "h.test", Path: "/x", TS: time.UnixMilli(1_000_000 + i)}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.DeleteFlowsKeepNewest(2)
	if err != nil {
		t.Fatalf("DeleteFlowsKeepNewest: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 deleted, got %d", n)
	}
	var remaining int
	st.db.QueryRow(`SELECT COUNT(1) FROM flows`).Scan(&remaining)
	if remaining != 2 {
		t.Fatalf("expected 2 flows kept, got %d", remaining)
	}
}

func containsStr(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
