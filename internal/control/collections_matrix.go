package control

import (
	"io"

	"github.com/Veyal/interseptor/internal/collmatrix"
	"github.com/Veyal/interseptor/internal/store"
)

// The Collections differentiators (identity matrix, OpenAPI coverage, example
// diff, Intruder handoff, run timing, run evidence) live in internal/collmatrix.
// This file only adapts the control layer's services to its small interfaces
// and mounts its routes next to the other collection routes, so collmatrix
// never imports control and the one shared backend (scope guard, scrub, jars)
// serves every send.

// authzIdentitySource lists the saved authz identities for the matrix.
type authzIdentitySource struct{ h *Hub }

func (a authzIdentitySource) Identities() []collmatrix.Identity {
	ids := (&authzAPI{a.h}).authzIdentities()
	out := make([]collmatrix.Identity, 0, len(ids))
	for _, id := range ids {
		out = append(out, collmatrix.Identity{
			Name: id.Name, Headers: collmatrix.ParseHeaderLines(id.Headers), Broken: id.Broken,
		})
	}
	return out
}

// storeFlows reads captured flows for the matrix and the example diff.
type storeFlows struct{ st *store.Store }

func (f storeFlows) FlowSummary(id int64) (collmatrix.FlowSummary, bool) {
	fl, err := f.st.GetFlow(id)
	if err != nil || fl == nil {
		return collmatrix.FlowSummary{}, false
	}
	loc := ""
	if v := fl.ResHeaders["Location"]; len(v) > 0 {
		loc = v[0]
	}
	return collmatrix.FlowSummary{Status: fl.Status, Length: fl.ResLen, BodyHash: fl.ResBodyHash, Mime: fl.Mime, Location: loc}, true
}

func (f storeFlows) FlowBody(id int64, maxBytes int64) (collmatrix.FlowBody, error) {
	fl, err := f.st.GetFlow(id)
	if err != nil {
		return collmatrix.FlowBody{}, err
	}
	rc, err := f.st.OpenBody(fl.ResBodyHash)
	if err != nil {
		return collmatrix.FlowBody{}, err
	}
	defer rc.Close()
	body, err := io.ReadAll(io.LimitReader(rc, maxBytes))
	if err != nil {
		return collmatrix.FlowBody{}, err
	}
	var hs []collmatrix.Header
	for k, vs := range fl.ResHeaders {
		for _, v := range vs {
			hs = append(hs, collmatrix.Header{Name: k, Value: v})
		}
	}
	return collmatrix.FlowBody{Status: fl.Status, Headers: hs, Body: body, Mime: fl.Mime}, nil
}

// collMatrixCallers maps a request to the caller identity: AI/MCP sources get
// the scope policy block and no secrets; only a UI session may opt in.
func collMatrixCallers() collmatrix.Callers {
	return collmatrix.Callers{IsAI: isAISource, Source: sourceOf}
}

// collMatrixRouteDocs is the catalogue form of the collmatrix routes. The
// handlers are method values on a nil service and are never invoked.
func collMatrixRouteDocs() []collmatrix.Route {
	return (*collmatrix.Service)(nil).Routes(collMatrixCallers())
}

func (c *collectionsAPI) registerMatrix() {
	svc := collmatrix.New(collmatrix.Deps{
		Backend:    c.backend(),
		Runs:       c.h.st,
		Identities: authzIdentitySource{c.h},
		Flows:      storeFlows{c.h.st},
		Bodies:     storeFlows{c.h.st},
		Evidence:   c.h.st,
	})
	for _, r := range svc.Routes(collMatrixCallers()) {
		c.h.mux.HandleFunc(r.Method+" "+r.Path, r.Handler)
	}
}

func init() {
	for _, r := range collMatrixRouteDocs() {
		apiRoutes = append(apiRoutes, apiRoute{Method: r.Method, Path: r.Path, Desc: r.Desc})
	}
	tools, _ := mcpDescriptor["tools"].([]map[string]string)
	mcpDescriptor["tools"] = append(tools, collMatrixMCPTools...)
}

// collMatrixMCPTools mirrors the tools internal/mcp registers for the
// differentiators (TestMCPDescriptorMatchesRegistry keeps both in step).
var collMatrixMCPTools = []map[string]string{
	{"name": "collection_identity_matrix", "desc": "Run a collection as each authz identity plus anonymous and return the access differential (scope policy block)"},
	{"name": "collection_openapi_coverage", "desc": "Which operations of an imported OpenAPI spec stored runs exercised"},
	{"name": "collection_intruder_handoff", "desc": "Build an Intruder attack spec from a collection request (secrets stay placeholders)"},
	{"name": "collection_example_diff", "desc": "Diff a saved response example against a captured response flow"},
	{"name": "collection_run_timing", "desc": "Timing breakdown and outliers of a stored collection run"},
	{"name": "collection_attach_run_evidence", "desc": "Attach the flows of a stored collection run to a finding as typed evidence"},
}
