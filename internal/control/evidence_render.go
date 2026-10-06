package control

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

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
}

func newEvidenceAPI(h *Hub) *evidenceAPI {
	return &evidenceAPI{Hub: h, authz: &authzRunCache{runs: map[string][]authzRunOut{}}}
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

// bufferedResponse captures a handler's response so it can be post-processed.
type bufferedResponse struct {
	header http.Header
	code   int
	body   []byte
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) WriteHeader(code int) {
	if b.code == 0 {
		b.code = code
	}
}
func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.code == 0 {
		b.code = http.StatusOK
	}
	b.body = append(b.body, p...)
	return len(p), nil
}

// captureAuthzRun wraps POST /api/authz/run: a successful response is cached
// under a new runId, which is added to the JSON as "runId". The authz handler
// itself is unchanged.
func (e *evidenceAPI) captureAuthzRun(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		buf := &bufferedResponse{header: http.Header{}}
		next(buf, r)
		body := buf.body
		if buf.code == http.StatusOK {
			var doc map[string]json.RawMessage
			var runs struct {
				Runs []authzRunOut `json:"runs"`
			}
			if json.Unmarshal(body, &doc) == nil && json.Unmarshal(body, &runs) == nil && len(runs.Runs) > 0 {
				id, _ := json.Marshal(e.authz.put(runs.Runs))
				doc["runId"] = id
				if out, err := json.Marshal(doc); err == nil {
					body = append(out, '\n')
				}
			}
		}
		for k, v := range buf.header {
			w.Header()[k] = v
		}
		if buf.code != 0 {
			w.WriteHeader(buf.code)
		}
		_, _ = w.Write(body)
	}
}

// ---- redaction -------------------------------------------------------------

var (
	redactHeaderLine = regexp.MustCompile(`(?i)\b((?:proxy-)?authorization|set-cookie|cookie|x-api-key|x-auth-token|x-access-token|x-csrf-token|x-xsrf-token)\b(\s*[:=]\s*)([^\r\n]+)`)
	redactScheme     = regexp.MustCompile(`(?i)\b(bearer|basic|digest|negotiate)(\s+)([A-Za-z0-9._~+/=:-]{6,})`)
	redactJWT        = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`)
	redactKeyValue   = regexp.MustCompile(`(?i)\b((?:access_|refresh_|id_)?token|api[_-]?key|secret|client_secret|password|passwd|pwd|session(?:id)?|sid|auth|signature)(["']?\s*[:=]\s*["']?)([^&\s"',;]+)`)
	redactLongToken  = regexp.MustCompile(`[A-Za-z0-9_+=]{32,}`)
)

func secretPlaceholder(v string) string { return redact.Describe(v).Placeholder() }

// redactEvidenceText masks credentials in free text before it is drawn:
// credential header lines, Bearer/Basic schemes, JWTs, key=value secrets and
// long opaque tokens. Masked values become a length+digest placeholder so two
// equal secrets stay comparable without being shown.
func redactEvidenceText(s string) string {
	if s == "" {
		return s
	}
	s = redactHeaderLine.ReplaceAllStringFunc(s, func(m string) string {
		p := redactHeaderLine.FindStringSubmatch(m)
		return p[1] + p[2] + secretPlaceholder(p[3])
	})
	s = redactScheme.ReplaceAllStringFunc(s, func(m string) string {
		p := redactScheme.FindStringSubmatch(m)
		if strings.HasPrefix(p[3], "[redacted") {
			return m
		}
		return p[1] + p[2] + secretPlaceholder(p[3])
	})
	s = redactJWT.ReplaceAllStringFunc(s, secretPlaceholder)
	s = redactKeyValue.ReplaceAllStringFunc(s, func(m string) string {
		p := redactKeyValue.FindStringSubmatch(m)
		if strings.HasPrefix(p[3], "[redacted") {
			return m
		}
		return p[1] + p[2] + secretPlaceholder(p[3])
	})
	return redactLongToken.ReplaceAllStringFunc(s, func(m string) string {
		if looksOpaqueToken(m) {
			return secretPlaceholder(m)
		}
		return m
	})
}

// looksOpaqueToken is true for long runs mixing letters and digits (API keys,
// session ids, hashes); long plain words and identifiers are left alone.
func looksOpaqueToken(s string) bool {
	letter, digit := false, false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			letter = true
		}
	}
	return letter && digit
}

func redactHeaderValue(name, value string) string {
	if isAuthHeaderKey(name) {
		return secretPlaceholder(value)
	}
	return redactEvidenceText(value)
}

// ---- common response helpers ----------------------------------------------

func writeRenderedPNG(w http.ResponseWriter, r *http.Request, rd preview.Rendered, sourceRef, name string) {
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Cache-Control", "private, max-age=60")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		writeJSON(w, http.StatusOK, map[string]any{
			"alt": rd.Alt, "summary": rd.Summary, "kind": rd.Kind,
			"width": rd.Width, "height": rd.Height, "sourceRef": sourceRef,
		})
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s.png"`, name))
	_, _ = w.Write(rd.PNG)
}

func evidenceOpts(width int) preview.Opts { return preview.Opts{Width: width} }

func parseWidthParam(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 100000 {
		return 0, evErr(http.StatusBadRequest, "width must be a positive integer")
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
	Mask      bool
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
		rd, err = preview.RenderIntruderRace(intruderRaceInput(env), o)
	default:
		rd, err = preview.RenderIntruderStrip(intruderStripInput(env, q.Mask), o)
	}
	if err != nil {
		return evidenceResult{}, err
	}
	return evidenceResult{R: rd, SourceRef: "intruder:" + id}, nil
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
	return evidenceResult{R: rd, SourceRef: "authz:" + id}, nil
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
	rd, err := preview.RenderFlowDiff(flowDiffInput(fa, fb, d), o)
	if err != nil {
		return evidenceResult{}, err
	}
	return evidenceResult{R: rd, SourceRef: fmt.Sprintf("flow-diff:%d-%d", q.A, q.B)}, nil
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
	u := f.Host + f.Path
	if f.Scheme != "" {
		u = f.Scheme + "://" + u
	}
	return redactEvidenceText(u)
}

func flowSide(f *store.Flow) preview.FlowSide {
	return preview.FlowSide{
		FlowID: f.ID, Method: f.Method, URL: flowDisplayURL(f),
		Status: f.Status, Length: int(f.ResLen), TimeMs: int(f.DurationMs),
	}
}

// flowDiffInput adapts a flowDiff into preview input. Header values and body
// lines are redacted here; the renderer never sees raw credentials.
func flowDiffInput(fa, fb *store.Flow, d flowDiff) preview.FlowDiffInput {
	in := preview.FlowDiffInput{A: flowSide(fa), B: flowSide(fb), Summary: redactEvidenceText(d.Summary)}
	for _, h := range d.HeaderDeltas {
		in.HeaderDeltas = append(in.HeaderDeltas, preview.FlowHeaderDelta{
			Name: h.Name, Kind: h.Kind, A: redactHeaderValue(h.Name, h.A), B: redactHeaderValue(h.Name, h.B),
		})
	}
	for _, b := range d.BodyDeltas {
		switch {
		case b.A == "" && b.B != "":
			in.BodyDeltas = append(in.BodyDeltas, preview.FlowBodyDelta{Kind: "+", Line: redactEvidenceText(b.B)})
		case b.B == "" && b.A != "":
			in.BodyDeltas = append(in.BodyDeltas, preview.FlowBodyDelta{Kind: "-", Line: redactEvidenceText(b.A)})
		default:
			in.BodyDeltas = append(in.BodyDeltas,
				preview.FlowBodyDelta{Kind: "-", Line: redactEvidenceText(b.A)},
				preview.FlowBodyDelta{Kind: "+", Line: redactEvidenceText(b.B)})
		}
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
			FlowID: f.ID, Method: f.Method, Path: redactEvidenceText(f.Path), Status: f.Status,
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
				in.Cols = append(in.Cols, r.Name)
			}
		}
	}
	if len(in.Cols) > 0 {
		in.BaselineName = in.Cols[0]
	}
	for _, run := range runs {
		row := preview.AuthzRow{Label: redactEvidenceText(strings.TrimSpace(run.Method + " " + run.Path)), Cells: make([]preview.AuthzCell, len(in.Cols))}
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
			ID: strconv.FormatInt(id, 10), Title: redactEvidenceText(fmt.Sprintf("#%d %s", id, f.Title)), Severity: f.Severity,
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

func widthAndMask(r *http.Request) (int, bool, error) {
	w, err := parseWidthParam(r.URL.Query().Get("width"))
	if err != nil {
		return 0, false, err
	}
	return w, preview.ParseBool(r.URL.Query().Get("mask"), false), nil
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
	a, _ := parseFlowIDParam(r.URL.Query().Get("a"))
	b, _ := parseFlowIDParam(r.URL.Query().Get("b"))
	e.serveRender(w, r, evidenceRequest{Kind: preview.KindFlowDiff, A: a, B: b, Width: width}, fmt.Sprintf("flow-diff-%d-%d", a, b))
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
	fid, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("findingId")), 10, 64)
	e.serveRender(w, r, evidenceRequest{Kind: preview.KindFindingChain, FindingID: fid, Width: width}, fmt.Sprintf("finding-chain-%d", fid))
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
		Kind      string  `json:"kind"`
		AttackID  string  `json:"attackId"`
		RunID     string  `json:"runId"`
		FlowIDs   []int64 `json:"flowIds"`
		A         int64   `json:"a"`
		B         int64   `json:"b"`
		FindingID int64   `json:"findingId"`
		Caption   string  `json:"caption"`
		Role      string  `json:"role"`
		Proof     string  `json:"proof"`
		Position  *int    `json:"position"`
		Width     int     `json:"width"`
		Mask      bool    `json:"mask"`
	}
	if !decodeLimitedJSON(w, r, maxEvidenceRenderRequestBytes, &in) {
		return
	}
	kind, ok := canonicalEvidenceKind(in.Kind)
	if !ok {
		httpErr(w, http.StatusBadRequest, "kind must be one of timeline, distribution, race, strip, authz, flow-diff, flow-waterfall, finding-chain")
		return
	}
	if in.Width < 0 || in.Width > 100000 || len(in.FlowIDs) > maxWaterfallFlows {
		httpErr(w, http.StatusBadRequest, "invalid width or too many flowIds")
		return
	}
	runID := strings.TrimSpace(in.AttackID)
	if runID == "" {
		runID = strings.TrimSpace(in.RunID)
	}
	if kind == preview.KindFindingChain && in.FindingID == 0 {
		in.FindingID = findingID
	}
	res, err := e.render(evidenceRequest{Kind: kind, RunID: runID, FlowIDs: in.FlowIDs, A: in.A, B: in.B, FindingID: in.FindingID, Width: in.Width, Mask: in.Mask})
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
	b, err := json.Marshal(env)
	if err == nil {
		err = st.PutIntruderRun(env.RunID, b)
	}
	if errors.Is(err, store.ErrIntruderRunTooLarge) {
		// Drop the bulky free-text fields and retry once; timing and status survive.
		for i := range env.State.Results {
			env.State.Results[i].Payload, env.State.Results[i].Extracted = "", ""
		}
		if b, err = json.Marshal(env); err == nil {
			err = st.PutIntruderRun(env.RunID, b)
		}
	}
	if err != nil {
		log.Printf("control: persist intruder run %s: %v", env.RunID, err)
	}
}
