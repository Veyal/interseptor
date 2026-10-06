package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func doJSON(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestInterceptionSetupRoundTripAndFlowProvenance(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	code, body := doJSON(t, http.MethodGet, ts.URL+"/api/interception-setup", "")
	if code != http.StatusOK || !strings.Contains(body, `"version":0`) {
		t.Fatalf("empty GET = %d %s", code, body)
	}

	code, body = doJSON(t, http.MethodPut, ts.URL+"/api/interception-setup",
		`{"proxyAddress":"127.0.0.1:8080","caFingerprint":"AA:BB","hosts":["*.example.com"],"enablers":[{"tool":"frida","scriptHash":"sha256:abc","targetLibrary":"libflutter.so","method":"verify_cert_chain"}]}`)
	if code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	var got struct {
		Setup store.InterceptionSetup `json:"setup"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.Setup.Version != 1 || len(got.Setup.Enablers) != 1 {
		t.Fatalf("PUT body = %s (%v)", body, err)
	}

	// An enabler without a tool is rejected without changing state.
	if code, _ := doJSON(t, http.MethodPut, ts.URL+"/api/interception-setup", `{"enablers":[{"method":"m"}]}`); code != http.StatusBadRequest {
		t.Fatalf("invalid enabler status = %d, want 400", code)
	}

	time.Sleep(5 * time.Millisecond)
	id, err := st.InsertFlow(&store.Flow{TS: time.Now(), Method: "GET", Host: "api.example.com", Path: "/x", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	code, body = doJSON(t, http.MethodGet, ts.URL+"/api/flows/"+itoa(id), "")
	var flow struct {
		Prov *store.InterceptionProvenance `json:"interceptionProvenance"`
	}
	if err := json.Unmarshal([]byte(body), &flow); err != nil || code != 200 || flow.Prov == nil || flow.Prov.SetupVersion != 1 || len(flow.Prov.Enablers) != 1 {
		t.Fatalf("flow provenance = %s (%v)", body, err)
	}
}

func TestInterceptionAnnotationRoute(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	connect, _ := st.InsertFlow(&store.Flow{TS: time.Now(), Method: "CONNECT", Host: "api.example.com", Path: "(tls handshake)", Flags: store.FlagTLSFailed})
	plain, _ := st.InsertFlow(&store.Flow{TS: time.Now(), Method: "GET", Host: "api.example.com", Path: "/", Status: 200})

	code, body := doJSON(t, http.MethodPut, ts.URL+"/api/flows/"+itoa(connect)+"/interception", `{"annotation":"pinning_blocked"}`)
	if code != http.StatusOK || !strings.Contains(body, "pinning_blocked") {
		t.Fatalf("annotate = %d %s", code, body)
	}
	if code, _ := doJSON(t, http.MethodPut, ts.URL+"/api/flows/"+itoa(connect)+"/interception", `{"annotation":"nope"}`); code != http.StatusBadRequest {
		t.Fatalf("bad annotation status = %d", code)
	}
	if code, _ := doJSON(t, http.MethodPut, ts.URL+"/api/flows/"+itoa(plain)+"/interception", `{"annotation":"not_intercepted"}`); code != http.StatusBadRequest {
		t.Fatalf("annotating a normal flow status = %d, want 400", code)
	}
	if code, _ := doJSON(t, http.MethodPut, ts.URL+"/api/flows/9999/interception", `{"annotation":"not_intercepted"}`); code != http.StatusNotFound {
		t.Fatalf("missing flow status = %d, want 404", code)
	}
}

func TestReportStatesHowEvidenceWasObtained(t *testing.T) {
	h, st, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	if _, err := st.SetInterceptionSetup(store.InterceptionSetup{
		ProxyAddress: "127.0.0.1:8080", CAFingerprint: "AA:BB:CC",
		Enablers: []store.InterceptionEnabler{{Tool: "frida", ScriptHash: "sha256:abc", TargetLibrary: "libflutter.so", Method: "verify_cert_chain"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateFinding(&store.Finding{Title: "t", Severity: "Low"}); err != nil {
		t.Fatal(err)
	}
	_, md := doJSON(t, http.MethodGet, ts.URL+"/api/findings/report?statuses=all", "")
	for _, want := range []string{"Interception setup v1", "127.0.0.1:8080", "AA:BB:CC", "frida", "sha256:abc", "libflutter.so", "verify_cert_chain"} {
		if !strings.Contains(md, want) {
			t.Errorf("report missing %q:\n%s", want, md)
		}
	}
	_, js := doJSON(t, http.MethodGet, ts.URL+"/api/findings/report?statuses=all&format=json", "")
	if !strings.Contains(js, `"interceptionSetupVersion":1`) {
		t.Errorf("json report missing interceptionSetupVersion: %s", js)
	}
}
