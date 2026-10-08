package collmatrix

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/store"
)

// Coverage states of one spec operation.
const (
	CovUntested = "untested" // never sent by a stored run
	CovBlocked  = "blocked"  // attempted but blocked (scope, unresolved, quarantine) and never sent
	CovFailing  = "failing"  // sent, a test failed or the request errored in the latest run
	CovPassing  = "passing"  // sent and no test failed
)

// CoverageRuns bounds how many stored runs are folded.
const CoverageRuns = 50

// OpCoverage is one operation of the imported OpenAPI spec.
type OpCoverage struct {
	Key         string `json:"key"` // "GET /users/{id}"
	Method      string `json:"method"`
	Path        string `json:"path"`
	OperationID string `json:"operationId,omitempty"`
	ItemUID     string `json:"itemUid"`
	ItemName    string `json:"itemName"`
	Group       string `json:"group,omitempty"` // first tag, else first path segment
	Deprecated  bool   `json:"deprecated,omitempty"`
	State       string `json:"state"`
	Hits        int    `json:"hits"`               // sent executions across the folded runs
	Statuses    []int  `json:"statuses,omitempty"` // distinct HTTP statuses seen, ascending
	Tested      bool   `json:"tested"`             // at least one test or assertion ran
	LastRunUID  string `json:"lastRunUid,omitempty"`
	LastFlowID  int64  `json:"lastFlowId,omitempty"`
}

// CoverageGroup rolls operations up by tag.
type CoverageGroup struct {
	Name      string `json:"name"`
	Total     int    `json:"total"`
	Exercised int    `json:"exercised"`
}

// CoverageReport answers "which operations of the spec were exercised".
type CoverageReport struct {
	CollectionUID string          `json:"collectionUid"`
	Total         int             `json:"total"`
	Exercised     int             `json:"exercised"`
	Percent       float64         `json:"percent"`
	NonSpecItems  int             `json:"nonSpecItems"` // requests without a spec operation
	RunsFolded    int             `json:"runsFolded"`
	Operations    []OpCoverage    `json:"operations"`
	Groups        []CoverageGroup `json:"groups,omitempty"`
}

type specRef struct {
	Path        string `json:"path"`
	Method      string `json:"method"`
	OperationID string `json:"operationId"`
}

func specOf(it store.Item) (specRef, bool) {
	var side struct {
		OpenAPI *specRef `json:"openapi"`
	}
	if len(it.Sidecar) == 0 || json.Unmarshal(it.Sidecar, &side) != nil || side.OpenAPI == nil {
		return specRef{}, false
	}
	if side.OpenAPI.Path == "" || side.OpenAPI.Method == "" {
		return specRef{}, false
	}
	return *side.OpenAPI, true
}

func tagsOf(raw json.RawMessage) []string {
	var t []string
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &t)
	}
	return t
}

func groupOf(tags []string, path string) string {
	for _, t := range tags {
		if t != "deprecated" {
			return t
		}
	}
	p := strings.Trim(path, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	if p == "" || strings.HasPrefix(p, "{") {
		return "(root)"
	}
	return p
}

// Coverage folds the stored runs of a collection (newest first) over the spec
// operations of its items. rs may be nil (every operation is untested).
// Interactive single sends are not runs and are not counted.
func Coverage(items []store.Item, rs collrun.RunStore, collectionUID string) (*CoverageReport, error) {
	rep := &CoverageReport{CollectionUID: collectionUID, Operations: []OpCoverage{}}
	idx := map[string]int{}
	for _, it := range items {
		if it.Kind == "folder" {
			continue
		}
		ref, ok := specOf(it)
		if !ok {
			rep.NonSpecItems++
			continue
		}
		tags := tagsOf(it.Tags)
		op := OpCoverage{
			Key: strings.ToUpper(ref.Method) + " " + ref.Path, Method: strings.ToUpper(ref.Method), Path: ref.Path, OperationID: ref.OperationID,
			ItemUID: it.UID, ItemName: it.Name, Group: groupOf(tags, ref.Path), State: CovUntested,
		}
		for _, t := range tags {
			op.Deprecated = op.Deprecated || t == "deprecated"
		}
		idx[it.UID] = len(rep.Operations)
		rep.Operations = append(rep.Operations, op)
	}
	if rs != nil && len(idx) > 0 {
		runs, err := rs.ListRuns(collectionUID, CoverageRuns)
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			rows, err := rs.ListRunResults(run.UID)
			if err != nil {
				return nil, err
			}
			rep.RunsFolded++
			for _, row := range rows {
				i, ok := idx[row.ItemUID]
				if !ok || row.ResultJSON == "" {
					continue
				}
				var res collrun.ItemResult
				if json.Unmarshal([]byte(row.ResultJSON), &res) != nil {
					continue
				}
				foldResult(&rep.Operations[i], res, run.UID)
			}
		}
	}
	finishCoverage(rep)
	return rep, nil
}

// foldResult adds one executed row to an operation. runs are visited newest
// first, so the first sent row fixes the "last" fields and, with newest, the
// pass/fail state.
func foldResult(op *OpCoverage, res collrun.ItemResult, runUID string) {
	sent := res.Outcome == collexec.OutcomeSent
	switch {
	case sent:
		op.Hits++
		if res.HTTPStatus > 0 && !containsInt(op.Statuses, res.HTTPStatus) {
			op.Statuses = append(op.Statuses, res.HTTPStatus)
		}
		if len(res.Tests) > 0 {
			op.Tested = true
		}
		if op.LastRunUID == "" {
			op.LastRunUID, op.LastFlowID = runUID, res.FlowID
			if res.Problem() {
				op.State = CovFailing
			} else {
				op.State = CovPassing
			}
		}
	case res.Outcome == collexec.OutcomeBlocked && op.State == CovUntested:
		op.State = CovBlocked
	}
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func finishCoverage(rep *CoverageReport) {
	groups := map[string]*CoverageGroup{}
	for i := range rep.Operations {
		op := &rep.Operations[i]
		sort.Ints(op.Statuses)
		rep.Total++
		g := groups[op.Group]
		if g == nil {
			g = &CoverageGroup{Name: op.Group}
			groups[op.Group] = g
		}
		g.Total++
		if op.Hits > 0 {
			rep.Exercised++
			g.Exercised++
		}
	}
	if rep.Total > 0 {
		rep.Percent = float64(int(float64(rep.Exercised)*1000/float64(rep.Total)+0.5)) / 10
	}
	for _, g := range groups {
		rep.Groups = append(rep.Groups, *g)
	}
	sort.Slice(rep.Groups, func(i, j int) bool { return rep.Groups[i].Name < rep.Groups[j].Name })
	// Untested first so gaps lead the list; stable inside each state.
	rank := map[string]int{CovUntested: 0, CovBlocked: 1, CovFailing: 2, CovPassing: 3}
	sort.SliceStable(rep.Operations, func(i, j int) bool { return rank[rep.Operations[i].State] < rank[rep.Operations[j].State] })
}
