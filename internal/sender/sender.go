// Package sender issues one-off HTTP/HTTPS requests directly to a target
// (bypassing the proxy listener) and records each as a flow. It backs the
// Repeater and Intruder modules.
package sender

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Veyal/interceptor/internal/capture"
	"github.com/Veyal/interceptor/internal/store"
)

// Request describes a request to send.
type Request struct {
	Method  string
	URL     string
	Headers map[string][]string
	Body    []byte
	Flags     int64           // e.g. store.FlagRepeater / store.FlagIntruder, OR'd onto the flow
	Context   context.Context // optional: cancel an in-flight send (e.g. an active-scan kill switch)
	NoSession bool            // skip the global session headers + token macro (authz replays carry their own identity)
}

// Header is a single session header applied to outgoing sends.
type Header struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// session holds auth headers auto-applied to every Repeater/Intruder send so a
// tester (or the AI) need not re-paste a token on each request. Guarded for
// concurrent reads (sends) and writes (settings changes).
type session struct {
	mu      sync.RWMutex
	enabled bool
	headers []Header
}

// Macro defines a token-refresh request whose response feeds a value into every
// subsequent send — for CSRF tokens that rotate per request, or to re-auth. The
// refresh request is sent with a plain client (never recorded, never recursive).
type Macro struct {
	Enabled    bool   `json:"enabled"`
	Target     string `json:"target"`     // scheme://host[:port] for the refresh request
	Request    string `json:"request"`    // raw HTTP request (request line + headers + optional body)
	Extract    string `json:"extract"`    // regex with one capture group, run over the refresh RESPONSE
	InjectMode string `json:"injectMode"` // "header" | "placeholder"
	InjectName string `json:"injectName"` // header name, or the placeholder text to replace (e.g. §CSRF§)
}

// Sender sends requests and persists them as flows.
type Sender struct {
	st   *store.Store
	cap  *capture.Capturer
	cl   *http.Client
	sess session

	macroMu sync.RWMutex
	macro   Macro
}

// SetMacro configures the token-refresh macro applied before each send.
func (s *Sender) SetMacro(m Macro) {
	s.macroMu.Lock()
	s.macro = m
	s.macroMu.Unlock()
}

// macroToken runs the refresh request (if enabled) and returns the extracted
// value plus where to inject it. Best-effort: any failure yields "".
func (s *Sender) macroToken() (token, name, mode string) {
	s.macroMu.RLock()
	m := s.macro
	s.macroMu.RUnlock()
	if !m.Enabled || m.Target == "" || m.Request == "" || m.Extract == "" || m.InjectName == "" {
		return "", "", ""
	}
	re, err := regexp.Compile(m.Extract)
	if err != nil {
		return "", "", ""
	}
	method, path, headers, body, err := parseRawRequest(m.Request)
	if err != nil {
		return "", "", ""
	}
	req, err := http.NewRequest(method, strings.TrimRight(m.Target, "/")+path, bytes.NewReader(body))
	if err != nil {
		return "", "", ""
	}
	for k, vs := range headers {
		if http.CanonicalHeaderKey(k) == "Host" {
			if len(vs) > 0 {
				req.Host = vs[0]
			}
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := s.cl.Do(req)
	if err != nil {
		return "", "", ""
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	// Match over status line + headers + body so the token can come from anywhere
	// (Set-Cookie, a header, or an HTML/JSON body).
	var sb strings.Builder
	fmt.Fprintf(&sb, "HTTP/1.1 %d\r\n", resp.StatusCode)
	_ = resp.Header.Write(&sb)
	sb.WriteString("\r\n")
	sb.Write(rb)
	mt := re.FindStringSubmatch(sb.String())
	if len(mt) < 2 {
		return "", "", ""
	}
	mode = m.InjectMode
	if mode == "" {
		mode = "header"
	}
	return mt[1], m.InjectName, mode
}

// parseRawRequest parses a raw HTTP request (request line + headers + body after a
// blank line). Content-Length is dropped (recomputed by the client).
func parseRawRequest(raw string) (method, path string, headers map[string][]string, body []byte, err error) {
	norm := strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\n", "\r\n")
	head := norm
	var bodyStr string
	if i := strings.Index(norm, "\r\n\r\n"); i >= 0 {
		head, bodyStr = norm[:i]+"\r\n\r\n", norm[i+4:]
	} else {
		head = strings.TrimRight(norm, "\r\n") + "\r\n\r\n"
	}
	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(head)))
	if err != nil {
		return "", "", nil, nil, err
	}
	h := req.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	h.Del("Content-Length")
	if req.Host != "" {
		h.Set("Host", req.Host)
	}
	p := req.URL.RequestURI()
	if p == "" {
		p = "/"
	}
	return req.Method, p, h, []byte(bodyStr), nil
}

// SetSession configures the session headers auto-applied to outgoing sends.
func (s *Sender) SetSession(enabled bool, headers []Header) {
	s.sess.mu.Lock()
	s.sess.enabled = enabled
	s.sess.headers = headers
	s.sess.mu.Unlock()
}

// applySession forces the configured session headers onto req (replacing any
// existing value), keeping every send authenticated. No-op when disabled.
func (s *Sender) applySession(req *http.Request) {
	s.sess.mu.RLock()
	defer s.sess.mu.RUnlock()
	if !s.sess.enabled {
		return
	}
	for _, h := range s.sess.headers {
		if h.Key == "" {
			continue
		}
		if http.CanonicalHeaderKey(h.Key) == "Host" {
			req.Host = h.Value
			continue
		}
		req.Header.Set(h.Key, h.Value)
	}
}

// New builds a Sender. The client does not follow redirects (each hop is its own
// flow, like Burp) and does not verify TLS — a security-testing tool routinely
// talks to targets with self-signed or invalid certificates.
func New(st *store.Store, cap *capture.Capturer) *Sender {
	return &Sender{
		st:  st,
		cap: cap,
		cl: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig:       &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // pentest tool, by design
				ResponseHeaderTimeout: 30 * time.Second,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Send issues r, captures the response, and persists a flow. Transport-level
// failures are recorded as an errored flow (502) rather than returned as errors;
// only malformed input returns an error.
func (s *Sender) Send(r Request) (*store.Flow, error) {
	u, err := url.Parse(r.URL)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return nil, fmt.Errorf("invalid request URL %q", r.URL)
	}
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}

	// Token macro: fetch a fresh value (e.g. CSRF) and inject it. Placeholder mode
	// rewrites the outgoing headers/body before the request is built; header mode is
	// applied to req below.
	var macroTok, macroName, macroMode string
	if !r.NoSession {
		macroTok, macroName, macroMode = s.macroToken()
	}
	if macroTok != "" && macroMode == "placeholder" && macroName != "" {
		r.Body = []byte(strings.ReplaceAll(string(r.Body), macroName, macroTok))
		for k, vs := range r.Headers {
			out := make([]string, len(vs))
			for i, v := range vs {
				out[i] = strings.ReplaceAll(v, macroName, macroTok)
			}
			r.Headers[k] = out
		}
	}

	req, err := http.NewRequest(method, r.URL, bytes.NewReader(r.Body))
	if err != nil {
		return nil, err
	}
	if r.Context != nil {
		req = req.WithContext(r.Context) // lets a caller (active-scan kill switch) abort in-flight
	}
	for k, vs := range r.Headers {
		if http.CanonicalHeaderKey(k) == "Host" {
			if len(vs) > 0 {
				req.Host = vs[0]
			}
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if !r.NoSession {
		s.applySession(req) // force session/auth headers (recorded on the flow below)
	}
	if macroTok != "" && macroMode == "header" && macroName != "" {
		req.Header.Set(macroName, macroTok) // inject the fresh macro token as a header
	}

	port := atoiOr(u.Port(), defaultPort(u.Scheme))
	start := time.Now()
	flow := &store.Flow{
		TS:          start,
		Method:      method,
		Scheme:      u.Scheme,
		Host:        u.Hostname(),
		Port:        port,
		Path:        u.RequestURI(),
		HTTPVersion: "HTTP/1.1",
		ReqHeaders:  reqHeaders(req),
		Flags:       r.Flags,
	}
	flow.ReqBodyHash, flow.ReqLen = s.storeBody(r.Body)

	resp, err := s.cl.Do(req)
	if err != nil {
		flow.Status = http.StatusBadGateway
		flow.Error = err.Error()
		flow.DurationMs = time.Since(start).Milliseconds()
		s.persist(flow)
		return flow, nil
	}
	defer resp.Body.Close()

	if tee, finalize, terr := s.cap.TeeBody(resp.Body); terr == nil && tee != nil {
		io.Copy(io.Discard, tee)
		flow.ResBodyHash, flow.ResLen, _ = finalize()
	} else if terr != nil {
		flow.Flags |= store.FlagCaptureError
	}

	flow.Status = resp.StatusCode
	flow.ResHeaders = resp.Header.Clone()
	flow.Mime = resp.Header.Get("Content-Type")
	flow.DurationMs = time.Since(start).Milliseconds()
	s.persist(flow)
	return flow, nil
}

func (s *Sender) persist(flow *store.Flow) { _, _ = s.st.InsertFlow(flow) }

func (s *Sender) storeBody(b []byte) (string, int64) {
	if len(b) == 0 {
		return "", 0
	}
	tee, finalize, err := s.cap.TeeBody(bytes.NewReader(b))
	if err != nil || tee == nil {
		return "", 0
	}
	io.Copy(io.Discard, tee)
	h, n, _ := finalize()
	return h, n
}

func reqHeaders(req *http.Request) map[string][]string {
	h := req.Header.Clone()
	if h == nil {
		h = http.Header{}
	}
	if req.Host != "" {
		h.Set("Host", req.Host)
	} else if req.URL != nil {
		h.Set("Host", req.URL.Host)
	}
	return h
}

func defaultPort(scheme string) int {
	if scheme == "https" {
		return 443
	}
	return 80
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
