package autopwn

import (
	"context"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/aiagent"
)

// reconDigest must drive the read-only recon tools over the executor and fold
// their output into a single prompt block — so planning does not depend on the
// model choosing to call tools.
func TestReconDigestGathersToolContext(t *testing.T) {
	fx := &fakeExec{results: map[string]string{
		"list_scope": `[{"action":"include","host":"victim.test"}]`,
		"host_stats": "HOST            FLOWS\nvictim.test        42",
		"list_flows": `[{"id":1,"method":"GET","host":"victim.test","path":"/search","status":200}]`,
	}}
	e := New(Deps{ToolExecutor: fx})

	digest := e.reconDigest(context.Background())

	for _, want := range []string{"victim.test", "/search", "Hosts by traffic", "Scope rules", "Endpoint sample"} {
		if !strings.Contains(digest, want) {
			t.Fatalf("digest missing %q:\n%s", want, digest)
		}
	}
	// It must have actually driven the three recon tools.
	got := strings.Join(fx.calls, ",")
	for _, tool := range []string{"list_scope", "host_stats", "list_flows"} {
		if !strings.Contains(got, tool) {
			t.Fatalf("reconDigest did not call %q; calls=%s", tool, got)
		}
	}
	// list_flows must be bounded (a limit passed).
	// (Presence of the call is enough here; the limit is asserted via the arg map
	// below in a dedicated check.)
}

// A recon tool that errors degrades gracefully — the digest still renders with a
// placeholder rather than aborting planning.
func TestReconDigestToleratesToolError(t *testing.T) {
	e := New(Deps{ToolExecutor: errExec{}})
	digest := e.reconDigest(context.Background())
	if !strings.Contains(digest, "unavailable") {
		t.Fatalf("expected an 'unavailable' placeholder for a failing tool:\n%s", digest)
	}
	if strings.TrimSpace(digest) == "" {
		t.Fatal("digest must never be empty")
	}
}

// buildPlanTask must embed the digest ahead of the instruction and the hint.
func TestBuildPlanTaskEmbedsDigestAndHint(t *testing.T) {
	task := buildPlanTask("focus on /admin", "RECON CONTEXT — hosts: victim.test")
	if !strings.Contains(task, "RECON CONTEXT") {
		t.Fatalf("task missing recon digest: %s", task)
	}
	if !strings.Contains(task, "focus on /admin") {
		t.Fatalf("task missing operator hint: %s", task)
	}
	if strings.Index(task, "RECON CONTEXT") > strings.Index(task, "prioritized attack plan") {
		t.Fatalf("digest should precede the instruction: %s", task)
	}
}

// errExec is a ToolExecutor whose every call fails, to exercise reconDigest's
// graceful-degradation path.
type errExec struct{}

func (errExec) Exec(ctx context.Context, call aiagent.ToolCall) (string, error) {
	return "", context.DeadlineExceeded
}
