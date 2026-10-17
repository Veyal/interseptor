package control

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/sender"
)

// selfSSRFHub starts the control plane on a real loopback listener and records
// that address as the hub's own, so a send that reaches it would be a genuine
// loopback-trusted control request.
func selfSSRFHub(t *testing.T) (*Hub, *httptest.Server, string) {
	t.Helper()
	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	t.Cleanup(ts.Close)
	h.SetSelfAddr(ts.Listener.Addr().String())
	_, port, _ := net.SplitHostPort(ts.Listener.Addr().String())
	return h, ts, port
}

// TestSendPathsRefuseOwnListenerAliases is the regression test for the
// self-SSRF chain: aliases of loopback (0.0.0.0, a trailing-dot localhost, an
// IPv4-mapped IPv6 literal) plus a spoofed loopback Host header used to reach
// the unauthenticated control API through Repeater and mint full API keys.
func TestSendPathsRefuseOwnListenerAliases(t *testing.T) {
	h, ts, port := selfSSRFHub(t)
	targets := []string{
		"http://0.0.0.0:" + port + "/api/keys",
		"http://localhost.:" + port + "/api/keys",
		"http://[::ffff:127.0.0.1]:" + port + "/api/keys",
		"http://[::]:" + port + "/api/keys",
	}
	for _, target := range targets {
		body, _ := json.Marshal(map[string]any{
			"method":  "POST",
			"url":     target,
			"headers": map[string]string{"Host": "127.0.0.1:" + port, "Content-Type": "application/json"},
			"body":    `{"label":"ssrf"}`,
		})
		resp, err := http.Post(ts.URL+"/api/repeater/send", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatalf("POST repeater %s: %v", target, err)
		}
		var out struct {
			Status int    `json:"status"`
			Error  string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && out.Status == http.StatusCreated {
			t.Errorf("repeater → %s reached the control API (201)", target)
		}
	}
	keys, err := h.st.ListAPIKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("self-SSRF minted %d API key(s): %+v", len(keys), keys)
	}
}

// A hostname that resolves to loopback must be refused at dial time even when
// the literal-host check cannot see it. "localhost." is resolved by the system
// resolver, so this exercises the post-resolution guard rather than the name
// comparison.
func TestSenderDialGuardRefusesResolvedOwnListener(t *testing.T) {
	h, _, port := selfSSRFHub(t)
	if h.targetsOwnListener("http://example.com:" + port + "/") {
		t.Fatal("a public name must not be treated as an own-listener alias by the literal check")
	}
	for _, alias := range []string{"0.0.0.0", "localhost.", "LOCALHOST", "[::]", "[::ffff:127.0.0.1]", "app.localhost"} {
		if !h.targetsOwnListener("http://" + alias + ":" + port + "/") {
			t.Errorf("targetsOwnListener(%s) = false, want true", alias)
		}
	}
}

// The hub must arm the shared sender's dial-time guard, so a send path that
// skips (or predates) the literal check still cannot reach the control API.
func TestHubArmsSenderSelfGuard(t *testing.T) {
	h, _, port := selfSSRFHub(t)
	f, err := h.snd.Send(sender.Request{Method: "POST", URL: "http://0.0.0.0:" + port + "/api/keys", Host: "127.0.0.1:" + port,
		Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(`{"label":"x"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if f.Status == http.StatusCreated || !strings.Contains(f.Error, "own listener") {
		t.Fatalf("direct sender send to own listener: status=%d err=%q", f.Status, f.Error)
	}
}

// The WebSocket repeater dials directly; it must refuse loopback aliases too.
func TestWSRepeaterRefusesOwnListenerAlias(t *testing.T) {
	_, ts, port := selfSSRFHub(t)
	resp, err := http.Post(ts.URL+"/api/ws/send", "application/json",
		strings.NewReader(`{"url":"ws://localhost.:`+port+`/api/events","headers":"Host: 127.0.0.1:`+port+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if msg, _ := out["error"].(string); resp.StatusCode == http.StatusOK || !strings.Contains(msg, "own listener") {
		t.Fatalf("ws → own listener alias: got %d %v, want a dial-time own-listener refusal", resp.StatusCode, out)
	}
}
