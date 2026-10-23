package intercept

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// ResponseDecision is the outcome of holding a response: forward the (possibly
// edited) raw response, or drop it.
type ResponseDecision struct {
	Drop   bool
	Edited bool
	// Canceled marks a Drop caused by the client going away while held.
	Canceled bool
	Raw      []byte // the raw response bytes to send to the client
}

// HeldResponse is a UI-facing snapshot of a response waiting in the hold queue.
type HeldResponse struct {
	ID   int64
	Flow *store.Flow
	Raw  []byte
	// DropPending: see Held.DropPending.
	DropPending bool
}

type respItem struct {
	held HeldResponse
	raw  []byte
	done chan ResponseDecision
}

// matchResponseField tests the intercept filter against a response. "url" and
// "method" read the flow, exactly as on the request side. "header" and "body"
// read this response. "any" is the default target and an operator writes it
// against the whole exchange, so it sees the request line as well; matching only
// the response bytes would make a path filter silently hold nothing.
func matchResponseField(target string, re *regexp.Regexp, flow *store.Flow, raw []byte) bool {
	if target == "any" || target == "" {
		var b strings.Builder
		if flow != nil {
			b.WriteString(flow.Method)
			b.WriteByte(' ')
			b.WriteString(flow.Host)
			b.WriteString(flow.Path)
			b.WriteString("\r\n")
		}
		b.Write(raw)
		return re.MatchString(b.String())
	}
	return matchField(target, re, flow, raw)
}

// ResponseEnabled reports whether response interception is on.
func (e *Engine) ResponseEnabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.respEnabled
}

// SetResponseEnabled toggles response interception. Turning it off forwards
// everything currently held (unchanged), except drop-pending items, which are
// dropped.
func (e *Engine) SetResponseEnabled(on bool) {
	e.mu.Lock()
	e.respEnabled = on
	var drained []*respItem
	if !on {
		for _, id := range e.respOrder {
			drained = append(drained, e.respQueue[id])
		}
		e.respQueue = map[int64]*respItem{}
		e.respOrder = nil
	}
	e.seq++
	seq := e.seq
	e.mu.Unlock()
	for _, item := range drained {
		if item.held.DropPending {
			item.done <- ResponseDecision{Drop: true}
			continue
		}
		item.done <- ResponseDecision{Raw: item.raw}
	}
	e.emit(Change{Kind: ChangeReset, Seq: seq})
}

// HoldResponse blocks (when response interception is on) until the UI forwards
// or drops the response. raw is the editable response snapshot.
func (e *Engine) HoldResponse(flow *store.Flow, raw []byte) ResponseDecision {
	return e.HoldResponseContext(context.Background(), flow, raw)
}

// HoldResponseContext releases a held response as dropped when its client is canceled.
func (e *Engine) HoldResponseContext(ctx context.Context, flow *store.Flow, raw []byte) ResponseDecision {
	e.mu.Lock()
	if !e.respEnabled {
		e.mu.Unlock()
		return ResponseDecision{Raw: raw}
	}
	// The same conditional-intercept filter selects requests and responses. Without
	// this, turning response interception on holds every in-scope response -- images,
	// fonts, analytics, stylesheets -- and the queue floods.
	if e.matchEnabled && e.matchRe != nil && !matchResponseField(e.matchTarget, e.matchRe, flow, raw) {
		e.mu.Unlock()
		return ResponseDecision{Raw: raw}
	}
	if e.respQueue == nil {
		e.respQueue = map[int64]*respItem{}
	}
	e.respNextID++
	id := e.respNextID
	item := &respItem{held: HeldResponse{ID: id, Flow: flow, Raw: raw}, raw: raw, done: make(chan ResponseDecision, 1)}
	e.respQueue[id] = item
	e.respOrder = append(e.respOrder, id)
	e.seq++
	seq := e.seq
	held := item.held
	e.mu.Unlock()
	e.emit(Change{Kind: ChangeHeld, Seq: seq, Side: SideResponse, ID: id, Response: held})
	select {
	case d := <-item.done:
		return d
	case <-ctx.Done():
		if seq, ok := e.removeResp(id); ok {
			e.emit(Change{Kind: ChangeReleased, Seq: seq, Side: SideResponse, ID: id, Reason: ReleaseCanceled})
			return ResponseDecision{Drop: true, Canceled: true}
		}
		return <-item.done
	}
}

// ForwardResponse releases a held response (optionally replacing its raw bytes).
func (e *Engine) ForwardResponse(id int64, editedRaw []byte) error {
	e.mu.Lock()
	item, ok := e.respQueue[id]
	e.mu.Unlock()
	if !ok {
		return ErrNotHeld
	}
	raw := item.raw
	edited := len(editedRaw) > 0
	if edited {
		raw = editedRaw
	}
	seq, err := e.takeResponseForForward(id)
	if err != nil {
		return err
	}
	item.done <- ResponseDecision{Edited: edited, Raw: raw}
	e.emit(Change{Kind: ChangeReleased, Seq: seq, Side: SideResponse, ID: id, Reason: ReleaseForwarded})
	return nil
}

// DropResponse drops a held response (client receives nothing).
func (e *Engine) DropResponse(id int64) error {
	e.mu.Lock()
	item, ok := e.respQueue[id]
	e.mu.Unlock()
	if !ok {
		return ErrNotHeld
	}
	seq, ok := e.removeResp(id)
	if !ok {
		return ErrNotHeld
	}
	item.done <- ResponseDecision{Drop: true}
	e.emit(Change{Kind: ChangeReleased, Seq: seq, Side: SideResponse, ID: id, Reason: ReleaseDropped})
	return nil
}

// removeResp deletes a held response and bumps the generation, returning the new
// Seq and whether the item was present.
func (e *Engine) removeResp(id int64) (int64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.respQueue[id]; !ok {
		return 0, false
	}
	return e.removeRespLocked(id), true
}

// removeRespLocked deletes a present held response and returns the new Seq.
// The caller holds e.mu and has checked that id is queued.
func (e *Engine) removeRespLocked(id int64) int64 {
	delete(e.respQueue, id)
	for i, v := range e.respOrder {
		if v == id {
			e.respOrder = append(e.respOrder[:i], e.respOrder[i+1:]...)
			break
		}
	}
	e.seq++
	return e.seq
}

// SetResponseDropPending marks (or unmarks) a held response as scheduled to
// drop; see SetDropPending.
func (e *Engine) SetResponseDropPending(id int64, pending bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	item, ok := e.respQueue[id]
	if !ok {
		return ErrNotHeld
	}
	item.held.DropPending = pending
	return nil
}

// takeResponseForForward removes a held response for ForwardResponse under one
// lock, refusing a drop-pending item.
func (e *Engine) takeResponseForForward(id int64) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	item, ok := e.respQueue[id]
	if !ok {
		return 0, ErrNotHeld
	}
	if item.held.DropPending {
		return 0, ErrDropPending
	}
	return e.removeRespLocked(id), nil
}

// ResponseQueue returns a snapshot of held responses in arrival order.
func (e *Engine) ResponseQueue() []HeldResponse {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]HeldResponse, 0, len(e.respOrder))
	for _, id := range e.respOrder {
		out = append(out, e.respQueue[id].held)
	}
	return out
}

// HasResponseRules reports whether any enabled response-side rule exists.
func (e *Engine) HasResponseRules() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, cr := range e.rules {
		if cr.enabled && (cr.typ == "res-header" || cr.typ == "res-body") {
			return true
		}
	}
	return false
}

// ResponseBodyScanLimit reports how many response body bytes to buffer for
// match-and-replace. hasBody is true when an enabled res-body rule exists.
// allowBig is true when any such rule opts into the 64 MB cap; otherwise the
// cap is RuleScanLimit (2 MB).
func (e *Engine) ResponseBodyScanLimit() (limit int64, hasBody, allowBig bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	limit = ruleScanLimit
	for _, cr := range e.rules {
		if !cr.enabled || cr.typ != "res-body" {
			continue
		}
		hasBody = true
		if cr.bigBody {
			return maxBodyRuleBytes, true, true
		}
	}
	return limit, hasBody, false
}

// NoteSkippedResponseBodyRules logs once per enabled res-body rule that is not
// opted into big bodies. Call it when a response body exceeds the scan limit.
func (e *Engine) NoteSkippedResponseBodyRules() {
	e.mu.Lock()
	rules := e.rules
	e.mu.Unlock()
	for _, cr := range rules {
		if cr.enabled && cr.typ == "res-body" && !cr.bigBody {
			logSkippedBodyRule(cr.id, cr.typ)
		}
	}
}

// ApplyResponseRules applies enabled response-side rules to a response's headers
// and body, returning the transformed pair. skipLargeBody omits res-body rules
// that have not opted into big bodies (header rules still run).
func (e *Engine) ApplyResponseRules(h http.Header, body []byte, skipLargeBody bool) (http.Header, []byte) {
	e.mu.Lock()
	rules := e.rules
	e.mu.Unlock()
	for _, cr := range rules {
		if !cr.enabled {
			continue
		}
		switch cr.typ {
		case "res-header":
			h = applyResHeaderRule(h, cr.re, cr.replace)
		case "res-body":
			if skipLargeBody && !cr.bigBody {
				logSkippedBodyRule(cr.id, cr.typ)
				continue
			}
			body = cr.re.ReplaceAll(body, []byte(cr.replace))
		}
	}
	return h, body
}

// ApplyResponseHeaderRules applies only the enabled res-header rules, for
// callers that stream the body untouched (header-only rules, or open-ended
// streams that must never be buffered).
func (e *Engine) ApplyResponseHeaderRules(h http.Header) http.Header {
	e.mu.Lock()
	rules := e.rules
	e.mu.Unlock()
	for _, cr := range rules {
		if cr.enabled && cr.typ == "res-header" {
			h = applyResHeaderRule(h, cr.re, cr.replace)
		}
	}
	return h
}

func applyResHeaderRule(h http.Header, re *regexp.Regexp, replace string) http.Header {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range h[k] {
			b.WriteString(k + ": " + v + "\n")
		}
	}
	transformed := re.ReplaceAllString(b.String(), replace)
	nh := http.Header{}
	for _, line := range strings.Split(transformed, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		nh.Add(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	return nh
}
