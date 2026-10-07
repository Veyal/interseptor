package collrun

import (
	"context"

	"github.com/Veyal/interseptor/internal/collexec"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

// StepMeta is run context a Backend needs beyond the StepInput.
type StepMeta struct {
	// IterationCount is the total number of iterations (pm.info.iterationCount).
	IterationCount int
}

// Backend is everything the runner needs from the host process. The control
// layer (UI/MCP) and the headless CLI each supply one; the runner never
// imports either, so there is exactly one runner for every caller.
type Backend interface {
	// Load returns a collection and its items.
	Load(collectionUID string) (store.Collection, []store.Item, error)
	// EnvName returns an environment's display name ("" when unknown).
	EnvName(envUID string) string
	// EnvPin returns the environment's base_target_pin.
	EnvPin(envUID string) string
	// Layers builds the stored variable layers (current-else-initial values)
	// and the request-local variables for one request.
	Layers(chain collexec.Chain, envUID string) ([]varstore.Layer, map[string]string, error)
	// Step runs one request through the shared collexec pipeline.
	Step(ctx context.Context, in collexec.StepInput, meta StepMeta) (*collexec.StepResult, error)
	// Commit stores variable writes as local current values and returns the
	// "scope.key" names it could not store.
	Commit(coll store.Collection, envUID string, changes []collexec.VarChange) []string
	// Trusted reports whether an exact script hash is trusted for a collection.
	Trusted(collectionUID, scriptHash string) bool
	// Scrub is the one secret scrubber: registered secret values plus
	// credential-shaped text. Everything a run records passes through it.
	Scrub(string) string
}

// RunStore persists run headers and per-request rows. *store.Store satisfies
// it; nil disables persistence (reports are still produced).
type RunStore interface {
	PutRun(store.CollRun) (*store.CollRun, error)
	ListRuns(collectionUID string, limit int) ([]store.CollRun, error)
	AddRunResult(store.CollRunResult) (int64, error)
	ListRunResults(runUID string) ([]store.CollRunResult, error)
}

var _ RunStore = (*store.Store)(nil)
