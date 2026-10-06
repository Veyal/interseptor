package control

import (
	"net/http"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

const (
	// maxProjectReadinessItems caps findings.items; Total always carries the full count.
	maxProjectReadinessItems = 200
	// maxProjectReadinessTarget bounds the echoed brief target.
	maxProjectReadinessTarget = 256
	// maxProjectReadinessGaps bounds gap codes echoed per finding.
	maxProjectReadinessGaps = 20
	// projectReadinessBudget is the documented response-time budget for a
	// project with 1,000 findings (it is exercised by the unit test).
	projectReadinessBudget = 3 * time.Second

	// blockerNoScope is the id of the "scope" check in readiness.go.
	blockerNoScope = "scope"
	// blockerNoTarget marks an engagement brief without an authorised target.
	blockerNoTarget = "brief_target"
)

type projectReadinessScope struct {
	Enabled bool `json:"enabled"`
	In      int  `json:"in"`
	Out     int  `json:"out"`
}

type projectReadinessBrief struct {
	Target string `json:"target"`
	OK     bool   `json:"ok"`
}

type projectReadinessEvidence struct {
	Flows int64 `json:"flows"`
	Shots int   `json:"shots"`
	WS    int64 `json:"ws"`
}

type projectReadinessItem struct {
	ID    int64    `json:"id"`
	Stage string   `json:"stage"`
	Gaps  []string `json:"gaps"`
}

type projectReadinessFindings struct {
	Total     int                    `json:"total"`
	Ready     int                    `json:"ready"`
	Items     []projectReadinessItem `json:"items"`
	Truncated bool                   `json:"truncated"`
}

// projectReadiness is the aggregate behind the engagement strip. Counts only:
// no bodies, titles or request data.
type projectReadiness struct {
	Scope    projectReadinessScope    `json:"scope"`
	Brief    projectReadinessBrief    `json:"brief"`
	Evidence projectReadinessEvidence `json:"evidence"`
	Findings projectReadinessFindings `json:"findings"`
	Blockers []string                 `json:"blockers"`
}

// getProjectReadiness serves GET /api/project/readiness.
func (h *projectAPI) getProjectReadiness(w http.ResponseWriter, r *http.Request) {
	out, err := h.buildProjectReadiness()
	if err != nil {
		httpInternalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *projectAPI) buildProjectReadiness() (projectReadiness, error) {
	out := projectReadiness{Blockers: []string{}}
	rules, err := h.st.ListScopeRules()
	if err != nil {
		return out, err
	}
	out.Scope = summarizeScope(rules)
	brief, err := h.st.GetEngagementBrief()
	if err != nil {
		return out, err
	}
	out.Brief = summarizeBrief(brief)
	if out.Evidence.Flows, err = h.st.FlowCount(); err != nil {
		return out, err
	}
	if out.Evidence.WS, err = h.st.WSFlowCount(); err != nil {
		return out, err
	}
	fs, err := h.st.ListFindings("", "", "")
	if err != nil {
		return out, err
	}
	out.Findings, out.Evidence.Shots = summarizeFindings(fs)
	out.Blockers = projectBlockers(out)
	return out, nil
}

func summarizeScope(rules []store.ScopeRule) projectReadinessScope {
	var s projectReadinessScope
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		switch r.Action {
		case "include":
			s.In++
		case "exclude":
			s.Out++
		}
	}
	s.Enabled = s.In > 0
	return s
}

func summarizeBrief(b store.EngagementBrief) projectReadinessBrief {
	target := strings.TrimSpace(b.Scope)
	if len(target) > maxProjectReadinessTarget {
		target = strings.ToValidUTF8(target[:maxProjectReadinessTarget], "")
	}
	return projectReadinessBrief{Target: target, OK: target != ""}
}

// summarizeFindings renders the server-computed readiness of each finding; it
// never re-derives pass/fail.
func summarizeFindings(fs []store.Finding) (projectReadinessFindings, int) {
	out := projectReadinessFindings{Total: len(fs), Items: []projectReadinessItem{}}
	shots := 0
	for i := range fs {
		f := &fs[i]
		if f.Ready {
			out.Ready++
		}
		if f.Readiness != nil {
			shots += f.Readiness.ScreenshotCount
		}
		if len(out.Items) >= maxProjectReadinessItems {
			out.Truncated = true
			continue
		}
		out.Items = append(out.Items, findingReadinessItem(f))
	}
	return out, shots
}

func findingReadinessItem(f *store.Finding) projectReadinessItem {
	it := projectReadinessItem{ID: f.ID, Stage: "draft", Gaps: []string{}}
	if f.Readiness == nil {
		return it
	}
	it.Stage = f.Readiness.Stage
	it.Gaps = append(it.Gaps, f.Readiness.Gaps...)
	if len(it.Gaps) > maxProjectReadinessGaps {
		it.Gaps = it.Gaps[:maxProjectReadinessGaps]
	}
	return it
}

// projectBlockers lists project-level blockers first, then each distinct
// finding gap code in first-seen order. Codes come from the server only.
func projectBlockers(p projectReadiness) []string {
	out := []string{}
	if !p.Brief.OK {
		out = append(out, blockerNoTarget)
	}
	if !p.Scope.Enabled {
		out = append(out, blockerNoScope)
	}
	seen := map[string]bool{}
	for _, it := range p.Findings.Items {
		for _, g := range it.Gaps {
			if !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	return out
}
