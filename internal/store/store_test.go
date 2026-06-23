package store

import (
	"sync"
	"testing"
	"time"
)

// TestConcurrentWritesNoBusy stresses the DB with many concurrent writers,
// readers, and settings updates — it must not surface "database is locked"
// (SQLITE_BUSY). Guards the per-connection busy_timeout/WAL config in Open.
func TestConcurrentWritesNoBusy(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const writers, each = 16, 40
	errs := make(chan error, writers*each)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if _, err := s.InsertFlow(&Flow{TS: time.Now(), Method: "GET", Scheme: "https", Host: "h", Path: "/x", Status: 200}); err != nil {
					errs <- err
				}
				s.QueryFlowsFilter(FlowFilter{Limit: 5}) // concurrent reader
				if i%6 == 0 {
					if err := s.SetSetting("k", "v"); err != nil { // concurrent writer to another table
						errs <- err
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write failed (SQLITE_BUSY?): %v", err)
	}
	got, _ := s.QueryFlowsFilter(FlowFilter{Limit: 100000})
	if len(got) != writers*each {
		t.Fatalf("expected %d flows persisted, got %d", writers*each, len(got))
	}
}

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
	if got.TS.UnixMilli() != in.TS.UnixMilli() {
		t.Fatalf("TS not round-tripped: got %v want %v", got.TS, in.TS)
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
