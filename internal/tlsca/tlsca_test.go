package tlsca

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The leaf cache is FIFO-bounded so many distinct SNIs can't grow it without limit.
func TestLeafCacheBounded(t *testing.T) {
	ca, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	old := leafCacheMax
	leafCacheMax = 5
	defer func() { leafCacheMax = old }()

	for i := 0; i < 12; i++ {
		if _, err := ca.LeafForHost(fmt.Sprintf("h%d.example.com", i)); err != nil {
			t.Fatalf("LeafForHost: %v", err)
		}
	}
	ca.mu.Lock()
	n := len(ca.cache)
	ca.mu.Unlock()
	if n > 5 {
		t.Fatalf("leaf cache should be bounded at 5, got %d", n)
	}
}

func TestLoadOrCreatePersists(t *testing.T) {
	dir := t.TempDir()
	ca1, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	// Files written.
	for _, name := range []string{"ca.crt", "ca.key"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s on disk: %v", name, err)
		}
	}
	ca2, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if string(ca1.CertPEM()) != string(ca2.CertPEM()) {
		t.Fatal("expected reloaded CA to equal the persisted one")
	}
}

// A cached leaf that has expired (e.g. a process running past the leaf validity
// window, or minted against a near-expiry CA) must be re-minted, not served from
// cache — otherwise TLS clients reject the MITM with "certificate expired".
func TestLeafForHostRemintsExpiredCacheEntry(t *testing.T) {
	ca, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	leaf1, err := ca.LeafForHost("example.com")
	if err != nil {
		t.Fatalf("LeafForHost: %v", err)
	}
	// Force the cached entry to look expired (same pointer lives in the cache).
	leaf1.Leaf.NotAfter = time.Now().Add(-time.Hour)

	leaf2, err := ca.LeafForHost("example.com")
	if err != nil {
		t.Fatalf("LeafForHost (re-mint): %v", err)
	}
	if leaf2 == leaf1 {
		t.Fatal("expired cached leaf was returned instead of re-minted")
	}
	if !leaf2.Leaf.NotAfter.After(time.Now()) {
		t.Fatalf("re-minted leaf is not valid into the future: %v", leaf2.Leaf.NotAfter)
	}
}

func TestLeafForHostVerifiesAgainstCA(t *testing.T) {
	ca, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}

	leaf, err := ca.LeafForHost("example.com")
	if err != nil {
		t.Fatalf("LeafForHost: %v", err)
	}
	x, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.CertPEM()) {
		t.Fatal("failed to add CA to pool")
	}
	if _, err := x.Verify(x509.VerifyOptions{DNSName: "example.com", Roots: roots}); err != nil {
		t.Fatalf("leaf does not chain to CA: %v", err)
	}

	// Cached: same host returns an identical leaf certificate.
	leaf2, err := ca.LeafForHost("example.com")
	if err != nil {
		t.Fatalf("LeafForHost cached: %v", err)
	}
	if &leaf.Certificate[0] == nil || string(leaf.Certificate[0]) != string(leaf2.Certificate[0]) {
		t.Fatal("expected cached leaf to be reused")
	}
}

func TestTLSConfigUsesSNI(t *testing.T) {
	ca, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	cfg := ca.TLSConfig()
	cert, err := cfg.GetCertificate(&tls.ClientHelloInfo{ServerName: "host.test"})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	x, _ := x509.ParseCertificate(cert.Certificate[0])
	if len(x.DNSNames) == 0 || x.DNSNames[0] != "host.test" {
		t.Fatalf("expected SAN host.test, got %v", x.DNSNames)
	}
}
