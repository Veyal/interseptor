// Package tlsca manages a local certificate authority for TLS interception:
// it loads or generates a CA, then mints and caches short-lived per-host leaf
// certificates signed by that CA so the proxy can terminate client TLS.
package tlsca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CA is a local certificate authority plus a cache of minted leaf certs.
type CA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte

	// mu guards the cache and the in-flight map. It is an RWMutex because the
	// hit path — every handshake after the first to a given host — only reads,
	// and minting happens with no lock held at all.
	mu       sync.RWMutex
	cache    map[string]*tls.Certificate
	cacheOrd []string // insertion order, for bounded (FIFO) eviction
	minting  map[string]*leafMint
}

// leafMint is one in-flight mint. Concurrent requests for the same host join
// the existing mint instead of starting another: a page opening ten parallel
// connections to a new host should cost one keygen and one signature, not ten.
// done is closed once tc/err are set, which is also what publishes them to the
// waiters.
type leafMint struct {
	done chan struct{}
	tc   *tls.Certificate
	err  error
}

const (
	caValidity   = 10 * 365 * 24 * time.Hour
	leafValidity = 397 * 24 * time.Hour // CA/Browser Forum max for leaf certs
)

// leafCacheMax bounds the per-host leaf cache (each entry holds a key + cert). A
// var so tests can lower it.
var leafCacheMax = 2048

// LoadOrCreate loads the CA from dir if present, otherwise generates a new one
// and persists ca.crt + ca.key under dir.
func LoadOrCreate(dir string) (*CA, error) {
	// 0o700: the CA private key lives here — only the owner should traverse the dir
	// (defense in depth; ca.key itself is already 0o600).
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	crtPath := filepath.Join(dir, "ca.crt")
	keyPath := filepath.Join(dir, "ca.key")

	if crtPEM, err := os.ReadFile(crtPath); err == nil {
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("ca.crt present but ca.key unreadable: %w", err)
		}
		return load(crtPEM, keyPEM)
	}
	return create(crtPath, keyPath)
}

func load(crtPEM, keyPEM []byte) (*CA, error) {
	cb, _ := pem.Decode(crtPEM)
	if cb == nil {
		return nil, fmt.Errorf("ca.crt: no PEM block")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, fmt.Errorf("ca.key: no PEM block")
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := keyAny.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("ca.key: not an ECDSA key")
	}
	return &CA{cert: cert, key: key, certPEM: crtPEM, cache: map[string]*tls.Certificate{}}, nil
}

func create(crtPath, keyPath string) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Interseptor CA",
			Organization: []string{"Interseptor"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	crtPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(crtPath, crtPEM, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	return &CA{cert: cert, key: key, certPEM: crtPEM, cache: map[string]*tls.Certificate{}}, nil
}

// CertPEM returns the PEM-encoded CA certificate (for the user to trust).
func (c *CA) CertPEM() []byte { return c.certPEM }

// Fingerprint returns the CA certificate's SHA-256 fingerprint as colon-separated
// uppercase hex, the form device certificate viewers display.
func (c *CA) Fingerprint() string {
	sum := sha256.Sum256(c.cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// LeafForHost mints (or returns a cached) leaf certificate for host, signed by
// the CA. host may be a DNS name or an IP literal.
//
// Minting — an ECDSA keygen, a signature and a parse — runs with no lock held,
// so handshakes to different hosts mint in parallel instead of queueing behind
// one another. Requests for the same host still collapse onto one mint.
func (c *CA) LeafForHost(host string) (*tls.Certificate, error) {
	if tc := c.cached(host); tc != nil {
		return tc, nil
	}

	c.mu.Lock()
	// Re-check: another goroutine may have finished minting this host between
	// the read above and this write lock.
	if tc, ok := c.cache[host]; ok && leafUsable(tc) {
		c.mu.Unlock()
		return tc, nil
	}
	if m, ok := c.minting[host]; ok {
		c.mu.Unlock()
		<-m.done
		return m.tc, m.err
	}
	m := &leafMint{done: make(chan struct{})}
	if c.minting == nil {
		c.minting = map[string]*leafMint{}
	}
	c.minting[host] = m
	c.mu.Unlock()

	tc, err := c.mintLeaf(host)

	c.mu.Lock()
	if err == nil {
		c.storeLeafLocked(host, tc)
	}
	// Clear the in-flight marker before publishing, so a caller arriving after
	// a failure starts a fresh mint instead of inheriting this one's error.
	delete(c.minting, host)
	c.mu.Unlock()

	m.tc, m.err = tc, err
	close(m.done)
	return tc, err
}

// cached returns a usable cached certificate for host, or nil. It takes only a
// read lock, which is the whole point: once a target's hosts are known, every
// handshake lands here and they no longer serialize against each other.
func (c *CA) cached(host string) *tls.Certificate {
	c.mu.RLock()
	tc, ok := c.cache[host]
	c.mu.RUnlock()
	if ok && leafUsable(tc) {
		return tc
	}
	return nil // absent, or near expiry: fall through and re-mint
}

func leafUsable(tc *tls.Certificate) bool {
	return time.Now().Before(tc.Leaf.NotAfter.Add(-time.Minute))
}

// storeLeafLocked caches a freshly minted certificate, evicting the oldest
// entries once over the bound so the cache can't grow without limit under many
// distinct SNIs (e.g. subdomain fuzzing through the proxy). Callers hold mu.
func (c *CA) storeLeafLocked(host string, tc *tls.Certificate) {
	if c.cache == nil {
		c.cache = map[string]*tls.Certificate{}
	}
	if _, existed := c.cache[host]; !existed {
		c.cacheOrd = append(c.cacheOrd, host)
		for len(c.cacheOrd) > leafCacheMax {
			oldest := c.cacheOrd[0]
			c.cacheOrd = c.cacheOrd[1:]
			delete(c.cache, oldest)
		}
	}
	c.cache[host] = tc
}

// mintLeaf generates a key and signs one per-host certificate. Every host gets
// its own key: sharing one leaf key across all minted certificates is common
// proxy practice, but the keygen is about a tenth of a mint here and was not
// what made concurrent handshakes slow. docs/architecture.md records the
// reasoning.
func (c *CA) mintLeaf(host string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	notAfter := now.Add(leafValidity)
	if notAfter.After(c.cert.NotAfter) {
		notAfter = c.cert.NotAfter
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        leaf,
	}, nil
}

// TLSConfig returns a *tls.Config that serves a per-host leaf chosen from the
// ClientHello's SNI. Suitable for tls.Server on a hijacked CONNECT conn.
func (c *CA) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			host := chi.ServerName
			if host == "" {
				host = "localhost"
			}
			return c.LeafForHost(host)
		},
	}
}

func randSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}
