package sender

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// LoginMacro records a login request. Running it extracts session credentials
// (Set-Cookie, Authorization) from the response and applies them to the global
// session headers. Optional TTL refresh and automatic re-auth on 401.
type LoginMacro struct {
	Enabled     bool   `json:"enabled"`
	Target      string `json:"target"`      // scheme://host[:port]
	Request     string `json:"request"`     // raw HTTP request
	RefreshSecs int    `json:"refreshSecs"` // auto-refresh interval; 0 = manual / 401 only
	ReauthOn401 bool   `json:"reauthOn401"` // retry the original send once after re-login
}

// ExtractSessionHeaders pulls auth headers from a login response.
func ExtractSessionHeaders(resp *http.Response) []Header {
	var out []Header
	if auth := resp.Header.Get("Authorization"); auth != "" {
		out = append(out, Header{Key: "Authorization", Value: auth})
	}
	var cookies []string
	for _, c := range resp.Header.Values("Set-Cookie") {
		nameVal, _, _ := strings.Cut(c, ";")
		nameVal = strings.TrimSpace(nameVal)
		if nameVal != "" {
			cookies = append(cookies, nameVal)
		}
	}
	if len(cookies) > 0 {
		out = append(out, Header{Key: "Cookie", Value: strings.Join(cookies, "; ")})
	}
	return out
}

// RunLoginMacro issues the recorded login request and returns session headers.
func RunLoginMacro(cl *http.Client, m LoginMacro) ([]Header, error) {
	if !m.Enabled || m.Target == "" || m.Request == "" {
		return nil, nil
	}
	method, path, headers, body, err := parseRawRequest(m.Request)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, strings.TrimRight(m.Target, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
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
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return ExtractSessionHeaders(resp), nil
}

// loginState tracks the configured login macro and last refresh time.
type loginState struct {
	mu         sync.RWMutex
	macro      LoginMacro
	at         time.Time
	refreshing bool // a TTL refresh is in flight (prevents a thundering herd)
}

// tryBeginRefresh atomically checks whether an auto-refresh is due and not already
// running, claiming it if so. Without this, every concurrent Send at TTL expiry
// (e.g. an Intruder run's N threads) would fire the login macro at once.
func (ls *loginState) tryBeginRefresh() bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if !ls.macro.Enabled || ls.macro.RefreshSecs <= 0 || ls.macro.Request == "" || ls.refreshing {
		return false
	}
	if !ls.at.IsZero() && time.Since(ls.at) < time.Duration(ls.macro.RefreshSecs)*time.Second {
		return false
	}
	ls.refreshing = true
	return true
}

func (ls *loginState) endRefresh() {
	ls.mu.Lock()
	ls.refreshing = false
	ls.mu.Unlock()
}

func (ls *loginState) set(m LoginMacro) {
	ls.mu.Lock()
	ls.macro = m
	ls.mu.Unlock()
}

func (ls *loginState) get() (LoginMacro, time.Time) {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	return ls.macro, ls.at
}

func (ls *loginState) markRefreshed() {
	ls.mu.Lock()
	ls.at = time.Now()
	ls.mu.Unlock()
}

func (s *Sender) SetLoginMacro(m LoginMacro) { s.login.set(m) }

// SetSessionRefresh registers a callback invoked when a login macro produces new
// session headers (auto-refresh, manual run, or 401 re-auth).
func (s *Sender) SetSessionRefresh(fn func([]Header)) { s.refreshSess = fn }

func (s *Sender) maybeRefreshLogin() {
	if !s.login.tryBeginRefresh() {
		return
	}
	defer s.login.endRefresh()
	_, _ = s.runLoginMacro()
}

func (s *Sender) runLoginMacro() ([]Header, error) {
	m, _ := s.login.get()
	hdrs, err := RunLoginMacro(s.cl, m)
	if err != nil || len(hdrs) == 0 {
		return hdrs, err
	}
	s.applySessionHeaders(hdrs)
	if s.refreshSess != nil {
		s.refreshSess(hdrs)
	}
	s.login.markRefreshed()
	return hdrs, nil
}

// applySessionHeaders replaces session headers with the login result.
func (s *Sender) applySessionHeaders(hdrs []Header) {
	s.sess.mu.Lock()
	s.sess.enabled = true
	s.sess.headers = hdrs
	s.sess.mu.Unlock()
}

// RunLoginMacroNow runs the login macro immediately (API / manual re-auth).
func (s *Sender) RunLoginMacroNow() ([]Header, error) { return s.runLoginMacro() }

func (s *Sender) shouldReauth401() bool {
	m, _ := s.login.get()
	return m.Enabled && m.ReauthOn401 && m.Request != ""
}
