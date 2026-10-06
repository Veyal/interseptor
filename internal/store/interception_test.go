package store

import (
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInterceptionSetupVersioningAndHistory(t *testing.T) {
	s := newTestStore(t)
	if cur, err := s.GetInterceptionSetup(); err != nil || cur.Version != 0 {
		t.Fatalf("empty = %+v err=%v", cur, err)
	}
	in := InterceptionSetup{ProxyAddress: "127.0.0.1:8080", CAFingerprint: "AA:BB"}
	v1, err := s.SetInterceptionSetup(in)
	if err != nil || v1.Version != 1 {
		t.Fatalf("v1 = %+v err=%v", v1, err)
	}
	if again, _ := s.SetInterceptionSetup(in); again.Version != 1 {
		t.Fatalf("unchanged save bumped version: %+v", again)
	}
	in.Enablers = []InterceptionEnabler{{Tool: "frida", ScriptHash: "sha256:abc", TargetLibrary: "libflutter.so", Method: "ssl_crypto_x509_session_verify_cert_chain", Hosts: []string{"*.example.com"}}}
	v2, err := s.SetInterceptionSetup(in)
	if err != nil || v2.Version != 2 || len(v2.Enablers) != 1 {
		t.Fatalf("v2 = %+v err=%v", v2, err)
	}
	hist, err := s.InterceptionHistory()
	if err != nil || len(hist) != 2 || hist[0].Version != 1 || hist[1].Version != 2 {
		t.Fatalf("history = %+v err=%v", hist, err)
	}
	if len(hist[0].Enablers) != 0 {
		t.Fatalf("v1 snapshot mutated: %+v", hist[0])
	}
}

func TestInterceptionSetupValidation(t *testing.T) {
	s := newTestStore(t)
	cases := map[string]InterceptionSetup{
		"long proxy":   {ProxyAddress: strings.Repeat("x", 600)},
		"too many":     {Enablers: make([]InterceptionEnabler, MaxInterceptionEnablers+1)},
		"empty tool":   {Enablers: []InterceptionEnabler{{Method: "m"}}},
		"many hosts":   {Hosts: make([]string, MaxInterceptionHosts+1)},
		"long enabler": {Enablers: []InterceptionEnabler{{Tool: strings.Repeat("t", 600)}}},
	}
	for name, in := range cases {
		if _, err := s.SetInterceptionSetup(in); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if cur, _ := s.GetInterceptionSetup(); cur.Version != 0 {
		t.Fatalf("rejected writes changed state: %+v", cur)
	}
}

func TestInterceptionProvenanceResolvesVersionInForce(t *testing.T) {
	s := newTestStore(t)
	before := time.Now().Add(-time.Hour)
	if p, err := s.InterceptionProvenance(before, "api.example.com"); err != nil || p != nil {
		t.Fatalf("no setup: p=%+v err=%v", p, err)
	}
	_, err := s.SetInterceptionSetup(InterceptionSetup{ProxyAddress: "127.0.0.1:8080", Hosts: []string{"*.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	mid := time.Now()
	time.Sleep(5 * time.Millisecond)
	_, err = s.SetInterceptionSetup(InterceptionSetup{ProxyAddress: "127.0.0.1:8080", Hosts: []string{"*.example.com"},
		Enablers: []InterceptionEnabler{{Tool: "frida", Method: "hook", Hosts: []string{"api.example.com"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := s.InterceptionProvenance(before, "api.example.com"); p != nil {
		t.Fatalf("flow older than any setup got provenance: %+v", p)
	}
	p, err := s.InterceptionProvenance(mid, "api.example.com")
	if err != nil || p == nil || p.SetupVersion != 1 || len(p.Enablers) != 0 {
		t.Fatalf("mid = %+v err=%v, want v1 without enablers", p, err)
	}
	p, _ = s.InterceptionProvenance(time.Now(), "api.example.com")
	if p == nil || p.SetupVersion != 2 || len(p.Enablers) != 1 || p.Enablers[0].Tool != "frida" {
		t.Fatalf("now = %+v, want v2 with frida", p)
	}
	// Enabler scoped to api.example.com does not apply to other covered hosts.
	p, _ = s.InterceptionProvenance(time.Now(), "www.example.com")
	if p == nil || p.SetupVersion != 2 || len(p.Enablers) != 0 {
		t.Fatalf("www = %+v, want v2 without enablers", p)
	}
	// Host outside the setup's coverage is not claimed.
	if p, _ := s.InterceptionProvenance(time.Now(), "other.test"); p != nil {
		t.Fatalf("uncovered host got provenance: %+v", p)
	}
}

func TestSetFlowInterceptionAnnotation(t *testing.T) {
	s := newTestStore(t)
	connect, err := s.InsertFlow(&Flow{TS: time.Now(), Method: "CONNECT", Host: "api.example.com", Path: "(tls handshake)", Flags: FlagTLSFailed})
	if err != nil {
		t.Fatal(err)
	}
	normal, _ := s.InsertFlow(&Flow{TS: time.Now(), Method: "GET", Host: "api.example.com", Path: "/", Status: 200})

	if _, err := s.SetFlowInterceptionAnnotation(connect, "bogus"); err == nil {
		t.Fatal("expected invalid annotation error")
	}
	if _, err := s.SetFlowInterceptionAnnotation(normal, AnnotationPinningBlocked); err == nil {
		t.Fatal("expected error annotating a flow with a response")
	}
	tags, err := s.SetFlowInterceptionAnnotation(connect, AnnotationPinningBlocked)
	if err != nil || !hasTag(tags, AnnotationPinningBlocked) {
		t.Fatalf("tags = %v err=%v", tags, err)
	}
	tags, err = s.SetFlowInterceptionAnnotation(connect, AnnotationNotIntercepted)
	if err != nil || hasTag(tags, AnnotationPinningBlocked) || !hasTag(tags, AnnotationNotIntercepted) {
		t.Fatalf("switch tags = %v err=%v", tags, err)
	}
	tags, err = s.SetFlowInterceptionAnnotation(connect, "")
	if err != nil || hasTag(tags, AnnotationNotIntercepted) {
		t.Fatalf("clear tags = %v err=%v", tags, err)
	}
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
