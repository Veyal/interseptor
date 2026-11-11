package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// req_len and res_len are nullable columns: a row written by an importer or an
// older build can hold NULL in either. One such row must not drop the other
// length from its host's total, and a host with only such rows must not fail
// the whole breakdown.
func TestR2HostStatsToleratesNullLengths(t *testing.T) {
	s := reviewOpen(t)
	reviewFlow(t, s, Flow{Host: "a.example.com", ReqLen: 10, ResLen: 100})
	half := reviewFlow(t, s, Flow{Host: "a.example.com", ReqLen: 7, ResLen: 5})
	none := reviewFlow(t, s, Flow{Host: "b.example.com", ReqLen: 3, ResLen: 4})
	if _, err := s.db.Exec(`UPDATE flows SET res_len = NULL WHERE id = ?`, half); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE flows SET req_len = NULL, res_len = NULL WHERE id = ?`, none); err != nil {
		t.Fatal(err)
	}
	stats, err := s.HostStats()
	if err != nil {
		t.Fatalf("HostStats: %v", err)
	}
	got := map[string]HostStat{}
	for _, hs := range stats {
		got[hs.Host] = hs
	}
	if a := got["a.example.com"]; a.Flows != 2 || a.Bytes != 117 {
		t.Errorf("a.example.com = %+v, want 2 flows / 117 bytes (the NULL response length counts as 0)", a)
	}
	if b := got["b.example.com"]; b.Flows != 1 || b.Bytes != 0 {
		t.Errorf("b.example.com = %+v, want 1 flow / 0 bytes", b)
	}
}

// A data directory whose name contains URI metacharacters must open the
// database inside that directory, not a file whose name stops at the '?'.
func TestR2OpenInADirectoryWithURIMetacharacters(t *testing.T) {
	for _, name := range []string{"a?b#c", "100%done", "q?_pragma=journal_mode(DELETE)", "plain dir"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, name)
			s, err := Open(dir)
			if err != nil {
				t.Fatalf("Open(%q): %v", dir, err)
			}
			if _, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "api.example.com", Path: "/"}); err != nil {
				t.Fatalf("InsertFlow: %v", err)
			}
			if err := s.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, currentDBName)); err != nil {
				t.Errorf("database is not in the data directory: %v", err)
			}
			entries, _ := os.ReadDir(root)
			for _, e := range entries {
				if e.Name() != name {
					t.Errorf("stray entry %q created beside the data directory", e.Name())
				}
			}
			s, err = Open(dir)
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			defer s.Close()
			if n, err := s.FlowCount(); err != nil || n != 1 {
				t.Errorf("FlowCount after reopen = %d, %v; want 1", n, err)
			}
			var mode string
			if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
				t.Errorf("journal_mode = %q, %v; want wal", mode, err)
			}
		})
	}
}

// The UI's "Allow this IP" offers the address the request came from, which for
// IPv6 can be bracketed or carry a zone. Both name one host.
func TestR2NormalizeAllowCIDRAcceptsBracketedAndZonedIPv6(t *testing.T) {
	for in, want := range map[string]string{
		"[fd7a::5]":        "fd7a::5/128",
		"fe80::1%en0":      "fe80::1/128",
		"[fe80::1%en0]":    "fe80::1/128",
		" [2001:db8::1] ":  "2001:db8::1/128",
		"fd7a::5":          "fd7a::5/128",
		"192.0.2.7":        "192.0.2.7/32",
		"192.0.2.0/24":     "192.0.2.0/24",
		"[2001:db8::]/32":  "2001:db8::/32",
		"::ffff:192.0.2.9": "192.0.2.9/32",
	} {
		got, err := NormalizeAllowCIDR(in)
		if err != nil || got != want {
			t.Errorf("NormalizeAllowCIDR(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "[]", "[fd7a::5", "fd7a::5]", "example.com", "[192.0.2.7]:80", "fe80::1%", "%en0"} {
		if got, err := NormalizeAllowCIDR(bad); err == nil {
			t.Errorf("NormalizeAllowCIDR(%q) = %q, want an error", bad, got)
		}
	}
}

// The machine-global keys database is opened through the same kind of URI.
func TestR2GlobalKeysInADirectoryWithURIMetacharacters(t *testing.T) {
	s := reviewOpen(t)
	global := filepath.Join(t.TempDir(), "glob?al#dir")
	if err := s.AttachGlobalKeys(global); err != nil {
		t.Fatalf("AttachGlobalKeys: %v", err)
	}
	if _, err := s.AddIPAllowlist("[fd7a::5]", "tailnet"); err != nil {
		t.Fatalf("AddIPAllowlist: %v", err)
	}
	if !s.AllowlistMatch("fd7a::5") {
		t.Error("the bracketed address was stored but does not match its own IP")
	}
	if _, err := os.Stat(filepath.Join(global, "keys.db")); err != nil {
		t.Errorf("keys.db is not in the global directory: %v", err)
	}
}
