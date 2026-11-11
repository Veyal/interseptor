package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// reviewStub records the last request path, query and JSON body.
type reviewStub struct {
	srv   *httptest.Server
	path  string
	query string
	body  map[string]any
	calls int
}

func newReviewStub(t *testing.T, reply func(r *http.Request) string) *reviewStub {
	t.Helper()
	st := &reviewStub{}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.calls++
		st.path, st.query = r.URL.Path, r.URL.RawQuery
		st.body = nil
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &st.body)
		}
		out := `{}`
		if reply != nil {
			out = reply(r)
		}
		_, _ = io.WriteString(w, out)
	}))
	t.Cleanup(st.srv.Close)
	return st
}

func strList(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, el := range list {
		out = append(out, fmt.Sprint(el))
	}
	return out
}

// An agent that sends hosts as a JSON array must not have the list collapsed
// into one bogus pattern: with mode=keepOnly that pattern matches no host and
// the prune deletes the whole history.
func TestPruneHistoryAcceptsArrayArguments(t *testing.T) {
	st := newReviewStub(t, nil)
	s := New(st.srv.URL)
	if _, err := s.Call("prune_history", map[string]any{
		"hosts": []any{"app.example.com", "*.cdn.example.com"}, "mode": "keepOnly", "dryRun": true,
		"methods": []any{"GET", "HEAD"}, "statuses": []any{float64(200), "204"},
	}); err != nil {
		t.Fatalf("prune_history: %v", err)
	}
	if got := strList(st.body["hosts"]); len(got) != 2 || got[0] != "app.example.com" || got[1] != "*.cdn.example.com" {
		t.Fatalf("hosts = %#v, want the two patterns", st.body["hosts"])
	}
	if got := strList(st.body["methods"]); len(got) != 2 || got[0] != "GET" || got[1] != "HEAD" {
		t.Fatalf("methods = %#v", st.body["methods"])
	}
	if got := strList(st.body["statuses"]); len(got) != 2 || got[0] != "200" || got[1] != "204" {
		t.Fatalf("statuses = %#v", st.body["statuses"])
	}
}

// A status filter that cannot be parsed used to be dropped, which silently
// widened the prune to every status of the selected hosts.
func TestPruneHistoryRejectsUnparseableStatusFilter(t *testing.T) {
	st := newReviewStub(t, nil)
	s := New(st.srv.URL)
	_, err := s.Call("prune_history", map[string]any{"hosts": "app.example.com", "statuses": "2xx"})
	if err == nil {
		t.Fatalf("an unparseable status filter must be rejected; sent body %#v", st.body)
	}
	if !strings.Contains(err.Error(), "statuses") || st.calls != 0 {
		t.Fatalf("err = %v, calls = %d (nothing may reach the control API)", err, st.calls)
	}
}

// get_flow side=response used to be forwarded verbatim; the control API reads
// anything but "res" as the request, so the agent got the wrong message.
func TestGetFlowNormalizesSideAliases(t *testing.T) {
	st := newReviewStub(t, func(r *http.Request) string { return "side=" + r.URL.Query().Get("side") })
	s := New(st.srv.URL)
	out, err := s.Call("get_flow", map[string]any{"id": 7, "side": "response"})
	if err != nil {
		t.Fatalf("get_flow: %v", err)
	}
	if !strings.Contains(out, "side=res") || strings.Contains(out, "side=response") {
		t.Fatalf("side=response must read the response: %q", out)
	}
	out, _ = s.Call("get_flow", map[string]any{"id": 7, "side": "Request"})
	if !strings.Contains(out, "side=req") || strings.Contains(out, "REQUEST ===") {
		t.Fatalf("side=Request must read only the request: %q", out)
	}
}

// The narrative limit is published in characters, so it must count runes.
func TestNarrativeLimitCountsCharactersNotBytes(t *testing.T) {
	text := strings.Repeat("検", 70) // 70 characters, 210 bytes
	if hard, _ := validateFindingFormat(findingFormatInput{Title: "t", Detail: text}); hard != nil {
		t.Fatalf("70 characters rejected under a 180-character limit: %v", hard)
	}
	long := strings.Repeat("検", 180)
	hard, _ := validateFindingFormat(findingFormatInput{Title: "t", Detail: long})
	if hard == nil || !strings.Contains(hard.Error(), "180 characters") {
		t.Fatalf("180 characters must be rejected and reported as 180: %v", hard)
	}
}

// The guide tells agents to put the explanation in each block's proof field,
// and promises a warning for writes that contain a JWT.
func TestSecretLintCoversEvidenceProofAndCaptions(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4ifQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	body, _ := json.Marshal([]map[string]any{{"type": "flow", "role": "result", "flowId": 3, "proof": "accepted token " + jwt}})
	_, warns := validateFindingFormat(findingFormatInput{Title: "t", Impact: "i", Why: "w", Body: string(body), Partial: true})
	joined := strings.Join(warns, "\n")
	if !strings.Contains(joined, "probable secret") {
		t.Fatalf("a JWT in a flow block proof produced no secret warning: %q", joined)
	}
	if strings.Contains(joined, jwt) {
		t.Fatal("the warning must not echo the secret")
	}
}

// The connect instructions tell agents the destructive tools accept
// expectedProject; the advertised schema has to say so too.
func TestDestructiveToolSchemasAdvertiseExpectedProject(t *testing.T) {
	s := New("http://127.0.0.1:1")
	for name := range destructiveTools {
		_, schema, ok := s.ToolMeta(name)
		if !ok {
			t.Fatalf("destructive tool %s is not registered", name)
		}
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["expectedProject"]; !ok {
			t.Errorf("%s: schema does not advertise expectedProject", name)
		}
	}
}

// A filtered intruder_state scans one 5000-row window; when the run is longer
// the reply must say where to continue instead of looking complete.
func TestIntruderStateFilterReportsNextOffset(t *testing.T) {
	const total = 6000
	st := newReviewStub(t, func(r *http.Request) string {
		if r.URL.Path == "/api/intruder/state" {
			return `{"running":false,"runId":"abc"}`
		}
		off := atoiDefault(r.URL.Query().Get("offset"), 0)
		lim := atoiDefault(r.URL.Query().Get("limit"), 200)
		var rows []string
		for i := off; i < off+lim && i < total; i++ {
			rows = append(rows, fmt.Sprintf(`{"id":%d,"anomaly":%t}`, i, i == 5500))
		}
		return fmt.Sprintf(`{"results":[%s],"total":%d,"offset":%d}`, strings.Join(rows, ","), total, off)
	})
	s := New(st.srv.URL)
	var page struct {
		Results    []json.RawMessage `json:"results"`
		NextOffset int               `json:"nextOffset"`
	}
	out, err := s.Call("intruder_state", map[string]any{"onlyAnomalies": true})
	if err != nil {
		t.Fatal(err)
	}
	if i := strings.Index(out, "\n\nproject:"); i >= 0 {
		out = out[:i]
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(page.Results) != 0 || page.NextOffset != 5000 {
		t.Fatalf("results=%d nextOffset=%d, want 0 and 5000 (the anomaly at 5500 is not reachable otherwise)", len(page.Results), page.NextOffset)
	}
}

// Numeric strings and Go ints in flowIds were silently dropped.
func TestRecordPlaybookCoverageKeepsLooseFlowIDs(t *testing.T) {
	st := newReviewStub(t, nil)
	s := New(st.srv.URL)
	if _, err := s.Call("record_playbook_coverage", map[string]any{
		"playbookId": "object-authz", "host": "app.example.com", "endpoint": "GET /orders/{id}", "status": "run",
		"flowIds": []any{float64(11), "12", 13},
	}); err != nil {
		t.Fatal(err)
	}
	if got := strList(st.body["flowIds"]); len(got) != 3 || got[0] != "11" || got[1] != "12" || got[2] != "13" {
		t.Fatalf("flowIds = %#v, want [11 12 13]", st.body["flowIds"])
	}
	if _, err := s.Call("record_playbook_coverage", map[string]any{
		"playbookId": "object-authz", "host": "app.example.com", "endpoint": "GET /orders/{id}", "status": "run",
		"flowIds": []any{"abc"},
	}); err == nil {
		t.Fatal("a non-integer flow id must be an error, not a silently shorter evidence list")
	}
}

// tags sent as a JSON array were stringified whole, producing the tags
// "[auth" and "idor]".
func TestTagFlowAcceptsTagArray(t *testing.T) {
	st := newReviewStub(t, nil)
	s := New(st.srv.URL)
	for tool, field := range map[string]string{"tag_flow": "add", "untag_flow": "remove"} {
		if _, err := s.Call(tool, map[string]any{"id": 4, "tags": []any{"auth", "idor"}}); err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if got := strList(st.body[field]); len(got) != 2 || got[0] != "auth" || got[1] != "idor" {
			t.Fatalf("%s: %s = %#v, want [auth idor]", tool, field, st.body[field])
		}
	}
	if _, err := s.Call("tag_flow", map[string]any{"id": 4, "tags": "auth, idor candidate"}); err != nil {
		t.Fatal(err)
	}
	if got := strList(st.body["add"]); len(got) != 3 {
		t.Fatalf("string form = %#v, want three tags", st.body["add"])
	}
}

// update_finding only ran the format validator (and with it the secret lint)
// when the write touched body/detail/impact/why/target, so a token pasted into
// summary, fix or retest alone was stored without the promised warning.
func TestUpdateFindingLintsSecretsInAnyTextField(t *testing.T) {
	st := newReviewStub(t, func(r *http.Request) string { return `{"id":5}` })
	s := New(st.srv.URL)
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4ifQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	out, err := s.Call("update_finding", map[string]any{"id": 5, "retest": "Replay with " + jwt + " and expect 401."})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "probable secret in retest") {
		t.Fatalf("no secret warning for a JWT in retest: %q", out)
	}
	if strings.Contains(out, jwt) {
		t.Fatal("the warning must not echo the secret")
	}
}
