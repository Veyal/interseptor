package collmatrix

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

func orderItem() store.Item {
	return store.Item{
		UID: "i1", Kind: "request", Method: "post", Name: "update order",
		URL:     json.RawMessage(`"{{baseUrl}}/orders/{{orderId}}?expand=items&uid={{userId}}"`),
		Headers: json.RawMessage(`[{"key":"Accept","value":"application/json"},{"key":"X-Off","value":"x","disabled":true},{"key":"Content-Length","value":"9"}]`),
		Auth:    json.RawMessage(`{"type":"bearer","bearer":[{"key":"token","value":"{{apiToken}}"}]}`),
		Body:    json.RawMessage(`{"mode":"raw","raw":"{\"note\":\"{{note}}\"}","options":{"raw":{"language":"json"}}}`),
	}
}

func TestBuildHandoffMarksChosenVariable(t *testing.T) {
	h, err := BuildHandoff(orderItem(), HandoffOptions{
		Positions: []string{"orderId"},
		Values:    map[string]string{"baseUrl": "https://api.example.com", "orderId": "42", "userId": "7", "note": "hi"},
		Secret:    map[string]bool{"apiToken": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.Target != "https://api.example.com" {
		t.Fatalf("target = %q", h.Target)
	}
	if !strings.HasPrefix(h.Template, "POST /orders/§42§?expand=items&uid=7 HTTP/1.1\r\nHost: api.example.com\r\n") {
		t.Fatalf("template = %q", h.Template)
	}
	for _, want := range []string{"Accept: application/json", "Authorization: Bearer {{apiToken}}", "Content-Type: application/json", `{"note":"hi"}`} {
		if !strings.Contains(h.Template, want) {
			t.Fatalf("missing %q in %q", want, h.Template)
		}
	}
	for _, bad := range []string{"X-Off", "Content-Length"} {
		if strings.Contains(h.Template, bad) {
			t.Fatalf("unexpected %q in %q", bad, h.Template)
		}
	}
	if len(h.Positions) != 1 || h.Positions[0] != "orderId" || h.AttackType != "sniper" {
		t.Fatalf("positions=%v type=%s", h.Positions, h.AttackType)
	}
	if len(h.Payloads) != 1 || len(h.Payloads[0]) != len(StarterPayloads) {
		t.Fatalf("payloads = %v", h.Payloads)
	}
	joined := strings.Join(h.Notes, "|")
	if !strings.Contains(joined, "secret variable {{apiToken}}") || !strings.Contains(joined, "starter list") {
		t.Fatalf("notes = %v", h.Notes)
	}
}

func TestBuildHandoffDefaultsToURLAndBodyVariables(t *testing.T) {
	h, err := BuildHandoff(orderItem(), HandoffOptions{
		Values:  map[string]string{"baseUrl": "https://api.example.com"},
		Dataset: map[string][]string{"orderId": {"1", "2"}, "userId": {"9", "8", "7"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.Positions, ",") != "orderId,userId,note" {
		t.Fatalf("positions = %v", h.Positions)
	}
	if n := strings.Count(h.Template, "§") / 2; n != 3 {
		t.Fatalf("markers = %d in %q", n, h.Template)
	}
}

func TestBuildHandoffPitchforkUsesOneListPerPosition(t *testing.T) {
	h, err := BuildHandoff(orderItem(), HandoffOptions{
		Positions: []string{"orderId", "userId"}, AttackType: "pitchfork",
		Values:  map[string]string{"baseUrl": "https://api.example.com"},
		Dataset: map[string][]string{"orderId": {"1", "2"}, "userId": {"9", "8"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.AttackType != "pitchfork" || len(h.Payloads) != 2 || h.Payloads[1][0] != "9" {
		t.Fatalf("h = %+v", h)
	}
	spec := h.Spec()
	if spec.Target != h.Target || spec.AttackType != "pitchfork" || len(spec.Payloads) != 2 {
		t.Fatalf("spec = %+v", spec)
	}
	// pitchfork with one position falls back to sniper
	one, err := BuildHandoff(orderItem(), HandoffOptions{Positions: []string{"orderId"}, AttackType: "pitchfork", Values: map[string]string{"baseUrl": "https://api.example.com"}})
	if err != nil || one.AttackType != "sniper" {
		t.Fatalf("one = %+v err=%v", one, err)
	}
}

func TestBuildHandoffErrors(t *testing.T) {
	cases := map[string]struct {
		it store.Item
		o  HandoffOptions
	}{
		"folder":      {store.Item{Kind: "folder"}, HandoffOptions{}},
		"no url":      {store.Item{Kind: "request"}, HandoffOptions{Positions: []string{"a"}}},
		"no variable": {store.Item{Kind: "request", URL: json.RawMessage(`"https://api.example.com/x"`)}, HandoffOptions{}},
		"not in req":  {orderItem(), HandoffOptions{Positions: []string{"ghost"}, Values: map[string]string{"baseUrl": "https://api.example.com"}}},
		"no host":     {orderItem(), HandoffOptions{Positions: []string{"orderId"}}},
	}
	for name, c := range cases {
		if _, err := BuildHandoff(c.it, c.o); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestBuildHandoffAbsoluteURLAndUrlencodedBody(t *testing.T) {
	it := store.Item{Kind: "request", Method: "POST",
		URL:  json.RawMessage(`{"raw":"http://app.example.com:8080/login"}`),
		Body: json.RawMessage(`{"mode":"urlencoded","urlencoded":[{"key":"user","value":"admin"},{"key":"pw","value":"{{password}}"}]}`)}
	h, err := BuildHandoff(it, HandoffOptions{Positions: []string{"password"}, Dataset: map[string][]string{"password": {"a", "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if h.Target != "http://app.example.com:8080" || !strings.Contains(h.Template, "user=admin&pw=§§") ||
		!strings.Contains(h.Template, "Content-Type: application/x-www-form-urlencoded") || !strings.HasPrefix(h.Template, "POST /login HTTP/1.1\r\nHost: app.example.com:8080") {
		t.Fatalf("h = %+v", h)
	}
}

func TestServiceHandoffNeverInlinesSecrets(t *testing.T) {
	be := newFake("x")
	be.items[0] = orderItem()
	be.items[0].CollectionUID = "c1"
	be.layers = []varstore.Layer{{Scope: varstore.ScopeEnvironment, Name: "env", Vars: map[string]varstore.Var{
		"baseUrl":  {Value: "https://api.example.com"},
		"apiToken": {Value: "canary-bearer-555", Secret: true},
		"userId":   {Value: "7"},
		"note":     {Value: "n"},
	}}}
	svc := New(Deps{Backend: be})
	h, err := svc.Handoff("c1", "i1", "", HandoffOptions{Positions: []string{"orderId"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.Template, "canary-bearer-555") || !strings.Contains(h.Template, "{{apiToken}}") {
		t.Fatalf("secret handling: %q", h.Template)
	}
	h, err = svc.Handoff("c1", "i1", "", HandoffOptions{Positions: []string{"orderId"}}, true)
	if err != nil || !strings.Contains(h.Template, "Bearer canary-bearer-555") {
		t.Fatalf("opt-in: %v %+v", err, h)
	}
}

func TestBuildHandoffKeepsBaseURLPathPrefix(t *testing.T) {
	h, err := BuildHandoff(orderItem(), HandoffOptions{Positions: []string{"orderId"}, Values: map[string]string{"baseUrl": "https://api.example.com/v1/"}})
	if err != nil {
		t.Fatal(err)
	}
	if h.Target != "https://api.example.com" || !strings.HasPrefix(h.Template, "POST /v1/orders/§§?") {
		t.Fatalf("h = %q %q", h.Target, h.Template)
	}
}
