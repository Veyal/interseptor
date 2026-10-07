package collmatrix

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/preview"
	"github.com/Veyal/interseptor/internal/store"
)

// EvidenceSink is the slice of the store finding-evidence API used to attach
// run results; *store.Store satisfies it (same contract as the authz
// differential sink).
type EvidenceSink interface {
	AttachFlowWithMetadata(findingID, flowID int64, note string, pos int, role, proof, source string, sourceFlowID int64, changes ...store.FindingChange) error
}

var _ EvidenceSink = (*store.Store)(nil)

// MaxAttach bounds one attach call.
const MaxAttach = 60

var errNoSink = errors.New("collmatrix: no finding evidence sink configured")

// Evidence is one flow to attach with its role and note.
type Evidence struct {
	FlowID int64
	Role   string // baseline | result | control | observation
	Note   string
}

func clipNote(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// MatrixEvidence lists the flows of a matrix. With onlyFlagged, rows with no
// flagged cell are left out and, for the rest, only the baseline and flagged
// cells are kept. The baseline cell is "baseline"; others are "result".
func MatrixEvidence(m *Matrix, onlyFlagged bool) []Evidence {
	base := indexOf(m.Identities, m.Baseline)
	var out []Evidence
	seen := map[int64]bool{}
	add := func(c Cell, role, note string) {
		if c.FlowID <= 0 || seen[c.FlowID] {
			return
		}
		seen[c.FlowID] = true
		out = append(out, Evidence{FlowID: c.FlowID, Role: role, Note: clipNote(note)})
	}
	for _, row := range m.Rows {
		flagged := false
		for _, c := range row.Cells {
			flagged = flagged || c.Flag != ""
		}
		if onlyFlagged && !flagged {
			continue
		}
		for ci, c := range row.Cells {
			if ci >= len(m.Identities) {
				break
			}
			if onlyFlagged && ci != base && c.Flag == "" {
				continue
			}
			role := "result"
			if ci == base {
				role = "baseline"
			}
			note := fmt.Sprintf("identity matrix - %s as %s: %s", row.Name, m.Identities[ci], c.Class)
			if c.Status > 0 {
				note += fmt.Sprintf(" (HTTP %d)", c.Status)
			}
			if c.Flag != "" {
				note += " [" + c.Flag + " - hypothesis, reproduce before reporting]"
			}
			add(c, role, note)
		}
	}
	return out
}

// ReportEvidence lists the flows of selected items of a run report (every item
// when itemUIDs is empty), with the run context in each note. Secrets are not
// in ItemResult by construction.
func ReportEvidence(rep *collrun.Report, itemUIDs []string) []Evidence {
	want := map[string]bool{}
	for _, u := range itemUIDs {
		want[u] = true
	}
	var out []Evidence
	seen := map[int64]bool{}
	for _, it := range rep.Items {
		if len(want) > 0 && !want[it.ItemUID] {
			continue
		}
		if it.FlowID <= 0 || seen[it.FlowID] {
			continue
		}
		seen[it.FlowID] = true
		role := "result"
		note := fmt.Sprintf("collection run %s - %s / %s iteration %d", rep.RunUID, rep.CollectionName, it.Name, it.Iteration+1)
		if rep.EnvName != "" {
			note += ", env " + rep.EnvName
		}
		if it.HTTPStatus > 0 {
			note += fmt.Sprintf(" (HTTP %d)", it.HTTPStatus)
		}
		for _, t := range it.Tests {
			if t.Status == "fail" || t.Status == "error" {
				note += "; failed: " + t.Name
				break
			}
		}
		out = append(out, Evidence{FlowID: it.FlowID, Role: role, Note: clipNote(note)})
	}
	return out
}

// Attach appends evidence to a finding as typed captured-flow blocks. It stops
// at MaxAttach and returns how many were attached.
func Attach(sink EvidenceSink, findingID int64, ev []Evidence, scrub func(string) string) (int, error) {
	if sink == nil {
		return 0, errNoSink
	}
	if findingID <= 0 {
		return 0, errors.New("collmatrix: findingId is required")
	}
	n := 0
	for _, e := range ev {
		if n >= MaxAttach {
			break
		}
		note := e.Note
		if scrub != nil {
			note = scrub(note)
		}
		if err := sink.AttachFlowWithMetadata(findingID, e.FlowID, note, -1, e.Role, "", "captured_flow", e.FlowID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// AuthzInput maps a matrix onto the authz-matrix evidence render. A violation
// is drawn as "broken", a denied cell as "denied", a cell that did not run or
// failed as "session invalid" (not classified).
func (m *Matrix) AuthzInput() preview.AuthzMatrixInput {
	in := preview.AuthzMatrixInput{RunID: m.ID, BaselineName: m.Baseline, Cols: append([]string(nil), m.Identities...)}
	for _, r := range m.Rows {
		row := preview.AuthzRow{Label: strings.TrimSpace(r.Method + " " + r.Name), Cells: make([]preview.AuthzCell, len(m.Identities))}
		for i, c := range r.Cells {
			if i >= len(row.Cells) {
				break
			}
			row.Cells[i] = preview.AuthzCell{
				Status: c.Status, Length: int(c.Size), FlowID: c.FlowID, SameAsBaseline: c.SameAsBaseline,
				AccessDenied:   c.Denied,
				Broken:         c.Flag == FlagViolation,
				SessionInvalid: c.Class == ClassNotRun || c.Class == ClassError || c.Class == ClassBlocked,
			}
		}
		in.Rows = append(in.Rows, row)
	}
	return in
}

// Render draws the matrix with the shared authz-matrix renderer.
func (m *Matrix) Render(width int, dark bool) (preview.Rendered, error) {
	return preview.RenderAuthzMatrix(m.AuthzInput(), preview.Opts{
		Width: width, Dark: dark, SourceRef: "collmatrix:" + m.ID, Deadline: time.Now().Add(10 * time.Second),
	})
}
