// Package tlsca manages a local certificate authority for TLS interception:
// it loads or generates a CA, then mints and caches short-lived per-host leaf
// certificates signed by that CA so the proxy can terminate client TLS.
package tlsca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CA is a local certificate authority plus a cache of minted leaf certs.
type CA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte

	mu       sync.Mutex
	cache    map[string]*tls.Certificate
	cacheOrd []string // insertion order, for bounded (FIFO) eviction
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
			CommonName:   "Interceptor CA",
			Organization: []string{"Interceptor"},
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

// LeafForHost mints (or returns a cached) leaf certificate for host, signed by
// the CA. host may be a DNS name or an IP literal.
func (c *CA) LeafForHost(host string) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if tc, ok := c.cache[host]; ok && time.Now().Before(tc.Leaf.NotAfter.Add(-time.Minute)) {
		return tc, nil // still valid; a near-expiry/expired entry falls through and is re-minted
	}

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
	tc := &tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        leaf,
	}
	if _, existed := c.cache[host]; !existed {
		c.cacheOrd = append(c.cacheOrd, host)
		// Evict oldest entries once over the bound so the cache can't grow without
		// limit under many distinct SNIs (e.g. subdomain fuzzing through the proxy).
		for len(c.cacheOrd) > leafCacheMax {
			oldest := c.cacheOrd[0]
			c.cacheOrd = c.cacheOrd[1:]
			delete(c.cache, oldest)
		}
	}
	c.cache[host] = tc
	return tc, nil
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
