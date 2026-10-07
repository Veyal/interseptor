package collauth

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
)

// ClientTLSConfig builds a tls.Config presenting a client certificate (mTLS).
// caPEM is optional extra roots; verify=false keeps InsecureSkipVerify for
// pentest targets with private CAs (caller decides). Encrypted private keys
// are rejected rather than guessed at.
func ClientTLSConfig(certPEM, keyPEM, caPEM []byte, insecureSkipVerify bool) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecureSkipVerify} // #nosec G402 -- caller opt-in
	if len(certPEM) > 0 || len(keyPEM) > 0 {
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, errors.New("invalid client certificate or key (encrypted keys are not supported)")
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	if len(caPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("no valid CA certificates in PEM")
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}
