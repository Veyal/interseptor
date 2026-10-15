package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRepeaterSendFollowsRedirectsWhenRequested(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			http.Redirect(w, r, "/b", http.StatusFound)
			return
		}
		w.Write([]byte("final"))
	}))
	defer target.Close()

	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// Default: no redirect following (historical behaviour).
	def := `{"method":"GET","url":"` + target.URL + `/a"}`
	var r1 map[string]any
	postJSONDecode(t, ts.URL+"/api/repeater/send", def, &r1)
	if r1["status"] != float64(http.StatusFound) {
		t.Fatalf("default must not follow redirects, status=%v", r1["status"])
	}

	// Opt in: follow to the 200.
	body := `{"method":"GET","url":"` + target.URL + `/a","followRedirects":true,"maxRedirects":5}`
	var r2 map[string]any
	postJSONDecode(t, ts.URL+"/api/repeater/send", body, &r2)
	if r2["status"] != float64(http.StatusOK) {
		t.Fatalf("followRedirects must reach 200, status=%v", r2["status"])
	}
}

func TestRepeaterSendVerifyTLSControlsTrust(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer target.Close()

	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// Default: TLS not verified -> self-signed cert accepted.
	var r1 map[string]any
	postJSONDecode(t, ts.URL+"/api/repeater/send", `{"method":"GET","url":"`+target.URL+`"}`, &r1)
	if r1["status"] != float64(http.StatusOK) {
		t.Fatalf("default (no TLS verify) must connect, status=%v", r1["status"])
	}

	// verifyTls:true -> self-signed cert rejected, errored flow.
	var r2 map[string]any
	postJSONDecode(t, ts.URL+"/api/repeater/send", `{"method":"GET","url":"`+target.URL+`","verifyTls":true}`, &r2)
	if r2["status"] != float64(http.StatusBadGateway) {
		t.Fatalf("verifyTls:true must reject the self-signed cert, status=%v err=%v", r2["status"], r2["error"])
	}
}

func TestRepeaterParamsParseAndBuildRoundTrip(t *testing.T) {
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	// Parse a URL with duplicate + out-of-order query and a form body.
	parseReq := `{"url":"https://example.com/x?b=2&a=1&b=3","body":"user=admin&role=1&role=2","form":true}`
	var parsed struct {
		Base  string          `json:"base"`
		Query []senderParamJS `json:"query"`
		Form  []senderParamJS `json:"form"`
	}
	postJSONDecode(t, ts.URL+"/api/repeater/params/parse", parseReq, &parsed)
	if parsed.Base != "https://example.com/x" {
		t.Fatalf("base = %q", parsed.Base)
	}
	if len(parsed.Query) != 3 || parsed.Query[0].Key != "b" || parsed.Query[0].Value != "2" ||
		parsed.Query[2].Key != "b" || parsed.Query[2].Value != "3" {
		t.Fatalf("query order/dups lost: %+v", parsed.Query)
	}
	if len(parsed.Form) != 3 || parsed.Form[1].Key != "role" || parsed.Form[2].Key != "role" {
		t.Fatalf("form order/dups lost: %+v", parsed.Form)
	}

	// Build it back and confirm the query string and body round-trip losslessly.
	buildReq, _ := json.Marshal(map[string]any{"base": parsed.Base, "query": parsed.Query, "form": parsed.Form})
	var built struct {
		URL  string `json:"url"`
		Body string `json:"body"`
	}
	postJSONDecode(t, ts.URL+"/api/repeater/params/build", string(buildReq), &built)
	if built.URL != "https://example.com/x?b=2&a=1&b=3" {
		t.Fatalf("rebuilt url = %q", built.URL)
	}
	if built.Body != "user=admin&role=1&role=2" {
		t.Fatalf("rebuilt body = %q", built.Body)
	}
}

type senderParamJS struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func postJSONDecode(t *testing.T, url, body string, out any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s status=%d", url, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
}
