package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/hostpattern"
)

const (
	interceptionSetupKey   = "interception.setup"
	interceptionHistoryKey = "interception.setup.history"

	// MaxInterceptionEnablers bounds the pinning-bypass enablers in one record.
	MaxInterceptionEnablers = 32
	// MaxInterceptionHosts bounds each host list in a record.
	MaxInterceptionHosts = 64
	maxInterceptionField = 512

	// AnnotationPinningBlocked marks a bodiless CONNECT whose TLS handshake
	// was rejected although the host is meant to be intercepted.
	AnnotationPinningBlocked = "pinning_blocked"
	// AnnotationNotIntercepted marks a bodiless CONNECT that was never meant
	// to be intercepted (tunnelled, bypassed or out of scope).
	AnnotationNotIntercepted = "not_intercepted"
)

// InterceptionEnabler is one test-side mechanism that makes traffic visible,
// typically a certificate-pinning bypass such as a Frida hook.
type InterceptionEnabler struct {
	Tool          string   `json:"tool"`
	ScriptHash    string   `json:"scriptHash,omitempty"`
	TargetLibrary string   `json:"targetLibrary,omitempty"`
	Method        string   `json:"method,omitempty"`
	Hosts         []string `json:"hosts,omitempty"` // empty = every host the setup covers
}

// InterceptionSetup records how traffic is being intercepted: the proxy, the
// CA, and any pinning-bypass enablers. It is versioned so a flow or report can
// state which setup produced it.
type InterceptionSetup struct {
	Version       int                   `json:"version"`
	UpdatedAt     int64                 `json:"updatedAt,omitempty"`
	ProxyAddress  string                `json:"proxyAddress"`
	CAFingerprint string                `json:"caFingerprint"`
	Hosts         []string              `json:"hosts,omitempty"` // hosts the setup applies to; empty = all
	Enablers      []InterceptionEnabler `json:"enablers"`
}

func (i *InterceptionSetup) versionMeta() (*int, *int64) { return &i.Version, &i.UpdatedAt }

// InterceptionProvenance states how a flow's traffic was obtained.
type InterceptionProvenance struct {
	SetupVersion  int                   `json:"setupVersion"`
	ProxyAddress  string                `json:"proxyAddress"`
	CAFingerprint string                `json:"caFingerprint"`
	Enablers      []InterceptionEnabler `json:"enablers"`
}

func invalidInterception(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidEngagement, fmt.Sprintf(format, a...))
}

func validateHostList(label string, hosts []string) error {
	if len(hosts) > MaxInterceptionHosts {
		return invalidInterception("%s has more than %d entries", label, MaxInterceptionHosts)
	}
	for _, h := range hosts {
		if len(h) > maxInterceptionField {
			return invalidInterception("%s entry exceeds %d bytes", label, maxInterceptionField)
		}
	}
	return nil
}

func (i InterceptionSetup) validate() error {
	if len(i.ProxyAddress) > maxInterceptionField || len(i.CAFingerprint) > maxInterceptionField {
		return invalidInterception("proxyAddress/caFingerprint exceed %d bytes", maxInterceptionField)
	}
	if err := validateHostList("hosts", i.Hosts); err != nil {
		return err
	}
	if len(i.Enablers) > MaxInterceptionEnablers {
		return invalidInterception("more than %d enablers", MaxInterceptionEnablers)
	}
	for n, e := range i.Enablers {
		if strings.TrimSpace(e.Tool) == "" {
			return invalidInterception("enablers[%d].tool is required", n)
		}
		for _, v := range []string{e.Tool, e.ScriptHash, e.TargetLibrary, e.Method} {
			if len(v) > maxInterceptionField {
				return invalidInterception("enablers[%d] field exceeds %d bytes", n, maxInterceptionField)
			}
		}
		if err := validateHostList(fmt.Sprintf("enablers[%d].hosts", n), e.Hosts); err != nil {
			return err
		}
	}
	return nil
}

// GetInterceptionSetup returns the current record (version 0 when unset).
func (s *Store) GetInterceptionSetup() (InterceptionSetup, error) {
	var i InterceptionSetup
	if _, err := loadVersionedSetting(s.db, interceptionSetupKey, &i); err != nil {
		return i, err
	}
	if i.Enablers == nil {
		i.Enablers = []InterceptionEnabler{}
	}
	return i, nil
}

// SetInterceptionSetup saves the record. The version increments only when
// content changes; each new version is also appended to the history used to
// resolve flow provenance. Version and UpdatedAt in the input are ignored.
func (s *Store) SetInterceptionSetup(in InterceptionSetup) (InterceptionSetup, error) {
	if err := in.validate(); err != nil {
		return InterceptionSetup{}, err
	}
	var out InterceptionSetup
	err := s.saveVersionedSetting(interceptionSetupKey, &out, func() {
		out.ProxyAddress, out.CAFingerprint = in.ProxyAddress, in.CAFingerprint
		out.Hosts, out.Enablers = in.Hosts, in.Enablers
		if out.Enablers == nil {
			out.Enablers = []InterceptionEnabler{}
		}
	}, func(tx *sql.Tx) error { return appendInterceptionHistory(tx, out) })
	if err == nil && out.Enablers == nil {
		out.Enablers = []InterceptionEnabler{}
	}
	return out, err
}

func appendInterceptionHistory(tx *sql.Tx, snap InterceptionSetup) error {
	var hist []InterceptionSetup
	if _, err := loadVersionedSetting(tx, interceptionHistoryKey, &hist); err != nil {
		return err
	}
	hist = append(hist, snap)
	raw, err := json.Marshal(hist)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, interceptionHistoryKey, string(raw))
	return err
}

// InterceptionHistory returns every saved version, oldest first.
func (s *Store) InterceptionHistory() ([]InterceptionSetup, error) {
	var hist []InterceptionSetup
	_, err := loadVersionedSetting(s.db, interceptionHistoryKey, &hist)
	return hist, err
}

func hostCovered(patterns []string, host string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if hostpattern.MatchHost(strings.TrimSpace(p), host) {
			return true
		}
	}
	return false
}

// InterceptionProvenance resolves the setup version in force when a flow for
// host was captured at ts, with the enablers applying to that host. It returns
// nil when no setup existed yet or the setup did not cover the host.
func (s *Store) InterceptionProvenance(ts time.Time, host string) (*InterceptionProvenance, error) {
	hist, err := s.InterceptionHistory()
	if err != nil {
		return nil, err
	}
	return resolveProvenance(hist, ts, host), nil
}

func resolveProvenance(hist []InterceptionSetup, ts time.Time, host string) *InterceptionProvenance {
	at := ts.UnixMilli()
	var inForce *InterceptionSetup
	for i := range hist {
		if hist[i].UpdatedAt <= at {
			inForce = &hist[i]
		}
	}
	if inForce == nil || !hostCovered(inForce.Hosts, host) {
		return nil
	}
	p := &InterceptionProvenance{
		SetupVersion: inForce.Version, ProxyAddress: inForce.ProxyAddress,
		CAFingerprint: inForce.CAFingerprint, Enablers: []InterceptionEnabler{},
	}
	for _, e := range inForce.Enablers {
		if hostCovered(e.Hosts, host) {
			p.Enablers = append(p.Enablers, e)
		}
	}
	return p
}

// SetFlowInterceptionAnnotation marks a bodiless CONNECT status-0 flow as
// pinning_blocked or not_intercepted ("" clears), so a capture gap is never
// read as a finding. It returns the flow's resulting tags.
func (s *Store) SetFlowInterceptionAnnotation(flowID int64, annotation string) ([]string, error) {
	if annotation != "" && annotation != AnnotationPinningBlocked && annotation != AnnotationNotIntercepted {
		return nil, invalidInterception("annotation must be %s, %s or empty", AnnotationPinningBlocked, AnnotationNotIntercepted)
	}
	f, err := s.GetFlow(flowID)
	if err != nil {
		return nil, err
	}
	if f.Method != "CONNECT" || f.Status != 0 || f.ReqBodyHash != "" || f.ResBodyHash != "" {
		return nil, invalidInterception("only a bodiless CONNECT with status 0 can be annotated")
	}
	// The two annotations are mutually exclusive; remove wins over add in
	// MutateFlowTags, so only the other one is removed when setting.
	var add, remove []string
	switch annotation {
	case "":
		remove = []string{AnnotationPinningBlocked, AnnotationNotIntercepted}
	case AnnotationPinningBlocked:
		add, remove = []string{annotation}, []string{AnnotationNotIntercepted}
	default:
		add, remove = []string{annotation}, []string{AnnotationPinningBlocked}
	}
	if err := s.MutateFlowTags([]int64{flowID}, add, remove); err != nil {
		return nil, err
	}
	return s.FlowTags(flowID)
}
