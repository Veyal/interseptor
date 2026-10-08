package collmatrix

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/store"
)

type spyBackend struct {
	fakeBackend
	got []collexec.StepInput
}

func (s *spyBackend) Step(ctx context.Context, in collexec.StepInput, m stepMeta) (*collexec.StepResult, error) {
	s.got = append(s.got, in)
	return s.fakeBackend.Step(ctx, in, m)
}

func headersOf(t *testing.T, raw json.RawMessage) map[string][]string {
	t.Helper()
	var kv []struct{ Key, Value string }
	if err := json.Unmarshal(raw, &kv); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, h := range kv {
		k := strings.ToLower(h.Key)
		out[k] = append(out[k], h.Value)
	}
	return out
}

func TestApplyIdentityReplacesCredentials(t *testing.T) {
	in := collexec.StepInput{Chain: collexec.Chain{Item: store.Item{
		UID:     "i1",
		Headers: json.RawMessage(`[{"key":"Authorization","value":"Bearer owner-secret"},{"key":"Accept","value":"application/json"},{"key":"X-Api-Key","value":"owner-key","disabled":false},{"key":"Cookie","value":"sid=owner"}]`),
		Auth:    json.RawMessage(`{"type":"bearer","bearer":[{"key":"token","value":"owner-secret"}]}`),
	}}}
	user := Identity{Name: "user", Headers: []Header{{"Authorization", "Bearer user-tok"}, {"X-Api-Key", "user-key"}}}
	out := applyIdentity(in, user, []Identity{user, {Name: "admin", Headers: []Header{{"X-Api-Key", "admin-key"}}}})
	h := headersOf(t, out.Chain.Item.Headers)
	if len(h["authorization"]) != 1 || h["authorization"][0] != "Bearer user-tok" {
		t.Fatalf("authorization = %v", h["authorization"])
	}
	if len(h["x-api-key"]) != 1 || h["x-api-key"][0] != "user-key" {
		t.Fatalf("x-api-key = %v", h["x-api-key"])
	}
	if len(h["cookie"]) != 0 {
		t.Fatalf("owner cookie leaked: %v", h["cookie"])
	}
	if h["accept"][0] != "application/json" {
		t.Fatalf("unrelated header dropped: %v", h)
	}
	if !strings.Contains(string(out.Chain.Item.Auth), "noauth") {
		t.Fatalf("item auth not neutralised: %s", out.Chain.Item.Auth)
	}
	if out.Identity != "user" {
		t.Fatalf("identity partition = %q", out.Identity)
	}
	// the input is not mutated
	if !strings.Contains(string(in.Chain.Item.Headers), "owner-secret") {
		t.Fatal("input mutated")
	}
}

func TestApplyIdentityAnonymousStripsEveryKnownCredentialHeader(t *testing.T) {
	in := collexec.StepInput{Chain: collexec.Chain{Item: store.Item{
		Headers: json.RawMessage(`[{"key":"authorization","value":"Bearer x"},{"key":"X-Session","value":"s"},{"key":"Accept","value":"*/*"}]`),
	}}}
	known := []Identity{{Name: "user", Headers: []Header{{"X-Session", "u"}}}}
	out := applyIdentity(in, Identity{Name: "anonymous", Anonymous: true}, known)
	h := headersOf(t, out.Chain.Item.Headers)
	if len(h["authorization"])+len(h["x-session"]) != 0 || len(h["accept"]) != 1 {
		t.Fatalf("headers = %v", h)
	}
	if out.Identity != "anonymous" {
		t.Fatalf("identity = %q", out.Identity)
	}
}

func TestApplyIdentityToleratesMissingOrBadHeaders(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`not json`), json.RawMessage(`{}`)} {
		out := applyIdentity(collexec.StepInput{Chain: collexec.Chain{Item: store.Item{Headers: raw}}}, Identity{Name: "u", Headers: []Header{{"Authorization", "Bearer t"}}}, nil)
		if h := headersOf(t, out.Chain.Item.Headers); len(h["authorization"]) != 1 {
			t.Fatalf("raw=%s headers=%v", raw, h)
		}
	}
}

func TestIdentityBackendMarksAppliedAndDelegates(t *testing.T) {
	spy := &spyBackend{fakeBackend: *newFake("a")}
	b := newIdentityBackend(spy, Identity{Name: "user", Headers: []Header{{"Authorization", "Bearer t"}}}, nil)
	res, err := b.Step(context.Background(), collexec.StepInput{Chain: collexec.Chain{Item: store.Item{UID: "i-a", Name: "a"}}}, stepMeta{})
	if err != nil || res == nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range res.Applied {
		found = found || a == "identity:user"
	}
	if !found || len(spy.got) != 1 {
		t.Fatalf("applied=%v calls=%d", res.Applied, len(spy.got))
	}
}
