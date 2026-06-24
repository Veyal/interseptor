// Package intruder runs payload-driven attacks (Sniper, Pitchfork) against a
// request template whose fuzz points are wrapped in §…§ markers. Each generated
// request is sent via internal/sender and recorded as a flow.
package intruder

import (
	"bufio"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Veyal/interceptor/internal/sender"
	"github.com/Veyal/interceptor/internal/store"
)

// maxRequests bounds a single attack so a huge payload list cannot run away.
const maxRequests = 2000

var marker = regexp.MustCompile(`§[^§]*§`)

// Spec describes an attack.
type Spec struct {
	Target     string     // scheme://host[:port]
	Template   string     // raw request with §…§ fuzz points
	AttackType string     // "sniper" | "pitchfork"
	Payloads   [][]string // sniper: one list; pitchfork: one list per position
	ExtraFlags int64      // OR'd onto every recorded send (e.g. store.FlagAI for AI-driven runs)
}

// Result is one attack request's outcome.
type Result struct {
	ID      int    `json:"id"`
	Payload string `json:"payload"`
	Status  int    `json:"status"`
	Length  int64  `json:"length"`
	TimeMs  int64  `json:"timeMs"`
	Error   string `json:"error"`
	FlowID  int64  `json:"flowId"`
	Flagged bool   `json:"flagged"`
}

// State is a snapshot of the current/last attack.
type State struct {
	Running bool     `json:"running"`
	Total   int      `json:"total"`
	Done    int      `json:"done"`
	Results []Result `json:"results"`
	Error   string   `json:"error"`
	Capped  bool     `json:"capped"`
}

// Engine runs one attack at a time.
type Engine struct {
	snd *sender.Sender

	mu      sync.Mutex
	running bool
	results []Result
	total   int
	done    int
	errMsg  string
	capped  bool
	notify  func()
}

// New returns an Engine backed by snd.
func New(snd *sender.Sender) *Engine { return &Engine{snd: snd} }

// SetNotifier registers a callback fired as the attack progresses.
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

// State returns a snapshot.
func (e *Engine) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := State{Running: e.running, Total: e.total, Done: e.done, Error: e.errMsg, Capped: e.capped}
	out.Results = append(out.Results, e.results...)
	return out
}

type job struct {
	label    string
	payloads []string // one per position
}

// Start validates the spec, builds the job list, and runs the attack in the
// background. It errors if an attack is already running or the spec is invalid.
func (e *Engine) Start(spec Spec) error {
	positions := marker.FindAllString(spec.Template, -1)
	if len(positions) == 0 {
		return errors.New("template has no §…§ fuzz points")
	}
	if len(spec.Payloads) == 0 || len(spec.Payloads[0]) == 0 {
		return errors.New("no payloads provided")
	}
	if spec.Target == "" {
		return errors.New("no target")
	}

	baselines := make([]string, len(positions))
	for i, p := range positions {
		baselines[i] = strings.TrimPrefix(strings.TrimSuffix(p, "§"), "§")
	}

	jobs, capped := buildJobs(spec, len(positions), baselines)
	if len(jobs) == 0 {
		return errors.New("attack produced no requests")
	}

	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return errors.New("an attack is already running")
	}
	e.running = true
	e.results = nil
	e.total = len(jobs)
	e.done = 0
	e.errMsg = ""
	e.capped = capped
	e.mu.Unlock()
	e.fireNotify()

	go e.run(spec, jobs)
	return nil
}

func buildJobs(spec Spec, nPositions int, baselines []string) (jobs []job, capped bool) {
	switch spec.AttackType {
	case "pitchfork":
		n := len(spec.Payloads[0])
		for _, list := range spec.Payloads {
			if len(list) < n {
				n = len(list)
			}
		}
		for k := 0; k < n; k++ {
			payloads := make([]string, nPositions)
			labels := make([]string, 0, nPositions)
			for i := 0; i < nPositions; i++ {
				if i < len(spec.Payloads) {
					payloads[i] = spec.Payloads[i][k]
				} else {
					payloads[i] = baselines[i]
				}
				labels = append(labels, payloads[i])
			}
			jobs = append(jobs, job{label: strings.Join(labels, " · "), payloads: payloads})
		}
	default: // sniper: vary one position at a time, others keep their baseline
		for pos := 0; pos < nPositions; pos++ {
			for _, pl := range spec.Payloads[0] {
				payloads := append([]string(nil), baselines...)
				payloads[pos] = pl
				jobs = append(jobs, job{label: pl, payloads: payloads})
			}
		}
	}
	if len(jobs) > maxRequests {
		jobs = jobs[:maxRequests]
		capped = true
	}
	return jobs, capped
}

func (e *Engine) run(spec Spec, jobs []job) {
	base := strings.TrimRight(spec.Target, "/")

	for i, j := range jobs {
		// Substitute payloads into the whole request, then parse — so fuzz points
		// in the request line / path / headers / body all take effect.
		method, path, headers, body, perr := parseRaw(substitute(spec.Template, j.payloads))
		res := Result{ID: i + 1, Payload: j.label}
		if perr != nil {
			res.Error = "parse: " + perr.Error()
			e.appendResult(res)
			continue
		}
		start := time.Now()
		flow, _ := e.snd.Send(sender.Request{
			Method:  method,
			URL:     base + path,
			Headers: headers,
			Body:    body,
			Flags:   store.FlagIntruder | spec.ExtraFlags,
		})
		res.TimeMs = time.Since(start).Milliseconds()
		if flow != nil {
			res.Status = flow.Status
			res.Length = flow.ResLen
			if res.Error == "" {
				res.Error = flow.Error
			}
			res.FlowID = flow.ID
		}
		e.appendResult(res)
	}

	e.flagAnomalies()
	e.mu.Lock()
	e.running = false
	e.mu.Unlock()
	e.fireNotify()
}

// flagAnomalies marks results whose status differs from the most common status
// — the classic Intruder "one of these is not like the others" signal.
func (e *Engine) flagAnomalies() {
	e.mu.Lock()
	defer e.mu.Unlock()
	// Only count successfully-sent responses; parse/transport failures (Status 0)
	// must not skew the modal status or they'd mis-flag the valid responses.
	counts := map[int]int{}
	for _, r := range e.results {
		if r.Status > 0 {
			counts[r.Status]++
		}
	}
	mode, best := 0, -1
	for st, c := range counts {
		if c > best {
			best, mode = c, st
		}
	}
	for i := range e.results {
		if st := e.results[i].Status; st > 0 && (st != mode || st >= 500) {
			e.results[i].Flagged = true
		}
	}
}

// substitute replaces the i-th §…§ marker with payloads[i].
func substitute(template string, payloads []string) string {
	i := 0
	return marker.ReplaceAllStringFunc(template, func(string) string {
		v := ""
		if i < len(payloads) {
			v = payloads[i]
		}
		i++
		return v
	})
}

func normalizeCRLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

func (e *Engine) appendResult(res Result) {
	e.mu.Lock()
	e.results = append(e.results, res)
	e.done = len(e.results)
	e.mu.Unlock()
	e.fireNotify()
}

// parseRaw parses a (payload-substituted) raw request. The request line and
// headers are parsed with http.ReadRequest; the body is taken as everything
// after the blank line (Burp-style — no Content-Length required). Content-Length
// is dropped since the sender recomputes it from the actual body.
func parseRaw(raw string) (method, path string, headers map[string][]string, body []byte, err error) {
	norm := normalizeCRLF(raw)
	head := norm
	var bodyStr string
	if i := strings.Index(norm, "\r\n\r\n"); i >= 0 {
		head = norm[:i] + "\r\n\r\n"
		bodyStr = norm[i+4:]
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
	return req.Method, orSlash(req.URL.RequestURI()), h, []byte(bodyStr), nil
}

func orSlash(s string) string {
	if s == "" {
		return "/"
	}
	return s
}
