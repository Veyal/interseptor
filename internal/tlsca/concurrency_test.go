package tlsca

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"
	"testing"
)

// TestLeafForHostCollapsesConcurrentMintsForOneHost pins the single-flight
// behaviour. A page opening ten parallel connections to one new host must
// produce one certificate, not ten: the duplicates would each burn a keygen and
// a signature, and the loser of the cache race would hand the client a
// certificate that is about to be evicted.
func TestLeafForHostCollapsesConcurrentMintsForOneHost(t *testing.T) {
	ca, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	const goroutines = 32
	got := make([]*tls.Certificate, goroutines)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			tc, err := ca.LeafForHost("api.example.com")
			if err != nil {
				t.Errorf("LeafForHost: %v", err)
				return
			}
			got[i] = tc
		}(i)
	}
	close(start)
	wg.Wait()

	for i, tc := range got {
		if tc == nil {
			t.Fatalf("goroutine %d got no certificate", i)
		}
		if tc != got[0] {
			t.Fatalf("goroutine %d got a separately minted certificate (serial %s, first %s)",
				i, tc.Leaf.SerialNumber, got[0].Leaf.SerialNumber)
		}
	}
}

// TestLeafForHostMintsDistinctHostsConcurrently is the correctness half of
// letting mints run in parallel: every host still gets its own certificate,
// correctly named and signed by the CA.
func TestLeafForHostMintsDistinctHostsConcurrently(t *testing.T) {
	ca, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)

	const hosts = 64
	certs := make([]*tls.Certificate, hosts)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < hosts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			tc, err := ca.LeafForHost(fmt.Sprintf("h%d.example.com", i))
			if err != nil {
				t.Errorf("LeafForHost %d: %v", i, err)
				return
			}
			certs[i] = tc
		}(i)
	}
	close(start)
	wg.Wait()

	serials := make(map[string]int, hosts)
	for i, tc := range certs {
		if tc == nil {
			t.Fatalf("host %d got no certificate", i)
		}
		host := fmt.Sprintf("h%d.example.com", i)
		if _, err := tc.Leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: roots}); err != nil {
			t.Fatalf("leaf for %s does not verify against the CA: %v", host, err)
		}
		if tc.Leaf.Subject.CommonName != host {
			t.Fatalf("leaf CN = %q, want %q", tc.Leaf.Subject.CommonName, host)
		}
		serial := tc.Leaf.SerialNumber.String()
		if prev, dup := serials[serial]; dup {
			t.Fatalf("hosts %d and %d share serial %s", prev, i, serial)
		}
		serials[serial] = i
	}

	// Asking again returns the cached certificate rather than a fresh mint.
	for i := 0; i < hosts; i++ {
		again, err := ca.LeafForHost(fmt.Sprintf("h%d.example.com", i))
		if err != nil {
			t.Fatalf("LeafForHost again: %v", err)
		}
		if again != certs[i] {
			t.Fatalf("host %d was re-minted instead of served from cache", i)
		}
	}
}

// TestLeafForHostKeysAreNotSharedBetweenHosts pins a deliberate decision.
// Sharing one leaf private key across every minted certificate is common proxy
// practice and issue #101 proposed it, but it was measured at roughly a tenth
// of one mint (an ECDSA P-256 keygen is ~8µs against ~84µs for the signature
// and parse) and was not what made concurrent handshakes slow. Per-host keys
// are kept so a key recovered from one connection authorises impersonating
// that one host, not every host this proxy has ever served. See
// docs/architecture.md.
func TestLeafForHostKeysAreNotSharedBetweenHosts(t *testing.T) {
	ca, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	a, err := ca.LeafForHost("a.example.com")
	if err != nil {
		t.Fatalf("LeafForHost a: %v", err)
	}
	b, err := ca.LeafForHost("b.example.com")
	if err != nil {
		t.Fatalf("LeafForHost b: %v", err)
	}
	ka, ok := a.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("leaf key for a is %T, want *ecdsa.PrivateKey", a.PrivateKey)
	}
	kb, ok := b.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("leaf key for b is %T, want *ecdsa.PrivateKey", b.PrivateKey)
	}
	if ka.Equal(kb) {
		t.Fatalf("a.example.com and b.example.com were minted with the same private key")
	}
	if ka.PublicKey.Equal(&kb.PublicKey) {
		t.Fatalf("a.example.com and b.example.com share a public key")
	}
}

// TestLeafForHostRecoversFromAFailedMint checks the single-flight map is not
// left holding a dead entry when a mint fails: the next caller has to be able
// to try again rather than inherit the failure forever.
func TestLeafForHostRecoversFromAFailedMint(t *testing.T) {
	ca, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	// A CA whose signing key does not match its certificate's public key makes
	// x509.CreateCertificate fail, which is a mint failure and nothing else.
	other, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreate other: %v", err)
	}
	broken := &CA{cert: other.cert, key: ca.key, certPEM: other.certPEM, cache: map[string]*tls.Certificate{}}
	if _, err := broken.LeafForHost("api.example.com"); err == nil {
		t.Fatalf("LeafForHost with a mismatched CA key succeeded")
	}
	broken.mu.Lock()
	inflight := len(broken.minting)
	broken.mu.Unlock()
	if inflight != 0 {
		t.Fatalf("%d hosts left marked as in-flight after a failed mint", inflight)
	}
	broken.key = other.key
	if _, err := broken.LeafForHost("api.example.com"); err != nil {
		t.Fatalf("LeafForHost after a failed mint: %v", err)
	}
}
