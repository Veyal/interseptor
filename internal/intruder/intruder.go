// Package intruder runs payload-driven attacks (Sniper, Pitchfork) against a
// request template whose fuzz points are wrapped in §…§ markers. Each generated
// request is sent via internal/sender and recorded as a flow.
package intruder

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
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
// Results stream to disk in chunks (resultLog), so this is no longer limited by
// how many results fit in memory.
const maxRequests = 20000

// resultChunkSize is how many results fill one on-disk chunk; liveHeadCap is how
// many of the earliest results State() returns while a run is still in flight
// (the rest are read on demand via Results / the results API).
const (
	resultChunkSize = 1000
	liveHeadCap     = 2000
)

const maxDispatchDelayMs = int64(1<<63-1) / int64(time.Millisecond)

var marker = regexp.MustCompile(`§[^§]*§`)

// ErrClosed is returned when an attack is started after engine shutdown.
var ErrClosed = errors.New("intruder engine is closed")

// Spec describes an attack.
type Spec struct {
	Target     string     // scheme://host[:port]
	Template   string     // raw request with §…§ fuzz points
	AttackType string     // "sniper" | "pitchfork" | "repeat" (alias: "null")
	Payloads   [][]string // sniper: one list; pitchfork: one list per position
	Repeat     int        // repeat/null mode: send the template this many times (no payloads)
	Threads    int        // max concurrent in-flight requests (default 1)
	DelayMs    int        // delay between dispatching each request (throttling); 0 = none
	// MaxBackoffMs caps a single adaptive pause when the target returns 429/503
	// with Retry-After / RateLimit-Reset. 0 uses the default cap. Negative
	// disables adaptive backoff.
	MaxBackoffMs int
	GrepMatch    string // flag a result if its response matches this regex (or contains this literal)
	GrepExtract  string // extract group 1 of this regex from each response into the result
	// MatchRules / ExtractRules add further match and extract rules beyond the
	// single GrepMatch/GrepExtract (which stay rule 0). Each match rule is a
	// regex, or a literal substring when it is not a valid regex; each extract
	// rule captures group 1. A result records a per-rule hit vector and capture.
	MatchRules   []string
	ExtractRules []string
	ProcessRules []string // payload transforms applied in order: "urlencode" | "base64" | "base64url" | "hex" | "htmlencode" | "md5" | "sha1" | "sha256" | "upper" | "lower" | "prefix:X" | "suffix:X"
	ExtraFlags   int64    // OR'd onto every recorded send (e.g. store.FlagAI for AI-driven runs)
	Barrier      bool     `json:"barrier,omitempty"` // repeat mode, Threads>1: launch the first Threads requests together
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
	Matched   bool   `json:"matched"`   // any grep-match rule hit in the response
	Extracted string `json:"extracted"` // first non-empty grep-extract capture from the response
	Binary    bool   `json:"binary"`    // true when the body is binary/undecodable and grep did not apply

	// Matches / Extracts carry the per-rule results when more than one match or
	// extract rule is configured (aligned to the run's rule order). Omitted for
	// old single-rule consumers.
	Matches  []bool   `json:"matches,omitempty"`
	Extracts []string `json:"extracts,omitempty"`

	// Recording fields (additive; zero values are omitted for old consumers).
	Seq       int               `json:"seq,omitempty"`       // dispatch order, 1-based
	Worker    int               `json:"worker,omitempty"`    // worker slot 1..Threads
	StartUs   int64             `json:"startUs,omitempty"`   // send start, microseconds from run start (monotonic)
	EndUs     int64             `json:"endUs,omitempty"`     // send end, microseconds from run start (monotonic)
	BodyHash  string            `json:"bodyHash,omitempty"`  // response body hash
	RLHeaders map[string]string `json:"rlHeaders,omitempty"` // whitelisted rate-limit response headers
}

// State is a snapshot of the current/last attack.
type State struct {
	Running bool     `json:"running"`
	Total   int      `json:"total"`
	Done    int      `json:"done"`
	Results []Result `json:"results"`
	Error   string   `json:"error"`
	Capped  bool     `json:"capped"`

	RunID      string `json:"runId,omitempty"`
	StartedTs  int64  `json:"startedTs,omitempty"` // unix ms, wall clock, display only
	Attack     string `json:"attack,omitempty"`
	Threads    int    `json:"threads,omitempty"`
	DelayMs    int    `json:"delayMs,omitempty"`
	Repeat     int    `json:"repeat,omitempty"`
	TargetHost string `json:"targetHost,omitempty"`
	Barrier    bool   `json:"barrier,omitempty"` // true only if workers really launched from a released barrier
}

// Engine runs one attack at a time.
type Engine struct {
	snd  *sender.Sender
	body func(hash string) ([]byte, error) // reads a stored response body (for grep); may be nil

	mu      sync.Mutex
	running bool
	log     *resultLog // disk-backed chunked result store for the current/last run
	total   int
	done    int
	errMsg  string
	capped  bool
	notify  func()
	doneCh  chan struct{}
	cancel  context.CancelFunc
	closed  bool

	runID, attack, targetHost string
	startedTs                 int64
	threads, delayMs, repeat  int
	barrier                   bool
	spec                      SpecSummary
	sink                      func(RunRecord)

	pauseUntil time.Time // adaptive 429/503 backoff: dispatch is held until this time
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
func New(snd *sender.Sender) *Engine {
	done := make(chan struct{})
	close(done)
	return &Engine{snd: snd, doneCh: done}
}

// SetBodyReader wires a simple response-body reader so grep-match/extract can
// inspect response contents. Use SetBodyReaderResult when read failures matter.
func (e *Engine) SetBodyReader(fn func(hash string) []byte) {
	if fn == nil {
		e.body = nil
		return
	}
	e.body = func(hash string) ([]byte, error) { return fn(hash), nil }
}

// SetBodyReaderResult wires an error-aware response-body reader. The engine has
// no store dependency, so the control plane supplies this adapter.
func (e *Engine) SetBodyReaderResult(fn func(hash string) ([]byte, error)) { e.body = fn }

// processPayload applies the configured transforms to a payload value, in order.
// Blank rules are skipped; any non-blank rule reaching this point has already
// passed validateProcessRules, so the final default is unreachable in practice
// and exists only so an unvalidated internal caller still gets a no-op rather
// than a silently wrong value.
func processPayload(pl string, rules []string) string {
	for _, ru := range rules {
		switch {
		case ru == "":
			// skip
		case ru == "urlencode":
			pl = url.QueryEscape(pl)
		case ru == "base64":
			pl = base64.StdEncoding.EncodeToString([]byte(pl))
		case ru == "base64url":
			pl = base64.URLEncoding.EncodeToString([]byte(pl))
		case ru == "hex":
			pl = hex.EncodeToString([]byte(pl))
		case ru == "htmlencode":
			pl = htmlEscaper.Replace(pl)
		case ru == "md5":
			sum := md5.Sum([]byte(pl))
			pl = hex.EncodeToString(sum[:])
		case ru == "sha1":
			sum := sha1.Sum([]byte(pl))
			pl = hex.EncodeToString(sum[:])
		case ru == "sha256":
			sum := sha256.Sum256([]byte(pl))
			pl = hex.EncodeToString(sum[:])
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

// htmlEscaper mirrors html.EscapeString's character set without pulling the
// html package in: &, <, >, ' and " — the characters that matter when a payload
// lands in HTML.
var htmlEscaper = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;",
)

// validateProcessRules rejects any non-blank payload transform the engine does
// not implement, so a typo (e.g. "base644" or "sha512") returns a clear error
// instead of silently passing the raw, un-transformed payload to the target.
func validateProcessRules(rules []string) error {
	for _, ru := range rules {
		if ru == "" {
			continue
		}
		switch {
		case ru == "urlencode", ru == "base64", ru == "base64url", ru == "hex",
			ru == "htmlencode", ru == "md5", ru == "sha1", ru == "sha256",
			ru == "upper", ru == "lower",
			strings.HasPrefix(ru, "prefix:"), strings.HasPrefix(ru, "suffix:"):
			// known
		default:
			return fmt.Errorf("unknown payload transform %q (allowed: urlencode, base64, base64url, hex, htmlencode, md5, sha1, sha256, upper, lower, prefix:X, suffix:X)", ru)
		}
	}
	return nil
}

// grepRule is one compiled match rule: a regex, or a literal substring when the
// pattern is not a valid regex (mirrors the legacy single-rule fallback).
type grepRule struct {
	re  *regexp.Regexp
	lit string
}

func (g grepRule) match(body string) bool {
	if g.re != nil {
		return g.re.MatchString(body)
	}
	return g.lit != "" && strings.Contains(body, g.lit)
}

// prependNonEmpty puts a legacy single value ahead of the rule list, dropping it
// when empty so an unset GrepMatch/GrepExtract adds no rule.
func prependNonEmpty(first string, rest []string) []string {
	if first == "" {
		return rest
	}
	return append([]string{first}, rest...)
}

func compileMatchRules(pats []string) []grepRule {
	var out []grepRule
	for _, p := range pats {
		if p == "" {
			continue
		}
		if re, err := regexp.Compile(p); err == nil {
			out = append(out, grepRule{re: re})
		} else {
			out = append(out, grepRule{lit: p})
		}
	}
	return out
}

// compileExtractRules compiles each extract pattern; an invalid regex is skipped
// (extraction needs a capture group, so there is no literal fallback).
func compileExtractRules(pats []string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, p := range pats {
		if p == "" {
			continue
		}
		if re, err := regexp.Compile(p); err == nil {
			out = append(out, re)
		}
	}
	return out
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
	out := State{Running: e.running, Total: e.total, Done: e.done, Error: e.errMsg, Capped: e.capped,
		RunID: e.runID, StartedTs: e.startedTs, Attack: e.attack, Threads: e.threads,
		DelayMs: e.delayMs, Repeat: e.repeat, TargetHost: e.targetHost, Barrier: e.barrier}
	if e.log != nil {
		// While running, return only the bounded live head (flags are derived at
		// finish); once finished, return the full, flagged result set. Large runs
		// stream the remainder through Results / the results API.
		if e.running {
			out.Results = e.log.snapshotHead()
		} else {
			out.Results = e.log.all()
		}
	}
	return out
}

// Results returns a window [offset, offset+limit) of the current/last run's
// results (flags applied once the run has finished), streamed from the disk log
// so a caller can page through a large run without loading it all. A
// non-positive limit returns everything from offset. The second value is the
// total result count.
func (e *Engine) Results(offset, limit int) ([]Result, int) {
	e.mu.Lock()
	log := e.log
	e.mu.Unlock()
	if log == nil {
		return nil, 0
	}
	return log.readRange(offset, limit), log.count()
}

// Close permanently stops the engine, cancels an active attack, and waits for
// its request workers to release the sender and store.
func (e *Engine) Close() {
	e.mu.Lock()
	e.closed = true
	cancel := e.cancel
	done := e.doneCh
	log := e.log
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	<-done
	log.cleanup()
}

// Stop cancels an active attack run without closing the engine permanently.
func (e *Engine) Stop() {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

type job struct {
	label    string
	payloads []string // one per position
}

// normalizeAttackType maps UI/API aliases to engine attack types.
func normalizeAttackType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "null", "repeat":
		return "repeat"
	case "sniper":
		return "sniper"
	case "pitchfork":
		return "pitchfork"
	case "battering", "batteringram":
		return "battering"
	case "cluster", "clusterbomb":
		return "cluster"
	default:
		return ""
	}
}

// Start validates the spec, builds the job list, and runs the attack in the
// background. It errors if an attack is already running or the spec is invalid.
func (e *Engine) Start(spec Spec) error {
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return ErrClosed
	}

	rawAttackType := spec.AttackType
	spec.AttackType = normalizeAttackType(rawAttackType)
	if spec.AttackType == "" {
		return fmt.Errorf("unsupported attack type %q", strings.TrimSpace(rawAttackType))
	}
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
		if spec.AttackType == "pitchfork" || spec.AttackType == "cluster" {
			for i := 0; i < len(spec.Payloads) && i < len(positions); i++ {
				if len(spec.Payloads[i]) == 0 {
					return fmt.Errorf("payload list %d is empty", i+1)
				}
			}
		}
	}
	if spec.Target == "" {
		return errors.New("no target")
	}
	if spec.DelayMs < 0 || int64(spec.DelayMs) > maxDispatchDelayMs {
		return fmt.Errorf("delay must be between 0 and %d milliseconds", maxDispatchDelayMs)
	}
	if err := validateProcessRules(spec.ProcessRules); err != nil {
		return err
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
	if e.closed {
		e.mu.Unlock()
		return ErrClosed
	}
	if e.running {
		e.mu.Unlock()
		return errors.New("an attack is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.running = true
	e.log.cleanup() // remove the previous run's on-disk chunks
	e.log = newRunLog(resultChunkSize, liveHeadCap)
	e.total = len(jobs)
	e.done = 0
	e.errMsg = ""
	e.capped = capped
	e.pauseUntil = time.Time{}
	e.runID = newRunID()
	e.startedTs = time.Now().UnixMilli()
	e.attack = spec.AttackType
	e.threads = clampThreads(spec.Threads)
	e.delayMs = spec.DelayMs
	e.repeat = 0
	if spec.AttackType == "repeat" {
		e.repeat = spec.Repeat
	}
	e.targetHost = hostOnly(spec.Target)
	e.barrier = spec.Barrier && spec.AttackType == "repeat" && e.threads > 1 && len(jobs) > 1
	method, path := endpointOf(spec.Template)
	e.spec = SpecSummary{Attack: e.attack, Target: e.targetHost, Threads: e.threads, DelayMs: spec.DelayMs,
		Method: method, Path: path, Repeat: e.repeat, Barrier: e.barrier, GrepMatch: spec.GrepMatch, GrepExtract: spec.GrepExtract,
		MatchRules: append([]string(nil), spec.MatchRules...), ExtractRules: append([]string(nil), spec.ExtractRules...),
		ProcessRules: append([]string(nil), spec.ProcessRules...)}
	e.doneCh = make(chan struct{})
	e.cancel = cancel
	e.mu.Unlock()
	e.fireNotify()

	go e.run(ctx, spec, jobs)
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
		lists := spec.Payloads
		if len(lists) > nPositions {
			lists = lists[:nPositions]
		}
		n := len(lists[0])
		for _, list := range lists {
			if len(list) < n {
				n = len(list)
			}
		}
		for k := 0; k < n; k++ {
			payloads := make([]string, nPositions)
			labels := make([]string, 0, nPositions)
			for i := 0; i < nPositions; i++ {
				if i < len(lists) {
					orig := lists[i][k]
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

func (e *Engine) run(ctx context.Context, spec Spec, jobs []job) {
	base := strings.TrimRight(spec.Target, "/")
	threads := clampThreads(spec.Threads) // bounded so a race test can't exhaust sockets/goroutines
	runStart := time.Now()                // monotonic origin for StartUs/EndUs
	backoff := backoffCap(spec.MaxBackoffMs)
	e.mu.Lock()
	useBarrier := e.barrier
	e.mu.Unlock()
	var bar *barrier
	barrierN := 0
	if useBarrier {
		barrierN = threads
		if len(jobs) < barrierN {
			barrierN = len(jobs)
		}
		bar = newBarrier(barrierN)
	}

	// Compile grep patterns once (literal Contains fallback if not a valid regex).
	// Rule 0 is the legacy single GrepMatch/GrepExtract; MatchRules/ExtractRules
	// follow.
	matchRules := compileMatchRules(prependNonEmpty(spec.GrepMatch, spec.MatchRules))
	extractRules := compileExtractRules(prependNonEmpty(spec.GrepExtract, spec.ExtractRules))
	// doGrep inspects the response body for grep-match and grep-extract patterns.
	// resHeaders provides the response's Content-Encoding and Content-Type so the
	// body can be decompressed before matching. If decompression fails, the raw
	// bytes are used as a fallback (never an error). If the content type indicates
	// a binary payload (image, audio, video, …) grep is skipped and res.Binary is
	// set so the caller can surface a warning to the user.
	doGrep := func(res *Result, hash string, resHeaders map[string][]string) {
		if (len(matchRules) == 0 && len(extractRules) == 0) || e.body == nil || hash == "" {
			return
		}
		raw, err := e.body(hash)
		if err != nil {
			res.Error = "response body unavailable"
			return
		}

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
		if len(matchRules) > 0 {
			res.Matches = make([]bool, len(matchRules))
			for i, g := range matchRules {
				res.Matches[i] = g.match(body)
				res.Matched = res.Matched || res.Matches[i]
			}
		}
		if len(extractRules) > 0 {
			res.Extracts = make([]string, len(extractRules))
			for i, re := range extractRules {
				if m := re.FindStringSubmatch(body); len(m) >= 2 {
					res.Extracts[i] = m[1]
					if res.Extracted == "" {
						res.Extracted = m[1]
					}
				}
			}
		}
	}

	slots := make(chan int, threads) // free worker slots 1..threads
	for i := 1; i <= threads; i++ {
		slots <- i
	}
	var wg sync.WaitGroup
dispatch:
	for i, j := range jobs {
		if spec.DelayMs > 0 && i > 0 { // throttle: wait between dispatching each request
			timer := time.NewTimer(time.Duration(spec.DelayMs) * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				break dispatch
			}
		}
		if ctx.Err() != nil {
			break
		}
		var slot int
		select {
		case slot = <-slots:
		case <-ctx.Done():
			break dispatch
		}
		// Adaptive pacing: once a worker slot is free, hold off launching while a
		// 429/503 backoff is active (checked here, after the slot handoff, so a
		// pause set by the just-finished request is already visible).
		if !e.waitBackoff(ctx) {
			slots <- slot
			break dispatch
		}
		wg.Add(1)
		go func(idx, slot int, j job) {
			defer wg.Done()
			defer func() { slots <- slot }()
			if bar != nil && idx < barrierN {
				bar.wait(ctx.Done())
			}
			// Substitute payloads into the whole request, then parse — so fuzz points
			// in the request line / path / headers / body all take effect.
			method, path, headers, body, perr := httplines.ParseRawRequest(substitute(spec.Template, j.payloads))
			res := Result{ID: idx + 1, Seq: idx + 1, Worker: slot, Payload: j.label}
			if perr != nil {
				res.Error = "parse: " + perr.Error()
				e.appendResult(res)
				return
			}
			sendStart := time.Now()
			res.StartUs = sendStart.Sub(runStart).Microseconds()
			flow, sendErr := e.snd.Send(sender.Request{
				Method:  method,
				URL:     base + path,
				Headers: headers,
				Body:    body,
				Flags:   store.FlagIntruder | spec.ExtraFlags,
				Context: ctx,
			})
			sendEnd := time.Now()
			res.TimeMs = sendEnd.Sub(sendStart).Milliseconds()
			res.EndUs = sendEnd.Sub(runStart).Microseconds()
			if res.EndUs < 1 {
				res.EndUs = 1 // 0 is reserved for "timing not recorded"
			}
			if sendErr != nil {
				res.Error = "send failed"
			}
			if flow != nil {
				res.Status = flow.Status
				res.Length = flow.ResLen
				if flow.Error != "" {
					res.Error = flow.Error
				}
				if res.Error == "" && flow.Flags&store.FlagCaptureError != 0 {
					res.Error = "response capture incomplete"
				}
				res.FlowID = flow.ID
				res.BodyHash = flow.ResBodyHash
				res.RLHeaders = rlHeaders(flow.ResHeaders)
				e.noteRateLimit(res.Status, flow.ResHeaders, backoff)
				if res.Error == "" {
					doGrep(&res, flow.ResBodyHash, flow.ResHeaders)
				}
			}
			e.appendResult(res)
		}(i, slot, j)
	}
	wg.Wait()

	e.log.finalize() // derive anomaly thresholds so reads can flag results
	e.mu.Lock()
	if bar != nil && bar.failed() {
		e.barrier = false // honest: the barrier did not release with every worker parked
		e.spec.Barrier = false
	}
	e.running = false
	cancel := e.cancel
	e.cancel = nil
	done := e.doneCh
	close(done)
	e.mu.Unlock()
	cancel()
	e.fireNotify()
	e.emitRunRecord()
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
	e.log.append(res)
	e.mu.Lock()
	e.done = e.log.count()
	e.mu.Unlock()
	e.fireNotify()
}
