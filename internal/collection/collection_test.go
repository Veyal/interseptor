package collection

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func newRepo(t *testing.T) (*store.Store, Repo) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "p"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, s
}

func TestBuildTreeOrdersAndKeepsOrphans(t *testing.T) {
	items := []Item{
		{UID: "c", ParentUID: "f", Rank: "b", Name: "c"},
		{UID: "f", Rank: "a", Name: "f", Kind: "folder"},
		{UID: "b", ParentUID: "f", Rank: "a", Name: "b"},
		{UID: "o", ParentUID: "gone", Rank: "z", Name: "orphan"},
	}
	roots := BuildTree(items)
	if len(roots) != 2 || roots[0].UID != "f" || roots[1].UID != "o" {
		t.Fatalf("roots %+v", roots)
	}
	if len(roots[0].Children) != 2 || roots[0].Children[0].UID != "b" {
		t.Fatalf("children %+v", roots[0].Children)
	}
}

func TestScriptHashBindsSourceLibsAndCaps(t *testing.T) {
	base := ScriptHash("pm.test()", []string{"b", "a"}, []string{"net.send", "vars.read"})
	if base != ScriptHash("pm.test()", []string{"a", "b"}, []string{"vars.read", "net.send"}) {
		t.Fatal("hash must not depend on list order")
	}
	for name, h := range map[string]string{
		"source": ScriptHash("pm.test() ", []string{"a", "b"}, []string{"net.send", "vars.read"}),
		"libs":   ScriptHash("pm.test()", []string{"a"}, []string{"net.send", "vars.read"}),
		"caps":   ScriptHash("pm.test()", []string{"a", "b"}, []string{"net.send"}),
	} {
		if h == base {
			t.Errorf("%s change did not change the hash", name)
		}
	}
}

func TestImportedScriptsAreQuarantinedUntilTrusted(t *testing.T) {
	s, r := newRepo(t)
	ev := json.RawMessage(`[{"listen":"prerequest","script":{"exec":["pm.environment.set('a','1');"]}},{"listen":"test","script":{"exec":"pm.test('x')"}}]`)
	c, err := r.CreateCollection(Collection{Name: "c", Events: ev})
	if err != nil {
		t.Fatal(err)
	}
	un, err := UntrustedScripts(r, c.UID, c.Events, nil)
	if err != nil || len(un) != 2 {
		t.Fatalf("untrusted=%v err=%v", un, err)
	}
	if err := s.TrustScript(c.UID, un[0]); err != nil {
		t.Fatal(err)
	}
	un2, _ := UntrustedScripts(r, c.UID, c.Events, nil)
	if len(un2) != 1 || un2[0] != un[1] {
		t.Fatalf("after trust: %v", un2)
	}
	// editing the script (different hash) is untrusted again
	edited := json.RawMessage(`[{"listen":"prerequest","script":{"exec":["pm.environment.set('a','2');"]}}]`)
	if un3, _ := UntrustedScripts(r, c.UID, edited, nil); len(un3) != 1 {
		t.Fatalf("edit kept trust: %v", un3)
	}
}

func TestDefaultsMatchOwnerDecisions(t *testing.T) {
	if DefaultScopePolicy != "block" || InteractiveScopePolicy != "warn" || DefaultRunnerPersist != "ask" ||
		DefaultCLIPersist != "discard" || DefaultUnresolvedPolicy != "block" || !DefaultSkipSessionHeader {
		t.Fatal("defaults drifted from the owner decisions")
	}
}

func TestServiceNewIDAndRank(t *testing.T) {
	if len(NewID()) != 26 {
		t.Fatal("id")
	}
	if r := RankBetween("a", "c"); !(r > "a" && r < "c") {
		t.Fatal(r)
	}
}
