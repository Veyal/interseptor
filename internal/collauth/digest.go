package collauth

import (
	"context"
	"crypto/md5" // #nosec G501 -- RFC 7616 digest still defines MD5
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// DigestChallenge is a parsed WWW-Authenticate: Digest header.
type DigestChallenge struct {
	Realm, Nonce, Opaque, Algorithm, QOP string
}

// ParseDigestChallenge parses a Digest WWW-Authenticate value.
func ParseDigestChallenge(h string) (DigestChallenge, bool) {
	h = strings.TrimSpace(h)
	if len(h) < 7 || !strings.EqualFold(h[:6], "digest") {
		return DigestChallenge{}, false
	}
	p := parseKV(h[6:])
	c := DigestChallenge{Realm: p["realm"], Nonce: p["nonce"], Opaque: p["opaque"], Algorithm: p["algorithm"], QOP: p["qop"]}
	return c, c.Nonce != ""
}

// parseKV parses comma separated key=value / key="value" pairs.
func parseKV(s string) map[string]string {
	out := map[string]string{}
	for len(s) > 0 {
		s = strings.TrimLeft(s, " ,\t")
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		k := strings.ToLower(strings.TrimSpace(s[:eq]))
		s = s[eq+1:]
		var v string
		if strings.HasPrefix(s, `"`) {
			end := 1
			var b strings.Builder
			for end < len(s) && s[end] != '"' {
				if s[end] == '\\' && end+1 < len(s) {
					end++
				}
				b.WriteByte(s[end])
				end++
			}
			v = b.String()
			if end < len(s) {
				end++
			}
			s = s[end:]
		} else {
			end := strings.IndexByte(s, ',')
			if end < 0 {
				end = len(s)
			}
			v = strings.TrimSpace(s[:end])
			s = s[end:]
		}
		out[k] = v
	}
	return out
}

func digestHash(alg string) (func() hash.Hash, string, error) {
	base := strings.TrimSuffix(strings.ToUpper(alg), "-SESS")
	switch base {
	case "", "MD5":
		return md5.New, "MD5", nil
	case "SHA-256":
		return sha256.New, "SHA-256", nil
	}
	return nil, "", fmt.Errorf("unsupported digest algorithm %q", alg)
}

// DigestHeader computes the Authorization header value (RFC 7616, qop auth or
// auth-int). cnonce and nc are explicit so tests are deterministic.
func DigestHeader(c DigestChallenge, user, pass, method, uri string, body []byte, cnonce string, nc int) (string, error) {
	hf, name, err := digestHash(c.Algorithm)
	if err != nil {
		return "", err
	}
	H := func(s string) string {
		h := hf()
		io.WriteString(h, s)
		return hex.EncodeToString(h.Sum(nil))
	}
	qop := ""
	for _, q := range strings.Split(c.QOP, ",") {
		q = strings.TrimSpace(q)
		if q == "auth" || (q == "auth-int" && qop == "") {
			qop = q
			if q == "auth" {
				break
			}
		}
	}
	ncs := fmt.Sprintf("%08x", nc)
	ha1 := H(user + ":" + c.Realm + ":" + pass)
	if strings.HasSuffix(strings.ToUpper(c.Algorithm), "-SESS") {
		ha1 = H(ha1 + ":" + c.Nonce + ":" + cnonce)
	}
	ha2 := H(method + ":" + uri)
	if qop == "auth-int" {
		ha2 = H(method + ":" + uri + ":" + H(string(body)))
	}
	var resp string
	if qop == "" {
		resp = H(ha1 + ":" + c.Nonce + ":" + ha2)
	} else {
		resp = H(ha1 + ":" + c.Nonce + ":" + ncs + ":" + cnonce + ":" + qop + ":" + ha2)
	}
	parts := []string{
		`username="` + escQ(user) + `"`, `realm="` + escQ(c.Realm) + `"`,
		`nonce="` + escQ(c.Nonce) + `"`, `uri="` + escQ(uri) + `"`,
		`response="` + resp + `"`,
	}
	if c.Algorithm != "" {
		a := name
		if strings.HasSuffix(strings.ToUpper(c.Algorithm), "-SESS") {
			a += "-sess"
		}
		parts = append(parts, "algorithm="+a)
	}
	if c.Opaque != "" {
		parts = append(parts, `opaque="`+escQ(c.Opaque)+`"`)
	}
	if qop != "" {
		parts = append(parts, "qop="+qop, "nc="+ncs, `cnonce="`+escQ(cnonce)+`"`)
	}
	return "Digest " + strings.Join(parts, ", "), nil
}

func escQ(s string) string { return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) }

func (m *Manager) applyDigest(ctx context.Context, cfg Config, req *Request) (Result, error) {
	if hasAuthz(req) {
		return Result{}, nil
	}
	user, pass := cfg.Fields["username"], cfg.Fields["password"]
	m.track(pass)
	ch := DigestChallenge{Realm: cfg.f("realm"), Nonce: cfg.f("nonce"), Opaque: cfg.f("opaque"), Algorithm: cfg.f("algorithm"), QOP: cfg.f("qop")}
	if ch.Nonce == "" {
		var err error
		if ch, err = m.probeDigest(ctx, req); err != nil {
			return Result{}, m.fail(err)
		}
	}
	cn := cfg.f("clientNonce")
	if cn == "" {
		var b [8]byte
		_, _ = rand.Read(b[:])
		cn = hex.EncodeToString(b[:])
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return Result{}, err
	}
	uri := u.RequestURI()
	hv, err := DigestHeader(ch, user, pass, req.Method, uri, req.Body, cn, 1)
	if err != nil {
		return Result{}, err
	}
	m.track(hv)
	req.Header.Set("Authorization", hv)
	return Result{Applied: "auth:digest"}, nil
}

// probeDigest sends a body-less request to learn the challenge.
func (m *Manager) probeDigest(ctx context.Context, req *Request) (DigestChallenge, error) {
	pr, err := http.NewRequestWithContext(ctx, req.Method, req.URL, nil)
	if err != nil {
		return DigestChallenge{}, err
	}
	resp, err := m.doer.Do(pr)
	if err != nil {
		return DigestChallenge{}, fmt.Errorf("digest probe failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	for _, h := range resp.Header.Values("Www-Authenticate") {
		if c, ok := ParseDigestChallenge(h); ok {
			return c, nil
		}
	}
	return DigestChallenge{}, errors.New("digest probe: server returned no Digest challenge")
}
