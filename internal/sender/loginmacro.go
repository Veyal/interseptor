package sender

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/httplines"
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

const maxLoginMacroRefreshSecs = int64(^uint64(0)>>1) / int64(time.Second)

// ValidateLoginMacro rejects refresh intervals that cannot be represented as a
// time.Duration. Keep this at the sender boundary so stored configuration and
// direct package callers cannot bypass the REST validation path.
func ValidateLoginMacro(m LoginMacro) error {
	if m.RefreshSecs < 0 {
		return fmt.Errorf("login macro refreshSecs must not be negative")
	}
	if int64(m.RefreshSecs) > maxLoginMacroRefreshSecs {
		return fmt.Errorf("login macro refreshSecs exceeds the supported duration")
	}
	return nil
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
	_, hdrs, err := runLoginMacroReq(context.Background(), cl, m)
	return hdrs, err
}

// macroRequestTimeout caps a token/login macro request so a stalled macro
// endpoint cannot hang sends, Intruder Stop, Engine.Close or shutdown.
const macroRequestTimeout = 30 * time.Second

// macroContext derives a macro request context from the send's context (nil
// means none) with the macroRequestTimeout cap.
func macroContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, macroRequestTimeout)
}

// runLoginMacroReq issues the login request and returns the response status code
// plus the session headers it yields. Shared by the live run and the dry-run test.
func runLoginMacroReq(parent context.Context, cl *http.Client, m LoginMacro) (int, []Header, error) {
	if !m.Enabled || m.Target == "" || m.Request == "" {
		return 0, nil, nil
	}
	method, path, headers, body, err := httplines.ParseRawRequest(m.Request)
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := macroContext(parent)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(m.Target, "/")+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
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
		return 0, nil, err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, ExtractSessionHeaders(resp), nil
}

// loginState tracks the configured login macro and last refresh time.
type loginState struct {
	mu         sync.RWMutex
	macro      LoginMacro
	at         time.Time
	refreshing bool         // a TTL refresh is in flight (prevents a thundering herd)
	gen        uint64       // bumped on every successful login
	flight     *loginFlight // the 401 re-auth login in flight, shared by waiters
}

// loginFlight is one in-flight 401 re-auth login that concurrent callers wait on.
type loginFlight struct {
	done chan struct{}
	err  error
}

func (ls *loginState) generation() uint64 {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	return ls.gen
}

// joinFlight returns the in-flight re-auth (leader=false) or starts one
// (leader=true). stale is true when a login already completed after the
// caller's send started (seen), so it can retry without logging in again.
func (ls *loginState) joinFlight(seen uint64) (f *loginFlight, leader, stale bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.gen != seen {
		return nil, false, true
	}
	if ls.flight != nil {
		return ls.flight, false, false
	}
	ls.flight = &loginFlight{done: make(chan struct{})}
	return ls.flight, true, false
}

func (ls *loginState) endFlight(f *loginFlight, err error) {
	ls.mu.Lock()
	f.err = err
	ls.flight = nil
	ls.mu.Unlock()
	close(f.done)
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
	ls.gen++
	ls.mu.Unlock()
}

func (s *Sender) SetLoginMacro(m LoginMacro) error {
	if err := ValidateLoginMacro(m); err != nil {
		return err
	}
	s.login.set(m)
	return nil
}

// SetSessionRefresh registers a callback invoked when a login macro produces new
// session headers (auto-refresh, manual run, or 401 re-auth).
func (s *Sender) SetSessionRefresh(fn func([]Header) error) { s.refreshSess = fn }

func (s *Sender) maybeRefreshLogin(ctx context.Context) {
	if !s.login.tryBeginRefresh() {
		return
	}
	defer s.login.endRefresh()
	_, _ = s.runLoginMacro(ctx)
}

// reauth401 makes sure a login has happened since the send that got a 401
// started (seen). Concurrent 401s share one login: the first caller runs it,
// the rest wait for its result (or their own context).
func (s *Sender) reauth401(ctx context.Context, seen uint64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	f, leader, stale := s.login.joinFlight(seen)
	if stale {
		return nil
	}
	if leader {
		_, err := s.runLoginMacro(ctx)
		s.login.endFlight(f, err)
		return err
	}
	select {
	case <-f.done:
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Sender) runLoginMacro(ctx context.Context) ([]Header, error) {
	m, _ := s.login.get()
	_, hdrs, err := runLoginMacroReq(ctx, s.cl, m)
	if err != nil || len(hdrs) == 0 {
		return hdrs, err
	}
	s.sess.mu.RLock()
	merged := mergeSessionHeaders(s.sess.headers, hdrs)
	s.sess.mu.RUnlock()
	if s.refreshSess != nil {
		if err := s.refreshSess(merged); err != nil {
			return nil, err
		}
	}
	s.applySessionHeaders(merged)
	s.login.markRefreshed()
	return hdrs, nil
}

// mergeSessionHeaders overlays the login result onto the current session
// headers by (case-insensitive) key: matching keys are replaced in place,
// unrelated headers are kept, new keys are appended.
func mergeSessionHeaders(cur, upd []Header) []Header {
	out := append([]Header(nil), cur...)
	for _, u := range upd {
		replaced := false
		for i := range out {
			if strings.EqualFold(out[i].Key, u.Key) {
				out[i], replaced = u, true
			}
		}
		if !replaced {
			out = append(out, u)
		}
	}
	return out
}

// applySessionHeaders replaces session headers with the (merged) login result.
func (s *Sender) applySessionHeaders(hdrs []Header) {
	s.sess.mu.Lock()
	s.sess.enabled = true
	s.sess.headers = append([]Header(nil), hdrs...)
	s.sess.mu.Unlock()
}

// RunLoginMacroNow runs the login macro immediately (API / manual re-auth).
func (s *Sender) RunLoginMacroNow() ([]Header, error) { return s.runLoginMacro(context.Background()) }

// TestLoginMacro dry-runs the given login macro: it issues the login request and
// returns the response status plus the session headers it WOULD set, WITHOUT
// touching the live session. Backs the UI "Test" button so an operator can see
// what the macro does (and whether it captures a session) before relying on it.
func (s *Sender) TestLoginMacro(m LoginMacro) (status int, headers []Header, err error) {
	return runLoginMacroReq(context.Background(), s.cl, m)
}

func (s *Sender) shouldReauth401() bool {
	m, _ := s.login.get()
	return m.Enabled && m.ReauthOn401 && m.Request != ""
}
