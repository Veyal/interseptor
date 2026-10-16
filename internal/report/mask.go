package report

import (
	"github.com/Veyal/interseptor/internal/redact"
	"github.com/Veyal/interseptor/internal/store"
)

// MaskSensitiveHeaders returns copies of findings whose attached raw HTTP
// evidence has its credential header values replaced by redact's length+digest
// placeholder. It covers Authorization, Proxy-Authorization, Cookie, Set-Cookie
// and the other names in redact.CredentialHeaderNames, in both canonical
// evidence blocks and the legacy flow list.
//
// It is opt-in. Every export path renders from Project/ProjectHTML, so a caller
// that does not run this pass delivers evidence exactly as captured — which is
// the default, because a report is evidence first.
//
// Scope and honest limits:
//   - Only the header block of a raw message is rewritten. Bodies, the request
//     line, every other header, roles, proof statements and image provenance are
//     untouched, so the finding still reads as the proof it was.
//   - A credential inside a body, a URL or finding prose is NOT masked here. The
//     secret-lint notice in the HTML report reports those instead.
//   - The placeholder keeps the value's byte length and a 12-hex SHA-256 prefix
//     so a reader can still tell whether two requests carried the same
//     credential. That digest is a correlation handle, not a safe publication:
//     a short or low-entropy credential could be confirmed by guessing against
//     it. A masked export is sanitised for a client deliverable, not anonymised
//     for an untrusted audience.
func MaskSensitiveHeaders(findings []store.Finding) []store.Finding {
	if len(findings) == 0 {
		return findings
	}
	out := make([]store.Finding, len(findings))
	for i, f := range findings {
		if len(f.Blocks) > 0 {
			blocks := make([]store.FindingBlock, len(f.Blocks))
			copy(blocks, f.Blocks)
			for j := range blocks {
				blocks[j].ReqRaw = redact.CredentialHeaders(blocks[j].ReqRaw)
				blocks[j].ResRaw = redact.CredentialHeaders(blocks[j].ResRaw)
			}
			f.Blocks = blocks
		}
		if len(f.Flows) > 0 {
			flows := make([]store.FindingFlow, len(f.Flows))
			copy(flows, f.Flows)
			for j := range flows {
				flows[j].ReqRaw = redact.CredentialHeaders(flows[j].ReqRaw)
				flows[j].ResRaw = redact.CredentialHeaders(flows[j].ResRaw)
			}
			f.Flows = flows
		}
		out[i] = f
	}
	return out
}
