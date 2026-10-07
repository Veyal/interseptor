package collauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const canary = "CANARY-secret-9f3a1c77"

func newReq(method, u string) *Request {
	return &Request{Method: method, URL: u, Header: http.Header{}}
}

func TestBasicBearerAPIKey(t *testing.T) {
	m := New(Options{})
	r := newReq("GET", "https://example.com/a")
	if _, err := m.Apply(context.Background(), "k", Config{Type: "basic", Fields: map[string]string{"username": "u", "password": canary}}, r); err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("u:"+canary))
	if r.Header.Get("Authorization") != want {
		t.Fatalf("basic: %q", r.Header.Get("Authorization"))
	}
	if strings.Contains(m.Scrub(r.Header.Get("Authorization")), canary) {
		t.Fatal("scrub failed")
	}

	r = newReq("GET", "https://example.com/a")
	r.Header.Set("Authorization", "explicit")
	m.Apply(context.Background(), "k", Config{Type: "bearer", Fields: map[string]string{"token": "t"}}, r)
	if r.Header.Get("Authorization") != "explicit" {
		t.Fatal("explicit header must win")
	}

	r = newReq("GET", "https://example.com/a?x=1")
	m.Apply(context.Background(), "k", Config{Type: "apikey", Fields: map[string]string{"key": "api_key", "value": "v1", "in": "query"}}, r)
	u, _ := url.Parse(r.URL)
	if u.Query().Get("api_key") != "v1" || u.Query().Get("x") != "1" {
		t.Fatalf("apikey query: %s", r.URL)
	}
	r = newReq("GET", "https://example.com/")
	m.Apply(context.Background(), "k", Config{Type: "apikey", Fields: map[string]string{"key": "X-Key", "value": "v2"}}, r)
	if r.Header.Get("X-Key") != "v2" {
		t.Fatal("apikey header")
	}
	if _, err := m.Apply(context.Background(), "k", Config{Type: "ntlm"}, r); err == nil {
		t.Fatal("want unsupported")
	}
}

func TestJWTHS256Vector(t *testing.T) {
	// jwt.io reference token for secret "your-256-bit-secret".
	got, err := SignJWT("HS256", []byte("your-256-bit-secret"), nil,
		map[string]any{"sub": "1234567890", "name": "John Doe", "iat": 1516239022})
	if err != nil {
		t.Fatal(err)
	}
	// Header/payload key order differs (Go sorts), so verify via round trip.
	parts := strings.Split(got, ".")
	if len(parts) != 3 {
		t.Fatal(got)
	}
	again, _ := SignJWT("HS256", []byte("your-256-bit-secret"), nil, map[string]any{"sub": "1234567890", "name": "John Doe", "iat": 1516239022})
	if again != got {
		t.Fatal("not deterministic")
	}
	sig, _ := signBytes("HS256", []byte("your-256-bit-secret"), []byte(parts[0]+"."+parts[1]))
	if b64.EncodeToString(sig) != parts[2] {
		t.Fatal("bad signature")
	}
	// Known jwt.io token verifies with our HMAC.
	known := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ"
	ks, _ := signBytes("HS256", []byte("your-256-bit-secret"), []byte(known))
	if b64.EncodeToString(ks) != "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c" {
		t.Fatal("HS256 vector mismatch")
	}
}

func TestJWTRSAandES(t *testing.T) {
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(rk)
	rpem := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	tok, err := SignJWT("RS256", rpem, nil, map[string]any{"a": 1})
	if err != nil || strings.Count(tok, ".") != 2 {
		t.Fatal(err, tok)
	}
	ek, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	eder, _ := x509.MarshalECPrivateKey(ek)
	epem := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: eder})
	tok, err = SignJWT("ES256", epem, nil, map[string]any{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	sig, _ := b64.DecodeString(strings.Split(tok, ".")[2])
	if len(sig) != 64 {
		t.Fatalf("ES256 sig len %d", len(sig))
	}
	if _, err := SignJWT("RS256", []byte("nope"), nil, nil); err == nil {
		t.Fatal("bad key accepted")
	}
}

func TestJWTApplyScrubsSecret(t *testing.T) {
	now := time.Unix(1700000000, 0)
	m := New(Options{Now: func() time.Time { return now }})
	r := newReq("GET", "https://example.com/")
	_, err := m.Apply(context.Background(), "k", Config{Type: "jwt", Fields: map[string]string{
		"algorithm": "HS256", "secret": canary, "payload": `{"sub":"x"}`, "expiresIn": "60", "addIssuedAt": "true"}}, r)
	if err != nil {
		t.Fatal(err)
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	pl, _ := b64.DecodeString(strings.Split(tok, ".")[1])
	var c map[string]any
	json.Unmarshal(pl, &c)
	if c["exp"].(float64) != 1700000060 || c["iat"].(float64) != 1700000000 {
		t.Fatalf("claims %v", c)
	}
	if strings.Contains(m.Scrub("x "+tok+" "+canary), tok) {
		t.Fatal("token not scrubbed")
	}
	if _, err := m.Apply(context.Background(), "k", Config{Type: "jwt", Fields: map[string]string{"algorithm": "none", "secret": "s"}}, newReq("GET", "https://example.com/")); err == nil {
		t.Fatal("alg none must be rejected")
	}
}

func TestDigestRFC7616Vector(t *testing.T) {
	// RFC 2617 section 3.5 example (MD5, qop=auth).
	c := DigestChallenge{Realm: "testrealm@host.com", Nonce: "dcd98b7102dd2f0e8b11d0f600bfb0c093", Opaque: "5ccc069c403ebaf9f0171e9517f40e41", QOP: "auth"}
	h, err := DigestHeader(c, "Mufasa", "Circle Of Life", "GET", "/dir/index.html", nil, "0a4f113b", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h, `response="6629fae49393a05397450978507c4ef1"`) {
		t.Fatalf("digest: %s", h)
	}
}

func TestDigestAgainstServer(t *testing.T) {
	var probes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a := r.Header.Get("Authorization"); a != "" {
			if strings.HasPrefix(a, "Digest ") && strings.Contains(a, `username="bob"`) && strings.Contains(a, "nc=00000001") {
				w.WriteHeader(200)
				return
			}
			w.WriteHeader(403)
			return
		}
		atomic.AddInt32(&probes, 1)
		w.Header().Set("WWW-Authenticate", `Digest realm="r", nonce="abc123", qop="auth", algorithm=SHA-256`)
		w.WriteHeader(401)
	}))
	defer srv.Close()
	m := New(Options{})
	r := newReq("GET", srv.URL+"/p?q=1")
	if _, err := m.Apply(context.Background(), "k", Config{Type: "digest", Fields: map[string]string{"username": "bob", "password": canary}}, r); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", r.URL, nil)
	req.Header = r.Header
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 200 || probes != 1 {
		t.Fatalf("status %d probes %d", resp.StatusCode, probes)
	}
	if strings.Contains(m.Scrub(r.Header.Get("Authorization")), `response="`) && m.Secrets().Contains(canary) == false {
		t.Fatal("secret not tracked")
	}
}

func TestSigV4GetVanilla(t *testing.T) {
	// AWS sigv4 test suite "get-vanilla".
	r := newReq("GET", "https://example.amazonaws.com/")
	err := SignSigV4(r, SigV4Params{
		AccessKey: "AKIDEXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		Region: "us-east-1", Service: "service", Time: time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders=host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"
	if got := r.Header.Get("Authorization"); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if err := SignSigV4(newReq("GET", "https://x/"), SigV4Params{}); err == nil {
		t.Fatal("missing params accepted")
	}
}

type tokenServer struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []url.Values
	auth  []string
	n     int
}

func newTokenServer(t *testing.T, expires int) *tokenServer {
	ts := &tokenServer{}
	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		ts.mu.Lock()
		ts.n++
		n := ts.n
		ts.calls = append(ts.calls, r.PostForm)
		ts.auth = append(ts.auth, r.Header.Get("Authorization"))
		ts.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		out := map[string]any{"access_token": "access-token-" + string(rune('0'+n)), "token_type": "Bearer", "expires_in": expires}
		if r.PostForm.Get("grant_type") != "refresh_token" {
			out["refresh_token"] = "refresh-token-1"
		}
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(ts.srv.Close)
	return ts
}

func oauthCfg(ts *tokenServer, grant string) Config {
	return Config{Type: "oauth2", Fields: map[string]string{
		"grantType": grant, "accessTokenUrl": ts.srv.URL + "/token", "clientId": "cid", "clientSecret": canary,
		"username": "u", "password": "p", "scope": "read",
	}}
}

func TestOAuth2ClientCredentialsAndAutoRefresh(t *testing.T) {
	ts := newTokenServer(t, 100)
	clock := time.Unix(1700000000, 0)
	var cmu sync.Mutex
	m := New(Options{Now: func() time.Time { cmu.Lock(); defer cmu.Unlock(); return clock }})
	cfg := oauthCfg(ts, "client_credentials")
	r := newReq("GET", "https://example.com/")
	res, err := m.Apply(context.Background(), "c1", cfg, r)
	if err != nil || !res.Refreshed || r.Header.Get("Authorization") != "Bearer access-token-1" {
		t.Fatalf("%v %+v %q", err, res, r.Header.Get("Authorization"))
	}
	if ts.auth[0] != "Basic "+base64.StdEncoding.EncodeToString([]byte("cid:"+canary)) {
		t.Fatalf("client auth: %s", ts.auth[0])
	}
	if ts.calls[0].Get("grant_type") != "client_credentials" || ts.calls[0].Get("scope") != "read" {
		t.Fatalf("form %v", ts.calls[0])
	}
	// Cached: no new call.
	r2 := newReq("GET", "https://example.com/")
	res, _ = m.Apply(context.Background(), "c1", cfg, r2)
	if res.Refreshed || ts.n != 1 {
		t.Fatalf("expected cache hit, calls=%d", ts.n)
	}
	// Within skew of expiry: refresh_token grant used.
	cmu.Lock()
	clock = clock.Add(80 * time.Second)
	cmu.Unlock()
	r3 := newReq("GET", "https://example.com/")
	res, err = m.Apply(context.Background(), "c1", cfg, r3)
	if err != nil || !res.Refreshed || ts.n != 2 {
		t.Fatalf("refresh: %v %+v n=%d", err, res, ts.n)
	}
	if ts.calls[1].Get("grant_type") != "refresh_token" || ts.calls[1].Get("refresh_token") != "refresh-token-1" {
		t.Fatalf("refresh form %v", ts.calls[1])
	}
	if r3.Header.Get("Authorization") != "Bearer access-token-2" {
		t.Fatal(r3.Header.Get("Authorization"))
	}
	tok, _ := m.store.Get("c1")
	if tok.Refresh != "refresh-token-1" {
		t.Fatal("refresh token must be retained")
	}
	b, _ := json.Marshal(tok)
	if strings.Contains(string(b), "access-token-2") || strings.Contains(string(b), "refresh-token-1") {
		t.Fatalf("token JSON leaks: %s", b)
	}
	if strings.Contains(m.Scrub("tok access-token-2 secret "+canary), "access-token-2") {
		t.Fatal("access token not scrubbed")
	}
}

func TestOAuth2PasswordBodyAuthAndErrors(t *testing.T) {
	ts := newTokenServer(t, 0)
	m := New(Options{})
	cfg := oauthCfg(ts, "password")
	cfg.Fields["clientAuth"] = "body"
	r := newReq("GET", "https://example.com/")
	if _, err := m.Apply(context.Background(), "k", cfg, r); err != nil {
		t.Fatal(err)
	}
	f := ts.calls[0]
	if f.Get("username") != "u" || f.Get("client_secret") != canary || ts.auth[0] != "" {
		t.Fatalf("form %v auth %q", f, ts.auth[0])
	}
	// Failing endpoint: error must not leak the client secret.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"bad","detail":"` + canary + `"}`))
	}))
	defer bad.Close()
	cfg2 := oauthCfg(ts, "client_credentials")
	cfg2.Fields["accessTokenUrl"] = bad.URL
	_, err := m.Apply(context.Background(), "k2", cfg2, newReq("GET", "https://example.com/"))
	if err == nil || strings.Contains(err.Error(), canary) {
		t.Fatalf("err must exist and be scrubbed: %v", err)
	}
}

func TestOAuth2AuthCodePKCE(t *testing.T) {
	var gotVerifier, gotCode, gotRedirect string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotVerifier, gotCode, gotRedirect = r.PostForm.Get("code_verifier"), r.PostForm.Get("code"), r.PostForm.Get("redirect_uri")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "ac-tok", "refresh_token": "rf", "expires_in": 3600, "token_type": "Bearer"})
	}))
	defer idp.Close()
	m := New(Options{})
	cfg := Config{Type: "oauth2", Fields: map[string]string{
		"grantType": "authorization_code", "authUrl": "https://idp.example.com/authorize",
		"accessTokenUrl": idp.URL, "clientId": "cid", "scope": "openid"}}
	redirect := "http://127.0.0.1:9966/api/collections/oauth/callback"

	// Without consent there is no token.
	if _, err := m.Apply(context.Background(), "ck", cfg, newReq("GET", "https://example.com/")); err == nil {
		t.Fatal("authorization_code without token must error")
	}
	au, state, err := m.BeginAuthCode("ck", cfg, redirect)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(au)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("state") != state || q.Get("redirect_uri") != redirect {
		t.Fatalf("auth url %s", au)
	}
	cb := httptest.NewServer(m.CallbackHandler())
	defer cb.Close()
	resp, _ := http.Get(cb.URL + "/?state=forged&code=x")
	if resp.StatusCode != 400 {
		t.Fatal("forged state accepted")
	}
	resp, _ = http.Get(cb.URL + "/?state=" + url.QueryEscape(state) + "&code=thecode")
	if resp.StatusCode != 200 {
		t.Fatalf("callback %d", resp.StatusCode)
	}
	if gotCode != "thecode" || gotRedirect != redirect || PKCEChallenge(gotVerifier) != q.Get("code_challenge") {
		t.Fatalf("exchange %q %q %q", gotCode, gotRedirect, gotVerifier)
	}
	// State is one-shot.
	resp, _ = http.Get(cb.URL + "/?state=" + url.QueryEscape(state) + "&code=thecode")
	if resp.StatusCode != 400 {
		t.Fatal("state replay accepted")
	}
	r := newReq("GET", "https://example.com/")
	if _, err := m.Apply(context.Background(), "ck", cfg, r); err != nil || r.Header.Get("Authorization") != "Bearer ac-tok" {
		t.Fatalf("%v %q", err, r.Header.Get("Authorization"))
	}
}

func TestPKCEChallengeRFC7636Vector(t *testing.T) {
	if got := PKCEChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatal(got)
	}
}

func TestPendingExpires(t *testing.T) {
	now := time.Unix(1, 0)
	m := New(Options{Now: func() time.Time { return now }})
	cfg := Config{Fields: map[string]string{"authUrl": "https://i.example.com/a", "accessTokenUrl": "https://i.example.com/t", "clientId": "c"}}
	_, st, _ := m.BeginAuthCode("k", cfg, "http://127.0.0.1/cb")
	now = now.Add(pendingTTL + time.Second)
	if err := m.CompleteAuthCode(context.Background(), st, "c"); err == nil {
		t.Fatal("expired state accepted")
	}
}

func TestClientTLSConfig(t *testing.T) {
	if _, err := ClientTLSConfig([]byte("x"), []byte("y"), nil, false); err == nil {
		t.Fatal("garbage cert accepted")
	}
	cfg, err := ClientTLSConfig(nil, nil, nil, true)
	if err != nil || !cfg.InsecureSkipVerify {
		t.Fatal(err)
	}
}

func TestMemoryStoreConcurrent(t *testing.T) {
	s := NewMemoryStore()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.Put("k", Token{Access: "a"}); s.Get("k"); s.Delete("x") }()
	}
	wg.Wait()
}

// Postman and OpenAPI imports write snake_case oauth2 field names and the
// "queryParams" token placement; the suite must honour them as written.
func TestOAuth2AcceptsImportedFieldNames(t *testing.T) {
	ts := newTokenServer(t, 100)
	m := New(Options{})
	cfg := Config{Type: "oauth2", Fields: map[string]string{
		"grant_type": "client_credentials", "accessTokenUrl": ts.srv.URL + "/token", "clientId": "cid",
		"clientSecret": canary, "client_authentication": "body", "addTokenTo": "queryParams",
	}}
	r := newReq("GET", "https://example.com/x")
	if _, err := m.Apply(context.Background(), "k", cfg, r); err != nil {
		t.Fatal(err)
	}
	if ts.calls[0].Get("grant_type") != "client_credentials" {
		t.Fatalf("grant_type alias ignored: %v", ts.calls[0])
	}
	if ts.auth[0] != "" || ts.calls[0].Get("client_id") != "cid" {
		t.Fatalf("client_authentication=body must send credentials in the form: auth=%q form=%v", ts.auth[0], ts.calls[0])
	}
	if !strings.Contains(r.URL, "access_token=access-token-1") || r.Header.Get("Authorization") != "" {
		t.Fatalf("addTokenTo=queryParams must put the token in the query: %s %v", r.URL, r.Header)
	}
}
