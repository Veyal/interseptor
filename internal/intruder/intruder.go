// Package intruder runs payload-driven attacks (Sniper, Pitchfork) against a
// request template whose fuzz points are wrapped in §…§ markers. Each generated
// request is sent via internal/sender and recorded as a flow.
package intruder

import (
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/codec"
	"github.com/Veyal/interseptor/internal/httplines"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

// maxRequests bounds a single attack so a huge payload list cannot run away.
const maxRequests = 2000

var marker = regexp.MustCompile(`§[^§]*§`)

// Spec describes an attack.
type Spec struct {
	Target       string     // scheme://host[:port]
	Template     string     // raw request with §…§ fuzz points
	AttackType   string     // "sniper" | "pitchfork" | "repeat" (alias: "null")
	Payloads     [][]string // sniper: one list; pitchfork: one list per position
	Repeat       int        // repeat/null mode: send the template this many times (no payloads)
	Threads      int        // max concurrent in-flight requests (default 1)
	DelayMs      int        // delay between dispatching each request (throttling); 0 = none
	GrepMatch    string     // flag a result if its response matches this regex (or contains this literal)
	GrepExtract  string     // extract group 1 of this regex from each response into the result
	ProcessRules []string   // payload transforms applied in order: "urlencode" | "base64" | "prefix:X" | "suffix:X" | "upper" | "lower"
	ExtraFlags   int64      // OR'd onto every recorded send (e.g. store.FlagAI for AI-driven runs)
}

// Result is one attack request's outcome.
type Result struct {
	ID        int    `json:"id"`
	Payload   string `json:"payload"`
	Status    int    `json:"status"`
	Length    int64  `json:"length"`
	TimeMs    int64  `json:"timeMs"`
	Error     string `json:"error"`
	FlowID    int64  `json:"flowId"`
	Flagged   bool   `json:"flagged"`
	Anomaly   bool   `json:"anomaly"`   // true when length deviates significantly from the typical length
	Matched   bool   `json:"matched"`   // grep-match hit in the response
	Extracted string `json:"extracted"` // grep-extract capture from the response
	Binary    bool   `json:"binary"`    // true when the body is binary/undecodable and grep did not apply
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
	snd  *sender.Sender
	body func(hash string) []byte // reads a stored response body (for grep); may be nil

	mu      sync.Mutex
	running bool
	results []Result
	total   int
	done    int
	errMsg  string
	capped  bool
	notify  func()
}

// headerVal returns the first value for a case-insensitive header key.
func headerVal(h map[string][]string, key string) string {
	for k, v := range h {
		if strings.EqualFold(k, key) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

// New returns an Engine backed by snd.
func New(snd *sender.Sender) *Engine { return &Engine{snd: snd} }

// SetBodyReader wires a response-body reader so grep-match/extract can inspect
// response contents (the engine itself has no store access).
func (e *Engine) SetBodyReader(fn func(hash string) []byte) { e.body = fn }

// processPayload applies the configured transforms to a payload value, in order.
func processPayload(pl string, rules []string) string {
	for _, ru := range rules {
		switch {
		case ru == "urlencode":
			pl = url.QueryEscape(pl)
		case ru == "base64":
			pl = base64.StdEncoding.EncodeToString([]byte(pl))
		case ru == "upper":
			pl = strings.ToUpper(pl)
		case ru == "lower":
			pl = strings.ToLower(pl)
		case strings.HasPrefix(ru, "prefix:"):
			pl = ru[len("prefix:"):] + pl
		case strings.HasPrefix(ru, "suffix:"):
			pl = pl + ru[len("suffix:"):]
		}
	}
	return pl
}

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

// normalizeAttackType maps UI/API aliases to engine attack types.
func normalizeAttackType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "null":
		return "repeat"
	default:
		return t
	}
}

// Start validates the spec, builds the job list, and runs the attack in the
// background. It errors if an attack is already running or the spec is invalid.
func (e *Engine) Start(spec Spec) error {
	spec.AttackType = normalizeAttackType(spec.AttackType)
	positions := marker.FindAllString(spec.Template, -1)
	if spec.AttackType == "repeat" {
		// Null/repeat mode: send the template verbatim N times — no markers or
		// payloads required (raise Threads for concurrent replays).
		if spec.Repeat < 1 {
			return errors.New("set how many times to send (repeat count)")
		}
	} else {
		if len(positions) == 0 {
			return errors.New("template has no §…§ fuzz points")
		}
		if len(spec.Payloads) == 0 || len(spec.Payloads[0]) == 0 {
			return errors.New("no payloads provided")
		}
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
	// add enforces the request cap DURING accumulation: a huge spec.Repeat or a
	// template with thousands of §markers × payloads would otherwise materialize
	// billions of jobs (OOM) before any post-loop truncation. add returns false
	// once the cap is hit so each loop can stop immediately.
	add := func(j job) bool {
		if len(jobs) >= maxRequests {
			capped = true
			return false
		}
		jobs = append(jobs, j)
		return true
	}
	switch spec.AttackType {
	case "repeat":
		// N identical sends of the template (markers, if any, keep their value).
		for k := 0; k < spec.Repeat; k++ {
			if !add(job{label: "#" + strconv.Itoa(k+1), payloads: append([]string(nil), baselines...)}) {
				break
			}
		}
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
					orig := spec.Payloads[i][k]
					payloads[i] = processPayload(orig, spec.ProcessRules) // sent value (transformed)
					labels = append(labels, orig)                         // label shows the original
				} else {
					payloads[i] = baselines[i]
					labels = append(labels, baselines[i])
				}
			}
			if !add(job{label: strings.Join(labels, " · "), payloads: payloads}) {
				break
			}
		}
	case "battering", "batteringram":
		// Same payload at every § marker simultaneously (Burp Battering ram).
		for _, pl := range spec.Payloads[0] {
			processed := processPayload(pl, spec.ProcessRules)
			payloads := make([]string, nPositions)
			for i := range payloads {
				payloads[i] = processed
			}
			if !add(job{label: pl, payloads: payloads}) {
				break
			}
		}
	case "cluster", "clusterbomb":
		// Cartesian product of payload lists (Burp Cluster bomb).
		lists := spec.Payloads
		if len(lists) < nPositions {
			padded := make([][]string, nPositions)
			copy(padded, lists)
			for i := len(lists); i < nPositions; i++ {
				padded[i] = []string{baselines[i]}
			}
			lists = padded
		} else if len(lists) > nPositions {
			lists = lists[:nPositions]
		}
		idx := make([]int, len(lists))
		for {
			payloads := make([]string, nPositions)
			labels := make([]string, 0, nPositions)
			for i := 0; i < nPositions; i++ {
				orig := lists[i][idx[i]]
				payloads[i] = processPayload(orig, spec.ProcessRules)
				labels = append(labels, orig)
			}
			if !add(job{label: strings.Join(labels, " · "), payloads: payloads}) {
				break
			}
			// advance odometer
			carried := true
			for i := len(idx) - 1; i >= 0; i-- {
				idx[i]++
				if idx[i] < len(lists[i]) {
					carried = false
					break
				}
				idx[i] = 0
			}
			if carried {
				break
			}
		}
	default: // sniper: vary one position at a time, others keep their baseline
		for pos := 0; pos < nPositions; pos++ {
			capHit := false
			for _, pl := range spec.Payloads[0] {
				payloads := append([]string(nil), baselines...)
				payloads[pos] = processPayload(pl, spec.ProcessRules)
				if !add(job{label: pl, payloads: payloads}) {
					capHit = true
					break
				}
			}
			if capHit {
				break
			}
		}
	}
	return jobs, capped
}

func (e *Engine) run(spec Spec, jobs []job) {
	base := strings.TrimRight(spec.Target, "/")
	threads := spec.Threads
	if threads < 1 {
		threads = 1
	}
	if threads > 64 { // bound concurrency so a race test can't exhaust sockets/goroutines
		threads = 64
	}

	// Compile grep patterns once (literal Contains fallback if not a valid regex).
	var grepM, grepX *regexp.Regexp
	var grepMLit string
	if spec.GrepMatch != "" {
		if re, err := regexp.Compile(spec.GrepMatch); err == nil {
			grepM = re
		} else {
			grepMLit = spec.GrepMatch
		}
	}
	if spec.GrepExtract != "" {
		grepX, _ = regexp.Compile(spec.GrepExtract)
	}
	// doGrep inspects the response body for grep-match and grep-extract patterns.
	// resHeaders provides the response's Content-Encoding and Content-Type so the
	// body can be decompressed before matching. If decompression fails, the raw
	// bytes are used as a fallback (never an error). If the content type indicates
	// a binary payload (image, audio, video, …) grep is skipped and res.Binary is
	// set so the caller can surface a warning to the user.
	doGrep := func(res *Result, hash string, resHeaders map[string][]string) {
		if (grepM == nil && grepMLit == "" && grepX == nil) || e.body == nil || hash == "" {
			return
		}
		raw := e.body(hash)

		// Skip grep on known-binary content types; flag the result so the UI can
		// show an informational badge rather than a silent non-match.
		if ct := headerVal(resHeaders, "Content-Type"); codec.IsBinaryContentType(ct) {
			res.Binary = true
			return
		}

		// Decompress if the response was Content-Encoding compressed. Fall back
		// gracefully to raw bytes on any decompression error.
		bodyBytes := raw
		if ce := headerVal(resHeaders, "Content-Encoding"); ce != "" && ce != "identity" {
			if dec, ok := codec.DecompressBody(ce, raw); ok {
				bodyBytes = dec
			}
			// else: fall through — grep the raw bytes rather than returning nothing
		}

		body := string(bodyBytes)
		if grepM != nil {
			res.Matched = grepM.MatchString(body)
		} else if grepMLit != "" {
			res.Matched = strings.Contains(body, grepMLit)
		}
		if grepX != nil {
			if m := grepX.FindStringSubmatch(body); len(m) >= 2 {
				res.Extracted = m[1]
			}
		}
	}

	sem := make(chan struct{}, threads)
	var wg sync.WaitGroup
	for i, j := range jobs {
		if spec.DelayMs > 0 && i > 0 { // throttle: wait between dispatching each request
			time.Sleep(time.Duration(spec.DelayMs) * time.Millisecond)
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(idx int, j job) {
			defer wg.Done()
			defer func() { <-sem }()
			// Substitute payloads into the whole request, then parse — so fuzz points
			// in the request line / path / headers / body all take effect.
			method, path, headers, body, perr := httplines.ParseRawRequest(substitute(spec.Template, j.payloads))
			res := Result{ID: idx + 1, Payload: j.label}
			if perr != nil {
				res.Error = "parse: " + perr.Error()
				e.appendResult(res)
				return
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
				doGrep(&res, flow.ResBodyHash, flow.ResHeaders)
			}
			e.appendResult(res)
		}(i, j)
	}
	wg.Wait()

	e.flagAnomalies()
	e.mu.Lock()
	e.running = false
	e.mu.Unlock()
	e.fireNotify()
}

// flagAnomalies marks results whose status differs from the most common status,
// or whose response length deviates significantly from the median length.
// It also flags results whose grep-match outcome differs from the majority.
//
// Length anomaly threshold: a result is anomalous when its length differs from
// the median by more than max(50 bytes, 20% of the median). This is conservative
// enough to ignore minor variation (e.g. CSRF-token noise) while catching the
// meaningful size shifts that indicate a different code path.
func (e *Engine) flagAnomalies() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Collect valid (successfully-sent) results only. Parse/transport failures
	// (Status 0) must not skew the modal status or median length.
	type valid struct{ idx int; st int; length int64; matched bool }
	var valids []valid
	for i, r := range e.results {
		if r.Status > 0 {
			valids = append(valids, valid{i, r.Status, r.Length, r.Matched})
		}
	}
	if len(valids) == 0 {
		return
	}

	// --- modal status ---
	statusCounts := map[int]int{}
	for _, v := range valids {
		statusCounts[v.st]++
	}
	modeStatus, best := 0, -1
	for st, c := range statusCounts {
		if c > best {
			best, modeStatus = c, st
		}
	}

	// --- median response length ---
	lengths := make([]int64, len(valids))
	for i, v := range valids {
		lengths[i] = v.length
	}
	medianLen := median(lengths)

	// Tolerance: flag when deviation > max(50 bytes, 20% of median).
	// This avoids flagging tiny uniform responses (e.g. all-zero length).
	tolerance := medianLen / 5 // 20%
	if tolerance < 50 {
		tolerance = 50
	}

	// --- grep-match majority ---
	// Only consider this signal when grep was actually configured (i.e. at
	// least one result has Matched=true; otherwise it was not used).
	matchedCount := 0
	for _, v := range valids {
		if v.matched {
			matchedCount++
		}
	}
	// Majority verdict: if more than half matched, a non-match is unusual; vice versa.
	majorityMatched := matchedCount*2 > len(valids)
	grepSignalActive := matchedCount > 0 && matchedCount < len(valids)

	for _, v := range valids {
		statusAnomaly := v.st != modeStatus || v.st >= 500
		diff := v.length - medianLen
		if diff < 0 {
			diff = -diff
		}
		lengthAnomaly := diff > tolerance
		grepAnomaly := grepSignalActive && (v.matched != majorityMatched)

		if lengthAnomaly {
			e.results[v.idx].Anomaly = true
		}
		if statusAnomaly || lengthAnomaly || grepAnomaly {
			e.results[v.idx].Flagged = true
		}
	}
}

// median returns the median of a slice of int64 values without modifying the
// original slice. Returns 0 for an empty slice.
func median(vals []int64) int64 {
	if len(vals) == 0 {
		return 0
	}
	// Insertion sort is fine here: attack results are bounded by maxRequests (2000).
	sorted := make([]int64, len(vals))
	copy(sorted, vals)
	for i := 1; i < len(sorted); i++ {
		x := sorted[i]
		j := i
		for j > 0 && sorted[j-1] > x {
			sorted[j] = sorted[j-1]
			j--
		}
		sorted[j] = x
	}
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
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

func (e *Engine) appendResult(res Result) {
	e.mu.Lock()
	e.results = append(e.results, res)
	e.done = len(e.results)
	e.mu.Unlock()
	e.fireNotify()
}
