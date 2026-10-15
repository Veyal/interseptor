package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func postCheckTest(t *testing.T, base, body string) map[string]any {
	t.Helper()
	resp, err := http.Post(base+"/api/checks/test", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/checks/test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a bad check must not be an HTTP error)", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func logLines(t *testing.T, res map[string]any) []string {
	t.Helper()
	raw, ok := res["logs"].([]any)
	if !ok {
		t.Fatalf("response has no logs array: %#v", res)
	}
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		s, _ := l.(string)
		out = append(out, s)
	}
	return out
}

// POST /api/checks/test must return the check's print() output. Without it the
// author of a check that returns an empty list has nothing to debug with.
func TestCheckTestReturnsPrintOutput(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com",
		Path: "/search?q=needle-value", Status: 200, Mime: "text/html",
		ResHeaders: map[string][]string{"Content-Type": {"text/html"}},
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	src := "print(\"loaded\")\n" +
		"def check(flow):\n" +
		"    print(\"params=\" + \",\".join(sorted(flow.query_params.keys())))\n" +
		"    print(\"cookies=\" + str(len(flow.cookies)))\n" +
		"    return []\n"
	body, _ := json.Marshal(map[string]any{"source": src})
	res := postCheckTest(t, ts.URL, string(body))

	if res["error"] != nil {
		t.Fatalf("unexpected error: %v", res["error"])
	}
	got := logLines(t, res)
	want := []string{"loaded", "params=q", "cookies=0"}
	if len(got) != len(want) {
		t.Fatalf("logs = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("logs = %q, want %q", got, want)
		}
	}
	if res["flowId"] == nil {
		t.Error("response does not say which flow it ran against")
	}
}

// A check that fails at runtime must still return what it printed first, and a
// check that fails to compile must return its module-level output.
func TestCheckTestReturnsLogsAlongsideErrors(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: "/", Status: 200,
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	runtimeFail, _ := json.Marshal(map[string]any{
		"source": "def check(flow):\n    print(\"got this far\")\n    fail(\"boom\")\n",
	})
	res := postCheckTest(t, ts.URL, string(runtimeFail))
	if res["error"] == nil {
		t.Fatal("expected a runtime error")
	}
	if got := logLines(t, res); len(got) != 1 || got[0] != "got this far" {
		t.Errorf("logs = %q, want the output from before the failure", got)
	}

	compileFail, _ := json.Marshal(map[string]any{
		"source": "print(\"module ran\")\ndef not_check(flow):\n    return []\n",
	})
	res = postCheckTest(t, ts.URL, string(compileFail))
	if res["error"] == nil {
		t.Fatal("expected a compile error")
	}
	if got := logLines(t, res); len(got) != 1 || got[0] != "module ran" {
		t.Errorf("logs = %q, want the module-level output", got)
	}
}

// With no captured flow there is nothing to run against, but the response
// shape must still carry logs so a client needs no special case.
func TestCheckTestLogsArrayIsAlwaysPresent(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{"source": "def check(flow):\n    return []\n"})
	res := postCheckTest(t, ts.URL, string(body))
	if got := logLines(t, res); len(got) != 0 {
		t.Errorf("logs = %q, want an empty array", got)
	}
	if res["note"] == nil {
		t.Errorf("expected the no-flow note: %#v", res)
	}
}
