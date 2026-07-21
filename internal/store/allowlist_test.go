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
	if e.CIDR != "100.65.105.2" || e.Label != "laptop" {
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
