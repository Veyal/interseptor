package collauth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"strings"
)

var b64 = base64.RawURLEncoding

// SignJWT builds a compact JWT. alg is HS256/384/512, RS256/384/512 or
// ES256/384/512. For HS* key is the secret bytes; for RS*/ES* it is a PEM
// private key (PKCS#1, PKCS#8 or SEC1).
func SignJWT(alg string, key []byte, header, payload map[string]any) (string, error) {
	h := map[string]any{"alg": alg, "typ": "JWT"}
	for k, v := range header {
		h[k] = v
	}
	hb, _ := json.Marshal(h)
	pb, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	signing := b64.EncodeToString(hb) + "." + b64.EncodeToString(pb)
	sig, err := signBytes(strings.ToUpper(alg), key, []byte(signing))
	if err != nil {
		return "", err
	}
	return signing + "." + b64.EncodeToString(sig), nil
}

func hashFor(bits string) (func() hash.Hash, crypto.Hash, error) {
	switch bits {
	case "256":
		return sha256.New, crypto.SHA256, nil
	case "384":
		return sha512.New384, crypto.SHA384, nil
	case "512":
		return sha512.New, crypto.SHA512, nil
	}
	return nil, 0, errors.New("unsupported hash size")
}

func signBytes(alg string, key, msg []byte) ([]byte, error) {
	if len(alg) != 5 {
		return nil, fmt.Errorf("unsupported JWT algorithm %q", alg)
	}
	hf, ch, err := hashFor(alg[2:])
	if err != nil {
		return nil, fmt.Errorf("unsupported JWT algorithm %q", alg)
	}
	switch alg[:2] {
	case "HS":
		if len(key) == 0 {
			return nil, errors.New("JWT secret is empty")
		}
		mac := hmac.New(hf, key)
		mac.Write(msg)
		return mac.Sum(nil), nil
	case "RS":
		pk, err := parsePrivateKey(key)
		if err != nil {
			return nil, err
		}
		rk, ok := pk.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("JWT key is not an RSA key")
		}
		h := hf()
		h.Write(msg)
		return rsa.SignPKCS1v15(rand.Reader, rk, ch, h.Sum(nil))
	case "ES":
		pk, err := parsePrivateKey(key)
		if err != nil {
			return nil, err
		}
		ek, ok := pk.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("JWT key is not an EC key")
		}
		h := hf()
		h.Write(msg)
		r, s, err := ecdsa.Sign(rand.Reader, ek, h.Sum(nil))
		if err != nil {
			return nil, err
		}
		n := (ek.Curve.Params().BitSize + 7) / 8
		out := make([]byte, 2*n)
		r.FillBytes(out[:n])
		s.FillBytes(out[n:])
		return out, nil
	}
	return nil, fmt.Errorf("unsupported JWT algorithm %q", alg)
}

func parsePrivateKey(pemBytes []byte) (any, error) {
	blk, _ := pem.Decode(pemBytes)
	if blk == nil {
		return nil, errors.New("JWT private key is not valid PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(blk.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParseECPrivateKey(blk.Bytes); err == nil {
		return k, nil
	}
	return nil, errors.New("JWT private key could not be parsed")
}

func (m *Manager) applyJWT(cfg Config, req *Request) (Result, error) {
	alg := strings.ToUpper(cfg.f("algorithm"))
	if alg == "" {
		alg = "HS256"
	}
	secret := []byte(cfg.Fields["secret"])
	if cfg.f("isSecretBase64") == "true" {
		d, err := base64.StdEncoding.DecodeString(cfg.f("secret"))
		if err != nil {
			return Result{}, errors.New("JWT secret is not valid base64")
		}
		secret = d
	}
	if pk := cfg.Fields["privateKey"]; pk != "" {
		secret = []byte(pk)
	}
	m.track(cfg.Fields["secret"], cfg.Fields["privateKey"])
	payload := map[string]any{}
	if p := cfg.f("payload"); p != "" {
		if err := json.Unmarshal([]byte(p), &payload); err != nil {
			return Result{}, errors.New("JWT payload is not a JSON object")
		}
	}
	now := m.now()
	if cfg.f("addIssuedAt") == "true" {
		payload["iat"] = now.Unix()
	}
	if s := cfg.f("expiresIn"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			return Result{}, errors.New("JWT expiresIn must be positive seconds")
		}
		payload["exp"] = now.Unix() + n
	}
	var hdr map[string]any
	if h := cfg.f("header"); h != "" {
		if err := json.Unmarshal([]byte(h), &hdr); err != nil {
			return Result{}, errors.New("JWT header is not a JSON object")
		}
	}
	tok, err := SignJWT(alg, secret, hdr, payload)
	if err != nil {
		return Result{}, m.fail(err)
	}
	m.track(tok)
	if cfg.f("addTokenTo") == "query" {
		k := cfg.f("queryParamKey")
		if k == "" {
			k = "token"
		}
		return Result{Applied: "auth:jwt"}, addQuery(req, k, tok)
	}
	if hasAuthz(req) {
		return Result{}, nil
	}
	prefix := cfg.f("headerPrefix")
	if prefix == "" {
		prefix = "Bearer"
	}
	req.Header.Set("Authorization", prefix+" "+tok)
	return Result{Applied: "auth:jwt"}, nil
}
