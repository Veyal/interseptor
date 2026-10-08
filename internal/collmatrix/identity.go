package collmatrix

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
)

type stepMeta = collrun.StepMeta

// AnonymousName is the name of the implicit no-credentials identity.
const AnonymousName = "anonymous"

// Expectation of an identity for the requests in a matrix.
const (
	ExpectAllow = "allow"
	ExpectDeny  = "deny"
)

// Identity is one "send as" principal. It mirrors an authz identity: a name and
// the credential headers (Authorization, Cookie, API keys) it sends.
type Identity struct {
	Name      string   `json:"name"`
	Headers   []Header `json:"-"` // credentials; never serialised or logged
	Anonymous bool     `json:"anonymous,omitempty"`
	// Expect optionally states what the identity should get: allow or deny.
	// Empty means: anonymous denies, the baseline allows, others unspecified.
	Expect string `json:"expect,omitempty"`
	Broken bool   `json:"broken,omitempty"`
}

// identityBackend wraps a collrun.Backend so every step is sent as one
// identity. It replaces the credentials of the stored request (the item copy in
// the StepInput; the stored item is never touched), keeps the identity's own
// cookie-jar partition and neutralises collection auth so the owner's token can
// not leak into another identity's request.
type identityBackend struct {
	collrun.Backend
	id    Identity
	known []Identity
}

func newIdentityBackend(b collrun.Backend, id Identity, known []Identity) *identityBackend {
	return &identityBackend{Backend: b, id: id, known: known}
}

func (b *identityBackend) Step(ctx context.Context, in collexec.StepInput, meta collrun.StepMeta) (*collexec.StepResult, error) {
	res, err := b.Backend.Step(ctx, applyIdentity(in, b.id, b.known), meta)
	if res != nil {
		res.Applied = append(res.Applied, "identity:"+b.id.Name)
	}
	return res, err
}

// applyIdentity returns a copy of in that sends as id. Headers named like a
// credential of any known identity (plus Authorization, Cookie and
// Proxy-Authorization) are removed from the request first, then the identity's
// own are added, so the result carries exactly that identity's credentials.
func applyIdentity(in collexec.StepInput, id Identity, known []Identity) collexec.StepInput {
	strip := append([]string(nil), credentialHeaders...)
	for _, k := range append([]Identity{id}, known...) {
		for _, h := range k.Headers {
			if !hasHeaderName(strip, h.Name) {
				strip = append(strip, h.Name)
			}
		}
	}
	var kept []json.RawMessage
	var arr []map[string]json.RawMessage
	if len(in.Chain.Item.Headers) > 0 && json.Unmarshal(in.Chain.Item.Headers, &arr) == nil {
		for _, h := range arr {
			var key string
			_ = json.Unmarshal(h["key"], &key)
			if hasHeaderName(strip, strings.TrimSpace(key)) {
				continue
			}
			raw, _ := json.Marshal(h)
			kept = append(kept, raw)
		}
	}
	if !id.Anonymous {
		for _, h := range id.Headers {
			raw, _ := json.Marshal(map[string]any{"key": h.Name, "value": h.Value})
			kept = append(kept, raw)
		}
	}
	if kept == nil {
		kept = []json.RawMessage{}
	}
	hdr, _ := json.Marshal(kept)
	in.Chain.Item.Headers = hdr
	in.Chain.Item.Auth = json.RawMessage(`{"type":"noauth"}`)
	in.Identity = id.Name
	return in
}
