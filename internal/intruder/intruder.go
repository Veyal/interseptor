// Package intruder runs payload-driven attacks (Sniper, Pitchfork) against a
// request template whose fuzz points are wrapped in §…§ markers. Each generated
// request is sent via internal/sender and recorded as a flow.
package intruder

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/codec"
	"github.com/Veyal/interseptor/internal/httplines"
	"github.com/Veyal/interseptor/internal/sender"
	"github.com/Veyal/interseptor/internal/store"
)

// An attack has no request cap. Payload lists and repeat counts are unbounded:
// jobs are generated lazily (jobPlan) instead of being materialised, results
// stream to disk in chunks (resultLog), and the anomaly accumulator keeps a
// bounded sample of lengths. The only limit on a run is the user stopping it.
//
// resultChunkSize is how many results fill one on-disk chunk; liveHeadCap is how
// many of the earliest results State() returns at most (while running and after
// finishing) — the rest are read on demand via Results / the results API, so a
// state poll stays a fixed-size response no matter how long the run was.
const (
	resultChunkSize = 1000
	liveHeadCap     = 2000
	// recordResultCap bounds the result set materialised into the persisted run
	// record. The store rejects a record over 4 MiB, so a million-request run
	// must not marshal a quarter-gigabyte JSON document only to have it refused.
	recordResultCap = 12000
)

const maxDispatchDelayMs = int64(1<<63-1) / int64(time.Millisecond)

// Empty §§ pairs are not fuzz points.
var marker = regexp.MustCompile(`§[^§]+§`)

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
	// Guard, when set, vets every send's resolved destination at dial time
	// (e.g. the tool's own listeners). Nil keeps the historical client path.
	Guard *sender.IPGuard `json:"-"`
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
	// GrepTruncated is true when the (decoded) response body is larger than
	// MaxGrepBytes, so grep-match/extract only saw its first MaxGrepBytes: a
	// non-match on such a row is not proof the pattern is absent.
	GrepTruncated bool `json:"grepTruncated,omitempty"`

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
	// PartialResults is true when Results is only the bounded head of the run
	// while Done rows exist. That is the normal case for a run larger than the
	// live head: a state poll stays a fixed-size response at any run length, and
	// the client pages the rest from /api/intruder/results.
	PartialResults bool `json:"partialResults,omitempty"`
	// Note explains a bounded record ("the first and last N results are kept").
	// It is not an error: the run finished. Error is reserved for a run that
	// failed, because clients render it as a failure state.
	Note string `json:"note,omitempty"`

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
	// bodyPrefix reads at most max bytes of a stored body; preferred over body
	// when set, because it never loads more than the grep cap into memory.
	bodyPrefix func(hash string, max int64) (b []byte, truncated bool, err error)

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

// MaxGrepBytes is how much of each response body grep-match and grep-extract
// inspect: the first 8 MiB of the stored body, and the first 8 MiB of its
// decoded form when it is Content-Encoding compressed. An attack runs up to 64
// requests at once, so an uncapped grep holds 64 whole bodies (and their
// inflated copies) in memory and runs every regex over all of them. A result
// cut here carries GrepTruncated. A variable so tests can lower it.
var MaxGrepBytes = 8 << 20

// SetBodyPrefixReader wires a bounded response-body reader: fn returns at most
// max bytes of the body and reports whether the body is longer. When set it is
// used instead of the whole-body reader, so grep never loads more than
// MaxGrepBytes of a response.
func (e *Engine) SetBodyPrefixReader(fn func(hash string, max int64) (b []byte, truncated bool, err error)) {
	e.bodyPrefix = fn
}

// grepBody returns the bytes grep may inspect for a stored body: at most
// MaxGrepBytes, with truncated set when the body is longer.
func (e *Engine) grepBody(hash string) (b []byte, truncated bool, err error) {
	limit := MaxGrepBytes
	if e.bodyPrefix != nil {
		b, truncated, err = e.bodyPrefix(hash, int64(limit))
	} else {
		b, err = e.body(hash)
	}
	if err != nil {
		return nil, false, err
	}
	if len(b) > limit {
		b, truncated = b[:limit], true
	}
	return b, truncated, nil
}

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

func (g grepRule) match(body []byte) bool {
	if g.re != nil {
		return g.re.Match(body)
	}
	return g.lit != "" && bytes.Contains(body, []byte(g.lit))
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

// applyGrep runs the match and extract rules over a (decoded) response body.
// It matches on the bytes as they are: converting the body to a string first
// copied every response once more per result, on every worker at the same time.
func applyGrep(res *Result, body []byte, matchRules []grepRule, extractRules []*regexp.Regexp) {
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
			if m := re.FindSubmatch(body); len(m) >= 2 {
				res.Extracts[i] = string(m[1]) // a copy: the result outlives the body
				if res.Extracted == "" {
					res.Extracted = res.Extracts[i]
				}
			}
		}
	}
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
	return e.stateLocked()
}

// stateLocked is State for callers already holding e.mu.
func (e *Engine) stateLocked() State {
	return e.snapshotLocked(false)
}

// recordStateLocked is the snapshot persisted as a finished run's record. It
// carries far more results than a state poll (the evidence renders need the
// run's shape) but is still bounded, and says so when it had to be.
func (e *Engine) recordStateLocked() State {
	return e.snapshotLocked(true)
}

// snapshotLocked builds a State. In state mode Results is the bounded live head
// and PartialResults says whether more exist; in record mode it is the bounded
// record set (head and tail of a very large run) for persistence.
func (e *Engine) snapshotLocked(record bool) State {
	out := State{Running: e.running, Total: e.total, Done: e.done, Error: e.errMsg, Capped: e.capped,
		RunID: e.runID, StartedTs: e.startedTs, Attack: e.attack, Threads: e.threads,
		DelayMs: e.delayMs, Repeat: e.repeat, TargetHost: e.targetHost, Barrier: e.barrier}
	if e.log == nil {
		return out
	}
	n := e.log.count()
	if record {
		// readRange applies the derived flags; a finished log is finalised.
		if n <= recordResultCap {
			out.Results = e.log.readRange(0, 0)
		} else {
			half := recordResultCap / 2
			head := e.log.readRange(0, half)
			tail := e.log.readRange(n-half, half)
			out.Results = append(head, tail...)
			out.Capped = true
			out.Note = fmt.Sprintf("run record keeps the first and last %d of %d results to stay within the store limit", half, n)
		}
		out.PartialResults = n > len(out.Results)
		return out
	}
	// A running log has no derived flags yet, so the cheap in-memory head is the
	// right read; once finished, read the head back so flags are applied.
	if e.running {
		out.Results = e.log.snapshotHead()
	} else {
		out.Results = e.log.readRange(0, liveHeadCap)
	}
	out.PartialResults = n > len(out.Results)
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

// RunID returns the current/last run's identity. A client paging results uses
// it to detect that another run started and its offsets no longer address the
// same log.
func (e *Engine) RunID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runID
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

	plan := planJobs(spec, len(positions), baselines)
	if plan.total == 0 {
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
	e.total = intSat(plan.total)
	e.done = 0
	e.errMsg = ""
	e.capped = false // nothing is truncated any more; kept for API compatibility
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
	e.barrier = spec.Barrier && spec.AttackType == "repeat" && e.threads > 1 && plan.total > 1
	method, path := endpointOf(spec.Template)
	e.spec = SpecSummary{Attack: e.attack, Target: e.targetHost, Threads: e.threads, DelayMs: spec.DelayMs,
		Method: method, Path: path, Repeat: e.repeat, Barrier: e.barrier, GrepMatch: spec.GrepMatch, GrepExtract: spec.GrepExtract,
		MatchRules: append([]string(nil), spec.MatchRules...), ExtractRules: append([]string(nil), spec.ExtractRules...),
		ProcessRules: append([]string(nil), spec.ProcessRules...)}
	e.doneCh = make(chan struct{})
	e.cancel = cancel
	e.mu.Unlock()
	e.fireNotify()

	go e.run(ctx, spec, plan)
	return nil
}

// jobPlan is an attack's request schedule: how many requests it will send, plus
// a generator that emits them one at a time. Materialising every job (the old
// buildJobs) made the request count a memory limit, which is why a payload list
// was capped. A plan costs constant memory, so a list of any length runs.
type jobPlan struct {
	total int64
	each  func(yield func(job) bool)
}

// mulSat multiplies two job counts, saturating at math.MaxInt64 instead of
// overflowing: a Cluster bomb over several long lists exceeds any real total,
// and a wrapped negative would make the run look empty.
func mulSat(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

// intSat narrows a job count to int for the public State, saturating on 32-bit
// builds rather than wrapping negative.
func intSat(v int64) int {
	if v > int64(math.MaxInt) {
		return math.MaxInt
	}
	if v < 0 {
		return 0
	}
	return int(v)
}

// planJobs builds the request schedule for spec. Nothing is materialised: each
// attack type closes over the payload lists and computes the next job on demand.
func planJobs(spec Spec, nPositions int, baselines []string) jobPlan {
	switch spec.AttackType {
	case "repeat":
		// N identical sends of the template (markers, if any, keep their value).
		n := spec.Repeat
		if n < 0 {
			n = 0
		}
		return jobPlan{total: int64(n), each: func(yield func(job) bool) {
			for k := 0; k < n; k++ {
				if !yield(job{label: "#" + strconv.Itoa(k+1), payloads: append([]string(nil), baselines...)}) {
					return
				}
			}
		}}
	case "pitchfork":
		lists := spec.Payloads
		if len(lists) > nPositions {
			lists = lists[:nPositions]
		}
		if len(lists) == 0 {
			return jobPlan{}
		}
		n := len(lists[0])
		for _, list := range lists {
			if len(list) < n {
				n = len(list)
			}
		}
		return jobPlan{total: int64(n), each: func(yield func(job) bool) {
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
				if !yield(job{label: strings.Join(labels, " · "), payloads: payloads}) {
					return
				}
			}
		}}
	case "battering", "batteringram":
		// Same payload at every § marker simultaneously (Burp Battering ram).
		var list []string
		if len(spec.Payloads) > 0 {
			list = spec.Payloads[0]
		}
		return jobPlan{total: int64(len(list)), each: func(yield func(job) bool) {
			for _, pl := range list {
				processed := processPayload(pl, spec.ProcessRules)
				payloads := make([]string, nPositions)
				for i := range payloads {
					payloads[i] = processed
				}
				if !yield(job{label: pl, payloads: payloads}) {
					return
				}
			}
		}}
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
		total := int64(1)
		for _, l := range lists {
			total = mulSat(total, int64(len(l)))
		}
		if total == 0 {
			return jobPlan{}
		}
		return jobPlan{total: total, each: func(yield func(job) bool) {
			idx := make([]int, len(lists))
			for {
				payloads := make([]string, nPositions)
				labels := make([]string, 0, nPositions)
				for i := 0; i < nPositions; i++ {
					orig := lists[i][idx[i]]
					payloads[i] = processPayload(orig, spec.ProcessRules)
					labels = append(labels, orig)
				}
				if !yield(job{label: strings.Join(labels, " · "), payloads: payloads}) {
					return
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
					return
				}
			}
		}}
	default: // sniper: vary one position at a time, others keep their baseline
		var list []string
		if len(spec.Payloads) > 0 {
			list = spec.Payloads[0]
		}
		return jobPlan{total: mulSat(int64(nPositions), int64(len(list))), each: func(yield func(job) bool) {
			for pos := 0; pos < nPositions; pos++ {
				for _, pl := range list {
					payloads := append([]string(nil), baselines...)
					payloads[pos] = processPayload(pl, spec.ProcessRules)
					if !yield(job{label: pl, payloads: payloads}) {
						return
					}
				}
			}
		}}
	}
}

func (e *Engine) run(ctx context.Context, spec Spec, plan jobPlan) {
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
		if plan.total < int64(barrierN) {
			barrierN = int(plan.total)
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
		if (len(matchRules) == 0 && len(extractRules) == 0) || (e.body == nil && e.bodyPrefix == nil) || hash == "" {
			return
		}
		// Skip grep on known-binary content types; flag the result so the UI can
		// show an informational badge rather than a silent non-match. Decided from
		// the headers, before the body is read at all.
		if ct := headerVal(resHeaders, "Content-Type"); codec.IsBinaryContentType(ct) {
			res.Binary = true
			return
		}
		raw, truncated, err := e.grepBody(hash)
		if err != nil {
			res.Error = "response body unavailable"
			return
		}
		res.GrepTruncated = truncated

		// Decompress if the response was Content-Encoding compressed, keeping at
		// most MaxGrepBytes of the decoded form. Fall back gracefully to raw
		// bytes on any decompression error.
		bodyBytes := raw
		if ce := headerVal(resHeaders, "Content-Encoding"); ce != "" && ce != "identity" {
			if dec, ok, cut := codec.DecompressBodyLimit(ce, raw, MaxGrepBytes); ok {
				bodyBytes = dec
				res.GrepTruncated = res.GrepTruncated || cut
			}
			// else: fall through — grep the raw bytes rather than returning nothing
		}

		applyGrep(res, bodyBytes, matchRules, extractRules)
	}

	slots := make(chan int, threads) // free worker slots 1..threads
	for i := 1; i <= threads; i++ {
		slots <- i
	}
	var wg sync.WaitGroup
	// The plan emits jobs one at a time, so an attack of any length runs in
	// constant memory. Returning false from the yield stops dispatch: there is no
	// job slice to break out of any more.
	i := 0
	plan.each(func(j job) bool {
		if spec.DelayMs > 0 && i > 0 { // throttle: wait between dispatching each request
			timer := time.NewTimer(time.Duration(spec.DelayMs) * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return false
			}
		}
		if ctx.Err() != nil {
			return false
		}
		var slot int
		select {
		case slot = <-slots:
		case <-ctx.Done():
			return false
		}
		// Adaptive pacing: once a worker slot is free, hold off launching while a
		// 429/503 backoff is active (checked here, after the slot handoff, so a
		// pause set by the just-finished request is already visible).
		if !e.waitBackoff(ctx) {
			slots <- slot
			return false
		}
		idx := i
		i++
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
			// The request line decides the path only. A payload (or template)
			// that turns the request target into something other than an
			// origin-form path would otherwise move the send to another host.
			sendURL, uerr := sender.TargetURL(spec.Target, path)
			if uerr != nil {
				res.Error = "parse: " + uerr.Error()
				e.appendResult(res)
				return
			}
			sendStart := time.Now()
			res.StartUs = sendStart.Sub(runStart).Microseconds()
			flow, sendErr := e.snd.Send(sender.Request{
				Method:  method,
				URL:     sendURL,
				Headers: headers,
				Body:    body,
				Flags:   store.FlagIntruder | spec.ExtraFlags,
				Context: ctx,
				Options: guardOptions(spec.Guard),
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
		}(idx, slot, j)
		return true
	})
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
	// Snapshot the record under this same lock, before running=false is published.
	// A Start() the moment the UI sees running=false replaces e.log and e.spec, and
	// reading them afterwards emitted this run's record under the next run's id
	// with an empty result set (the old run then vanished from the history).
	record := RunRecord{State: e.recordStateLocked(), Spec: e.spec}
	sink := e.sink
	e.mu.Unlock()
	cancel()
	e.fireNotify()
	if sink != nil {
		sink(record)
	}
	// Released last: Close waits on this before the caller tears down the store
	// the sink writes the run record to. A new Start is gated on running, not on
	// this channel, so the next run is not held up by a slow sink.
	close(done)
}

// median returns the median of a slice of int64 values without modifying the
// original slice. Returns 0 for an empty slice.
func median(vals []int64) int64 {
	if len(vals) == 0 {
		return 0
	}
	// O(n log n): a run is unbounded, so the sample can be large (the insertion
	// sort this replaced was quadratic and would hang on a long run).
	sorted := make([]int64, len(vals))
	copy(sorted, vals)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
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

// guardOptions wraps a dial-time guard in send options (nil when unguarded).
func guardOptions(g *sender.IPGuard) *sender.SendOptions {
	if g == nil {
		return nil
	}
	return &sender.SendOptions{Guard: g}
}
