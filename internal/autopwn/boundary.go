package autopwn

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/Veyal/interseptor/internal/scope"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/strutil"
	"github.com/Veyal/interseptor/internal/verify"
)

// boundaryGuard enforces the run's hard safety boundary (docs/AUTONOMOUS-PENTEST.md
// §8.1/§8.2) on the verifier's re-send path. Unlike the execution phase (whose
// tool calls go over the control bus, which re-checks scope + own-listener), the
// verify phase's Gate-1/Gate-3 re-sends go straight through the sender adapter,
// which does NOT gate on scope — so the engine must re-check every URL a
// candidate would probe before handing it to verify.Verify.
//
// The scope matcher is built from the run's SNAPSHOT (the rules JSON persisted in
// pentest_run.scope), not the live rules: the snapshot is the authorization the
// run was started under, so a host removed from scope mid-run is still probed
// against the boundary the operator approved. The own-listener predicate is
// injected (control wraps its isOwnListener); a nil predicate allows everything
// (tests may pass a simple loopback-port check).
type boundaryGuard struct {
	sc            *scope.Engine
	isOwnListener func(rawURL string) bool
}

// newBoundaryGuard builds a guard from a snapshotted scope-rules JSON string
// (the exact value stored in pentest_run.scope). A rules JSON that cannot be
// decoded yields an empty engine — which, per scope semantics, treats everything
// as in scope; the run's Start-time gate already guaranteed non-empty rules, so
// this is a defensive fallback, not the primary boundary.
func newBoundaryGuard(scopeJSON string, isOwnListener func(rawURL string) bool) *boundaryGuard {
	sc := scope.New()
	var rules []store.ScopeRule
	if scopeJSON != "" {
		if err := json.Unmarshal([]byte(scopeJSON), &rules); err == nil {
			sc.SetRules(rules)
		}
	}
	return &boundaryGuard{sc: sc, isOwnListener: isOwnListener}
}

// allowed reports whether a raw URL may be probed: it must parse to an absolute
// URL, be in the snapshot scope, and not be one of Interseptor's own listeners.
// The reason names the first boundary that rejected it ("" when allowed).
func (g *boundaryGuard) allowed(rawURL string) (ok bool, reason string) {
	if strings.TrimSpace(rawURL) == "" {
		// An empty URL (e.g. a Gate variant a class does not use) is not a probe;
		// treat it as allowed so it does not falsely reject a candidate.
		return true, ""
	}
	f, err := urlToFlow(rawURL)
	if err != nil {
		return false, "unparseable target URL"
	}
	if g.isOwnListener != nil && g.isOwnListener(rawURL) {
		return false, "own listener"
	}
	if !g.sc.InScope(f) {
		return false, "out of scope"
	}
	return true, ""
}

// candidateAllowed checks every URL a candidate would actually probe: its Target,
// the resolved Gate-1 baseline/payload/control/true/false URLs, and the Gate-3
// OOB probe URL (with the placeholder already resolved to the callback URL). It
// returns the first rejecting URL's reason so the skip is diagnosable.
func (g *boundaryGuard) candidateAllowed(c verifyURLs) (ok bool, reason string) {
	for _, u := range c.urls() {
		if ok, reason := g.allowed(u); !ok {
			return false, reason
		}
	}
	return true, ""
}

// verifyURLs is the set of URLs a candidate's verification would send to. It is
// assembled from the resolved verify.Candidate (after OOB injection) so the guard
// sees exactly what the sender would receive.
type verifyURLs struct {
	target   string
	gate1    []string
	oobProbe string
}

func (v verifyURLs) urls() []string {
	out := make([]string, 0, len(v.gate1)+2)
	if v.target != "" {
		out = append(out, v.target)
	}
	out = append(out, v.gate1...)
	if v.oobProbe != "" {
		out = append(out, v.oobProbe)
	}
	return out
}

// candidateURLs assembles the exact set of URLs the verifier would send to for a
// prepared candidate: the human Target, every Gate-1 request URL that the
// candidate's class actually issues, and (blind) the resolved Gate-3 OOB probe
// URL (after placeholder→callback injection). vc is the RESOLVED verify.Candidate
// so the guard sees the post-injection probe URL, not the placeholder template.
func candidateURLs(c Candidate, vc verify.Candidate) verifyURLs {
	u := verifyURLs{target: c.Target}
	d := vc.Diff
	switch d.Class {
	case verify.ClassReflected, verify.ClassError:
		u.gate1 = append(u.gate1, d.Baseline.URL, d.Payload.URL)
	case verify.ClassBoolean:
		u.gate1 = append(u.gate1, d.Baseline.URL, d.PayloadTrue.URL, d.PayloadFalse.URL)
	case verify.ClassTiming:
		u.gate1 = append(u.gate1, d.Baseline.URL, d.Payload.URL, d.Control.URL)
	default:
		u.gate1 = append(u.gate1, d.Baseline.URL, d.Payload.URL)
	}
	if vc.OOB != nil {
		u.oobProbe = vc.OOB.Probe.URL
	}
	return u
}

// urlToFlow parses a raw URL into the minimal store.Flow shape scope.InScope
// needs (host, path, scheme, port). An invalid/relative URL is an error.
func urlToFlow(rawURL string) (*store.Flow, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !u.IsAbs() || u.Host == "" {
		if err == nil {
			err = errString("not an absolute URL")
		}
		return nil, err
	}
	port := strutil.AtoiOr(u.Port(), defaultPortForScheme(u.Scheme))
	return &store.Flow{
		Scheme: u.Scheme,
		Host:   u.Hostname(),
		Port:   port,
		Path:   u.RequestURI(),
	}, nil
}

func defaultPortForScheme(scheme string) int {
	if strings.EqualFold(scheme, "https") {
		return 443
	}
	return 80
}
