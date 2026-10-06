package store

import (
	"testing"
)

func TestIPAllowlistCRUDAndMatch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	e, err := s.AddIPAllowlist("100.65.105.2", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if e.CIDR != "100.65.105.2/32" || e.Label != "laptop" {
		t.Fatalf("%+v", e)
	}
	if _, err := s.AddIPAllowlist("100.64.0.0/10", "tailscale"); err != nil {
		t.Fatal(err)
	}
	if !s.AllowlistMatch("100.65.105.2") {
		t.Fatal("exact match")
	}
	if !s.AllowlistMatch("100.100.1.1") {
		t.Fatal("cidr match")
	}
	if s.AllowlistMatch("8.8.8.8") {
		t.Fatal("should not match public")
	}
	list, err := s.ListIPAllowlist()
	if err != nil || len(list) != 2 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	if err := s.DeleteIPAllowlist(e.ID); err != nil {
		t.Fatal(err)
	}
	if s.AllowlistMatch("100.65.105.2") && !s.AllowlistMatch("100.100.1.1") {
		// 100.65 still in /10
	}
	if !s.AllowlistMatch("100.65.105.2") {
		t.Fatal("still in tailscale cidr")
	}
}

func TestNormalizeAllowCIDR(t *testing.T) {
	got, err := NormalizeAllowCIDR(" 10.0.0.1/24 ")
	if err != nil || got != "10.0.0.0/24" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := NormalizeAllowCIDR("not-an-ip"); err == nil {
		t.Fatal("expected error")
	}
}

func TestNormalizeAllowCIDR_bareIPs(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1":        "127.0.0.1/32",
		"::1":              "::1/128",
		"::ffff:192.0.2.7": "192.0.2.7/32",
		" 2001:db8::5 ":    "2001:db8::5/128",
		"2001:db8::/32":    "2001:db8::/32",
	}
	for in, want := range cases {
		got, err := NormalizeAllowCIDR(in)
		if err != nil || got != want {
			t.Errorf("%q => %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "1.2.3", "1.2.3.4/33", "example.com"} {
		if _, err := NormalizeAllowCIDR(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestAllowlistMatch_mappedPeerMatchesBareEntry(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.AddIPAllowlist("127.0.0.1", "local"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddIPAllowlist("::1", "local6"); err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"127.0.0.1", "::ffff:127.0.0.1", "::1"} {
		if !s.AllowlistMatch(ip) {
			t.Errorf("%s should match", ip)
		}
	}
	if s.AllowlistMatch("127.0.0.2") {
		t.Error("127.0.0.2 must not match")
	}
}
