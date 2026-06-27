// Package intercept implements the request hold queue (Burp-style intercept)
// and the request-side match-&-replace engine. The proxy calls Hold on every
// request; when intercept is enabled the calling goroutine blocks until the UI
// forwards (optionally with edits) or drops it.
package intercept

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Veyal/interceptor/internal/store"
)

// ErrNotHeld is returned when resolving an id that is not (or no longer) held.
var ErrNotHeld = errors.New("intercept: request not held")

// Decision is the outcome of a hold: forward the (possibly edited) request, or drop it.
type Decision struct {
	Drop    bool
	Edited  bool
	Held    bool // the request actually entered the hold queue (vs. filtered straight through)
	Request *http.Request
}

// Held is a UI-facing snapshot of a request waiting in the hold queue.
type Held struct {
	ID   int64
	Flow *store.Flow
	Raw  []byte // origin-form raw request bytes, editable before forwarding
}

type heldItem struct {
	held Held
	req  *http.Request
	done chan Decision
}

type compiledRule struct {
	enabled bool
	typ     string
	re      *regexp.Regexp
	replace string
}

// Engine owns the hold queue and the compiled rule set.
type Engine struct {
	mu      sync.Mutex
	enabled bool
	nextID  int64
	queue   map[int64]*heldItem
	order   []int64
	rules   []compiledRule
	notify  func()

	// optional intercept filter: when set, only requests matching the regex on
	// the chosen field are held; everything else is forwarded without holding.
	matchEnabled bool
	matchTarget  string // "any" | "url" | "header" | "body" | "method"
	matchPattern string
	matchRe      *regexp.Regexp

	// response interception
	respEnabled bool
	respNextID  int64
	respQueue   map[int64]*respItem
	respOrder   []int64
}

// New returns an Engine with intercept disabled and no rules.
func New() *Engine {
	return &Engine{queue: map[int64]*heldItem{}}
}

// Enabled reports whether intercept is on.
func (e *Engine) Enabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.enabled
}

// SetEnabled toggles intercept. Turning it off forwards everything currently held.
func (e *Engine) SetEnabled(on bool) {
	e.mu.Lock()
	e.enabled = on
	var drained []*heldItem
	if !on {
		for _, id := range e.order {
			drained = append(drained, e.queue[id])
		}
		e.queue = map[int64]*heldItem{}
		e.order = nil
	}
	e.mu.Unlock()

	for _, item := range drained {
		item.done <- Decision{Request: item.req}
	}
	e.fireNotify()
}

// SetInterceptFilter configures the conditional-intercept filter. When enabled
// with a valid pattern, only requests whose chosen field matches the regex are
// held. An invalid regex is returned as an error and the filter is left unchanged.
func (e *Engine) SetInterceptFilter(enabled bool, target, pattern string) error {
	var re *regexp.Regexp
	if enabled && pattern != "" {
		c, err := regexp.Compile(pattern)
		if err != nil {
			return err
		}
		re = c
	}
	if target == "" {
		target = "any"
	}
	e.mu.Lock()
	e.matchEnabled = enabled && re != nil
	e.matchTarget, e.matchPattern, e.matchRe = target, pattern, re
	e.mu.Unlock()
	return nil
}

// InterceptFilter returns the current filter (enabled, target, pattern).
func (e *Engine) InterceptFilter() (bool, string, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.matchTarget
	if t == "" {
		t = "any"
	}
	return e.matchEnabled, t, e.matchPattern
}

// matchField tests the regex against the selected part of the request.
func matchField(target string, re *regexp.Regexp, flow *store.Flow, raw []byte) bool {
	var s string
	switch target {
	case "url":
		if flow != nil {
			s = flow.Host + flow.Path
		}
	case "method":
		if flow != nil {
			s = flow.Method
		}
	case "header":
		if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
			s = string(raw[:i])
		} else {
			s = string(raw)
		}
	case "body":
		if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
			s = string(raw[i+4:])
		}
	default: // "any" — the whole raw request
		s = string(raw)
	}
	return re.MatchString(s)
}

// SetNotifier registers a callback invoked whenever the queue changes.
func (e *Engine) SetNotifier(fn func()) {
	e.mu.Lock()
	e.notify = fn
	e.mu.Unlock()
}

func (e *Engine) fireNotify() {
	e.mu.Lock()
	fn := e.notify
	e.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Hold submits a request to the gate. If intercept is off it returns immediately
// with a forward decision; otherwise it blocks until the UI resolves it. raw is
// the editable origin-form snapshot shown in the UI.
func (e *Engine) Hold(flow *store.Flow, req *http.Request, raw []byte) Decision {
	e.mu.Lock()
	if !e.enabled {
		e.mu.Unlock()
		return Decision{Request: req}
	}
	// Conditional intercept: only hold requests matching the filter regex.
	if e.matchEnabled && e.matchRe != nil && !matchField(e.matchTarget, e.matchRe, flow, raw) {
		e.mu.Unlock()
		return Decision{Request: req}
	}
	e.nextID++
	id := e.nextID
	item := &heldItem{
		held: Held{ID: id, Flow: flow, Raw: raw},
		req:  req,
		done: make(chan Decision, 1),
	}
	e.queue[id] = item
	e.order = append(e.order, id)
	e.mu.Unlock()

	e.fireNotify()
	d := <-item.done
	d.Held = true
	return d
}

// Queue returns a snapshot of held requests in arrival order.
func (e *Engine) Queue() []Held {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Held, 0, len(e.order))
	for _, id := range e.order {
		out = append(out, e.queue[id].held)
	}
	return out
}

// Forward releases a held request. If editedRaw is non-empty it replaces the
// request with the re-parsed edited version (forwarding target preserved).
func (e *Engine) Forward(id int64, editedRaw []byte) error {
	e.mu.Lock()
	item, ok := e.queue[id]
	e.mu.Unlock()
	if !ok {
		return ErrNotHeld
	}

	req := item.req
	edited := len(editedRaw) > 0
	if edited {
		nr, err := parseEditedRequest(editedRaw, item.req)
		if err != nil {
			return fmt.Errorf("parse edited request: %w", err)
		}
		req = nr
	}

	if !e.remove(id) {
		return ErrNotHeld // resolved concurrently
	}
	item.done <- Decision{Request: req, Edited: edited}
	e.fireNotify()
	return nil
}

// Drop releases a held request as dropped (never forwarded).
func (e *Engine) Drop(id int64) error {
	e.mu.Lock()
	item, ok := e.queue[id]
	e.mu.Unlock()
	if !ok {
		return ErrNotHeld
	}
	if !e.remove(id) {
		return ErrNotHeld
	}
	item.done <- Decision{Drop: true}
	e.fireNotify()
	return nil
}

func (e *Engine) remove(id int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.queue[id]; !ok {
		return false
	}
	delete(e.queue, id)
	for i, v := range e.order {
		if v == id {
			e.order = append(e.order[:i], e.order[i+1:]...)
			break
		}
	}
	return true
}

// SetRules compiles and installs the rule set, rejecting invalid regexes.
func (e *Engine) SetRules(rules []store.Rule) error {
	compiled := make([]compiledRule, 0, len(rules))
	for _, r := range rules {
		re, err := regexp.Compile(r.Match)
		if err != nil {
			return fmt.Errorf("rule %d (%s): %w", r.ID, r.Type, err)
		}
		compiled = append(compiled, compiledRule{enabled: r.Enabled, typ: r.Type, re: re, replace: r.Replace})
	}
	e.mu.Lock()
	e.rules = compiled
	e.mu.Unlock()
	return nil
}

// ApplyRules mutates req per the enabled request-side rules, in order.
func (e *Engine) ApplyRules(req *http.Request) error {
	e.mu.Lock()
	rules := e.rules
	e.mu.Unlock()
	for _, cr := range rules {
		if !cr.enabled {
			continue
		}
		switch cr.typ {
		case "req-header":
			applyHeaderRule(req, cr.re, cr.replace)
		case "req-body":
			if err := applyBodyRule(req, cr.re, cr.replace); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseEditedRequest(raw []byte, orig *http.Request) (*http.Request, error) {
	r, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		return nil, err
	}
	r.RequestURI = "" // required for client requests
	// The raw is origin-form; restore the forwarding target from the original.
	r.URL.Scheme = orig.URL.Scheme
	if r.URL.Host == "" {
		r.URL.Host = orig.URL.Host
	}
	r.RemoteAddr = orig.RemoteAddr

	// Re-derive the body straight from the edited raw so changing it doesn't
	// require hand-fixing Content-Length (http.ReadRequest otherwise truncates
	// the body to the stale header). Chunked bodies carry their own framing, so
	// leave those alone; and don't add a length to a genuinely body-less request.
	if !isChunked(r.Header) {
		body, _ := rawBody(raw)
		if len(body) > 0 || r.Header.Get("Content-Length") != "" {
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
			r.Header.Set("Content-Length", strconv.Itoa(len(body)))
		}
	}
	return r, nil
}

// rawBody returns the body of a raw HTTP message — everything after the first
// blank line — handling either CRLF or (textarea-normalized) LF separators.
func rawBody(raw []byte) (body []byte, ok bool) {
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return raw[i+4:], true
	}
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		return raw[i+2:], true
	}
	return nil, false
}

// isChunked reports whether the headers request chunked transfer encoding.
func isChunked(h http.Header) bool {
	for _, te := range h["Transfer-Encoding"] {
		if strings.Contains(strings.ToLower(te), "chunked") {
			return true
		}
	}
	return false
}

// applyHeaderRule serializes the header block to text, applies the regex, and
// re-parses it back onto req (Host handled separately, as Go keeps it off Header).
func applyHeaderRule(req *http.Request, re *regexp.Regexp, replace string) {
	keys := make([]string, 0, len(req.Header)+1)
	for k := range req.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	if req.Host != "" {
		b.WriteString("Host: " + req.Host + "\n")
	}
	for _, k := range keys {
		for _, v := range req.Header[k] {
			b.WriteString(k + ": " + v + "\n")
		}
	}

	transformed := re.ReplaceAllString(b.String(), replace)

	req.Header = http.Header{}
	for _, line := range strings.Split(transformed, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if http.CanonicalHeaderKey(k) == "Host" {
			req.Host = v
			continue
		}
		req.Header.Add(k, v)
	}
}

// maxBodyRuleBytes bounds how much request body is buffered to apply a body
// match-&-replace rule; a larger body is forwarded untransformed.
const maxBodyRuleBytes = 64 << 20

func applyBodyRule(req *http.Request, re *regexp.Regexp, replace string) error {
	if req.Body == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, maxBodyRuleBytes+1))
	if err != nil {
		req.Body.Close()
		return err
	}
	if int64(len(body)) > maxBodyRuleBytes {
		// Too large to buffer for a body rule — forward untransformed, preserving
		// Close so the original body isn't leaked.
		req.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), req.Body), req.Body}
		return nil
	}
	req.Body.Close()
	nb := re.ReplaceAll(body, []byte(replace))
	req.Body = io.NopCloser(bytes.NewReader(nb))
	req.ContentLength = int64(len(nb))
	req.Header.Set("Content-Length", strconv.Itoa(len(nb)))
	return nil
}
