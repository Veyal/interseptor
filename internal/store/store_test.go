package store

import (
	"testing"
	"time"
)

func TestInsertAndGetFlow(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	in := &Flow{
		TS:         time.UnixMilli(1_700_000_000_000),
		Method:     "GET",
		Scheme:     "http",
		Host:       "example.com",
		Port:       80,
		Path:       "/hello?x=1",
		Status:     200,
		ReqHeaders: map[string][]string{"Accept": {"application/json"}},
		ResHeaders: map[string][]string{"Content-Type": {"text/plain"}},
		Mime:       "text/plain",
		ClientAddr: "127.0.0.1:55555",
	}
	id, err := s.InsertFlow(in)
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := s.GetFlow(id)
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	if got.Method != "GET" || got.Host != "example.com" || got.Path != "/hello?x=1" {
		t.Fatalf("unexpected flow: %+v", got)
	}
	if got.Status != 200 || got.Mime != "text/plain" {
		t.Fatalf("unexpected status/mime: %+v", got)
	}
	if got.ReqHeaders["Accept"][0] != "application/json" {
		t.Fatalf("headers not round-tripped: %+v", got.ReqHeaders)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, ok, _ := s.GetSetting("proxy.addr"); ok {
		t.Fatal("expected missing setting")
	}
	if err := s.SetSetting("proxy.addr", "127.0.0.1:8080"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	v, ok, err := s.GetSetting("proxy.addr")
	if err != nil || !ok || v != "127.0.0.1:8080" {
		t.Fatalf("GetSetting = %q, %v, %v", v, ok, err)
	}
}
