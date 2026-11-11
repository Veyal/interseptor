package collmatrix

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/store"
)

// A secret variable declared on the request itself must stay a placeholder in
// an AI handoff, exactly like a secret of any other scope. It used to travel
// in the backend's untyped local map, which the handoff inlined verbatim.
func TestServiceHandoffNeverInlinesRequestScopedSecret(t *testing.T) {
	const canary = "CANARYhandoffReq8812"
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	coll, err := st.CreateCollection(store.Collection{Name: "H"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := json.Marshal("https://example.com/items/{{id}}?k={{reqKey}}")
	it, err := st.CreateItem(store.Item{CollectionUID: coll.UID, Kind: "request", Name: "target", Method: "GET", URL: u})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetVariables(store.VarOwnerRequest, it.UID, []store.Variable{
		{OwnerKind: store.VarOwnerRequest, OwnerUID: it.UID, Key: "reqKey", Type: store.VarTypeSecret, Enabled: true},
		{OwnerKind: store.VarOwnerRequest, OwnerUID: it.UID, Key: "id", Type: store.VarTypeDefault, InitialValue: "7", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCurrentValue(store.VarOwnerRequest, it.UID, "reqKey", canary, "user"); err != nil {
		t.Fatal(err)
	}
	// A scrubber that knows nothing: the handoff itself must not inline it.
	s := New(Deps{Backend: collrun.NewStoreBackend(collrun.StoreConfig{Store: st}), Scrub: func(x string) string { return x }})
	h, err := s.Handoff(coll.UID, it.UID, "", HandoffOptions{Positions: []string{"id"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(h)
	if strings.Contains(string(raw), canary) {
		t.Fatalf("request-scoped secret inlined into the handoff: %s", raw)
	}
	if !strings.Contains(h.Template, "{{reqKey}}") {
		t.Fatalf("secret should stay a placeholder: %s", h.Template)
	}
}
