package control

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// AI-origin tagging contract (issues #114, #115).
//
// Every control-plane endpoint that records a flow must turn the
// "X-Interseptor-Source: ai" header (stamped by the MCP bridge on every call)
// into store.FlagAI, and must NOT set it when the header is absent. Tagging is
// applied per send site (aiSourceFlag(r)), so a new send path that forgets it is
// invisible to the rest of the suite. Two tests close that gap:
//
//   - TestAISourceFlagOnEverySendEndpoint drives each endpoint in
//     aiFlagEndpoints with and without the header and checks the recorded flows.
//     Subtests are named after the endpoint, so a failure says which site lost
//     its aiSourceFlag(r).
//   - TestAIFlagTableCoversEveryMCPSendEndpoint statically scans internal/mcp
//     for every control-API path a tool calls with anything but a plain GET. An
//     endpoint must either have a row in aiFlagEndpoints or be listed, with a
//     reason, in aiFlagNotFlowCreating; otherwise the test fails.
//
// Convention: when you add an MCP tool that sends traffic, add its endpoint to
// aiFlagEndpoints. When you add any other endpoint that records a flow, add it
// too (set mcp:false if no MCP tool reaches it).

// aiFlagEnv is one fresh hub plus the targets a send endpoint can talk to.
type aiFlagEnv struct {
	*collFixture
	seedFlow int64 // GET /probe on the target, usable as an evidence/probe flow
}

// aiFlagRow describes one flow-creating endpoint.
type aiFlagRow struct {
	// name is "METHOD /path" with {} for path parameters, e.g. the key the MCP
	// cross-check compares against.
	name string
	// mcp is true when an MCP tool is expected to reach this endpoint (the
	// cross-check then verifies the scan still finds it).
	mcp bool
	// setup prepares fixtures and returns the concrete request path and body.
	// It must run before the baseline flow id is taken.
	setup func(t *testing.T, e *aiFlagEnv) (path string, body any)
}

func (r aiFlagRow) endpoint() string {
	return strings.TrimPrefix(strings.TrimPrefix(r.name, "POST "), "GET ")
}

func (e *aiFlagEnv) hostPort() string { return strings.TrimPrefix(e.target.URL, "http://") }

func (e *aiFlagEnv) setIdentities(t *testing.T) {
	t.Helper()
	if err := e.st.SetSetting("authz.identities", `[{"name":"admin","headers":"Authorization: Bearer admin"},{"name":"user","headers":"Authorization: Bearer user"}]`); err != nil {
		t.Fatal(err)
	}
}

func (e *aiFlagEnv) scopeTarget() {
	u, _ := url.Parse(e.target.URL)
	e.h.sc.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: u.Hostname()}})
}

var aiFlagEndpoints = []aiFlagRow{
	{name: "POST /api/repeater/send", mcp: true, setup: func(t *testing.T, e *aiFlagEnv) (string, any) {
		return "/api/repeater/send", map[string]any{"method": "GET", "url": e.target.URL + "/probe"}
	}},
	{name: "POST /api/findings/{}/retest", setup: func(t *testing.T, e *aiFlagEnv) (string, any) {
		tg := &retestTarget{server: e.target, status: 200}
		fid, _ := retestFixture(t, e.h, e.st, tg, http.MethodGet)
		return fmt.Sprintf("/api/findings/%d/retest", fid), map[string]any{}
	}},
	{name: "POST /api/flows/{}/replay", setup: func(t *testing.T, e *aiFlagEnv) (string, any) {
		return fmt.Sprintf("/api/flows/%d/replay", e.seedFlow), map[string]any{}
	}},
	{name: "POST /api/intruder/start", mcp: true, setup: func(t *testing.T, e *aiFlagEnv) (string, any) {
		return "/api/intruder/start", map[string]any{
			"target":     e.target.URL,
			"template":   "GET /probe?id=§1§ HTTP/1.1\r\nHost: " + e.hostPort() + "\r\n\r\n",
			"attackType": "sniper",
			"payloads":   [][]string{{"a"}},
			"threads":    1,
		}
	}},
	{name: "POST /api/ws/send", mcp: true, setup: func(t *testing.T, e *aiFlagEnv) (string, any) {
		return "/api/ws/send", map[string]any{"url": "ws://" + wsTestServer(t) + "/stream", "message": "ping"}
	}},
	{name: "POST /api/collections/send", mcp: true, setup: func(t *testing.T, e *aiFlagEnv) (string, any) {
		e.scopeTarget()
		_, item, env := e.pathCollection(e.target.URL, false, false)
		return "/api/collections/send", map[string]any{"itemUid": item, "envUid": env, "persist": "keep"}
	}},
	{name: "POST /api/collections/run", mcp: true, setup: func(t *testing.T, e *aiFlagEnv) (string, any) {
		e.scopeTarget()
		coll, _, env := e.pathCollection(e.target.URL, false, false)
		return "/api/collections/run", map[string]any{"collectionUid": coll, "envUid": env, "persist": "keep"}
	}},
	{name: "POST /api/collmatrix/run", mcp: true, setup: func(t *testing.T, e *aiFlagEnv) (string, any) {
		e.scopeTarget()
		e.setIdentities(t)
		coll, _, env := e.pathCollection(e.target.URL, false, false)
		return "/api/collmatrix/run", map[string]any{"collectionUid": coll, "envUid": env}
	}},
	{name: "POST /api/authz/run", mcp: true, setup: func(t *testing.T, e *aiFlagEnv) (string, any) {
		e.setIdentities(t)
		return "/api/authz/run", map[string]any{"flowId": e.seedFlow}
	}},
	{name: "POST /api/authz/check-sessions", mcp: true, setup: func(t *testing.T, e *aiFlagEnv) (string, any) {
		e.setIdentities(t)
		return "/api/authz/check-sessions", map[string]any{"flowId": e.seedFlow}
	}},
	{name: "POST /api/authz/differential", mcp: true, setup: func(t *testing.T, e *aiFlagEnv) (string, any) {
		e.setIdentities(t)
		// invalidBody and sideEffectFlowId reach the probe and observeAround send sites.
		return "/api/authz/differential", map[string]any{"flowId": e.seedFlow, "invalidBody": "bad", "sideEffectFlowId": e.seedFlow}
	}},
	{name: "POST /api/authz/cross-host-replay", mcp: true, setup: func(t *testing.T, e *aiFlagEnv) (string, any) {
		return "/api/authz/cross-host-replay", map[string]any{"flowId": e.seedFlow, "jwt": "a.b.c"}
	}},
}

func newAIFlagEnv(t *testing.T) *aiFlagEnv {
	t.Helper()
	e := &aiFlagEnv{collFixture: newCollFixture(t)}
	u, _ := url.Parse(e.target.URL)
	port, _ := strconv.Atoi(u.Port())
	id, err := e.st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "http", Host: u.Hostname(), Port: port, Path: "/probe",
		ReqHeaders: map[string][]string{"Authorization": {"Bearer captured"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.seedFlow = id
	return e
}

func (e *aiFlagEnv) maxFlowID(t *testing.T) int64 {
	t.Helper()
	fs, err := e.st.QueryFlowsFilter(store.FlowFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) == 0 {
		return 0
	}
	return fs[0].ID // newest first
}

// newFlowsSince returns the flows recorded after baseline, waiting briefly for
// asynchronous senders (Intruder) to finish.
func (e *aiFlagEnv) newFlowsSince(t *testing.T, baseline int64) []*store.Flow {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		fs, err := e.st.QueryFlowsFilter(store.FlowFilter{Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		var out []*store.Flow
		for _, f := range fs {
			if f.ID > baseline {
				out = append(out, f)
			}
		}
		if (len(out) > 0 && !e.h.intr.State().Running) || time.Now().After(deadline) {
			return out
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAISourceFlagOnEverySendEndpoint(t *testing.T) {
	for _, row := range aiFlagEndpoints {
		row := row
		for _, ai := range []bool{true, false} {
			ai := ai
			mode := "with ai header"
			if !ai {
				mode = "without ai header"
			}
			t.Run(row.name+" "+mode, func(t *testing.T) {
				e := newAIFlagEnv(t)
				path, body := row.setup(t, e)
				baseline := e.maxFlowID(t)
				h := hdrs{"X-Interseptor-CSRF": "1"}
				if ai {
					h = asAI
				}
				code, resp := e.do("POST", path, body, h)
				flows := e.newFlowsSince(t, baseline)
				if len(flows) == 0 {
					t.Fatalf("%s: recorded no flow (HTTP %d: %.200s) so FlagAI cannot be checked", row.name, code, resp)
				}
				for _, f := range flows {
					gotAI := f.Flags&store.FlagAI != 0
					switch {
					case ai && !gotAI:
						t.Errorf("%s: flow %d (%s %s) lacks FlagAI although the request carried X-Interseptor-Source: ai (flags=%b); call aiSourceFlag(r) at this send site", row.name, f.ID, f.Method, f.Path, f.Flags)
					case !ai && gotAI:
						t.Errorf("%s: flow %d (%s %s) has FlagAI without the X-Interseptor-Source header (flags=%b)", row.name, f.ID, f.Method, f.Path, f.Flags)
					}
				}
			})
		}
	}
}

// aiFlagNotFlowCreating lists the non-GET control-API paths MCP tools call that
// do not put a request on the network and record a flow, with the reason. A new
// MCP endpoint must land here or in aiFlagEndpoints.
var aiFlagNotFlowCreating = map[string]string{
	"/api/android/setup":                   "device setup over adb, no HTTP flow",
	"/api/android/unproxy":                 "device setup over adb, no HTTP flow",
	"/api/authz":                           "stores identities",
	"/api/authz/from-flow/{}":              "promotes a captured flow to an identity",
	"/api/authz/identity":                  "stores an identity",
	"/api/checks":                          "stores check scripts",
	"/api/checks/test":                     "runs a check against an already stored flow",
	"/api/codecs":                          "stores codecs",
	"/api/codecs/encode":                   "pure transform",
	"/api/codecs/test":                     "pure transform",
	"/api/collmatrix/attach-run":           "attaches stored run evidence",
	"/api/collmatrix/diff-example":         "diffs stored flows",
	"/api/collmatrix/handoff":              "builds an Intruder spec, sends nothing",
	"/api/decode":                          "pure transform",
	"/api/engagement-brief":                "stores a note",
	"/api/export/full/file":                "archive export",
	"/api/finding-cvss":                    "pure computation",
	"/api/finding-revisions/{}/{}/restore": "finding bookkeeping",
	"/api/finding-targets/preview":         "pure computation",
	"/api/findings":                        "finding bookkeeping",
	"/api/findings/report":                 "report export",
	"/api/findings/{}":                     "finding bookkeeping",
	"/api/findings/{}/evidence-render":     "renders stored evidence",
	"/api/findings/{}/flow-preview":        "renders a stored flow",
	"/api/findings/{}/flows":               "finding bookkeeping",
	"/api/findings/{}/flows/{}":            "finding bookkeeping",
	"/api/findings/{}/images":              "finding bookkeeping",
	"/api/findings/{}/images/{}/classify":  "finding bookkeeping",
	"/api/findings/{}/normalize-targets":   "finding bookkeeping",
	"/api/flows/purge":                     "deletes flows",
	"/api/flows/tags":                      "flow bookkeeping",
	"/api/flows/{}/auth-timeline":          "reads stored flows",
	"/api/flows/{}/interception":           "flow bookkeeping",
	"/api/flows/{}/note":                   "flow bookkeeping",
	"/api/flows/{}/ws/{}/note":             "flow bookkeeping",
	"/api/human-input":                     "operator prompt",
	"/api/import/full/file":                "archive import",
	"/api/intercept/response/toggle":       "proxy interception state",
	"/api/intercept/response/{}/drop":      "proxy interception state",
	"/api/intercept/response/{}/forward":   "proxy interception state",
	"/api/intercept/toggle":                "proxy interception state",
	"/api/intercept/{}/drop":               "proxy interception state",
	"/api/intercept/{}/forward":            "proxy interception state",
	"/api/interception-setup":              "stores a note",
	"/api/intruder/attacks/{}/render":      "renders stored results",
	"/api/ios/install-ca":                  "device setup, no HTTP flow",
	"/api/ios/setup":                       "device setup, no HTTP flow",
	"/api/ios/ssh/install-ca":              "device setup over ssh, no HTTP flow",
	"/api/ios/ssh/setup":                   "device setup over ssh, no HTTP flow",
	"/api/ios/ssh/status":                  "device status over ssh, no HTTP flow",
	"/api/notes":                           "stores a note",
	"/api/oob/base":                        "OOB listener settings",
	"/api/oob/new":                         "mints an OOB token",
	"/api/redact":                          "pure transform",
	"/api/rules":                           "match/replace rule bookkeeping",
	"/api/rules/{}":                        "match/replace rule bookkeeping",
	"/api/scanner/run":                     "passive scan over stored flows",
	"/api/scope":                           "scope bookkeeping",
	"/api/session":                         "session settings",
	"/api/session/login/from-flow/{}":      "builds a login macro from a stored flow",
	"/api/session/login/run":               "login macro runs without recording a flow",
	"/api/session/login/test":              "login macro test runs without recording a flow",
	"/api/settings":                        "settings",
	"/api/variables":                       "variable store",
	"/api/vault/backup":                    "vault transfer",
	"/api/vault/import":                    "vault transfer",
	"/api/vault/merge":                     "vault transfer",
}

var mcpAPIPathRe = regexp.MustCompile(`^/api/[A-Za-z0-9_./{}%-]+$`)
var printfVerbRe = regexp.MustCompile(`%[0-9]*[a-z]`)

// normalizeMCPPath turns a path literal from the MCP sources into a comparable
// endpoint: query dropped, printf verbs collapsed to {}, trailing slash trimmed.
func normalizeMCPPath(s string) string {
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(printfVerbRe.ReplaceAllString(s, "{}"), "/")
}

// mcpSendEndpoints statically scans the MCP server sources and returns every
// control-API path literal outside a plain GET call (s.apiGet(...) or
// s.api(http.MethodGet, ...)). It over-approximates on purpose: a path
// assembled in a variable is still found, because the literal is.
func mcpSendEndpoints(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "mcp", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("cannot find the MCP sources: %v", err)
	}
	out := map[string]bool{}
	fset := token.NewFileSet()
	for _, fn := range files {
		if strings.HasSuffix(fn, "_test.go") {
			continue
		}
		src, err := os.ReadFile(fn)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, fn, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", fn, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok && isPlainGETCall(c) {
				return false
			}
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if v, err := strconv.Unquote(lit.Value); err == nil && mcpAPIPathRe.MatchString(v) {
					out[normalizeMCPPath(v)] = true
				}
			}
			return true
		})
	}
	return out
}

func isPlainGETCall(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch sel.Sel.Name {
	case "apiGet":
		return true
	case "api":
		if len(c.Args) == 0 {
			return false
		}
		switch m := c.Args[0].(type) {
		case *ast.SelectorExpr:
			return m.Sel.Name == "MethodGet"
		case *ast.BasicLit:
			return m.Value == `"GET"`
		}
	}
	return false
}

func TestAIFlagTableCoversEveryMCPSendEndpoint(t *testing.T) {
	scanned := mcpSendEndpoints(t)
	if len(scanned) < 20 {
		t.Fatalf("MCP scan found only %d endpoints; the scanner is broken", len(scanned))
	}
	table := map[string]aiFlagRow{}
	for _, r := range aiFlagEndpoints {
		table[r.endpoint()] = r
	}

	var missing []string
	for p := range scanned {
		_, inTable := table[p]
		_, nonFlow := aiFlagNotFlowCreating[p]
		if !inTable && !nonFlow {
			missing = append(missing, p)
		}
	}
	sort.Strings(missing)
	for _, p := range missing {
		t.Errorf("MCP tool endpoint %s is neither a row in aiFlagEndpoints (if it records a flow, it must set FlagAI from X-Interseptor-Source) nor listed in aiFlagNotFlowCreating (with a reason)", p)
	}

	for p, r := range table {
		if r.mcp && !scanned[p] {
			t.Errorf("aiFlagEndpoints row %q is marked mcp:true but no MCP tool calls %s any more; fix the row", r.name, p)
		}
		if _, dup := aiFlagNotFlowCreating[p]; dup {
			t.Errorf("%s is both a flow-creating row and in aiFlagNotFlowCreating", p)
		}
	}
	for p := range aiFlagNotFlowCreating {
		if !scanned[p] {
			t.Errorf("aiFlagNotFlowCreating lists %s but no MCP tool calls it; remove the stale entry", p)
		}
	}
}
