# Interceptor Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the foundation of Interceptor — a persistent, lean storage layer and a runnable HTTP forward proxy that captures every flow (metadata to SQLite, bodies to an on-disk content-addressed store) without ever holding bodies in memory.

**Architecture:** A Go module with four focused packages built bottom-up: `store` (SQLite metadata + content-addressed body files), `capture` (streams bodies into the store via `io.TeeReader` while they forward), `proxy` (an `http.Handler` forward proxy that tees request/response bodies and records flows), and `cmd/interceptor` (wires them and listens on `127.0.0.1:8080`). HTTPS/TLS interception, intercept, the control API, and the UI are later plans — this plan is HTTP-only and proves the capture + bounded-memory approach.

**Tech Stack:** Go 1.23, `modernc.org/sqlite` (pure-Go SQLite driver, no cgo → single static binary), Go standard library (`net/http`, `crypto/sha256`, `database/sql`).

**Scope of this plan (maps to spec `docs/superpowers/specs/2026-06-22-interceptor-proxy-core-design.md`):** the `store` and `capture` packages, the HTTP path of `proxy`, and `cmd/interceptor`. Deferred to later plans: TLS MITM (`tlsca`, `CONNECT`), `intercept` + `rules`, `control` API/WS, the React UI, runtime listener rebind, gzip body compression, backpressure-driven truncation.

**Convention:** every commit ends with the trailer `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`. Run all `go` commands from the repo root.

---

### Task 1: Project scaffold

**Files:**
- Create: `go.mod` (via `go mod init`)
- Create: directory layout `internal/store/`, `internal/capture/`, `internal/proxy/`, `cmd/interceptor/`

- [ ] **Step 1: Initialize the module and add the SQLite driver**

Run:
```bash
go mod init github.com/Veyal/interceptor
go get modernc.org/sqlite@latest
mkdir -p internal/store internal/capture internal/proxy cmd/interceptor
```
Expected: `go.mod` created with `module github.com/Veyal/interceptor` and a `require modernc.org/sqlite ...` line; `go.sum` written.

- [ ] **Step 2: Verify the toolchain builds an empty module**

Run: `go build ./...`
Expected: exits 0 with no output (no packages to build yet).

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: scaffold Go module and dependencies" \
  -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: `store` — flows + settings

**Files:**
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/store_test.go`:
```go
package store

import (
	"testing"
	"time"
)

func TestInsertAndGetFlow(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	in := &Flow{
		TS:         time.UnixMilli(1_700_000_000_000),
		Method:     "GET",
		Scheme:     "http",
		Host:       "example.com",
		Port:       80,
		Path:       "/hello?x=1",
		Status:     200,
		ReqHeaders: map[string][]string{"Accept": {"application/json"}},
		ResHeaders: map[string][]string{"Content-Type": {"text/plain"}},
		Mime:       "text/plain",
		ClientAddr: "127.0.0.1:55555",
	}
	id, err := s.InsertFlow(in)
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := s.GetFlow(id)
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	if got.Method != "GET" || got.Host != "example.com" || got.Path != "/hello?x=1" {
		t.Fatalf("unexpected flow: %+v", got)
	}
	if got.Status != 200 || got.Mime != "text/plain" {
		t.Fatalf("unexpected status/mime: %+v", got)
	}
	if got.ReqHeaders["Accept"][0] != "application/json" {
		t.Fatalf("headers not round-tripped: %+v", got.ReqHeaders)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, ok, _ := s.GetSetting("proxy.addr"); ok {
		t.Fatal("expected missing setting")
	}
	if err := s.SetSetting("proxy.addr", "127.0.0.1:8080"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	v, ok, err := s.GetSetting("proxy.addr")
	if err != nil || !ok || v != "127.0.0.1:8080" {
		t.Fatalf("GetSetting = %q, %v, %v", v, ok, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestInsertAndGetFlow|TestSettingsRoundTrip' -v`
Expected: FAIL — compile error, `undefined: Open` / `undefined: Flow`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/store/store.go`:
```go
// Package store persists flow metadata in SQLite and bodies on disk.
package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store owns the SQLite database and the on-disk body directory.
type Store struct {
	db        *sql.DB
	bodiesDir string
}

// Flow is one captured request/response exchange. Bodies are referenced by
// content hash, never embedded.
type Flow struct {
	ID          int64
	TS          time.Time
	Method      string
	Scheme      string
	Host        string
	Port        int
	Path        string
	HTTPVersion string
	Status      int
	ReqHeaders  map[string][]string
	ResHeaders  map[string][]string
	ReqBodyHash string
	ResBodyHash string
	ReqLen      int64
	ResLen      int64
	Mime        string
	DurationMs  int64
	ClientAddr  string
	Error       string
}

const schema = `
CREATE TABLE IF NOT EXISTS flows (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  method TEXT, scheme TEXT, host TEXT, port INTEGER, path TEXT,
  http_version TEXT, status INTEGER,
  req_headers TEXT, res_headers TEXT,
  req_body_hash TEXT, res_body_hash TEXT,
  req_len INTEGER, res_len INTEGER,
  mime TEXT, duration_ms INTEGER, client_addr TEXT, error TEXT,
  flags INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_flows_host ON flows(host);
CREATE INDEX IF NOT EXISTS idx_flows_status ON flows(status);
CREATE INDEX IF NOT EXISTS idx_flows_method ON flows(method);
CREATE INDEX IF NOT EXISTS idx_flows_ts ON flows(ts);

CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

// Open creates (or opens) the database and body store under dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	bodiesDir := filepath.Join(dir, "bodies")
	if err := os.MkdirAll(bodiesDir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "interceptor.db"))
	if err != nil {
		return nil, err
	}
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA synchronous=NORMAL;",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, bodiesDir: bodiesDir}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// InsertFlow stores a new flow and sets f.ID to the assigned row id.
func (s *Store) InsertFlow(f *Flow) (int64, error) {
	rh, _ := json.Marshal(f.ReqHeaders)
	sh, _ := json.Marshal(f.ResHeaders)
	res, err := s.db.Exec(
		`INSERT INTO flows
		 (ts, method, scheme, host, port, path, http_version, status,
		  req_headers, res_headers, req_body_hash, res_body_hash,
		  req_len, res_len, mime, duration_ms, client_addr, error)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.TS.UnixMilli(), f.Method, f.Scheme, f.Host, f.Port, f.Path, f.HTTPVersion, f.Status,
		string(rh), string(sh), f.ReqBodyHash, f.ResBodyHash,
		f.ReqLen, f.ResLen, f.Mime, f.DurationMs, f.ClientAddr, f.Error)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	f.ID = id
	return id, nil
}

// GetFlow loads a single flow by id.
func (s *Store) GetFlow(id int64) (*Flow, error) {
	row := s.db.QueryRow(
		`SELECT id, ts, method, scheme, host, port, path, http_version, status,
		        req_headers, res_headers, req_body_hash, res_body_hash,
		        req_len, res_len, mime, duration_ms, client_addr, error
		 FROM flows WHERE id = ?`, id)
	return scanFlow(row)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanFlow(row scanner) (*Flow, error) {
	var (
		f          Flow
		tsMillis   int64
		reqH, resH string
	)
	if err := row.Scan(
		&f.ID, &tsMillis, &f.Method, &f.Scheme, &f.Host, &f.Port, &f.Path, &f.HTTPVersion, &f.Status,
		&reqH, &resH, &f.ReqBodyHash, &f.ResBodyHash,
		&f.ReqLen, &f.ResLen, &f.Mime, &f.DurationMs, &f.ClientAddr, &f.Error,
	); err != nil {
		return nil, err
	}
	f.TS = time.UnixMilli(tsMillis)
	_ = json.Unmarshal([]byte(reqH), &f.ReqHeaders)
	_ = json.Unmarshal([]byte(resH), &f.ResHeaders)
	return &f, nil
}

// SetSetting upserts a key/value setting.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// GetSetting returns the value and whether it was present.
func (s *Store) GetSetting(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestInsertAndGetFlow|TestSettingsRoundTrip' -v`
Expected: PASS (both tests `ok`).

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): SQLite flow metadata + settings" \
  -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: `store` — content-addressed body store

**Files:**
- Create: `internal/store/bodies.go`
- Test: `internal/store/bodies_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/bodies_test.go`:
```go
package store

import (
	"io"
	"testing"
)

func TestBodyWriterStoreDedupAndRead(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	write := func(data string) (string, int64) {
		w, err := s.NewBodyWriter()
		if err != nil {
			t.Fatalf("NewBodyWriter: %v", err)
		}
		if _, err := io.WriteString(w, data); err != nil {
			t.Fatalf("write: %v", err)
		}
		hash, n, err := w.Finalize()
		if err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		return hash, n
	}

	h1, n1 := write("hello world")
	h2, n2 := write("hello world") // identical -> dedup, same hash
	if h1 != h2 {
		t.Fatalf("expected identical hashes, got %s vs %s", h1, h2)
	}
	if n1 != 11 || n2 != 11 {
		t.Fatalf("expected len 11, got %d/%d", n1, n2)
	}

	rc, err := s.OpenBody(h1)
	if err != nil {
		t.Fatalf("OpenBody: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "hello world" {
		t.Fatalf("body mismatch: %q", got)
	}

	empty, err := s.OpenBody("")
	if err != nil {
		t.Fatalf("OpenBody empty: %v", err)
	}
	defer empty.Close()
	if b, _ := io.ReadAll(empty); len(b) != 0 {
		t.Fatalf("expected empty body for empty hash, got %q", b)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestBodyWriterStoreDedupAndRead -v`
Expected: FAIL — `undefined: (*Store).NewBodyWriter` / `OpenBody`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/store/bodies.go`:
```go
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BodyWriter streams bytes to a temp file while hashing them, then commits
// the file to a content-addressed path on Finalize. Safe for bounded memory:
// bytes are never buffered whole.
type BodyWriter struct {
	s   *Store
	tmp *os.File
	h   hash.Hash
	n   int64
}

// NewBodyWriter starts a new body capture.
func (s *Store) NewBodyWriter() (*BodyWriter, error) {
	tmp, err := os.CreateTemp(s.bodiesDir, ".tmp-*")
	if err != nil {
		return nil, err
	}
	return &BodyWriter{s: s, tmp: tmp, h: sha256.New()}, nil
}

// Write implements io.Writer.
func (w *BodyWriter) Write(p []byte) (int, error) {
	n, err := w.tmp.Write(p)
	w.h.Write(p[:n])
	w.n += int64(n)
	return n, err
}

// Finalize commits the body and returns its sha256 hex hash and byte length.
// If a body with the same hash already exists it is deduplicated.
func (w *BodyWriter) Finalize() (string, int64, error) {
	if err := w.tmp.Close(); err != nil {
		return "", 0, err
	}
	sum := hex.EncodeToString(w.h.Sum(nil))
	dst := w.s.bodyPath(sum)
	if _, err := os.Stat(dst); err == nil {
		os.Remove(w.tmp.Name()) // already stored
		return sum, w.n, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", 0, err
	}
	if err := os.Rename(w.tmp.Name(), dst); err != nil {
		return "", 0, err
	}
	return sum, w.n, nil
}

// Abort discards an in-progress body (e.g. on error).
func (w *BodyWriter) Abort() {
	w.tmp.Close()
	os.Remove(w.tmp.Name())
}

func (s *Store) bodyPath(sum string) string {
	return filepath.Join(s.bodiesDir, sum[:2], sum[2:4], sum)
}

// OpenBody returns a reader for the body with the given hash. An empty hash
// yields an empty reader (no body).
func (s *Store) OpenBody(sum string) (io.ReadCloser, error) {
	if sum == "" {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return os.Open(s.bodyPath(sum))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS (all store tests `ok`).

- [ ] **Step 5: Commit**

```bash
git add internal/store/bodies.go internal/store/bodies_test.go
git commit -m "feat(store): content-addressed body store with dedup" \
  -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: `capture` — tee bodies into the store while streaming

**Files:**
- Create: `internal/capture/capture.go`
- Test: `internal/capture/capture_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/capture/capture_test.go`:
```go
package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	"github.com/Veyal/interceptor/internal/store"
)

func TestTeeBodyStreamsAndStores(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	c := New(s)

	const payload = "the quick brown fox"
	tee, finalize, err := c.TeeBody(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("TeeBody: %v", err)
	}

	// Reading the tee yields the original bytes (proves streaming pass-through).
	got, _ := io.ReadAll(tee)
	if string(got) != payload {
		t.Fatalf("tee passthrough mismatch: %q", got)
	}

	hash, n, err := finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	want := sha256.Sum256([]byte(payload))
	if hash != hex.EncodeToString(want[:]) {
		t.Fatalf("hash mismatch: %s", hash)
	}
	if n != int64(len(payload)) {
		t.Fatalf("len mismatch: %d", n)
	}

	rc, err := s.OpenBody(hash)
	if err != nil {
		t.Fatalf("OpenBody: %v", err)
	}
	defer rc.Close()
	stored, _ := io.ReadAll(rc)
	if string(stored) != payload {
		t.Fatalf("stored body mismatch: %q", stored)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/capture/ -run TestTeeBodyStreamsAndStores -v`
Expected: FAIL — `undefined: New` / `undefined: (*Capturer).TeeBody`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/capture/capture.go`:
```go
// Package capture streams HTTP bodies into the store as they pass through,
// without buffering them in memory.
package capture

import (
	"io"

	"github.com/Veyal/interceptor/internal/store"
)

// Capturer creates body-capturing tees backed by a Store.
type Capturer struct {
	st *store.Store
}

// New returns a Capturer writing to st.
func New(st *store.Store) *Capturer { return &Capturer{st: st} }

// TeeBody wraps src so that every byte read from the returned reader is also
// written to the store. Call finalize after the reader is fully consumed to
// commit the body and obtain its hash and length. If src is nil, the returned
// reader is empty and finalize reports an empty body.
func (c *Capturer) TeeBody(src io.Reader) (r io.Reader, finalize func() (string, int64, error), err error) {
	if src == nil {
		return nil, func() (string, int64, error) { return "", 0, nil }, nil
	}
	w, err := c.st.NewBodyWriter()
	if err != nil {
		return nil, nil, err
	}
	return io.TeeReader(src, w), w.Finalize, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/capture/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/capture/capture.go internal/capture/capture_test.go
git commit -m "feat(capture): stream bodies into the store via TeeReader" \
  -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: `proxy` — HTTP forward proxy that captures flows

**Files:**
- Create: `internal/proxy/proxy.go`
- Test: `internal/proxy/proxy_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/proxy/proxy_test.go`:
```go
package proxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interceptor/internal/capture"
	"github.com/Veyal/interceptor/internal/store"
)

func TestProxyForwardsAndCapturesFlow(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		io.WriteString(w, "echo:"+string(body))
	}))
	defer upstream.Close()

	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	srv := New(s, capture.New(s))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)

	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}

	resp, err := client.Post(upstream.URL+"/submit", "text/plain", strings.NewReader("ping"))
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(got) != "echo:ping" {
		t.Fatalf("unexpected response body: %q", got)
	}

	// Allow the proxy goroutine to finish recording the flow.
	deadline := time.Now().Add(2 * time.Second)
	var flows []*store.Flow
	for time.Now().Before(deadline) {
		flows, _ = s.QueryFlows(10)
		if len(flows) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(flows) != 1 {
		t.Fatalf("expected 1 captured flow, got %d", len(flows))
	}
	f := flows[0]
	if f.Method != "POST" || f.Status != 200 || f.Path != "/submit" {
		t.Fatalf("unexpected flow: %+v", f)
	}
	if f.ReqLen != 4 { // "ping"
		t.Fatalf("expected req body len 4, got %d", f.ReqLen)
	}
	if f.ResBodyHash == "" {
		t.Fatal("expected response body to be captured")
	}
}
```

> Note: this test calls `s.QueryFlows(10)`, which Task 2 did not implement. Add it as part of Step 3 below.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy/ -run TestProxyForwardsAndCapturesFlow -v`
Expected: FAIL — `undefined: New` (proxy) and `undefined: (*Store).QueryFlows`.

- [ ] **Step 3: Write minimal implementation**

First, add `QueryFlows` to `internal/store/store.go` (append this method):
```go
// QueryFlows returns up to limit flows, newest first.
func (s *Store) QueryFlows(limit int) ([]*Flow, error) {
	rows, err := s.db.Query(
		`SELECT id, ts, method, scheme, host, port, path, http_version, status,
		        req_headers, res_headers, req_body_hash, res_body_hash,
		        req_len, res_len, mime, duration_ms, client_addr, error
		 FROM flows ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Flow
	for rows.Next() {
		f, err := scanFlow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
```

Then create `internal/proxy/proxy.go`:
```go
// Package proxy implements an HTTP forward proxy that captures every flow.
package proxy

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Veyal/interceptor/internal/capture"
	"github.com/Veyal/interceptor/internal/store"
)

// Server is the forward-proxy HTTP handler.
type Server struct {
	st  *store.Store
	cap *capture.Capturer
	tr  *http.Transport
}

// New builds a proxy Server backed by st and cap.
func New(st *store.Store, cap *capture.Capturer) *Server {
	return &Server{
		st:  st,
		cap: cap,
		tr: &http.Transport{
			Proxy:                 nil, // dial upstream directly
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

// Serve accepts connections on ln until it is closed.
func (s *Server) Serve(ln net.Listener) error {
	return (&http.Server{Handler: s}).Serve(ln)
}

// hopHeaders are stripped when forwarding (RFC 7230 §6.1).
var hopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func removeHopHeaders(h http.Header) {
	for _, k := range hopHeaders {
		h.Del(k)
	}
}

func copyHeader(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		// HTTPS (CONNECT) is implemented in the TLS-MITM plan.
		http.Error(w, "CONNECT not supported yet", http.StatusNotImplemented)
		return
	}
	if !r.URL.IsAbs() {
		http.Error(w, "this is a forward proxy; use an absolute-URI request", http.StatusBadRequest)
		return
	}

	start := time.Now()
	port := 80
	if ps := r.URL.Port(); ps != "" {
		port, _ = strconv.Atoi(ps)
	}
	flow := &store.Flow{
		TS:          start,
		Method:      r.Method,
		Scheme:      "http",
		Host:        r.URL.Hostname(),
		Port:        port,
		Path:        r.URL.RequestURI(),
		HTTPVersion: r.Proto,
		ClientAddr:  r.RemoteAddr,
		ReqHeaders:  r.Header.Clone(),
	}

	out := r.Clone(r.Context())
	out.RequestURI = ""
	removeHopHeaders(out.Header)

	reqTee, reqFinalize, err := s.cap.TeeBody(r.Body)
	if err != nil {
		s.fail(w, flow, "capture init: "+err.Error())
		return
	}
	if reqTee != nil {
		out.Body = io.NopCloser(reqTee)
	}

	resp, err := s.tr.RoundTrip(out)
	if err != nil {
		// Request body has (at most partially) been read; finalize to avoid leaking the temp file.
		reqHash, reqLen, _ := reqFinalize()
		flow.ReqBodyHash, flow.ReqLen = reqHash, reqLen
		s.fail(w, flow, "upstream: "+err.Error())
		return
	}
	defer resp.Body.Close()

	reqHash, reqLen, _ := reqFinalize()
	flow.ReqBodyHash, flow.ReqLen = reqHash, reqLen

	removeHopHeaders(resp.Header)
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	resTee, resFinalize, err := s.cap.TeeBody(resp.Body)
	if err != nil {
		s.fail(w, flow, "capture resp: "+err.Error())
		return
	}
	io.Copy(w, resTee)
	resHash, resLen, _ := resFinalize()

	flow.Status = resp.StatusCode
	flow.ResHeaders = resp.Header.Clone()
	flow.ResBodyHash, flow.ResLen = resHash, resLen
	flow.Mime = resp.Header.Get("Content-Type")
	flow.DurationMs = time.Since(start).Milliseconds()
	s.st.InsertFlow(flow)
}

// fail records an errored flow and writes a 502 to the client.
func (s *Server) fail(w http.ResponseWriter, flow *store.Flow, msg string) {
	flow.Status = http.StatusBadGateway
	flow.Error = msg
	flow.DurationMs = time.Since(flow.TS).Milliseconds()
	s.st.InsertFlow(flow)
	http.Error(w, msg, http.StatusBadGateway)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -v`
Expected: PASS across `store`, `capture`, and `proxy`.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/proxy.go internal/proxy/proxy_test.go internal/store/store.go
git commit -m "feat(proxy): HTTP forward proxy with flow capture" \
  -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 6: `cmd/interceptor` — runnable binary

**Files:**
- Create: `cmd/interceptor/main.go`

- [ ] **Step 1: Write the implementation**

Create `cmd/interceptor/main.go`:
```go
// Command interceptor runs the Interceptor HTTP proxy.
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Veyal/interceptor/internal/capture"
	"github.com/Veyal/interceptor/internal/proxy"
	"github.com/Veyal/interceptor/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".interceptor")

	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	defer st.Close()

	addr := "127.0.0.1:8080"
	if v, ok, _ := st.GetSetting("proxy.addr"); ok && v != "" {
		addr = v
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	srv := &http.Server{Handler: proxy.New(st, capture.New(st))}
	log.Printf("Interceptor proxy listening on http://%s (data: %s)", addr, dir)

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("serve: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	log.Println("shutting down...")
	return srv.Shutdown(ctx)
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./cmd/interceptor`
Expected: exits 0; a binary `interceptor` is produced in the repo root.

- [ ] **Step 3: Manual smoke test (proxy a real HTTP request)**

Run:
```bash
go run ./cmd/interceptor &
sleep 1
curl -s -x http://127.0.0.1:8080 http://example.com/ -o /dev/null -w "%{http_code}\n"
kill %1
```
Expected: prints `200`. The log line `Interceptor proxy listening on http://127.0.0.1:8080` appeared, and `~/.interceptor/interceptor.db` plus `~/.interceptor/bodies/` now exist.

- [ ] **Step 4: Verify the full suite still passes**

Run: `go test ./...`
Expected: `ok` for all three packages.

- [ ] **Step 5: Commit**

```bash
git add cmd/interceptor/main.go
git commit -m "feat(cmd): runnable interceptor proxy binary" \
  -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Done criteria for this plan

- `go test ./...` passes (`store`, `capture`, `proxy`).
- `go run ./cmd/interceptor` serves a forward proxy on `127.0.0.1:8080`; proxied HTTP requests are captured as flows with bodies on disk.
- RAM does not grow with body size (bodies stream through `io.TeeReader`; never read whole into memory).
- No `cgo` (single static binary): `CGO_ENABLED=0 go build ./cmd/interceptor` succeeds.

## Next plan (preview)

Plan 2 (TLS MITM) adds: `internal/tlsca` (CA generate/load under `~/.interceptor/ca/`, per-host leaf minting + cache), `CONNECT` handling in `proxy` to terminate client TLS, and HTTPS capture — reusing the `capture`/`store` layers unchanged.
