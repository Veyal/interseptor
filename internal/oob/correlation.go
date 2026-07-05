package oob

import (
	"strings"
	"time"
)

// maxCorrelations bounds the correlation map so a long autonomous run cannot
// grow it without limit. It mirrors the interaction ring's maxInteractions cap.
const maxCorrelations = 500

// Correlation links a minted OOB token back to the exact probe that injected it,
// so an arriving interaction is attributable to a specific run/candidate/flow.
// This is the missing wire for blind-vulnerability verification (§5 Gate 3):
// the verifier mints a token FOR a probe, injects its URL, then polls for a
// callback carrying that exact token as proof the payload executed server-side.
type Correlation struct {
	Token       string `json:"token"`
	RunID       int64  `json:"runId"`
	CandidateID string `json:"candidateId"`
	ProbeFlowID int64  `json:"probeFlowId"`
	InjectedAt  int64  `json:"injectedAt"` // unix millis
}

// MintCorrelated mints a fresh token, stores its correlation to the injecting
// probe, and returns the token plus a ready-to-inject callback URL.
//
// baseURL is the operator-configured public OOB base (built by the caller from
// the request Host + the persisted oob.baseUrl setting — that URL-building lives
// in internal/control, so it is passed in here to keep the package boundary
// clean). The returned url is baseURL joined with the token; if baseURL is empty
// the returned url is empty and the caller can build it from just the token.
//
// The correlation is additive metadata: it does not touch the interaction ring,
// so the existing manual-OOB Record/List/Count/Clear flow is unchanged.
func (c *Catcher) MintCorrelated(runID int64, candidateID string, probeFlowID int64) (token, url string) {
	return c.mintCorrelated(runID, candidateID, probeFlowID, "")
}

// MintCorrelatedURL is MintCorrelated with a caller-supplied public base URL; it
// returns the token and the full callback URL (baseURL + "/" + token).
func (c *Catcher) MintCorrelatedURL(runID int64, candidateID string, probeFlowID int64, baseURL string) (token, url string) {
	return c.mintCorrelated(runID, candidateID, probeFlowID, baseURL)
}

func (c *Catcher) mintCorrelated(runID int64, candidateID string, probeFlowID int64, baseURL string) (token, url string) {
	tok := c.Token()
	corr := Correlation{
		Token:       tok,
		RunID:       runID,
		CandidateID: candidateID,
		ProbeFlowID: probeFlowID,
		InjectedAt:  time.Now().UnixMilli(),
	}
	c.cmu.Lock()
	if c.corr == nil {
		c.corr = make(map[string]Correlation)
		c.corrOrder = nil
	}
	c.corr[tok] = corr
	c.corrOrder = append(c.corrOrder, tok)
	// Bound the map: evict oldest tokens once over the cap.
	for len(c.corrOrder) > maxCorrelations {
		oldest := c.corrOrder[0]
		c.corrOrder = c.corrOrder[1:]
		delete(c.corr, oldest)
	}
	c.cmu.Unlock()

	if baseURL != "" {
		url = strings.TrimRight(baseURL, "/") + "/" + tok
	}
	return tok, url
}

// CorrelationFor returns the correlation recorded for a token, if any.
func (c *Catcher) CorrelationFor(token string) (Correlation, bool) {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	corr, ok := c.corr[token]
	return corr, ok
}

// InteractionsForToken returns any recorded interactions whose path-token equals
// token, newest first. This is the poll Gate 3 uses: a non-empty result means
// the injected payload was dereferenced server-side — proof of a blind vuln.
//
// It reads the existing interaction ring, so it works for both correlated tokens
// (minted via MintCorrelated) and plain manual-OOB tokens.
func (c *Catcher) InteractionsForToken(token string) []Interaction {
	if token == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Interaction
	// Walk newest-first so callers see the most recent callback first.
	for i := len(c.items) - 1; i >= 0; i-- {
		if c.items[i].Token == token {
			out = append(out, c.items[i])
		}
	}
	return out
}
