// Package sender issues one-off HTTP/HTTPS requests directly to a target
// (bypassing the proxy listener) and records each as a flow. It backs the
// Repeater and Intruder modules.
package sender

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
	Flags   int64 // e.g. store.FlagRepeater / store.FlagIntruder, OR'd onto the flow
}

// Sender sends requests and persists them as flows.
type Sender struct {
	st  *store.Store
	cap *capture.Capturer
	cl  *http.Client
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

	req, err := http.NewRequest(method, r.URL, bytes.NewReader(r.Body))
	if err != nil {
		return nil, err
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
