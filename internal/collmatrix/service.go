package collmatrix

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/store"
)

// IdentitySource lists the saved authz identities (the control layer adapts
// its authz store). Credentials stay inside Identity.Headers.
type IdentitySource interface {
	Identities() []Identity
}

// FlowSummary is the small, secret-free slice of a captured flow the matrix
// and the example diff need.
type FlowSummary struct {
	Status   int
	Length   int64
	BodyHash string
	Mime     string
	Location string
}

// FlowReader reads captured flows. Optional: without it cells are compared by
// status and length only.
type FlowReader interface {
	FlowSummary(id int64) (FlowSummary, bool)
}

// FlowBody is a captured response, bounded by the reader.
type FlowBody struct {
	Status  int
	Headers []Header
	Body    []byte
	Mime    string
}

// BodyReader reads captured response bodies for the saved-example diff.
type BodyReader interface {
	FlowBody(id int64, maxBytes int64) (FlowBody, error)
}

// Deps are the host services of a Service. Only Backend is required.
type Deps struct {
	Backend    collrun.Backend
	Runs       collrun.RunStore // optional; run headers/rows are persisted when set
	Identities IdentitySource
	Flows      FlowReader
	Bodies     BodyReader
	Evidence   EvidenceSink // optional; needed for attach routes
	// Scrub is the one secret scrubber; every string that leaves the package
	// passes through it. A nil Scrub is replaced by the backend's.
	Scrub func(string) string
	Now   func() time.Time
}

// Service is the entry point of the package.
type Service struct {
	d Deps

	mu    sync.Mutex
	kept  map[string]*Matrix
	order []string
}

const keptMatrices = 20

// New returns a Service. It panics only on a nil Backend, a wiring bug.
func New(d Deps) *Service {
	if d.Backend == nil {
		panic("collmatrix: nil Backend")
	}
	if d.Scrub == nil {
		d.Scrub = d.Backend.Scrub
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d, kept: map[string]*Matrix{}}
}

func (s *Service) scrub(v string) string {
	if v == "" {
		return v
	}
	return s.d.Scrub(v)
}

// remember stores a matrix for the render and attach routes, evicting the
// oldest beyond keptMatrices.
func (s *Service) remember(m *Matrix) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kept[m.ID] = m
	s.order = append(s.order, m.ID)
	for len(s.order) > keptMatrices {
		delete(s.kept, s.order[0])
		s.order = s.order[1:]
	}
}

// Matrix returns a remembered matrix.
func (s *Service) Matrix(id string) (*Matrix, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.kept[id]
	return m, ok
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

var _ collrun.RunStore = (*store.Store)(nil)
