package control

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/intruder"
	"github.com/Veyal/interseptor/internal/preview"
	"github.com/Veyal/interseptor/internal/redact"
	"github.com/Veyal/interseptor/internal/store"
)

// Evidence renders: deterministic PNGs drawn from recorded data only. This
// file adapts recorded data (Intruder runs, authz runs, flows, findings) into
// the plain input structs of internal/preview and exposes them over REST. The
// preview package never imports control, store or intruder.

const (
	maxEvidenceRenderRequestBytes int64 = 64 << 10
	maxWaterfallFlows                   = 50
	maxChainNodes                       = 12
	maxChainDepth                       = 3
	authzRunCacheSize                   = 20
	renderConcurrency                   = 4                // simultaneous renders (each can hold ~12 MB of pixels)
	renderTimeout                       = 10 * time.Second // wall-clock budget for one render
	maxRenderHeaderRunes                = 2000
	maxInlineRenderPNG                  = 1 << 20 // largest PNG embedded in a png=1 JSON response
	minEvidenceWidth                    = 640
	maxEvidenceWidth                    = 1600
	evidenceRenderSource                = "evidence_render"
	latestRunAlias                      = "latest"
)

// evidenceStatusError carries an HTTP status with a client-safe message.
type evidenceStatusError struct {
	code int
	msg  string
}

func (e *evidenceStatusError) Error() string { return e.msg }

func evErr(code int, msg string) error { return &evidenceStatusError{code: code, msg: msg} }

func writeEvidenceError(w http.ResponseWriter, err error) {
	var se *evidenceStatusError
	if errors.As(err, &se) {
		httpErr(w, se.code, se.msg)
		return
	}
	httpInternalErr(w, err)
}

// ---- API receiver and authz run capture -----------------------------------

type evidenceAPI struct {
	*Hub
	authz *authzRunCache
	sem   chan struct{} // bounds concurrent renders; a full channel answers 503
}

func newEvidenceAPI(h *Hub) *evidenceAPI {
	return &evidenceAPI{Hub: h, authz: &authzRunCache{runs: map[string][]authzRunOut{}}, sem: make(chan struct{}, renderConcurrency)}
}

var errRenderTimeout = preview.ErrRenderTimeout

// mapRenderError turns a renderer deadline into a retryable 503.
func mapRenderError(err error) error {
	if errors.Is(err, preview.ErrRenderTimeout) {
		return evErr(http.StatusServiceUnavailable, "render timed out; try a smaller run or retry")
	}
	return err
}

// authzRunCache keeps the most recent authz runs in memory so the matrix can
// be rendered after the run. Runs are not persisted; an attached image keeps
// its sourceRef after the cache entry is gone.
type authzRunCache struct {
	mu    sync.Mutex
	order []string
	runs  map[string][]authzRunOut
}

func (c *authzRunCache) put(runs []authzRunOut) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runs[id] = runs
	c.order = append(c.order, id)
	for len(c.order) > authzRunCacheSize {
		delete(c.runs, c.order[0])
		c.order = c.order[1:]
	}
	return id
}

func (c *authzRunCache) get(id string) (string, []authzRunOut, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == latestRunAlias && len(c.order) > 0 {
		id = c.order[len(c.order)-1]
	}
	runs, ok := c.runs[id]
	return id, runs, ok
}

const (
	maxAuthzCaptureBytes = 8 << 20 // larger authz responses are streamed through uncached
	authzCacheRows       = 100     // rows kept per cached run (the matrix draws 40)
	authzCacheCols       = 32      // identities kept per row (the matrix draws 8)
)

// bufferedResponse captures a handler's response so it can be post-processed,
// up to limit bytes. Past the limit (or on Flush) it spills what it holds to
// dst and streams the rest, so an unexpectedly large response is never held in
// memory and streaming handlers keep working.
type bufferedResponse struct {
	dst      http.ResponseWriter
	header   http.Header
	code     int
	body     []byte
	limit    int
	overflow bool // true once everything goes straight to dst
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) WriteHeader(code int) {
	if b.code == 0 {
		b.code = code
	}
	if b.overflow {
		b.dst.WriteHeader(code)
	}
}
func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.code == 0 {
		b.code = http.StatusOK
	}
	if !b.overflow && len(b.body)+len(p) > b.limit {
		b.spill()
	}
	if b.overflow {
		return b.dst.Write(p)
	}
	b.body = append(b.body, p...)
	return len(p), nil
}

// Flush forces streaming: a handler that flushes expects the client to see the
// bytes now, so buffering for post-processing ends here.
func (b *bufferedResponse) Flush() {
	b.spill()
	if f, ok := b.dst.(http.Flusher); ok {
		f.Flush()
	}
}

func (b *bufferedResponse) spill() {
	if b.overflow {
		return
	}
	b.overflow = true
	for k, v := range b.header {
		b.dst.Header()[k] = v
	}
	if b.code != 0 {
		b.dst.WriteHeader(b.code)
	}
	if len(b.body) > 0 {
		_, _ = b.dst.Write(b.body)
	}
	b.body = nil
}

// trimAuthzRuns keeps the bounded subset of an authz run the matrix needs:
// the first authzCacheRows rows and authzCacheCols identities, with only the
// verdict fields (no MIME, body hashes or long error text).
func trimAuthzRuns(runs []authzRunOut) []authzRunOut {
	out := make([]authzRunOut, 0, min(len(runs), authzCacheRows))
	for _, run := range runs[:min(len(runs), authzCacheRows)] {
		t := authzRunOut{FlowID: run.FlowID, Method: clipText(run.Method, 16), Path: clipText(run.Path, maxEvidenceURLRunes*4), BaselineStatus: run.BaselineStatus}
		for _, r := range run.Results[:min(len(run.Results), authzCacheCols)] {
			t.Results = append(t.Results, authzResult{
				Name: clipText(r.Name, maxEvidenceNameRunes*4), Status: r.Status, Length: r.Length, FlowID: r.FlowID,
				Same: r.Same, SessionInvalid: r.SessionInvalid, AccessDenied: r.AccessDenied, Broken: r.Broken,
				Error: clipText(r.Error, 1), // only "was there an error" matters to the matrix
			})
		}
		out = append(out, t)
	}
	return out
}

// captureAuthzRun wraps POST /api/authz/run: a successful response is cached
// (bounded, see trimAuthzRuns) under a new runId, which is added to the JSON as
// "runId". The authz handler itself is unchanged. Responses over
// maxAuthzCaptureBytes are streamed to the client untouched and not cached.
func (e *evidenceAPI) captureAuthzRun(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		buf := &bufferedResponse{dst: w, header: http.Header{}, limit: maxAuthzCaptureBytes}
		next(buf, r)
		if buf.overflow {
			return
		}
		body := buf.body
		if buf.code == http.StatusOK {
			var doc map[string]json.RawMessage
			if json.Unmarshal(body, &doc) == nil {
				var runs []authzRunOut
				if json.Unmarshal(doc["runs"], &runs) == nil && len(runs) > 0 {
					id, _ := json.Marshal(e.authz.put(trimAuthzRuns(runs)))
					doc["runId"] = id
					if out, err := json.Marshal(doc); err == nil {
						body = append(out, '\n')
					}
				}
			}
		}
		for k, v := range buf.header {
			w.Header()[k] = v
		}
		w.Header().Del("Content-Length") // the body may have grown by the runId
		if buf.code != 0 {
			w.WriteHeader(buf.code)
		}
		_, _ = w.Write(body)
	}
}

// ---- redaction and bounds --------------------------------------------------

// Hostile (target-controlled) text is clipped before it reaches a renderer:
// header values, URLs, titles, payloads and run metadata can be megabytes.
const (
	maxEvidenceValueRunes = 256 // header values, payloads, extracted values
	maxEvidenceURLRunes   = 512 // URLs and paths
	maxEvidenceLabelRunes = 300 // summaries, grep labels, request labels
	maxEvidenceTitleRunes = 160 // finding titles
	maxEvidenceNameRunes  = 80  // identity, header and attack names
	maxEvidenceRunIDRunes = 64
)

// evidenceText redacts credentials with the shared redact package and bounds
// the result to n runes. It redacts a wider window first so a secret cut by the
// bound is still masked rather than half shown.
func evidenceText(s string, n int) string {
	return clipText(redact.Text(clipText(s, n*4)), n)
}

// redactEvidenceText masks credentials in free text before it is drawn (see
// redact.Text) and bounds it to a label length.
func redactEvidenceText(s string) string { return evidenceText(s, maxEvidenceLabelRunes) }

func redactHeaderValue(name, value string) string {
	value = clipText(value, maxEvidenceValueRunes*4)
	if isAuthHeaderKey(name) {
		return clipText(redact.Describe(value).Placeholder(), maxEvidenceValueRunes)
	}
	return evidenceText(value, maxEvidenceValueRunes)
}

// maskedValue is the drawn form of a masked payload or extracted value: its
// length and a short digest, so equal values stay comparable without showing a
// single character of the secret (a 6-digit OTP must not leak half its digits).
func maskedValue(v string) string {
	d := redact.Describe(v)
	return fmt.Sprintf("[len %d #%s]", d.Len, d.SHA256Prefix[:6])
}

// ---- common response helpers ----------------------------------------------

func writeRenderedPNG(w http.ResponseWriter, r *http.Request, rd preview.Rendered, sourceRef, name string) {
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Cache-Control", "private, max-age=60")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		out := map[string]any{
			"alt": rd.Alt, "summary": rd.Summary, "kind": rd.Kind,
			"width": rd.Width, "height": rd.Height, "sourceRef": sourceRef,
		}
		if preview.ParseBool(r.URL.Query().Get("png"), false) {
			// png=1 embeds the image for agents (MCP). Anything over the inline
			// cap is withheld with its size so a client never has to read megabytes.
			if len(rd.PNG) <= maxInlineRenderPNG {
				out["png"] = base64.StdEncoding.EncodeToString(rd.PNG)
			} else {
				out["pngOmitted"], out["bytes"] = true, len(rd.PNG)
			}
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s.png"`, name))
	// The UI shows these (and stores the alt as the image caption); they are
	// percent-encoded so any text is a valid header value.
	w.Header().Set("X-Render-Alt", url.PathEscape(clipText(rd.Alt, maxRenderHeaderRunes)))
	w.Header().Set("X-Render-Summary", url.PathEscape(clipText(rd.Summary, maxRenderHeaderRunes)))
	_, _ = w.Write(rd.PNG)
}

// clipText bounds untrusted text to n runes before it reaches a renderer.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func evidenceOpts(width int) preview.Opts {
	return preview.Opts{Width: width, Deadline: time.Now().Add(renderTimeout)}
}

// parseExpectedParam reads the optional race baseline (how many requests the
// application should have accepted).
func parseExpectedParam(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 1_000_000 {
		return 0, evErr(http.StatusBadRequest, "expected must be a non-negative integer")
	}
	return n, nil
}

func validEvidenceWidth(n int) bool {
	return n == 0 || (n >= minEvidenceWidth && n <= maxEvidenceWidth)
}

func parseWidthParam(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || !validEvidenceWidth(n) {
		return 0, evErr(http.StatusBadRequest, fmt.Sprintf("width must be 0 (default) or between %d and %d", minEvidenceWidth, maxEvidenceWidth))
	}
	return n, nil
}

// ---- render requests -------------------------------------------------------

// evidenceRequest is the normalized description of one render, shared by the
// GET endpoints and the attach endpoint.
type evidenceRequest struct {
	Kind      string // canonical preview.Kind*
	RunID     string // intruder attack id or authz run id
	FlowIDs   []int64
	A, B      int64
	FindingID int64
	Width     int
	// Mask hides payloads and extracted values (default). Unmasking is an
	// explicit opt-in because those values are often credentials.
	Mask bool
	// IncludeBody draws response-body diff lines (default off: bodies can carry
	// PII that pattern redaction cannot see).
	IncludeBody bool
	// Expected is how many requests the application should have accepted (race).
	Expected int
}

// evidenceResult is a rendered image plus its server-derived provenance.
type evidenceResult struct {
	R            preview.Rendered
	SourceRef    string
	SourceFlowID int64
}

func canonicalEvidenceKind(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "timeline", "intruder_timeline", "intruder-timeline":
		return preview.KindIntruderTimeline, true
	case "distribution", "intruder_distribution", "intruder-distribution":
		return preview.KindIntruderDistribution, true
	case "race", "intruder_race", "intruder-race":
		return preview.KindIntruderRace, true
	case "strip", "intruder_strip", "intruder-strip":
		return preview.KindIntruderStrip, true
	case "authz", "authz_matrix", "authz-matrix":
		return preview.KindAuthzMatrix, true
	case "flow-diff", "flow_diff", "diff":
		return preview.KindFlowDiff, true
	case "flow-waterfall", "flow_waterfall", "waterfall":
		return preview.KindFlowWaterfall, true
	case "finding-chain", "finding_chain", "chain":
		return preview.KindFindingChain, true
	}
	return "", false
}

func isIntruderKind(k string) bool {
	switch k {
	case preview.KindIntruderTimeline, preview.KindIntruderDistribution, preview.KindIntruderRace, preview.KindIntruderStrip:
		return true
	}
	return false
}

func (e *evidenceAPI) render(q evidenceRequest) (evidenceResult, error) {
	select {
	case e.sem <- struct{}{}:
		defer func() { <-e.sem }()
	default:
		return evidenceResult{}, evErr(http.StatusServiceUnavailable, "too many renders in progress; retry shortly")
	}
	res, err := e.renderLimited(q)
	return res, mapRenderError(err)
}

func (e *evidenceAPI) renderLimited(q evidenceRequest) (evidenceResult, error) {
	o := evidenceOpts(q.Width)
	switch {
	case isIntruderKind(q.Kind):
		return e.renderIntruder(q, o)
	case q.Kind == preview.KindAuthzMatrix:
		return e.renderAuthz(q, o)
	case q.Kind == preview.KindFlowDiff:
		return e.renderDiff(q, o)
	case q.Kind == preview.KindFlowWaterfall:
		return e.renderWaterfall(q, o)
	case q.Kind == preview.KindFindingChain:
		return e.renderChain(q, o)
	}
	return evidenceResult{}, evErr(http.StatusBadRequest, "unknown render kind")
}

func (e *evidenceAPI) renderIntruder(q evidenceRequest, o preview.Opts) (evidenceResult, error) {
	env, id, err := e.loadIntruderRun(q.RunID)
	if err != nil {
		return evidenceResult{}, err
	}
	var rd preview.Rendered
	switch q.Kind {
	case preview.KindIntruderTimeline:
		rd, err = preview.RenderIntruderTimeline(intruderTimelineInput(env), o)
	case preview.KindIntruderDistribution:
		rd, err = preview.RenderIntruderDistribution(intruderDistributionInput(env), o)
	case preview.KindIntruderRace:
		rd, err = preview.RenderIntruderRace(intruderRaceInput(env, q.Mask, q.Expected), o)
	default:
		rd, err = preview.RenderIntruderStrip(intruderStripInput(env, q.Mask), o)
	}
	if err != nil {
		return evidenceResult{}, err
	}
	return evidenceResult{R: rd, SourceRef: "intruder:" + id, SourceFlowID: env.firstFlowID()}, nil
}

// firstFlowID is the flow of the earliest dispatched request that recorded
// one, so an attached render links back to captured evidence.
func (env intruderRunEnvelope) firstFlowID() int64 {
	var best intruder.Result
	found := false
	for _, r := range env.State.Results {
		if r.FlowID > 0 && (!found || resultSeq(r) < resultSeq(best)) {
			best, found = r, true
		}
	}
	if !found {
		return 0
	}
	return best.FlowID
}

func (e *evidenceAPI) renderAuthz(q evidenceRequest, o preview.Opts) (evidenceResult, error) {
	id, runs, ok := e.authz.get(strings.TrimSuffix(q.RunID, ".png"))
	if !ok {
		return evidenceResult{}, evErr(http.StatusNotFound, "authz run not found (runs are kept in memory; re-run the authz test)")
	}
	rd, err := preview.RenderAuthzMatrix(authzMatrixInput(id, runs), o)
	if err != nil {
		return evidenceResult{}, err
	}
	var flow int64
	if len(runs) > 0 {
		flow = runs[0].FlowID
	}
	return evidenceResult{R: rd, SourceRef: "authz:" + id, SourceFlowID: flow}, nil
}

func (e *evidenceAPI) renderDiff(q evidenceRequest, o preview.Opts) (evidenceResult, error) {
	if q.A <= 0 || q.B <= 0 {
		return evidenceResult{}, evErr(http.StatusBadRequest, "a and b are required (two integer flow ids)")
	}
	fa, err := e.st.GetFlow(q.A)
	if err != nil {
		return evidenceResult{}, evidenceLookupErr(err, "flow a not found")
	}
	fb, err := e.st.GetFlow(q.B)
	if err != nil {
		return evidenceResult{}, evidenceLookupErr(err, "flow b not found")
	}
	d, err := (&flowAPI{e.Hub}).buildFlowDiff(fa, fb, diffDefaultMaxBytes)
	if err != nil {
		return evidenceResult{}, evErr(http.StatusNotFound, "response body not found")
	}
	rd, err := preview.RenderFlowDiff(flowDiffInput(fa, fb, d, q.IncludeBody), o)
	if err != nil {
		return evidenceResult{}, err
	}
	return evidenceResult{R: rd, SourceRef: fmt.Sprintf("flow-diff:%d-%d", q.A, q.B), SourceFlowID: q.A}, nil
}

func (e *evidenceAPI) renderWaterfall(q evidenceRequest, o preview.Opts) (evidenceResult, error) {
	if len(q.FlowIDs) == 0 {
		return evidenceResult{}, evErr(http.StatusBadRequest, "ids is required (comma-separated flow ids)")
	}
	if len(q.FlowIDs) > maxWaterfallFlows {
		return evidenceResult{}, evErr(http.StatusBadRequest, fmt.Sprintf("at most %d flows", maxWaterfallFlows))
	}
	flows := make([]*store.Flow, 0, len(q.FlowIDs))
	for _, id := range q.FlowIDs {
		f, err := e.st.GetFlow(id)
		if err != nil {
			return evidenceResult{}, evidenceLookupErr(err, fmt.Sprintf("flow %d not found", id))
		}
		flows = append(flows, f)
	}
	rd, err := preview.RenderFlowWaterfall(flowWaterfallInput(flows), o)
	if err != nil {
		return evidenceResult{}, err
	}
	res := evidenceResult{R: rd, SourceRef: waterfallSourceRef(q.FlowIDs)}
	if len(q.FlowIDs) == 1 {
		res.SourceFlowID = q.FlowIDs[0]
	}
	return res, nil
}

func (e *evidenceAPI) renderChain(q evidenceRequest, o preview.Opts) (evidenceResult, error) {
	if q.FindingID <= 0 {
		return evidenceResult{}, evErr(http.StatusBadRequest, "findingId is required")
	}
	in, err := e.findingChainInput(q.FindingID)
	if err != nil {
		return evidenceResult{}, err
	}
	rd, err := preview.RenderFindingChain(in, o)
	if err != nil {
		return evidenceResult{}, err
	}
	return evidenceResult{R: rd, SourceRef: fmt.Sprintf("finding:%d", q.FindingID)}, nil
}

func evidenceLookupErr(err error, notFound string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return evErr(http.StatusNotFound, notFound)
	}
	return err
}

// waterfallSourceRef names the flows; long lists collapse to first-last-count
// so the ref stays within the store's length limit.
func waterfallSourceRef(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	ref := "flows:" + strings.Join(parts, "-")
	if len(ref) <= 80 {
		return ref
	}
	return fmt.Sprintf("flows:%d-%d-n%d", ids[0], ids[len(ids)-1], len(ids))
}

// ---- adapters: flows -------------------------------------------------------

func flowDisplayURL(f *store.Flow) string {
	u := clipText(f.Host, maxEvidenceNameRunes*2) + clipText(f.Path, maxEvidenceURLRunes*4)
	if f.Scheme != "" {
		u = clipText(f.Scheme, 16) + "://" + u
	}
	return evidenceText(u, maxEvidenceURLRunes)
}

func flowSide(f *store.Flow) preview.FlowSide {
	return preview.FlowSide{
		FlowID: f.ID, Method: clipText(f.Method, 16), URL: flowDisplayURL(f),
		Status: f.Status, Length: int(f.ResLen), TimeMs: int(f.DurationMs),
	}
}

// flowDiffInput adapts a flowDiff into preview input. Header values are
// redacted and bounded here; the renderer never sees raw credentials. Body
// lines are omitted unless includeBody is set (only the changed-line count is
// kept); when included they are redacted and the footer says so.
func flowDiffInput(fa, fb *store.Flow, d flowDiff, includeBody bool) preview.FlowDiffInput {
	in := preview.FlowDiffInput{A: flowSide(fa), B: flowSide(fb), Summary: redactEvidenceText(d.Summary)}
	for _, h := range d.HeaderDeltas {
		in.HeaderDeltas = append(in.HeaderDeltas, preview.FlowHeaderDelta{
			Name: clipText(h.Name, maxEvidenceNameRunes), Kind: h.Kind,
			A: redactHeaderValue(h.Name, h.A), B: redactHeaderValue(h.Name, h.B),
		})
	}
	for _, b := range d.BodyDeltas {
		switch {
		case b.A == "" && b.B != "":
			in.BodyAdded++
		case b.B == "" && b.A != "":
			in.BodyRemoved++
		default:
			in.BodyAdded++
			in.BodyRemoved++
		}
		if !includeBody {
			continue
		}
		line := func(v string) string { return evidenceText(v, maxEvidenceLabelRunes) }
		switch {
		case b.A == "" && b.B != "":
			in.BodyDeltas = append(in.BodyDeltas, preview.FlowBodyDelta{Kind: "+", Line: line(b.B)})
		case b.B == "" && b.A != "":
			in.BodyDeltas = append(in.BodyDeltas, preview.FlowBodyDelta{Kind: "-", Line: line(b.A)})
		default:
			in.BodyDeltas = append(in.BodyDeltas,
				preview.FlowBodyDelta{Kind: "-", Line: line(b.A)},
				preview.FlowBodyDelta{Kind: "+", Line: line(b.B)})
		}
	}
	if includeBody {
		in.BodyNote = "Body excerpt redacted: yes (pattern redaction cannot hide personal data)"
	} else {
		in.BodyOmitted = true
		in.BodyNote = "Body excerpt not included (opt in with includeBody=1)"
	}
	if d.BodyMoreLines > 0 {
		in.Summary = strings.TrimSpace(in.Summary + fmt.Sprintf(" (+%d more changed lines not shown)", d.BodyMoreLines))
	}
	return in
}

func flowWaterfallInput(flows []*store.Flow) preview.WaterfallInput {
	in := preview.WaterfallInput{Title: "Flow timing sequence"}
	for _, f := range flows {
		in.Rows = append(in.Rows, preview.WaterfallRow{
			FlowID: f.ID, Method: clipText(f.Method, 16), Path: evidenceText(f.Path, maxEvidenceURLRunes), Status: f.Status,
			StartMs: f.TS.UnixMilli(), DurationMs: int(f.DurationMs),
		})
	}
	return in
}

// ---- adapters: authz -------------------------------------------------------

// authzMatrixInput builds the identity-by-request matrix. Columns are the
// identities in first-seen order (the first is the baseline). A non-baseline
// cell is marked Broken only when it succeeded with the same access as the
// baseline, which is the condition the authz run itself reports as flagged;
// skipped (account-marked-broken) cells are shown as session invalid.
func authzMatrixInput(runID string, runs []authzRunOut) preview.AuthzMatrixInput {
	in := preview.AuthzMatrixInput{RunID: runID}
	colIdx := map[string]int{}
	for _, run := range runs {
		for _, r := range run.Results {
			if _, ok := colIdx[r.Name]; !ok {
				colIdx[r.Name] = len(in.Cols)
				in.Cols = append(in.Cols, evidenceText(r.Name, maxEvidenceNameRunes))
			}
		}
	}
	if len(in.Cols) > 0 {
		in.BaselineName = in.Cols[0]
	}
	for _, run := range runs {
		row := preview.AuthzRow{Label: evidenceText(strings.TrimSpace(clipText(run.Method, 16)+" "+clipText(run.Path, maxEvidenceURLRunes*4)), maxEvidenceURLRunes), Cells: make([]preview.AuthzCell, len(in.Cols))}
		for _, r := range run.Results {
			ci := colIdx[r.Name]
			skipped := r.Error != "" && r.Status == 0
			row.Cells[ci] = preview.AuthzCell{
				Status: r.Status, Length: int(r.Length), FlowID: r.FlowID,
				SameAsBaseline: r.Same, AccessDenied: r.AccessDenied,
				SessionInvalid: r.SessionInvalid || skipped,
				Broken:         ci > 0 && r.Same && r.Status > 0 && r.Status < 400 && !r.AccessDenied,
			}
		}
		in.Rows = append(in.Rows, row)
	}
	return in
}

// ---- adapters: finding chain ----------------------------------------------

type chainFinder interface {
	GetFinding(id int64) (*store.Finding, error)
}

// chainInputFrom walks relatedFindings breadth-first from root (bounded by
// depth and node count) and normalizes relation direction: enabled_by is the
// reverse of enables; chain, duplicate and escalates are symmetric and
// ordered by id so mirrored records collapse to one edge.
func chainInputFrom(src chainFinder, root int64) (preview.ChainInput, error) {
	rootF, err := src.GetFinding(root)
	if err != nil {
		return preview.ChainInput{}, err
	}
	nodes := map[int64]*store.Finding{root: rootF}
	order := []int64{root}
	depth := map[int64]int{root: 0}
	type edgeKey struct {
		from, to int64
		kind     string
	}
	seen := map[edgeKey]bool{}
	var edges []edgeKey
	addEdge := func(from, to int64, kind string) {
		k := edgeKey{from, to, kind}
		if !seen[k] {
			seen[k] = true
			edges = append(edges, k)
		}
	}
	for i := 0; i < len(order); i++ {
		id := order[i]
		f := nodes[id]
		for _, rel := range f.RelatedFindings {
			other := rel.ID
			if other == id {
				continue
			}
			if _, ok := nodes[other]; !ok {
				if len(order) >= maxChainNodes || depth[id] >= maxChainDepth {
					continue
				}
				of, err := src.GetFinding(other)
				if err != nil || of == nil {
					continue
				}
				nodes[other] = of
				depth[other] = depth[id] + 1
				order = append(order, other)
			}
			switch rel.Relation {
			case "enables":
				addEdge(id, other, "enables")
			case "enabled_by":
				addEdge(other, id, "enables")
			default:
				lo, hi := id, other
				if lo > hi {
					lo, hi = hi, lo
				}
				addEdge(lo, hi, rel.Relation)
			}
		}
	}
	in := preview.ChainInput{Title: fmt.Sprintf("Finding chain around #%d", root)}
	for _, id := range order {
		f := nodes[id]
		in.Nodes = append(in.Nodes, preview.ChainNode{
			ID: strconv.FormatInt(id, 10), Title: evidenceText(fmt.Sprintf("#%d %s", id, clipText(f.Title, maxEvidenceTitleRunes*4)), maxEvidenceTitleRunes), Severity: clipText(f.Severity, 16),
		})
	}
	for _, e := range edges {
		in.Edges = append(in.Edges, preview.ChainEdge{From: strconv.FormatInt(e.from, 10), To: strconv.FormatInt(e.to, 10), Kind: e.kind})
	}
	return in, nil
}

func (e *evidenceAPI) findingChainInput(root int64) (preview.ChainInput, error) {
	in, err := chainInputFrom(e.st, root)
	if err != nil {
		return in, evidenceLookupErr(err, "finding not found")
	}
	return in, nil
}

// ---- handlers --------------------------------------------------------------

func (e *evidenceAPI) serveRender(w http.ResponseWriter, r *http.Request, q evidenceRequest, name string) {
	res, err := e.render(q)
	if err != nil {
		writeEvidenceError(w, err)
		return
	}
	writeRenderedPNG(w, r, res.R, res.SourceRef, name)
}

// widthAndMask parses width and the masking choice. Masking is the default;
// mask=0 or unmask=1 opts out.
func widthAndMask(r *http.Request) (int, bool, error) {
	w, err := parseWidthParam(r.URL.Query().Get("width"))
	if err != nil {
		return 0, false, err
	}
	return w, queryMask(r), nil
}

func queryMask(r *http.Request) bool {
	q := r.URL.Query()
	if preview.ParseBool(q.Get("unmask"), false) {
		return false
	}
	return preview.ParseBool(q.Get("mask"), true)
}

// GET /api/render/authz/{runId}[.png]
func (e *evidenceAPI) getAuthzRender(w http.ResponseWriter, r *http.Request) {
	width, _, err := widthAndMask(r)
	if err != nil {
		writeEvidenceError(w, err)
		return
	}
	id := strings.TrimSuffix(r.PathValue("runId"), ".png")
	e.serveRender(w, r, evidenceRequest{Kind: preview.KindAuthzMatrix, RunID: id, Width: width}, "authz-"+id)
}

// GET /api/render/flow-diff.png?a=&b=
func (e *evidenceAPI) getFlowDiffRender(w http.ResponseWriter, r *http.Request) {
	width, _, err := widthAndMask(r)
	if err != nil {
		writeEvidenceError(w, err)
		return
	}
	v := r.URL.Query()
	a, err := parseStrictID(v.Get("a"), "a")
	if err != nil {
		writeEvidenceError(w, err)
		return
	}
	b, err := parseStrictID(v.Get("b"), "b")
	if err != nil {
		writeEvidenceError(w, err)
		return
	}
	e.serveRender(w, r, evidenceRequest{Kind: preview.KindFlowDiff, A: a, B: b, Width: width,
		IncludeBody: preview.ParseBool(v.Get("includeBody"), false)}, fmt.Sprintf("flow-diff-%d-%d", a, b))
}

// parseStrictID reads an optional positive integer id: empty means unset (0),
// anything else that is not a positive integer is a 400 rather than a silent 0.
func parseStrictID(raw, name string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	id, ok := parseFlowIDParam(raw)
	if !ok {
		return 0, evErr(http.StatusBadRequest, name+" must be a positive integer")
	}
	return id, nil
}

// GET /api/render/flow-waterfall.png?ids=1,2,3
func (e *evidenceAPI) getFlowWaterfallRender(w http.ResponseWriter, r *http.Request) {
	width, _, err := widthAndMask(r)
	if err != nil {
		writeEvidenceError(w, err)
		return
	}
	ids, err := parseFlowIDList(r.URL.Query().Get("ids"))
	if err != nil {
		writeEvidenceError(w, err)
		return
	}
	e.serveRender(w, r, evidenceRequest{Kind: preview.KindFlowWaterfall, FlowIDs: ids, Width: width}, "flow-waterfall")
}

// GET /api/render/finding-chain.png?findingId=
func (e *evidenceAPI) getFindingChainRender(w http.ResponseWriter, r *http.Request) {
	width, _, err := widthAndMask(r)
	if err != nil {
		writeEvidenceError(w, err)
		return
	}
	fid, err := parseStrictID(r.URL.Query().Get("findingId"), "findingId")
	if err != nil {
		writeEvidenceError(w, err)
		return
	}
	e.serveRender(w, r, evidenceRequest{Kind: preview.KindFindingChain, FindingID: fid, Width: width}, fmt.Sprintf("finding-chain-%d", fid))
}

// GET /api/evidence-render?kind=&runId=&flowIdA=&flowIdB=&flowIds=&findingIds=
// is the single-endpoint form of the renders above, used by the MCP tools.
func (e *evidenceAPI) getEvidenceRender(w http.ResponseWriter, r *http.Request) {
	q, err := evidenceRequestFromQuery(r)
	if err != nil {
		writeEvidenceError(w, err)
		return
	}
	e.serveRender(w, r, q, "evidence-"+q.Kind)
}

func evidenceRequestFromQuery(r *http.Request) (evidenceRequest, error) {
	v := r.URL.Query()
	kind, ok := canonicalEvidenceKind(v.Get("kind"))
	if !ok {
		return evidenceRequest{}, evErr(http.StatusBadRequest, "kind must be one of timeline, distribution, race, strip, authz_matrix, flow_diff, flow_waterfall, finding_chain")
	}
	width, mask, err := widthAndMask(r)
	if err != nil {
		return evidenceRequest{}, err
	}
	q := evidenceRequest{Kind: kind, RunID: strings.TrimSpace(v.Get("runId")), Width: width, Mask: mask,
		IncludeBody: preview.ParseBool(v.Get("includeBody"), false)}
	if q.Expected, err = parseExpectedParam(v.Get("expected")); err != nil {
		return evidenceRequest{}, err
	}
	if q.A, err = parseStrictID(orVal(v.Get("flowIdA"), v.Get("a")), "flowIdA"); err != nil {
		return evidenceRequest{}, err
	}
	if q.B, err = parseStrictID(orVal(v.Get("flowIdB"), v.Get("b")), "flowIdB"); err != nil {
		return evidenceRequest{}, err
	}
	if q.FlowIDs, err = parseFlowIDList(orVal(v.Get("flowIds"), v.Get("ids"))); err != nil {
		return evidenceRequest{}, err
	}
	if q.FindingID, err = firstFindingID(orVal(v.Get("findingId"), v.Get("findingIds"))); err != nil {
		return evidenceRequest{}, err
	}
	if isIntruderKind(kind) && q.RunID == "" {
		q.RunID = "latest"
	}
	return q, nil
}

// firstFindingID returns the first id of a comma-separated list (the chain
// root); a non-numeric entry is a 400.
func firstFindingID(raw string) (int64, error) {
	for _, p := range strings.Split(raw, ",") {
		if strings.TrimSpace(p) == "" {
			continue
		}
		return parseStrictID(p, "findingId")
	}
	return 0, nil
}

func parseFlowIDList(raw string) ([]int64, error) {
	var ids []int64
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, ok := parseFlowIDParam(p)
		if !ok {
			return nil, evErr(http.StatusBadRequest, "ids must be comma-separated positive integers")
		}
		ids = append(ids, id)
		if len(ids) > maxWaterfallFlows {
			return nil, evErr(http.StatusBadRequest, fmt.Sprintf("at most %d flows", maxWaterfallFlows))
		}
	}
	return ids, nil
}

// POST /api/findings/{id}/evidence-render
func (e *evidenceAPI) attachEvidenceRender(w http.ResponseWriter, r *http.Request) {
	findingID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	fd := &findingsAPI{e.Hub}
	if !fd.requireFinding(w, findingID) {
		return
	}
	var in struct {
		Kind        string  `json:"kind"`
		AttackID    string  `json:"attackId"`
		RunID       string  `json:"runId"`
		FlowIDs     []int64 `json:"flowIds"`
		A           int64   `json:"a"`
		B           int64   `json:"b"`
		FlowIDA     int64   `json:"flowIdA"`
		FlowIDB     int64   `json:"flowIdB"`
		FindingID   int64   `json:"findingId"`
		FindingIDs  []int64 `json:"findingIds"`
		Caption     string  `json:"caption"`
		Role        string  `json:"role"`
		Proof       string  `json:"proof"`
		Position    *int    `json:"position"`
		Width       int     `json:"width"`
		Mask        *bool   `json:"mask"`
		Unmask      bool    `json:"unmask"`
		IncludeBody bool    `json:"includeBody"`
		Expected    int     `json:"expected"`
	}
	if !decodeLimitedJSON(w, r, maxEvidenceRenderRequestBytes, &in) {
		return
	}
	kind, ok := canonicalEvidenceKind(in.Kind)
	if !ok {
		httpErr(w, http.StatusBadRequest, "kind must be one of timeline, distribution, race, strip, authz, flow-diff, flow-waterfall, finding-chain")
		return
	}
	if !validEvidenceWidth(in.Width) || len(in.FlowIDs) > maxWaterfallFlows {
		httpErr(w, http.StatusBadRequest, fmt.Sprintf("width must be 0 (default) or between %d and %d, with at most %d flowIds", minEvidenceWidth, maxEvidenceWidth, maxWaterfallFlows))
		return
	}
	for _, id := range append([]int64{in.A, in.B, in.FlowIDA, in.FlowIDB, in.FindingID}, append(in.FlowIDs, in.FindingIDs...)...) {
		if id < 0 {
			httpErr(w, http.StatusBadRequest, "ids must be positive integers")
			return
		}
	}
	runID := strings.TrimSpace(in.AttackID)
	if runID == "" {
		runID = strings.TrimSpace(in.RunID)
	}
	if in.A == 0 {
		in.A = in.FlowIDA
	}
	if in.B == 0 {
		in.B = in.FlowIDB
	}
	if in.FindingID == 0 && len(in.FindingIDs) > 0 {
		in.FindingID = in.FindingIDs[0]
	}
	if kind == preview.KindFindingChain && in.FindingID == 0 {
		in.FindingID = findingID
	}
	mask := !in.Unmask && (in.Mask == nil || *in.Mask)
	if in.Expected < 0 || in.Expected > 1_000_000 {
		httpErr(w, http.StatusBadRequest, "expected must be a non-negative integer")
		return
	}
	res, err := e.render(evidenceRequest{Kind: kind, RunID: runID, FlowIDs: in.FlowIDs, A: in.A, B: in.B, FindingID: in.FindingID,
		Width: in.Width, Mask: mask, IncludeBody: in.IncludeBody, Expected: in.Expected})
	if err != nil {
		writeEvidenceError(w, err)
		return
	}
	caption := strings.TrimSpace(in.Caption)
	if caption == "" {
		caption = res.R.Alt
	}
	pos := -1
	if in.Position != nil {
		pos = *in.Position
	}
	if _, _, err = e.st.PutAndAttachImageRef(findingID, "image/png", res.R.PNG, caption, pos, in.Role, in.Proof,
		evidenceRenderSource, res.SourceFlowID, res.SourceRef, findingAPIChange("")); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	e.broadcast(map[string]any{"type": "findings.update"})
	out, err := e.st.GetFinding(findingID)
	if err != nil {
		httpNotFoundOrInternal(w, err, "finding not found")
		return
	}
	writeJSON(w, http.StatusOK, findingAPIResponse(out, nil))
}

// ---- Intruder run persistence ---------------------------------------------

// WireIntruderRunSink persists every finished Intruder run to the store so
// renders and GET /api/intruder/attacks/{id} survive the next run and restarts.
func (h *Hub) WireIntruderRunSink() {
	if h.intr == nil || h.st == nil {
		return
	}
	h.intr.SetRunSink(func(rec intruder.RunRecord) { persistIntruderRun(h.st, rec) })
}

type intruderRunStore interface {
	PutIntruderRun(id string, rec []byte) error
}

func persistIntruderRun(st intruderRunStore, rec intruder.RunRecord) {
	env, ok := newIntruderEnvelope(rec)
	if !ok {
		return
	}
	err := putIntruderEnvelope(st, env)
	if errors.Is(err, store.ErrIntruderRunTooLarge) {
		// Stage 1: drop the free-text fields (payload, extracted value).
		for i := range env.State.Results {
			env.State.Results[i].Payload, env.State.Results[i].Extracted = "", ""
		}
		err = putIntruderEnvelope(st, env)
	}
	if errors.Is(err, store.ErrIntruderRunTooLarge) {
		// Stage 2: drop bulky text (rate-limit headers, error strings). The
		// error flag survives as a single character; timing and status stay.
		for i := range env.State.Results {
			r := &env.State.Results[i]
			r.RLHeaders = nil
			r.Error = clipText(r.Error, 1)
		}
		err = putIntruderEnvelope(st, env)
	}
	if errors.Is(err, store.ErrIntruderRunTooLarge) {
		// Stage 3: keep the head and tail of the run, halving until it fits,
		// and say so in the run state so a render never silently shows a
		// shorter run than the operator started.
		all := env.State.Results
		for keep := len(all) / 4; keep >= 1 && errors.Is(err, store.ErrIntruderRunTooLarge); keep /= 2 {
			env.State.Results = append(append([]intruder.Result(nil), all[:keep]...), all[len(all)-keep:]...)
			env.State.Capped = true
			env.State.Error = fmt.Sprintf("run record truncated to the first and last %d of %d results to fit the %d MiB store limit", keep, len(all), 4)
			err = putIntruderEnvelope(st, env)
		}
	}
	if err != nil {
		log.Printf("control: persist intruder run %s: %v", env.RunID, err)
	}
}

func putIntruderEnvelope(st intruderRunStore, env intruderRunEnvelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return st.PutIntruderRun(env.RunID, b)
}
