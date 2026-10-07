package collmatrix

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/collexec"
)

// maxBody bounds every JSON request body.
const maxBody = 1 << 20

// Route is one REST route. The control layer registers Routes() in its route
// table (and so in the API catalogue); the paths are stable.
type Route struct {
	Method  string
	Path    string
	Desc    string
	Handler http.HandlerFunc
}

// Callers tells the package who is calling. Without it every caller is treated
// as AI/MCP: scope policy block, no secrets in handoffs.
type Callers struct {
	// IsAI reports an AI, MCP or agent caller.
	IsAI func(*http.Request) bool
	// Source maps the request to the collexec source label.
	Source func(*http.Request) collexec.Source
}

func (c Callers) ai(r *http.Request) bool {
	if c.IsAI == nil {
		return true
	}
	return c.IsAI(r)
}

func (c Callers) source(r *http.Request) collexec.Source {
	if c.Source == nil {
		return collexec.SourceRunner
	}
	return c.Source(r)
}

// Routes returns the REST routes of the package.
func (s *Service) Routes(c Callers) []Route {
	return []Route{
		{"POST", "/api/collmatrix/run", "Run requests as each identity (and anonymous) and return the access differential. Body: {collectionUid, folderUid?, itemUids?, envUid?, identities?, baseline?, noScripts?}. Scope policy block; script writes discarded", s.hRun(c)},
		{"GET", "/api/collmatrix/{id}", "Return a matrix returned by an earlier run (the last 20 are kept in memory)", s.hGet},
		{"GET", "/api/collmatrix/{id}/render.png", "Render a matrix with the authz-matrix evidence renderer. Query: width, dark=1", s.hRender},
		{"POST", "/api/collmatrix/{id}/attach", "Attach a matrix's flows to a finding as typed evidence. Body: {findingId, onlyFlagged?}", s.hAttachMatrix},
		{"POST", "/api/collmatrix/attach-run", "Attach the flows of a stored collection run (or selected items) to a finding. Body: {collectionUid, runUid, itemUids?, findingId}", s.hAttachRun},
		{"GET", "/api/collmatrix/coverage", "OpenAPI coverage of a collection: which spec operations stored runs exercised. Query: collection", s.hCoverage},
		{"GET", "/api/collmatrix/timing", "Timing breakdown of a stored run. Query: collection, run", s.hTiming},
		{"POST", "/api/collmatrix/handoff", "Build an Intruder attack from a collection item. Body: {collectionUid, itemUid, envUid?, positions?, dataset?, payloads?, attackType?, includeSecrets?}. Secrets are only included for a human UI session", s.hHandoff(c)},
		{"POST", "/api/collmatrix/diff-example", "Diff a saved response example of an item against a captured response flow. Body: {collectionUid, itemUid, example, flowId, ignorePaths?, ignoreHeaders?}", s.hDiff},
	}
}

// Mount registers Routes on a mux (used by tests and standalone harnesses).
func (s *Service) Mount(mux *http.ServeMux, c Callers) {
	for _, r := range s.Routes(c) {
		mux.HandleFunc(r.Method+" "+r.Path, r.Handler)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	switch {
	case errors.Is(err, errNotFound):
		code = http.StatusNotFound
	case errors.Is(err, errNoRuns), errors.Is(err, errNoSink):
		code = http.StatusServiceUnavailable
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = http.StatusRequestTimeout
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json: " + err.Error()})
		return false
	}
	return true
}

func (s *Service) hRun(c Callers) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req MatrixRequest
		if !decode(w, r, &req) {
			return
		}
		req.AI, req.Source = c.ai(r), c.source(r)
		req.ScopePolicy, req.Local = "", nil // never caller-chosen: the default policy is block
		m, err := s.RunMatrix(r.Context(), req)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"matrix": m, "timing": TimingDifferential(m), "timingNote": TimingNote})
	}
}

func (s *Service) hGet(w http.ResponseWriter, r *http.Request) {
	m, ok := s.Matrix(r.PathValue("id"))
	if !ok {
		writeErr(w, errNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"matrix": m, "timing": TimingDifferential(m), "timingNote": TimingNote})
}

func (s *Service) hRender(w http.ResponseWriter, r *http.Request) {
	m, ok := s.Matrix(r.PathValue("id"))
	if !ok {
		writeErr(w, errNotFound)
		return
	}
	width, _ := strconv.Atoi(r.URL.Query().Get("width"))
	rd, err := m.Render(width, r.URL.Query().Get("dark") == "1")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Alt-Text", strings.ReplaceAll(rd.Alt, "\n", " "))
	_, _ = w.Write(rd.PNG)
}

func (s *Service) hAttachMatrix(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FindingID   int64 `json:"findingId"`
		OnlyFlagged bool  `json:"onlyFlagged"`
	}
	if !decode(w, r, &in) {
		return
	}
	n, err := s.AttachMatrix(r.PathValue("id"), in.FindingID, in.OnlyFlagged)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attached": n})
}

func (s *Service) hAttachRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CollectionUID string   `json:"collectionUid"`
		RunUID        string   `json:"runUid"`
		ItemUIDs      []string `json:"itemUids"`
		FindingID     int64    `json:"findingId"`
	}
	if !decode(w, r, &in) {
		return
	}
	n, err := s.AttachRun(in.CollectionUID, in.RunUID, in.ItemUIDs, in.FindingID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attached": n})
}

func (s *Service) hCoverage(w http.ResponseWriter, r *http.Request) {
	rep, err := s.CoverageFor(r.URL.Query().Get("collection"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Service) hTiming(w http.ResponseWriter, r *http.Request) {
	tr, err := s.TimingForRun(r.URL.Query().Get("collection"), r.URL.Query().Get("run"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

func (s *Service) hHandoff(c Callers) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			CollectionUID  string `json:"collectionUid"`
			ItemUID        string `json:"itemUid"`
			EnvUID         string `json:"envUid"`
			IncludeSecrets bool   `json:"includeSecrets"`
			HandoffOptions
		}
		if !decode(w, r, &in) {
			return
		}
		h, err := s.Handoff(in.CollectionUID, in.ItemUID, in.EnvUID, in.HandoffOptions, in.IncludeSecrets && !c.ai(r))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h)
	}
}

func (s *Service) hDiff(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CollectionUID string `json:"collectionUid"`
		ItemUID       string `json:"itemUid"`
		Example       string `json:"example"`
		FlowID        int64  `json:"flowId"`
		DiffOptions
	}
	if !decode(w, r, &in) {
		return
	}
	d, err := s.DiffExample(r.Context(), in.CollectionUID, in.ItemUID, in.Example, in.FlowID, in.DiffOptions)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}
