package collmatrix

import (
	"encoding/json"
	"errors"
	"sort"

	"github.com/Veyal/interseptor/internal/collrun"
)

var errNoRuns = errors.New("collmatrix: no run store configured")

// StoredReport rebuilds a (header + rows) report of a stored run. The rows are
// the masked ItemResults the runner persisted; no bodies or raw values exist
// in them. Items are in execution order.
func (s *Service) StoredReport(collectionUID, runUID string) (*collrun.Report, error) {
	if s.d.Runs == nil {
		return nil, errNoRuns
	}
	runs, err := s.d.Runs.ListRuns(collectionUID, CoverageRuns)
	if err != nil {
		return nil, err
	}
	rep := &collrun.Report{RunUID: runUID, CollectionUID: collectionUID, Items: []collrun.ItemResult{}}
	found := false
	for _, r := range runs {
		if r.UID != runUID {
			continue
		}
		found = true
		rep.EnvUID, rep.Source, rep.Status = r.EnvUID, r.Source, r.Status
		rep.StartedMs, rep.FinishedMs = r.StartedTS, r.FinishedTS
		var sum collrun.RunSummary
		if r.SummaryJSON != "" && json.Unmarshal([]byte(r.SummaryJSON), &sum) == nil {
			rep.Iterations, rep.Totals = sum.Iterations, sum.Totals
		}
	}
	if !found {
		return nil, errNotFound
	}
	rows, err := s.d.Runs.ListRunResults(runUID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var it collrun.ItemResult
		if row.ResultJSON == "" || json.Unmarshal([]byte(row.ResultJSON), &it) != nil || it.ItemUID == "" {
			continue
		}
		rep.Items = append(rep.Items, it)
	}
	sort.SliceStable(rep.Items, func(i, j int) bool { return rep.Items[i].Seq < rep.Items[j].Seq })
	scrubReport(rep, s.scrub)
	return rep, nil
}

func scrubReport(rep *collrun.Report, scrub func(string) string) {
	for i := range rep.Items {
		it := &rep.Items[i]
		it.Name, it.URL, it.Error = scrub(it.Name), scrub(it.URL), scrub(it.Error)
	}
}

// TimingForRun is the timing breakdown of a stored run.
func (s *Service) TimingForRun(collectionUID, runUID string) (*TimingReport, error) {
	rep, err := s.StoredReport(collectionUID, runUID)
	if err != nil {
		return nil, err
	}
	return Timing(rep), nil
}

// CoverageFor is the OpenAPI coverage of a collection.
func (s *Service) CoverageFor(collectionUID string) (*CoverageReport, error) {
	_, items, err := s.d.Backend.Load(collectionUID)
	if err != nil {
		return nil, err
	}
	return Coverage(items, s.d.Runs, collectionUID)
}

// AttachMatrix attaches a remembered matrix to a finding.
func (s *Service) AttachMatrix(id string, findingID int64, onlyFlagged bool) (int, error) {
	m, ok := s.Matrix(id)
	if !ok {
		return 0, errNotFound
	}
	return Attach(s.d.Evidence, findingID, MatrixEvidence(m, onlyFlagged), s.scrub)
}

// AttachRun attaches the flows of a stored run (all of it, or the selected
// items) to a finding.
func (s *Service) AttachRun(collectionUID, runUID string, itemUIDs []string, findingID int64) (int, error) {
	rep, err := s.StoredReport(collectionUID, runUID)
	if err != nil {
		return 0, err
	}
	return Attach(s.d.Evidence, findingID, ReportEvidence(rep, itemUIDs), s.scrub)
}
