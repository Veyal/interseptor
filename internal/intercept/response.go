package intercept

import (
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
	Raw    []byte // the raw response bytes to send to the client
}

// HeldResponse is a UI-facing snapshot of a response waiting in the hold queue.
type HeldResponse struct {
	ID   int64
	Flow *store.Flow
	Raw  []byte
}

type respItem struct {
	held HeldResponse
	raw  []byte
	done chan ResponseDecision
}

// ResponseEnabled reports whether response interception is on.
func (e *Engine) ResponseEnabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.respEnabled
}

// SetResponseEnabled toggles response interception. Turning it off forwards
// everything currently held (unchanged).
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
	e.mu.Unlock()
	for _, item := range drained {
		item.done <- ResponseDecision{Raw: item.raw}
	}
	e.fireNotify()
}

// HoldResponse blocks (when response interception is on) until the UI forwards
// or drops the response. raw is the editable response snapshot.
func (e *Engine) HoldResponse(flow *store.Flow, raw []byte) ResponseDecision {
	e.mu.Lock()
	if !e.respEnabled {
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
	e.mu.Unlock()
	e.fireNotify()
	return <-item.done
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
	if !e.removeResp(id) {
		return ErrNotHeld
	}
	item.done <- ResponseDecision{Edited: edited, Raw: raw}
	e.fireNotify()
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
	if !e.removeResp(id) {
		return ErrNotHeld
	}
	item.done <- ResponseDecision{Drop: true}
	e.fireNotify()
	return nil
}

func (e *Engine) removeResp(id int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.respQueue[id]; !ok {
		return false
	}
	delete(e.respQueue, id)
	for i, v := range e.respOrder {
		if v == id {
			e.respOrder = append(e.respOrder[:i], e.respOrder[i+1:]...)
			break
		}
	}
	return true
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

// ApplyResponseRules applies enabled response-side rules to a response's headers
// and body, returning the transformed pair.
func (e *Engine) ApplyResponseRules(h http.Header, body []byte) (http.Header, []byte) {
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
			body = cr.re.ReplaceAll(body, []byte(cr.replace))
		}
	}
	return h, body
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
