package control

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestClassifyAuthzOutcome(t *testing.T) {
	loginRedirect := map[string][]string{"Location": {"/login"}}
	cases := []struct {
		name    string
		status  int
		hasAuth bool
		hdrs    map[string][]string
		want    string
	}{
		{"401 anonymous", 401, false, nil, outcomeAuthFailure},
		{"401 with auth", 401, true, nil, outcomeAuthFailure},
		{"403 anonymous", 403, false, nil, outcomeAuthFailure},
		{"403 with auth", 403, true, nil, outcomeAuthzFailure},
		{"login redirect", 302, false, loginRedirect, outcomeAuthFailure},
		{"400", 400, false, nil, outcomeValidationFailure},
		{"422", 422, true, nil, outcomeValidationFailure},
		{"200", 200, true, nil, outcomeSuccess},
		{"204", 204, false, nil, outcomeSuccess},
		{"500", 500, true, nil, outcomeOther},
		{"plain redirect", 302, true, map[string][]string{"Location": {"/home"}}, outcomeOther},
	}
	for _, c := range cases {
		if got := classifyAuthzOutcome(c.status, c.hasAuth, c.hdrs); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

func TestDetectAuthOrder(t *testing.T) {
	cases := []struct {
		valid, invalid, want string
	}{
		{outcomeAuthFailure, outcomeAuthFailure, authOrderAuthFirst},
		{outcomeAuthFailure, outcomeValidationFailure, authOrderValidationFirst},
		{outcomeSuccess, outcomeValidationFailure, authOrderNotEnforced},
		{outcomeOther, outcomeOther, authOrderInconclusive},
	}
	for _, c := range cases {
		if got := detectAuthOrder(c.valid, c.invalid); got != c.want {
			t.Errorf("valid=%s invalid=%s: got %s want %s", c.valid, c.invalid, got, c.want)
		}
	}
}

func differentialTarget(t *testing.T, state *atomic.Int64) (*httptest.Server, *store.Flow) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/state" {
			_, _ = io.WriteString(w, "counter="+strconv.FormatInt(state.Load(), 10))
			return
		}
		body, _ := io.ReadAll(r.Body)
		// Validation deliberately runs BEFORE authentication.
		if strings.Contains(string(body), "bad") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.Header.Get("Authorization") {
		case "Bearer admin":
			state.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "done")
		case "Bearer user":
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	return srv, &store.Flow{Method: "POST", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/api/items"}
}

func TestAuthzDifferentialClassifiesAndRetainsFlows(t *testing.T) {
	var state atomic.Int64
	_, f := differentialTarget(t, &state)
	h, st, _ := newHub(t)
	api := &authzAPI{h}
	id, err := st.InsertFlow(f)
	if err != nil {
		t.Fatal(err)
	}
	f.ID = id
	// A captured state-observer flow.
	stateFlow := *f
	stateFlow.ID, stateFlow.Method, stateFlow.Path = 0, "GET", "/state"
	stateID, err := st.InsertFlow(&stateFlow)
	if err != nil {
		t.Fatal(err)
	}
	stateFlow.ID = stateID

	ev := api.authzDifferential(f, []identity{
		{Name: "admin", Headers: "Authorization: Bearer admin"},
		{Name: "user", Headers: "Authorization: Bearer user"},
	}, differentialOpts{InvalidBody: "bad", SideEffectFlow: &stateFlow})

	want := map[string]string{"admin": outcomeSuccess, "user": outcomeAuthzFailure, "anonymous": outcomeAuthFailure}
	if len(ev.Contexts) != 3 {
		t.Fatalf("contexts = %+v, want admin,user,anonymous", ev.Contexts)
	}
	for _, c := range ev.Contexts {
		if c.Outcome != want[c.Name] {
			t.Errorf("%s outcome = %s, want %s", c.Name, c.Outcome, want[c.Name])
		}
		if c.FlowID == 0 {
			t.Errorf("%s lost its raw flow id", c.Name)
		}
	}
	if ev.AuthOrder != authOrderValidationFirst {
		t.Errorf("authOrder = %s, want validation_first", ev.AuthOrder)
	}
	if ev.InvalidProbe == nil || ev.InvalidProbe.FlowID == 0 || ev.InvalidProbe.Outcome != outcomeValidationFailure {
		t.Errorf("invalid probe = %+v", ev.InvalidProbe)
	}
	for _, c := range ev.Contexts {
		if c.SideEffect == nil || c.SideEffect.BeforeFlowID == 0 || c.SideEffect.AfterFlowID == 0 {
			t.Fatalf("%s side-effect check missing: %+v", c.Name, c.SideEffect)
		}
		if c.Name == "admin" && !c.SideEffect.Changed {
			t.Errorf("admin mutated state but Changed=false")
		}
		if c.Name != "admin" && c.SideEffect.Changed {
			t.Errorf("%s changed state unexpectedly", c.Name)
		}
	}
	for _, fid := range ev.FlowIDs() {
		if _, err := st.GetFlow(fid); err != nil {
			t.Errorf("flow %d not retained: %v", fid, err)
		}
	}
	if len(ev.Hypotheses) == 0 {
		t.Errorf("expected a hypothesis about validation preceding authentication")
	}
}

func TestAuthzDifferentialFlagsMissingAuthentication(t *testing.T) {
	open := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "same")
	}))
	defer open.Close()
	u, _ := url.Parse(open.URL)
	port, _ := strconv.Atoi(u.Port())
	h, _, _ := newHub(t)
	ev := (&authzAPI{h}).authzDifferential(
		&store.Flow{Method: "GET", Scheme: u.Scheme, Host: u.Hostname(), Port: port, Path: "/x"},
		[]identity{{Name: "admin", Headers: "Authorization: Bearer admin"}}, differentialOpts{})
	if len(ev.Contexts) != 2 || ev.Contexts[1].Name != "anonymous" || !ev.Contexts[1].SameAsBaseline {
		t.Fatalf("contexts = %+v", ev.Contexts)
	}
	if !ev.AuthNotEnforced {
		t.Fatalf("anonymous matched the baseline; AuthNotEnforced should be set")
	}
}

type fakeEvidenceSink struct{ calls []string }

func (f *fakeEvidenceSink) AttachFlowWithMetadata(findingID, flowID int64, note string, pos int, role, proof, source string, sourceFlowID int64, changes ...store.FindingChange) error {
	f.calls = append(f.calls, role+":"+strconv.FormatInt(flowID, 10))
	return nil
}

func TestDifferentialEvidenceAttachesEveryFlow(t *testing.T) {
	ev := authzDifferentialEvidence{Contexts: []differentialContext{
		{Name: "admin", FlowID: 10, Outcome: outcomeSuccess, SideEffect: &differentialSideEffect{BeforeFlowID: 11, AfterFlowID: 12}},
		{Name: "anonymous", FlowID: 13, Outcome: outcomeAuthFailure},
	}, InvalidProbe: &differentialContext{Name: "anonymous+invalid-body", FlowID: 14}}
	sink := &fakeEvidenceSink{}
	if err := ev.AttachTo(sink, 7); err != nil {
		t.Fatal(err)
	}
	if len(sink.calls) != 5 {
		t.Fatalf("calls = %v, want 5 (every raw flow)", sink.calls)
	}
	if sink.calls[0] != "baseline:10" {
		t.Errorf("first = %s, want baseline:10", sink.calls[0])
	}
}

func TestDifferentialEvidenceAttachesToRealFinding(t *testing.T) {
	h, st, _ := newHub(t)
	_ = h
	fid, err := st.CreateFinding(&store.Finding{Title: "missing auth"})
	if err != nil {
		t.Fatal(err)
	}
	flowID, err := st.InsertFlow(&store.Flow{Method: "GET", Scheme: "https", Host: "example.com", Path: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	ev := authzDifferentialEvidence{Contexts: []differentialContext{{Name: "anonymous", FlowID: flowID, Outcome: outcomeSuccess}}}
	if err := ev.AttachTo(st, fid); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetFinding(fid)
	if err != nil || len(got.Flows) != 1 {
		t.Fatalf("finding flows = %+v err=%v", got, err)
	}
}
