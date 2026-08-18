package store

import (
	"testing"
	"time"
)

func writeTestBody(t *testing.T, s *Store, content string) string {
	t.Helper()
	w, err := s.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	hash, _, err := w.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

// Endpoints aggregates flows into unique (host, method, path) endpoints — so
// repeated hits (and noise like many 404s) collapse to one row carrying the hit
// count, the distinct statuses seen, and the latest status/flow.
func TestEndpointsAggregate(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "a.com", Path: "/x", Status: 200})
	s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Host: "a.com", Path: "/x", Status: 200})
	s.InsertFlow(&Flow{TS: time.UnixMilli(3), Method: "GET", Host: "a.com", Path: "/x", Status: 404})
	s.InsertFlow(&Flow{TS: time.UnixMilli(4), Method: "POST", Host: "a.com", Path: "/y", Status: 201})
	s.InsertFlow(&Flow{TS: time.UnixMilli(5), Method: "GET", Host: "b.com", Path: "/z", Status: 500, Flags: FlagIntruder})

	eps, _, err := s.Endpoints(EndpointFilter{ExcludeFlags: FlagIntruder})
	if err != nil {
		t.Fatalf("Endpoints: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("got %d endpoints, want 2 (b.com/z is attack traffic)", len(eps))
	}

	var x *Endpoint
	for i := range eps {
		if eps[i].Host == "a.com" && eps[i].Path == "/x" {
			x = &eps[i]
		}
	}
	if x == nil {
		t.Fatal("missing GET a.com/x endpoint")
	}
	if x.Hits != 3 {
		t.Fatalf("hits = %d, want 3", x.Hits)
	}
	if x.LastStatus != 404 {
		t.Fatalf("lastStatus = %d, want 404 (latest)", x.LastStatus)
	}
	if len(x.Statuses) != 2 {
		t.Fatalf("statuses = %v, want two distinct (200, 404)", x.Statuses)
	}
	if x.LastFlowID == 0 {
		t.Fatal("lastFlowID should point at the most recent hit")
	}
}

func TestEndpointsHeaderSearch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.InsertFlow(&Flow{
		TS: time.UnixMilli(1), Method: "GET", Host: "api.test", Path: "/v1/users", Status: 200,
		ReqHeaders: map[string][]string{"Authorization": {"Bearer SECRET123"}},
	})
	s.InsertFlow(&Flow{
		TS: time.UnixMilli(2), Method: "GET", Host: "api.test", Path: "/v1/other", Status: 200,
	})

	eps, _, err := s.Endpoints(EndpointFilter{
		Search: "SECRET123", SearchScope: EndpointSearchHeaders,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 1 || eps[0].Path != "/v1/users" {
		t.Fatalf("header search: got %+v", eps)
	}
}

func TestEndpointsBodySearch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	bodyHash := writeTestBody(t, s, `{"flag":"UNIQUE_BODY_TOKEN"}`)
	s.InsertFlow(&Flow{
		TS: time.UnixMilli(1), Method: "POST", Host: "app.test", Path: "/api/data", Status: 200,
		ResBodyHash: bodyHash, ResLen: 28,
	})
	s.InsertFlow(&Flow{
		TS: time.UnixMilli(2), Method: "GET", Host: "app.test", Path: "/health", Status: 200,
	})

	eps, _, err := s.Endpoints(EndpointFilter{
		Search: "UNIQUE_BODY_TOKEN", SearchScope: EndpointSearchBody,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 1 || eps[0].Path != "/api/data" {
		t.Fatalf("body search: got %+v", eps)
	}
}

func TestEndpointsAllSearch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	bodyHash := writeTestBody(t, s, "plain body needle")
	s.InsertFlow(&Flow{
		TS: time.UnixMilli(1), Method: "GET", Host: "h.test", Path: "/a", Status: 200,
		ResBodyHash: bodyHash,
	})
	s.InsertFlow(&Flow{
		TS: time.UnixMilli(2), Method: "GET", Host: "h.test", Path: "/b", Status: 200,
		ReqHeaders: map[string][]string{"X-Custom": {"needle-header"}},
	})

	eps, _, err := s.Endpoints(EndpointFilter{Search: "needle", SearchScope: EndpointSearchAll})
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 2 {
		t.Fatalf("all search: got %d endpoints, want 2", len(eps))
	}
}

func TestEndpointsHideNoiseOnly(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "app.test", Path: "/alive", Status: 200})
	s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Host: "app.test", Path: "/missing", Status: 404})
	s.InsertFlow(&Flow{TS: time.UnixMilli(3), Method: "GET", Host: "app.test", Path: "/forbidden", Status: 403})
	s.InsertFlow(&Flow{TS: time.UnixMilli(4), Method: "GET", Host: "app.test", Path: "/mixed", Status: 404})
	s.InsertFlow(&Flow{TS: time.UnixMilli(5), Method: "GET", Host: "app.test", Path: "/mixed", Status: 200})

	all, _, err := s.Endpoints(EndpointFilter{ExcludeFlags: FlagIntruder})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("unfiltered: got %d endpoints, want 4", len(all))
	}

	filtered, _, err := s.Endpoints(EndpointFilter{
		ExcludeFlags:  FlagIntruder,
		HideNoiseOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 2 {
		t.Fatalf("hide noise: got %d endpoints, want 2 (/alive and /mixed)", len(filtered))
	}
	for _, e := range filtered {
		if e.Path == "/missing" || e.Path == "/forbidden" {
			t.Fatalf("noise endpoint should be hidden: %+v", e)
		}
	}
}

func TestEndpointsResBodyHashClustering(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	sharedHash := writeTestBody(t, s, `<html>same SPA shell</html>`)
	otherHash := writeTestBody(t, s, `{"ok":true}`)

	s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "app.test", Path: "/a", Status: 200, ResBodyHash: sharedHash, ResLen: 28})
	s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Host: "app.test", Path: "/b", Status: 200, ResBodyHash: sharedHash, ResLen: 28})
	s.InsertFlow(&Flow{TS: time.UnixMilli(3), Method: "GET", Host: "app.test", Path: "/c", Status: 200, ResBodyHash: otherHash, ResLen: 12})

	eps, _, err := s.Endpoints(EndpointFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 3 {
		t.Fatalf("got %d endpoints, want 3", len(eps))
	}
	byPath := map[string]Endpoint{}
	for _, e := range eps {
		byPath[e.Path] = e
	}
	if byPath["/a"].ResBodyHash != sharedHash || byPath["/b"].ResBodyHash != sharedHash {
		t.Fatalf("/a and /b should share hash %q, got %q and %q", sharedHash, byPath["/a"].ResBodyHash, byPath["/b"].ResBodyHash)
	}
	if byPath["/c"].ResBodyHash != otherHash {
		t.Fatalf("/c hash = %q, want %q", byPath["/c"].ResBodyHash, otherHash)
	}
}

func TestEndpointsLastStatusFromLatestFlow(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	body200 := writeTestBody(t, s, "ok")
	body404 := writeTestBody(t, s, "gone")

	s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "app.test", Path: "/x", Status: 200, Scheme: "http", ResBodyHash: body200})
	s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Host: "app.test", Path: "/x", Status: 404, Scheme: "https", ResBodyHash: body404})

	eps, _, err := s.Endpoints(EndpointFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var x *Endpoint
	for i := range eps {
		if eps[i].Path == "/x" {
			x = &eps[i]
		}
	}
	if x == nil {
		t.Fatal("missing /x endpoint")
	}
	if x.LastStatus != 404 {
		t.Fatalf("lastStatus = %d, want 404 from latest flow", x.LastStatus)
	}
	if x.Scheme != "https" {
		t.Fatalf("scheme = %q, want https from latest flow", x.Scheme)
	}
	if x.ResBodyHash != body404 {
		t.Fatalf("resBodyHash = %q, want latest flow body %q", x.ResBodyHash, body404)
	}
}

func TestEndpointsSoft404(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	softHash := writeTestBody(t, s, `<html><body>Page not found</body></html>`)
	realHash := writeTestBody(t, s, `{"users":[{"id":1}]}`)
	honest404 := writeTestBody(t, s, `Not Found`)

	s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Host: "app.test", Path: "/soft", Status: 200, ResBodyHash: softHash})
	s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Host: "app.test", Path: "/real", Status: 200, ResBodyHash: realHash})
	s.InsertFlow(&Flow{TS: time.UnixMilli(3), Method: "GET", Host: "app.test", Path: "/honest", Status: 404, ResBodyHash: honest404})

	eps, _, err := s.Endpoints(EndpointFilter{})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Endpoint{}
	for _, e := range eps {
		byPath[e.Path] = e
	}
	if !byPath["/soft"].Soft404 {
		t.Fatal("/soft with 200 + 'Page not found' should be soft404")
	}
	if byPath["/real"].Soft404 {
		t.Fatal("/real with genuine content should not be soft404")
	}
	if byPath["/honest"].Soft404 {
		t.Fatal("honest 404 status should not be flagged soft404")
	}
}
