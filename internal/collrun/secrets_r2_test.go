package collrun

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

const reqCanary = "CANARYreqScopedSecret7781"

func (x *e2e) requestSecret(t *testing.T, itemUID, key, value string) {
	t.Helper()
	if err := x.st.SetVariables(store.VarOwnerRequest, itemUID, []store.Variable{
		{OwnerKind: store.VarOwnerRequest, OwnerUID: itemUID, Key: key, Type: store.VarTypeSecret, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := x.st.SetCurrentValue(store.VarOwnerRequest, itemUID, key, value, "user"); err != nil {
		t.Fatal(err)
	}
}

// A secret variable declared on the request itself must be masked exactly like
// a secret of any other scope: it reaches the wire, never the report.
func TestRequestScopedSecretIsMaskedInReport(t *testing.T) {
	x := newE2E(t)
	it := x.add("probe", "GET", "/user?u={{reqKey}}", nil)
	x.requestSecret(t, it.UID, "reqKey", reqCanary)

	rep := x.run(x.backend(), Options{NoScripts: true})
	if rep.Totals.Sent != 1 || len(x.users) != 1 || x.users[0] != reqCanary {
		t.Fatalf("the secret must still reach the wire: totals %+v users %v", rep.Totals, x.users)
	}
	raw, _ := json.Marshal(rep)
	if strings.Contains(string(raw), reqCanary) {
		t.Fatalf("request-scoped secret leaked into the run report:\n%s", raw)
	}
}

// Without secrets.read a script must not be able to read a request-scoped
// secret, and nothing it prints may carry the value.
func TestRequestScopedSecretIsNotReadableWithoutSecretsCap(t *testing.T) {
	x := newE2E(t)
	it := x.add("probe", "GET", "/user?u=1", func(it *store.Item) {
		it.Events = events("prerequest",
			"console.log('seen=' + pm.variables.get('reqKey'));",
			"pm.environment.set('copy', String(pm.variables.get('reqKey')));")
	})
	x.requestSecret(t, it.UID, "reqKey", reqCanary)

	rep := x.run(x.backend(x.allHashes()...), Options{})
	raw, _ := json.Marshal(rep)
	if strings.Contains(string(raw), reqCanary) {
		t.Fatalf("request-scoped secret leaked through a script:\n%s", raw)
	}
	if len(rep.Items) != 1 || len(rep.Items[0].Console) == 0 || !strings.Contains(rep.Items[0].Console[0].Text, "seen=undefined") {
		t.Fatalf("script read a secret without secrets.read: %s", raw)
	}
}

// A CLI/API local override still beats a stored request variable of the same
// name (highest precedence), secret or not.
func TestLocalOverrideBeatsRequestScopedSecret(t *testing.T) {
	x := newE2E(t)
	it := x.add("probe", "GET", "/user?u={{reqKey}}", nil)
	x.requestSecret(t, it.UID, "reqKey", reqCanary)
	x.run(x.backend(), Options{NoScripts: true, Local: map[string]string{"reqKey": "override"}})
	if len(x.users) != 1 || x.users[0] != "override" {
		t.Fatalf("local override lost to the stored request variable: %v", x.users)
	}
}

// A run forced to scope policy block must hold for pm.sendRequest too, even
// when the collection's own policy is looser.
func TestRunBlockPolicyAppliesToScriptSends(t *testing.T) {
	x := newE2E(t)
	var outside atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outside.Add(1)
		fmt.Fprint(w, "ok")
	}))
	t.Cleanup(other.Close)
	// Same listener family, different host name: 127.0.0.1 is in scope,
	// localhost is not.
	outURL := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)

	for _, pol := range []string{store.ScopePolicyOff, store.ScopePolicyWarn} {
		outside.Store(0)
		coll, err := x.st.GetCollection(x.coll.UID)
		if err != nil {
			t.Fatal(err)
		}
		coll.ScopePolicy = pol
		coll.Caps = js([]string{"vars.read", "vars.write", "net.send"})
		if _, err := x.st.UpdateCollection(*coll); err != nil {
			t.Fatal(err)
		}
		items, _ := x.st.ListItems(x.coll.UID)
		for _, it := range items {
			_ = x.st.DeleteItem(it.UID)
		}
		x.add("main", "GET", "/user?u=1", func(it *store.Item) {
			it.Events = events("prerequest",
				"pm.sendRequest('"+outURL+"/exfil', function (err, res) { console.log('nested ' + (err ? 'refused' : 'sent')); });")
		})
		rep := x.run(x.backend(x.allHashes()...), Options{ScopePolicy: store.ScopePolicyBlock})
		if n := outside.Load(); n != 0 {
			raw, _ := json.Marshal(rep)
			t.Fatalf("collection policy %q: a run under block let pm.sendRequest reach an out-of-scope host %d time(s)\n%s", pol, n, raw)
		}
	}
}
