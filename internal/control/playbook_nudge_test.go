package control

import (
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// An agent that only polls readiness still learns the corpus has checks for the
// traffic it already captured.
func TestReadinessNamesApplicablePlaybooks(t *testing.T) {
	h, _, _ := newHub(t)
	h.ProjectDir = t.TempDir()
	if _, err := h.st.InsertFlow(&store.Flow{
		TS: time.Now(), Method: "POST", Scheme: "https", Host: "api.example", Port: 443,
		Path: "/checkout/pay?price=1", Status: 200, Mime: "application/json",
	}); err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	rep := (&authzAPI{h}).buildReadiness()
	for _, c := range rep.Checks {
		if c.ID != "playbooks" {
			continue
		}
		if !strings.Contains(c.Detail, "price-tampering") && !strings.Contains(c.Detail, "apply") {
			t.Fatalf("playbooks row = %q", c.Detail)
		}
		return
	}
	t.Fatalf("no playbooks row in %+v", rep.Checks)
}

func TestReadinessOmitsPlaybooksWhenNothingMatches(t *testing.T) {
	h, _, _ := newHub(t)
	h.ProjectDir = t.TempDir()
	if _, err := h.st.InsertFlow(&store.Flow{
		TS: time.Now(), Method: "GET", Scheme: "https", Host: "static.example", Port: 443,
		Path: "/robots.txt", Status: 200, Mime: "text/plain",
	}); err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	for _, c := range (&authzAPI{h}).buildReadiness().Checks {
		if c.ID == "playbooks" {
			t.Fatalf("a flow nothing matches must not add the row: %+v", c)
		}
	}
}
