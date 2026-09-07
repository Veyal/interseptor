package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// ErrFlowNotFound is returned by AttachFlow when the referenced flow id has no
// row in the flows table (typo, purged, or never captured).
var ErrFlowNotFound = errors.New("flow not found")

// ErrInvalidFinding identifies caller-correctable finding validation failures.
var ErrInvalidFinding = errors.New("invalid finding")

// maxFindingBodyBytes is the maximum serialized size of the canonical finding
// narrative. Keep this guard in the store as well as at the HTTP boundary: AI,
// MCP, imports, and legacy partial updates must all observe the same limit.
const maxFindingBodyBytes = 1 << 20 // 1 MiB

// Scalar report fields have their own aggregate cap so the canonical blocks
// can use the documented body allowance without permitting AI/API clients to
// place several additional MiB in summary, impact, remediation, or review text.
const maxFindingNarrativeBytes = 1 << 20 // 1 MiB

func validateFindingBodySize(body string) error {
	if len(body) > maxFindingBodyBytes {
		return fmt.Errorf("%w: body too large (max 1 MiB)", ErrInvalidFinding)
	}
	return nil
}

func validateFindingNarrativeSize(f Finding) error {
	if err := validateFindingBodySize(f.Body); err != nil {
		return err
	}
	metadata, err := json.Marshal(struct {
		Targets FindingTargets
		Review  FindingProofReview
	}{f.Targets, f.ProofReview})
	if err != nil {
		return fmt.Errorf("%w: metadata: %v", ErrInvalidFinding, err)
	}
	size := len(metadata) + len(f.Title) + len(f.Summary) + len(f.Target) + len(f.Fix) +
		len(f.Impact) + len(f.Why) + len(f.Cwe) + len(f.Cvss) +
		len(f.VerificationInstructions) + len(f.Retest) + len(f.Detail) + len(f.Evidence)
	// Legacy detail/evidence normally mirror canonical blocks, but migration
	// clients can supply distinct values and reports still render them. Count the
	// stored copies even when Body is present so compatibility fields cannot
	// become an unbounded side channel.
	if size > maxFindingNarrativeBytes {
		return fmt.Errorf("%w: finding narrative too large (max 1 MiB across report fields)", ErrInvalidFinding)
	}
	return nil
}

type findingNarrativeScanner interface {
	Scan(dest ...any) error
}

func scanFindingNarrative(row findingNarrativeScanner) (Finding, error) {
	var f Finding
	err := row.Scan(&f.Title, &f.Summary, &f.Target, &f.Detail, &f.Evidence, &f.Fix,
		&f.Body, &f.Impact, &f.Why, &f.Cwe, &f.Cvss, &f.VerificationInstructions, &f.Retest, &f.Targets, &f.ProofReview, &f.Status, &f.Severity)
	return f, err
}

func findingNarrativeRow(tx *sql.Tx, id int64) findingNarrativeScanner {
	return tx.QueryRow(`SELECT title, summary, target, detail, evidence, fix, body,
		impact, why, cwe, cvss, verification_instructions, retest, targets, proof_review, status, severity
		FROM findings WHERE id=?`, id)
}

// NormalizeFindingBody coerces common agent mistakes (type md/markdown → text)
// and rejects unknown block types. Returns the normalized JSON body (or "" for
// empty input). Empty/invalid JSON that is not an array is rejected when non-empty.
func NormalizeFindingBody(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", nil
	}
	var recs []blockRecord
	if err := json.Unmarshal([]byte(body), &recs); err != nil {
		return "", fmt.Errorf("body must be a JSON array of blocks: %w", err)
	}
	seenFlows := make(map[int64]struct{})
	normalized := recs[:0]
	for i := range recs {
		if strings.TrimSpace(recs[i].Role) != "" && normalizeFindingBlockRole(recs[i].Role) == "" {
			return "", fmt.Errorf("body block[%d]: invalid role %q", i, recs[i].Role)
		}
		recs[i].Role = normalizeFindingBlockRole(recs[i].Role)
		if strings.TrimSpace(recs[i].Source) != "" && normalizeFindingBlockSource(recs[i].Source) == "" {
			return "", fmt.Errorf("body block[%d]: invalid source %q", i, recs[i].Source)
		}
		recs[i].Proof = strings.TrimSpace(recs[i].Proof)
		recs[i].Source = normalizeFindingBlockSource(recs[i].Source)
		switch strings.ToLower(strings.TrimSpace(recs[i].Type)) {
		case "text":
			recs[i].Type = "text"
		case "md", "markdown":
			recs[i].Type = "text"
		case "flow":
			recs[i].Type = "flow"
			if recs[i].FlowID <= 0 {
				return "", fmt.Errorf("body block[%d]: flow block missing flowId", i)
			}
			if _, ok := seenFlows[recs[i].FlowID]; ok {
				continue
			}
			seenFlows[recs[i].FlowID] = struct{}{}
		case "image":
			recs[i].Type = "image"
		default:
			return "", fmt.Errorf("body block[%d]: type must be text|flow|image, got %q", i, recs[i].Type)
		}
		normalized = append(normalized, recs[i])
	}
	recs = normalized
	j, err := json.Marshal(recs)
	if err != nil {
		return "", err
	}
	return string(j), nil
}

func normalizeFindingBlockRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "before", "baseline":
		return "baseline"
	case "after", "proof", "result":
		return "result"
	case "context", "setup", "action", "control", "retest", "observation":
		return strings.ToLower(strings.TrimSpace(role))
	default:
		return ""
	}
}

func normalizeFindingBlockSource(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "device_screenshot":
		return "device_screenshot"
	case "browser_screenshot", "screenshot":
		return "browser_screenshot"
	case "flow_preview", "preview":
		return "flow_preview"
	case "operator_upload", "upload":
		return "operator_upload"
	case "captured_flow":
		return "captured_flow"
	case "tool_output":
		return "tool_output"
	case "generated_image":
		return "generated_image"
	case "other":
		return "other"
	default:
		return ""
	}
}

func validateFindingEvidenceMetadata(role, source string, sourceFlowID int64) error {
	if strings.TrimSpace(role) != "" && normalizeFindingBlockRole(role) == "" {
		return fmt.Errorf("%w: evidence role %q", ErrInvalidFinding, role)
	}
	if strings.TrimSpace(source) != "" && normalizeFindingBlockSource(source) == "" {
		return fmt.Errorf("%w: evidence source %q", ErrInvalidFinding, source)
	}
	if sourceFlowID < 0 {
		return fmt.Errorf("%w: sourceFlowId must not be negative", ErrInvalidFinding)
	}
	return nil
}

// MarshalFindingBlocks validates and serializes structured evidence blocks for REST/MCP callers.
func MarshalFindingBlocks(blocks []FindingBlock) (string, error) {
	for i, block := range blocks {
		if err := validateFindingEvidenceMetadata(block.Role, block.Source, block.SourceFlowID); err != nil {
			return "", fmt.Errorf("body block[%d]: %w", i, err)
		}
	}
	return NormalizeFindingBody(marshalBody(blocks))
}

// NormalizeFindingBlocks validates and canonicalizes structured blocks.
func NormalizeFindingBlocks(blocks []FindingBlock) ([]FindingBlock, error) {
	body, err := MarshalFindingBlocks(blocks)
	if err != nil {
		return nil, err
	}
	if body == "" {
		return []FindingBlock{}, nil
	}
	var recs []blockRecord
	if err := json.Unmarshal([]byte(body), &recs); err != nil {
		return nil, err
	}
	out := make([]FindingBlock, len(recs))
	for i, r := range recs {
		out[i] = FindingBlock{Type: r.Type, MD: r.MD, FlowID: r.FlowID, Note: r.Note, Hash: r.Hash, Mime: r.Mime, Caption: r.Caption, Role: r.Role, Proof: r.Proof, Provenance: r.Provenance, Source: r.Source, SourceFlowID: r.SourceFlowID}
	}
	return out, nil
}

// Finding is a curated vulnerability write-up for a project. Unlike a scanner
// Issue (auto-generated, ephemeral), a Finding is persistent and human/AI-curated:
// it carries a status the operator manages and has a narrative body — an ordered
// sequence of text blocks (markdown) and flow-reference blocks (clickable PoC
// request/response), freely interleaved.
type Finding struct {
	ID               int64              `json:"id"`
	TS               int64              `json:"ts"`        // created, unix millis
	UpdatedTS        int64              `json:"updatedTs"` // last modified, unix millis
	Severity         string             `json:"severity"`  // Critical | High | Medium | Low | Info
	Status           string             `json:"status"`    // open | needs_verification | verified | false_positive | wont_fix | fixed
	Source           string             `json:"source"`    // human | ai | scanner
	Title            string             `json:"title"`
	Summary          string             `json:"summary,omitempty"`
	Target           string             `json:"target"`
	Targets          FindingTargets     `json:"targets"`
	ProofReview      FindingProofReview `json:"proofReview"`
	CvssScore        *float64           `json:"cvssScore,omitempty"`
	CvssRating       string             `json:"cvssRating,omitempty"`
	CvssNomenclature string             `json:"cvssNomenclature,omitempty"`
	Confidence       string             `json:"confidence,omitempty"`
	Detail           string             `json:"detail"`                // legacy / MCP compat: first text block synced here
	Evidence         string             `json:"evidence"`              // legacy only
	Fix              string             `json:"fix"`                   // back-compat: kept but superseded by Impact
	Impact           string             `json:"impact"`                // security impact — what an attacker gains / business consequence
	Why              string             `json:"why"`                   // why this is a vulnerability (broken security property)
	Cwe              string             `json:"cwe,omitempty"`         // CWE id or short class, e.g. CWE-639 / IDOR
	Environment      string             `json:"environment,omitempty"` // production | staging | development | testing | local; legacy prod
	Cvss             string             `json:"cvss,omitempty"`        // CVSS:4.0 vector for readiness; older score/vector strings remain readable
	// VerificationInstructions tells a human reviewer exactly what to check when
	// Status is needs_verification (e.g. "download X and run file on it").
	VerificationInstructions string         `json:"verificationInstructions,omitempty"`
	Retest                   string         `json:"retest,omitempty"`
	Body                     string         `json:"body,omitempty"` // stored JSON blocks (use Blocks for rendering)
	Flows                    []FindingFlow  `json:"flows"`          // attached flow metadata (for list sidebar count)
	Blocks                   []FindingBlock `json:"blocks"`         // ordered narrative body (source of truth for UI)
	// Tags are report-scoping labels (same slug model as flow tags), e.g. cms / api / out-of-scope.
	Tags []string `json:"tags"`
	// Verification is the Autopilot/machine proof-record when present (not stored on the finding row).
	Verification *FindingVerification `json:"verification,omitempty"`
	// Ready / Missing are computed at read time (not stored) — report-ready checklist.
	Ready     bool              `json:"ready"`
	Missing   []string          `json:"missing,omitempty"`
	Readiness *FindingReadiness `json:"readiness,omitempty"`
}

// FindingReadiness is the structured report readiness summary. Ready/Missing
// remain the compatibility surface; this adds counts and actionable stage data.
type FindingReadiness struct {
	Checks                 []FindingQualityCheck `json:"checks"`
	Stage                  string                `json:"stage"`
	TargetEvidenceGaps     []int                 `json:"targetEvidenceGaps,omitempty"`
	Gaps                   []string              `json:"gaps,omitempty"`
	EvidenceCount          int                   `json:"evidenceCount"`
	AnnotatedEvidenceCount int                   `json:"annotatedEvidenceCount"`
	FlowCount              int                   `json:"flowCount"`
	ScreenshotCount        int                   `json:"screenshotCount"`
	ImageCount             int                   `json:"imageCount"`
	VisualProofRecommended bool                  `json:"visualProofRecommended"`
}

// FindingBlock is one element in a finding's narrative body.
type FindingBlock struct {
	Type         string                  `json:"type"`              // "text", "flow", or "image"
	MD           string                  `json:"md,omitempty"`      // type=="text": markdown content
	FlowID       int64                   `json:"flowId,omitempty"`  // type=="flow": attached flow
	Note         string                  `json:"note,omitempty"`    // type=="flow": annotation
	Hash         string                  `json:"hash,omitempty"`    // type=="image": content-addressed sha256
	Mime         string                  `json:"mime,omitempty"`    // type=="image": sanitized MIME
	Caption      string                  `json:"caption,omitempty"` // type=="image": optional caption
	Role         string                  `json:"role,omitempty"`    // context/setup/baseline/action/result/control/retest/observation
	Proof        string                  `json:"proof,omitempty"`   // exact claim this evidence establishes
	Source       string                  `json:"source,omitempty"`  // captured_flow/flow_preview/browser_screenshot/operator_upload/tool_output/other
	SourceFlowID int64                   `json:"sourceFlowId,omitempty"`
	Provenance   *FindingImageProvenance `json:"provenance,omitempty"`

	// Enriched at read time from the flows JOIN — never stored in the body JSON.
	Method string `json:"method,omitempty"`
	Host   string `json:"host,omitempty"`
	Path   string `json:"path,omitempty"`
	Status int    `json:"status,omitempty"`

	// ReqRaw / ResRaw are reconstructed HTTP messages for report export only
	// (same shape as GET /api/flows/{id}/raw). Never stored; omitted from list APIs.
	ReqRaw string `json:"reqRaw,omitempty"`
	ResRaw string `json:"resRaw,omitempty"`

	// URL is set at read time for image blocks (GET /api/findings/images/{hash}).
	URL string `json:"url,omitempty"`

	// Missing is set when referenced evidence is gone: a purged flow (type=="flow")
	// or a missing body blob (type=="image"). The block and annotation/caption are
	// preserved; the UI/report surface that the evidence is gone.
	Missing    bool `json:"missing,omitempty"`
	RawMissing bool `json:"rawMissing,omitempty"`
}

// FindingFlow is one PoC flow attached to a finding, enriched with a compact flow
// summary for display (the human selects request/responses to record here).
type FindingFlow struct {
	RawMissing bool   `json:"rawMissing,omitempty"`
	FlowID     int64  `json:"flowId"`
	Ord        int    `json:"ord"`
	Note       string `json:"note,omitempty"`
	Method     string `json:"method,omitempty"`
	Host       string `json:"host,omitempty"`
	Path       string `json:"path,omitempty"`
	Status     int    `json:"status,omitempty"`

	// Missing is true when the referenced flow row no longer exists in the flows
	// table (purged via prune_history / GC). The attachment row and note survive.
	Missing bool `json:"missing,omitempty"`

	// ReqRaw / ResRaw are report-export enrichments (not stored).
	ReqRaw string `json:"reqRaw,omitempty"`
	ResRaw string `json:"resRaw,omitempty"`
}

// blockRecord is the minimal form written to the body column (no enriched metadata).
type blockRecord struct {
	Type         string                  `json:"type"`
	MD           string                  `json:"md,omitempty"`
	FlowID       int64                   `json:"flowId,omitempty"`
	Note         string                  `json:"note,omitempty"`
	Hash         string                  `json:"hash,omitempty"`
	Mime         string                  `json:"mime,omitempty"`
	Caption      string                  `json:"caption,omitempty"`
	Role         string                  `json:"role,omitempty"`
	Proof        string                  `json:"proof,omitempty"`
	Source       string                  `json:"source,omitempty"`
	SourceFlowID int64                   `json:"sourceFlowId,omitempty"`
	Provenance   *FindingImageProvenance `json:"provenance,omitempty"`
	Missing      bool                    `json:"missing,omitempty"`
}

// marshalBody serializes blocks for storage, stripping enriched metadata.
func marshalBody(blocks []FindingBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	recs := make([]blockRecord, len(blocks))
	for i, b := range blocks {
		recs[i] = blockRecord{
			Type: b.Type, MD: b.MD, FlowID: b.FlowID, Note: b.Note,
			Hash: b.Hash, Mime: b.Mime, Caption: b.Caption, Role: normalizeFindingBlockRole(b.Role), Proof: b.Proof,
			Provenance: b.Provenance, Source: normalizeFindingBlockSource(b.Source), SourceFlowID: b.SourceFlowID, Missing: b.Type == "flow" && b.Missing,
		}
	}
	j, _ := json.Marshal(recs)
	return string(j)
}

// buildBlocks parses the stored body JSON and enriches flow blocks with flow
// metadata. If body is empty, synthesizes blocks from legacy detail/evidence/flows.
func buildBlocks(body, detail, evidence string, flows []FindingFlow) []FindingBlock {
	// Build a lookup of flow metadata.
	flowMeta := make(map[int64]FindingFlow, len(flows))
	for _, fl := range flows {
		flowMeta[fl.FlowID] = fl
	}

	if body != "" {
		var recs []blockRecord
		if err := json.Unmarshal([]byte(body), &recs); err == nil && len(recs) > 0 {
			blocks := make([]FindingBlock, len(recs))
			for i, r := range recs {
				blocks[i] = FindingBlock{
					Type: r.Type, MD: r.MD, FlowID: r.FlowID, Note: r.Note,
					Hash: r.Hash, Mime: r.Mime, Caption: r.Caption, Role: r.Role, Proof: r.Proof,
					Provenance: r.Provenance, Source: r.Source, SourceFlowID: r.SourceFlowID, Missing: r.Missing,
				}
				if blocks[i].Role == "" && r.Type == "flow" {
					// Older AttachFlow callers only had a free-form note. Preserve
					// their common Before/After labels in the canonical read model so
					// legacy findings can still satisfy the reproduction gate without
					// imposing a differential requirement on new findings.
					blocks[i].Role = findingBlockRoleFromNote(r.Note)
				}
				if r.Type == "flow" && !r.Missing {
					if fl, ok := flowMeta[r.FlowID]; ok {
						blocks[i].Method = fl.Method
						blocks[i].Host = fl.Host
						blocks[i].Path = fl.Path
						blocks[i].Status = fl.Status
						blocks[i].Missing = fl.Missing
						blocks[i].RawMissing = fl.RawMissing
					} else {
						// No attachment row for this flow id at all — the referenced
						// flow is gone (purged). Preserve the block; mark it missing.
						blocks[i].Missing = true
					}
				} else if r.Type == "flow" {
					// A persisted missing marker is authoritative even if a later
					// local flow happens to reuse the same numeric id.
					blocks[i].Missing = true
				}
			}
			return blocks
		}
	}

	// Legacy synthesis: detail text + evidence text + flow rows.
	var blocks []FindingBlock
	if detail != "" {
		blocks = append(blocks, FindingBlock{Type: "text", MD: detail})
	}
	if evidence != "" {
		blocks = append(blocks, FindingBlock{Type: "text", MD: evidence})
	}
	for _, fl := range flows {
		blocks = append(blocks, FindingBlock{
			Type: "flow", FlowID: fl.FlowID, Note: fl.Note,
			Role:   findingBlockRoleFromNote(fl.Note),
			Method: fl.Method, Host: fl.Host, Path: fl.Path, Status: fl.Status,
			Missing: fl.Missing,
		})
	}
	return blocks
}

func findingBlockRoleFromNote(note string) string {
	trimmed := strings.TrimSpace(note)
	if trimmed == "" {
		return ""
	}
	label := trimmed
	if colon := strings.IndexByte(label, ':'); colon >= 0 {
		label = label[:colon]
	}
	return normalizeFindingBlockRole(label)
}

// initialBody creates the first body JSON from create-time text fields.
func initialBody(detail, evidence string) string {
	var blocks []blockRecord
	if detail != "" {
		blocks = append(blocks, blockRecord{Type: "text", MD: detail})
	}
	if evidence != "" {
		blocks = append(blocks, blockRecord{Type: "text", MD: evidence})
	}
	if len(blocks) == 0 {
		return ""
	}
	j, _ := json.Marshal(blocks)
	return string(j)
}

// appendFlowToBody adds a flow block at the end of the stored body JSON.
// If the flow is already present, its note is updated. Returns the new body JSON.
func appendFlowToBody(bodyJSON string, flowID int64, note string) string {
	return insertFlowIntoBody(bodyJSON, flowID, note, -1)
}

// insertFlowIntoBody inserts a flow block at position pos (0-based block index)
// in the stored body JSON. pos < 0 or pos >= len means append at end.
// If the flow is already present, its note is updated in-place (position unchanged).
func insertFlowIntoBody(bodyJSON string, flowID int64, note string, pos int) string {
	return insertFlowIntoBodyWithMetadata(bodyJSON, flowID, note, pos, "", "", "", 0)
}

func insertFlowIntoBodyWithMetadata(bodyJSON string, flowID int64, note string, pos int, role, proof, source string, sourceFlowID int64) string {
	var recs []blockRecord
	if bodyJSON != "" {
		_ = json.Unmarshal([]byte(bodyJSON), &recs)
	}
	// If already present, update the note in-place — don't change position.
	for i, r := range recs {
		if r.Type == "flow" && r.FlowID == flowID {
			recs[i].Note = note
			if strings.TrimSpace(role) != "" {
				recs[i].Role = normalizeFindingBlockRole(role)
			}
			if strings.TrimSpace(proof) != "" {
				recs[i].Proof = strings.TrimSpace(proof)
			}
			if strings.TrimSpace(source) != "" {
				recs[i].Source = normalizeFindingBlockSource(source)
			}
			if sourceFlowID != 0 {
				recs[i].SourceFlowID = sourceFlowID
			}
			j, _ := json.Marshal(recs)
			return string(j)
		}
	}
	newBlock := blockRecord{Type: "flow", FlowID: flowID, Note: note, Role: normalizeFindingBlockRole(role), Proof: strings.TrimSpace(proof), Source: normalizeFindingBlockSource(source), SourceFlowID: sourceFlowID}
	if pos < 0 || pos >= len(recs) {
		recs = append(recs, newBlock)
	} else {
		recs = append(recs, blockRecord{}) // grow by one
		copy(recs[pos+1:], recs[pos:])
		recs[pos] = newBlock
	}
	j, _ := json.Marshal(recs)
	return string(j)
}

// removeFlowFromBody removes all flow blocks with the given flowID from the body JSON.
func removeFlowFromBody(bodyJSON string, flowID int64) string {
	if bodyJSON == "" {
		return ""
	}
	var recs []blockRecord
	if err := json.Unmarshal([]byte(bodyJSON), &recs); err != nil {
		return bodyJSON
	}
	filtered := recs[:0]
	for _, r := range recs {
		if r.Type != "flow" || r.FlowID != flowID {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == 0 {
		return ""
	}
	j, _ := json.Marshal(filtered)
	return string(j)
}

// firstTextMD returns the markdown content of the first text block in the body JSON.
func firstTextMD(bodyJSON string) string {
	if bodyJSON == "" {
		return ""
	}
	var recs []blockRecord
	if err := json.Unmarshal([]byte(bodyJSON), &recs); err != nil {
		return ""
	}
	for _, r := range recs {
		if r.Type == "text" && r.MD != "" {
			return r.MD
		}
	}
	return ""
}

func bodyHasText(bodyJSON, text string) bool {
	if bodyJSON == "" || text == "" {
		return false
	}
	var recs []blockRecord
	if err := json.Unmarshal([]byte(bodyJSON), &recs); err != nil {
		return false
	}
	for _, rec := range recs {
		if rec.Type == "text" && rec.MD == text {
			return true
		}
	}
	return false
}

func legacyEvidenceAfterBodyReplace(existingBody, nextBody, evidence string) (string, bool) {
	if evidence == "" {
		return "", false
	}
	var existing, next []blockRecord
	if json.Unmarshal([]byte(existingBody), &existing) != nil || json.Unmarshal([]byte(nextBody), &next) != nil {
		return "", false
	}
	match := -1
	for i := len(existing) - 1; i >= 0; i-- {
		if existing[i].Type == "text" && existing[i].MD == evidence {
			match = i
			break
		}
	}
	if match < 0 {
		return "", false
	}
	if bodyHasText(nextBody, evidence) {
		return evidence, true
	}
	if match < len(next) && next[match].Type == "text" {
		return next[match].MD, true
	}
	return "", true
}

func preserveMissingFlowMarkers(existingBody, nextBody string) string {
	var existing, next []blockRecord
	if existingBody != "" {
		_ = json.Unmarshal([]byte(existingBody), &existing)
	}
	if nextBody == "" || json.Unmarshal([]byte(nextBody), &next) != nil {
		return nextBody
	}
	generated := make(map[string]blockRecord)
	missing := make(map[int64]bool)
	for _, rec := range existing {
		if rec.Type == "image" && (rec.Source == "flow_preview" || rec.Source == "generated_image") {
			generated[rec.Hash] = rec
		}
		if rec.Type == "flow" && rec.FlowID > 0 && rec.Missing {
			missing[rec.FlowID] = true
		}
	}
	for i := range next {
		if old, ok := generated[next[i].Hash]; ok && next[i].Type == "image" {
			next[i].Source = old.Source
			next[i].SourceFlowID = old.SourceFlowID
		}
		next[i].Missing = next[i].Type == "flow" && missing[next[i].FlowID]
	}
	encoded, _ := json.Marshal(next)
	return string(encoded)
}

// updateFirstTextInBody replaces the first text block's content in body JSON.
// If no text block exists, prepends one.
func updateFirstTextInBody(bodyJSON, md string) string {
	var recs []blockRecord
	if bodyJSON != "" {
		_ = json.Unmarshal([]byte(bodyJSON), &recs)
	}
	for i, r := range recs {
		if r.Type == "text" {
			recs[i].MD = md
			j, _ := json.Marshal(recs)
			return string(j)
		}
	}
	// No text block yet — prepend one.
	recs = append([]blockRecord{{Type: "text", MD: md}}, recs...)
	j, _ := json.Marshal(recs)
	return string(j)
}

// updateLegacyEvidenceInBody keeps the deprecated evidence field and the
// canonical ordered blocks aligned. The last exact old-evidence text match is
// used so identical detail/evidence values do not overwrite the opening step.
func updateLegacyEvidenceInBody(bodyJSON, oldEvidence, nextEvidence string) string {
	var recs []blockRecord
	if bodyJSON != "" {
		_ = json.Unmarshal([]byte(bodyJSON), &recs)
	}
	match := -1
	if oldEvidence != "" {
		for i := len(recs) - 1; i >= 0; i-- {
			if recs[i].Type == "text" && recs[i].MD == oldEvidence {
				match = i
				break
			}
		}
	}
	if match >= 0 {
		if nextEvidence == "" {
			recs = append(recs[:match], recs[match+1:]...)
		} else {
			recs[match].MD = nextEvidence
		}
	} else if nextEvidence != "" {
		recs = append(recs, blockRecord{Type: "text", MD: nextEvidence, Role: "observation"})
	}
	j, _ := json.Marshal(recs)
	return string(j)
}

func normalizeFindingSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return "Critical"
	case "high":
		return "High"
	case "low":
		return "Low"
	case "info", "informational":
		return "Info"
	default:
		return "Medium"
	}
}

func normalizeFindingStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "verified":
		return "verified"
	case "needs_verification", "needs-verification", "needsverification":
		return "needs_verification"
	case "false_positive", "false-positive", "fp":
		return "false_positive"
	case "wont_fix", "wontfix", "won't_fix":
		return "wont_fix"
	case "fixed", "remediated":
		return "fixed"
	default:
		return "open"
	}
}

func normalizeFindingSource(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "ai":
		return "ai"
	case "scanner":
		return "scanner"
	default:
		return "human"
	}
}

func normalizeFindingEnvironment(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "dev":
		return "development"
	case "stage", "stg":
		return "staging"
	default:
		return s
	}
}

func validateFindingEnvironment(s string) error {
	switch normalizeFindingEnvironment(s) {
	case "", "production", "prod", "staging", "development", "testing", "local":
		return nil
	default:
		return fmt.Errorf("%w: environment must be production, staging, development, testing, local, or legacy prod", ErrInvalidFinding)
	}
}

func normalizeFindingConfidence(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "tentative":
		return "tentative"
	case "firm", "high":
		return "firm"
	case "certain", "confirmed":
		return "certain"
	default:
		return ""
	}
}

func validateFindingConfidence(s string) error {
	if strings.TrimSpace(s) != "" && normalizeFindingConfidence(s) == "" {
		return fmt.Errorf("%w: confidence %q", ErrInvalidFinding, s)
	}
	return nil
}

// EnrichCompleteness fills Ready/Missing and best-effort migrates Why from old
// "## Why this is a vulnerability" narrative when the why column is empty.
func (f *Finding) EnrichCompleteness() {
	if f == nil {
		return
	}
	if strings.TrimSpace(f.Why) == "" {
		if w := ExtractWhyFromNarrative(f.narrativeText()); w != "" {
			f.Why = w
		}
	}
	r := f.ReadinessSummary()
	f.Readiness = &r
	// Ready/Missing are the long-standing compatibility fields. Derive them
	// from the canonical envelope so clients never see a contradictory state
	// (for example Ready=true while Readiness still reports gaps). Keep the old
	// "poc" spelling as an output alias for the canonical "evidence" gap.
	f.Missing = compatibilityFindingGaps(r.Gaps)
	f.Ready = r.Stage == "report_ready"
}

func compatibilityFindingGaps(gaps []string) []string {
	if len(gaps) == 0 {
		return nil
	}
	out := make([]string, 0, len(gaps))
	for _, gap := range gaps {
		if gap == "evidence" {
			gap = "poc"
		}
		// poc_before_after was an old severity heuristic, not a canonical
		// requirement. Do not leak it through the compatibility surface.
		if gap == "poc_before_after" {
			continue
		}
		out = append(out, gap)
	}
	return out
}

func (f *Finding) narrativeText() string {
	var parts []string
	if d := strings.TrimSpace(f.Detail); d != "" {
		parts = append(parts, d)
	}
	for _, b := range f.Blocks {
		if b.Type == "text" && strings.TrimSpace(b.MD) != "" {
			parts = append(parts, b.MD)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (f *Finding) completenessGaps() []string {
	var miss []string
	if strings.TrimSpace(f.Title) == "" {
		miss = append(miss, "title")
	}
	if strings.TrimSpace(f.Impact) == "" {
		miss = append(miss, "impact")
	}
	if strings.TrimSpace(f.Why) == "" {
		miss = append(miss, "why")
	}
	if strings.TrimSpace(f.Target) == "" {
		miss = append(miss, "target")
	}
	flowN, imgN := 0, 0
	for _, b := range f.Blocks {
		switch b.Type {
		case "flow":
			if !b.Missing {
				flowN++
			}
		case "image":
			if !b.Missing && b.Hash != "" {
				imgN++
			}
		}
	}
	// Also count finding_flows if blocks empty of flows but Flows populated.
	if flowN == 0 {
		for _, fl := range f.Flows {
			if !fl.Missing {
				flowN++
			}
		}
	}
	if flowN+imgN == 0 {
		miss = append(miss, "poc")
	}
	return miss
}

// ReadinessSummary reports evidence completeness using the canonical envelope.
func (f *Finding) ReadinessSummary() FindingReadiness {
	f.enrichAssessment()
	r := FindingReadiness{}
	var gaps []string
	if strings.TrimSpace(f.Title) == "" {
		gaps = append(gaps, "title")
	}
	if strings.TrimSpace(f.Summary) == "" {
		gaps = append(gaps, "summary")
	}
	if strings.TrimSpace(f.Target) == "" {
		gaps = append(gaps, "target")
	}
	if strings.TrimSpace(f.Impact) == "" {
		gaps = append(gaps, "impact")
	}
	if strings.TrimSpace(f.Why) == "" {
		gaps = append(gaps, "why")
	}
	var typed bool
	var unproved int
	for _, b := range f.Blocks {
		if b.Missing {
			continue
		}
		switch b.Type {
		case "flow":
			r.FlowCount++
			if strings.TrimSpace(b.Proof) != "" {
				r.AnnotatedEvidenceCount++
			} else {
				unproved++
			}
		case "image":
			if b.Hash == "" {
				continue
			}
			if capturedFindingImage(b.Source) {
				r.ScreenshotCount++
			}
			r.ImageCount++
			if strings.TrimSpace(b.Proof) != "" {
				r.AnnotatedEvidenceCount++
			} else {
				unproved++
			}
		}
		if b.Role == "baseline" || b.Role == "action" || b.Role == "result" || b.Role == "control" || b.Role == "retest" {
			typed = true
		}
	}
	r.EvidenceCount = r.FlowCount + r.ImageCount
	r.VisualProofRecommended = r.ScreenshotCount == 0
	if r.EvidenceCount == 0 {
		gaps = append(gaps, "evidence")
	}
	if unproved > 0 {
		gaps = append(gaps, "proof")
	}
	capabilityGaps := f.assessmentGaps(&r)
	if !slices.Contains(capabilityGaps, "action") && !slices.Contains(capabilityGaps, "result") && !slices.Contains(capabilityGaps, "control") {
		typed = true
	}
	if !typed && r.EvidenceCount > 0 {
		gaps = append(gaps, "reproduction")
	}
	if strings.TrimSpace(f.Fix) == "" {
		gaps = append(gaps, "fix")
	}
	if strings.TrimSpace(f.Retest) == "" {
		gaps = append(gaps, "retest")
	}
	if strings.TrimSpace(f.Confidence) == "" {
		gaps = append(gaps, "confidence")
	}
	gaps = append(gaps, capabilityGaps...)
	gaps = append(gaps, f.capabilityClaimGaps()...)
	for _, b := range f.Blocks {
		if b.Missing || b.RawMissing {
			gaps = addUniqueFindingGap(gaps, "evidence_missing")
		}
	}
	if f.Status == "needs_verification" {
		gaps = addUniqueFindingGap(gaps, "verification")
	}
	r.Checks = qualityChecks(gaps)
	r.Gaps = gaps
	if len(gaps) > 0 && (r.EvidenceCount == 0 || len(gaps) >= 1 && (strings.TrimSpace(f.Title) == "" || strings.TrimSpace(f.Summary) == "" || strings.TrimSpace(f.Target) == "" || strings.TrimSpace(f.Impact) == "" || strings.TrimSpace(f.Why) == "")) {
		r.Stage = "draft"
		return r
	}
	incompleteProof := false
	for _, gap := range []string{"action", "result", "control", "visual", "target_evidence", "execution"} {
		incompleteProof = incompleteProof || slices.Contains(gaps, gap)
	}
	if len(gaps) > 0 && (!typed || unproved > 0 || incompleteProof) {
		r.Stage = "evidence_attached"
		return r
	}
	if len(gaps) > 0 {
		r.Stage = "reproducible"
		return r
	}
	r.Stage = "report_ready"
	return r
}

// ExtractWhyFromNarrative pulls the body of "## Why this is a vulnerability"
// from legacy markdown (best-effort migration).
func ExtractWhyFromNarrative(text string) string {
	re := regexp.MustCompile(`(?im)^##\s+Why this is a vulnerability\s*\n+([\s\S]*?)(?:\n##\s+|$)`)
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// CreateFinding inserts a finding and sets f.ID/f.TS/f.UpdatedTS. Title is required.
// If Body is empty it is synthesized from Detail + Evidence so new findings are
// immediately in the interleaved-body format.
func (s *Store) CreateFinding(f *Finding, changes ...FindingChange) (int64, error) {
	now := time.Now().UnixMilli()
	f.TS, f.UpdatedTS = now, now
	f.Severity = normalizeFindingSeverity(f.Severity)
	f.Status = normalizeFindingStatus(f.Status)
	f.Source = normalizeFindingSource(f.Source)
	if err := validateFindingEnvironment(f.Environment); err != nil {
		return 0, err
	}
	f.Environment = normalizeFindingEnvironment(f.Environment)
	if err := validateFindingConfidence(f.Confidence); err != nil {
		return 0, err
	}
	f.Confidence = normalizeFindingConfidence(f.Confidence)
	if err := normalizeFindingAssessment(f); err != nil {
		return 0, err
	}
	if f.Body == "" {
		f.Body = initialBody(f.Detail, f.Evidence)
	}
	normBody, err := NormalizeFindingBody(f.Body)
	if err != nil {
		return 0, err
	}
	f.Body = normBody
	if f.Detail == "" && f.Body != "" {
		f.Detail = firstTextMD(f.Body)
	}
	if err := validateFindingNarrativeSize(*f); err != nil {
		return 0, err
	}
	normTags := NormalizeTags(f.Tags)
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := validateFindingReferences(tx, *f); err != nil {
		return 0, err
	}
	f.Body, err = stampFindingImageProvenance(tx, "", f.Body, firstFindingChange(changes))
	if err != nil {
		return 0, err
	}
	if err := validateFindingNarrativeSize(*f); err != nil {
		return 0, err
	}
	res, err := tx.Exec(
		`INSERT INTO findings (ts, updated_ts, severity, status, source, title, summary, target, confidence, detail, evidence, fix, body, impact, why, cwe, environment, cvss, verification_instructions, retest, targets, proof_review)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.TS, f.UpdatedTS, f.Severity, f.Status, f.Source, f.Title, f.Summary, f.Target, f.Confidence, f.Detail, f.Evidence, f.Fix, f.Body, f.Impact, f.Why, f.Cwe, f.Environment, f.Cvss, f.VerificationInstructions, f.Retest, f.Targets, f.ProofReview)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if err := syncFindingFlowsFromBody(tx, id, f.Body); err != nil {
		return 0, err
	}
	for _, tag := range normTags {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO finding_tags (finding_id, tag) VALUES (?,?)`, id, tag); err != nil {
			return 0, err
		}
	}
	if err := appendFindingRevision(tx, id, "create", firstFindingChange(changes)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	f.ID = id
	f.Tags = normTags
	if f.Tags == nil {
		f.Tags = []string{}
	}
	return id, nil
}

// UpdateFinding applies non-nil fields and bumps updated_ts.
// body, when set, is stored as the new narrative body (already-serialized JSON).
// When detail is set but body is nil, the first text block in an existing body
// is updated (MCP backward-compat: AI updates detail → UI sees the change).
// When body is set, detail is synced from its first text block so MCP list_findings
// still shows meaningful text.
func (s *Store) UpdateFinding(id int64, severity, status, title, target, detail, evidence, fix, body, impact, why, cwe, environment, cvss, verificationInstructions *string) error {
	return s.updateFinding(id, severity, status, title, target, detail, evidence, fix, body, impact, why, cwe, environment, cvss, verificationInstructions, nil, nil, nil, nil)
}

// UpdateFindingWithTags applies field, body/flow, and optional tag changes in one
// transaction. A nil tags pointer preserves tags; a non-nil pointer replaces them.
func (s *Store) UpdateFindingWithTags(id int64, severity, status, title, target, detail, evidence, fix, body, impact, why, cwe, environment, cvss, verificationInstructions *string, tags *[]string) error {
	return s.updateFinding(id, severity, status, title, target, detail, evidence, fix, body, impact, why, cwe, environment, cvss, verificationInstructions, nil, nil, nil, tags)
}

// UpdateFindingCanonical applies legacy and evidence-first fields in one transaction.
func (s *Store) UpdateFindingCanonical(id int64, severity, status, title, target, detail, evidence, fix, body, impact, why, cwe, environment, cvss, verificationInstructions, summary, confidence, retest *string, tags *[]string, metadata ...FindingMetadataPatch) error {
	return s.updateFinding(id, severity, status, title, target, detail, evidence, fix, body, impact, why, cwe, environment, cvss, verificationInstructions, summary, confidence, retest, tags, metadata...)
}

// UpdateFindingEnvelope updates additive report fields without changing the
// long-standing UpdateFinding argument list.
func (s *Store) UpdateFindingEnvelope(id int64, summary, confidence, retest *string) error {
	return s.updateFinding(id, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, summary, confidence, retest, nil)
}

func (s *Store) updateFinding(id int64, severity, status, title, target, detail, evidence, fix, body, impact, why, cwe, environment, cvss, verificationInstructions, summary, confidence, retest *string, tags *[]string, metadata ...FindingMetadataPatch) error {
	if environment != nil {
		if err := validateFindingEnvironment(*environment); err != nil {
			return err
		}
	}
	if confidence != nil {
		if err := validateFindingConfidence(*confidence); err != nil {
			return err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := revisionBefore(tx, id); err != nil {
		return err
	}

	// Read the retained body before deriving any legacy partial-update body. A
	// detail/evidence-only update still writes the complete canonical body, so
	// it must be checked as an aggregate rather than only checking the changed
	// field at the HTTP layer.
	current, err := scanFindingNarrative(findingNarrativeRow(tx, id))
	if err != nil {
		return err
	}
	if err := markMissingFindingReferences(tx, &current); err != nil {
		return err
	}
	existingBody := current.Body

	// Keep legacy text fields synchronized with the canonical ordered body.
	if (detail != nil || evidence != nil) && body == nil {
		newBody := existingBody
		if detail != nil {
			newBody = updateFirstTextInBody(newBody, *detail)
		}
		if evidence != nil {
			newBody = updateLegacyEvidenceInBody(newBody, current.Evidence, *evidence)
		}
		if newBody != existingBody {
			body = &newBody
		}
	}
	// Normalize / coerce body before detail sync so type=md becomes text.
	if body != nil {
		norm, err := NormalizeFindingBody(*body)
		if err != nil {
			return err
		}
		*body = preserveMissingFlowMarkers(existingBody, norm)
	}
	resultingBody := existingBody
	if body != nil {
		resultingBody = *body
	}
	if body != nil && detail == nil && (current.Detail == "" || bodyHasText(existingBody, current.Detail)) {
		md := firstTextMD(*body)
		detail = &md
	}
	if body != nil && evidence == nil {
		if nextEvidence, matched := legacyEvidenceAfterBodyReplace(existingBody, *body, current.Evidence); matched {
			evidence = &nextEvidence
		}
	}
	resulting := current
	resulting.Body = resultingBody
	if title != nil {
		resulting.Title = *title
	}
	if summary != nil {
		resulting.Summary = *summary
	}
	if target != nil {
		resulting.Target = *target
	}
	if detail != nil {
		resulting.Detail = *detail
	}
	if evidence != nil {
		resulting.Evidence = *evidence
	}
	if fix != nil {
		resulting.Fix = *fix
	}
	if impact != nil {
		resulting.Impact = *impact
	}
	if why != nil {
		resulting.Why = *why
	}
	if cwe != nil {
		resulting.Cwe = *cwe
	}
	if cvss != nil {
		resulting.Cvss = *cvss
	}
	if verificationInstructions != nil {
		resulting.VerificationInstructions = *verificationInstructions
	}
	if retest != nil {
		resulting.Retest = *retest
	}

	if status != nil {
		resulting.Status = normalizeFindingStatus(*status)
	}
	if severity != nil {
		resulting.Severity = normalizeFindingSeverity(*severity)
	}
	var patch FindingMetadataPatch
	if len(metadata) > 0 {
		patch = metadata[0]
	}
	if patch.Targets != nil {
		resulting.Targets = *patch.Targets
		if len(resulting.Targets) == 0 {
			resulting.Target = ""
		}
		target = &resulting.Target
	}
	if target != nil && patch.Targets == nil && len(resulting.Targets) > 0 {
		resulting.Targets = append(FindingTargets(nil), resulting.Targets...)
		if strings.TrimSpace(*target) == "" {
			return fmt.Errorf("%w: remove targets through targets, rather than clearing only the primary target", ErrInvalidFinding)
		}
		resulting.Targets[0].URL = *target
		patch.Targets = &resulting.Targets
	}
	if patch.ProofReview != nil {
		resulting.ProofReview = *patch.ProofReview
	}
	if body != nil {
		stamped, err := stampFindingImageProvenance(tx, existingBody, *body, patch.Change)
		if err != nil {
			return err
		}
		*body = stamped
		resulting.Body = stamped
	}
	preserveAssessmentMissing(current, &resulting)
	if err := normalizeFindingAssessment(&resulting); err != nil {
		return err
	}
	if err := validateFindingReferences(tx, resulting); err != nil {
		return err
	}
	if status != nil || resulting.Status != current.Status {
		status = &resulting.Status
	}
	if err := validateFindingNarrativeSize(resulting); err != nil {
		return err
	}

	sets := []string{"updated_ts=?"}
	args := []any{time.Now().UnixMilli()}
	if patch.Targets != nil {
		sets = append(sets, "targets=?")
		args = append(args, resulting.Targets)
	}
	if patch.ProofReview != nil {
		sets = append(sets, "proof_review=?")
		args = append(args, resulting.ProofReview)
	}
	if severity != nil {
		sets = append(sets, "severity=?")
		args = append(args, normalizeFindingSeverity(*severity))
	}
	if status != nil {
		sets = append(sets, "status=?")
		args = append(args, normalizeFindingStatus(*status))
	}
	if title != nil {
		sets = append(sets, "title=?")
		args = append(args, *title)
	}
	if target != nil {
		sets = append(sets, "target=?")
		args = append(args, *target)
	}
	if detail != nil {
		sets = append(sets, "detail=?")
		args = append(args, *detail)
	}
	if evidence != nil {
		sets = append(sets, "evidence=?")
		args = append(args, *evidence)
	}
	if fix != nil {
		sets = append(sets, "fix=?")
		args = append(args, *fix)
	}
	if body != nil {
		sets = append(sets, "body=?")
		args = append(args, *body)
	}
	if impact != nil {
		sets = append(sets, "impact=?")
		args = append(args, *impact)
	}
	if why != nil {
		sets = append(sets, "why=?")
		args = append(args, *why)
	}
	if cwe != nil {
		sets = append(sets, "cwe=?")
		args = append(args, *cwe)
	}
	if environment != nil {
		sets = append(sets, "environment=?")
		args = append(args, normalizeFindingEnvironment(*environment))
	}
	if cvss != nil {
		sets = append(sets, "cvss=?")
		args = append(args, *cvss)
	}
	if verificationInstructions != nil {
		sets = append(sets, "verification_instructions=?")
		args = append(args, *verificationInstructions)
	}
	if summary != nil {
		sets = append(sets, "summary=?")
		args = append(args, *summary)
	}
	if confidence != nil {
		sets = append(sets, "confidence=?")
		args = append(args, normalizeFindingConfidence(*confidence))
	}
	if retest != nil {
		sets = append(sets, "retest=?")
		args = append(args, *retest)
	}
	args = append(args, id)

	res, err := tx.Exec(`UPDATE findings SET `+strings.Join(sets, ", ")+` WHERE id=?`, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	// Body rewrite must keep finding_flows in sync (UI enrichment joins that table).
	if body != nil {
		if err := syncFindingFlowsFromBody(tx, id, *body); err != nil {
			return err
		}
	}
	if tags != nil {
		if _, err := tx.Exec(`DELETE FROM finding_tags WHERE finding_id=?`, id); err != nil {
			return err
		}
		for _, tag := range NormalizeTags(*tags) {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO finding_tags (finding_id, tag) VALUES (?,?)`, id, tag); err != nil {
				return err
			}
		}
	}
	if err := appendFindingRevision(tx, id, "update", patch.Change); err != nil {
		return err
	}
	return tx.Commit()
}

// syncFindingFlowsFromBody replaces finding_flows rows for a finding from the
// ordered type=flow blocks in body JSON. Unknown flow ids are rejected.
func syncFindingFlowsFromBody(tx *sql.Tx, findingID int64, body string) error {
	// Existing attachments may reference flows intentionally purged by retention;
	// preserve those references while rejecting newly introduced unknown IDs.
	existing := make(map[int64]struct{})
	rows, err := tx.Query(`SELECT flow_id FROM finding_flows WHERE finding_id=?`, findingID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		existing[id] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM finding_flows WHERE finding_id=?`, findingID); err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		return nil
	}
	var recs []blockRecord
	if err := json.Unmarshal([]byte(body), &recs); err != nil {
		return fmt.Errorf("body must be a JSON array of blocks: %w", err)
	}
	ord := 0
	for _, r := range recs {
		if r.Type != "flow" {
			continue
		}
		if r.FlowID <= 0 {
			return fmt.Errorf("body flow block missing flowId")
		}
		if r.Missing {
			// Missing evidence is intentionally not an attachment: keeping the
			// marker in the body prevents a reused local id from reattaching it.
			continue
		}
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(1) FROM flows WHERE id=?`, r.FlowID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, ok := existing[r.FlowID]; !ok {
				return fmt.Errorf("%w: %d", ErrFlowNotFound, r.FlowID)
			}
		}
		if _, err := tx.Exec(
			`INSERT INTO finding_flows (finding_id, flow_id, ord, note) VALUES (?,?,?,?)`,
			findingID, r.FlowID, ord, r.Note,
		); err != nil {
			return err
		}
		ord++
	}
	return nil
}

// DeleteFinding removes a finding and its PoC attachments.
func (s *Store) DeleteFinding(id int64, changes ...FindingChange) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := revisionBefore(tx, id); err != nil {
		return err
	}
	if err := appendFindingRevision(tx, id, "delete", firstFindingChange(changes)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM finding_flows WHERE finding_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM finding_tags WHERE finding_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM finding_verification WHERE finding_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM findings WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// AttachFlow records a flow as a PoC for a finding and inserts (or updates) a
// flow block in the finding's narrative body. pos is the 0-based block index at
// which to insert the flow block; pass -1 to append at the end. Idempotent on
// re-attach — updates the note in both tables and in the body block (position
// unchanged if the block already exists).
//
// Returns ErrFlowNotFound when flowID has no row in flows — callers must not
// create orphan PoC attachments that later render as Missing.
func (s *Store) AttachFlow(findingID, flowID int64, note string, pos int, changes ...FindingChange) error {
	return s.AttachFlowWithMetadata(findingID, flowID, note, pos, "", "", "", 0, changes...)
}

// AttachFlowWithMetadata is the structured evidence variant of AttachFlow.
func (s *Store) AttachFlowWithMetadata(findingID, flowID int64, note string, pos int, role, proof, source string, sourceFlowID int64, changes ...FindingChange) error {
	if err := validateFindingEvidenceMetadata(role, source, sourceFlowID); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := revisionBefore(tx, findingID); err != nil {
		return err
	}

	narrative, err := scanFindingNarrative(findingNarrativeRow(tx, findingID))
	if err != nil {
		return err
	}
	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM flows WHERE id=?`, flowID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %d", ErrFlowNotFound, flowID)
		}
		return err
	}

	var nextOrd int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(ord)+1, 0) FROM finding_flows WHERE finding_id=?`, findingID).Scan(&nextOrd); err != nil {
		return err
	}
	// Sync flow block into the body at the requested position. Build and validate
	// the complete body before inserting the attachment row so a cap failure is
	// atomic and cannot leave an orphan finding_flows record.
	newBody := insertFlowIntoBodyWithMetadata(narrative.Body, flowID, note, pos, role, proof, source, sourceFlowID)
	narrative.Body = newBody
	if detailSync := firstTextMD(newBody); detailSync != "" {
		narrative.Detail = detailSync
	}
	if err := validateFindingNarrativeSize(narrative); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO finding_flows (finding_id, flow_id, ord, note) VALUES (?,?,?,?)
			 ON CONFLICT(finding_id, flow_id) DO UPDATE SET note=excluded.note`,
		findingID, flowID, nextOrd, note); err != nil {
		return err
	}
	// Also update detail from first text block if needed.
	detailSync := firstTextMD(newBody)
	if _, err := tx.Exec(
		`UPDATE findings SET body=?, detail=CASE WHEN ?<>'' THEN ? ELSE detail END, updated_ts=? WHERE id=?`,
		newBody, detailSync, detailSync, time.Now().UnixMilli(), findingID); err != nil {
		return err
	}
	if err := appendFindingRevision(tx, findingID, "update", firstFindingChange(changes)); err != nil {
		return err
	}
	return tx.Commit()
}

// DetachFlow removes a PoC flow from a finding's flow table and body.
func (s *Store) DetachFlow(findingID, flowID int64, changes ...FindingChange) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := revisionBefore(tx, findingID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM finding_flows WHERE finding_id=? AND flow_id=?`, findingID, flowID); err != nil {
		return err
	}
	var bodyJSON string
	if err := tx.QueryRow(`SELECT body FROM findings WHERE id=?`, findingID).Scan(&bodyJSON); err != nil {
		return err
	}
	newBody := removeFlowFromBody(bodyJSON, flowID)
	if _, err := tx.Exec(`UPDATE findings SET body=?, updated_ts=? WHERE id=?`, newBody, time.Now().UnixMilli(), findingID); err != nil {
		return err
	}
	if err := appendFindingRevision(tx, findingID, "update", firstFindingChange(changes)); err != nil {
		return err
	}
	return tx.Commit()
}

// findingFlows loads the PoC flows for a finding (for the sidebar count and block enrichment).
func (s *Store) findingFlows(findingID int64) ([]FindingFlow, error) {
	rows, err := s.db.Query(
		`SELECT ff.flow_id, ff.ord, ff.note, f.method, f.host, f.path, f.status, f.id IS NOT NULL, COALESCE(f.req_body_hash,''), COALESCE(f.res_body_hash,''), COALESCE(f.original_req_body_hash,''), COALESCE(f.original_res_body_hash,''), COALESCE(f.req_len,0), COALESCE(f.res_len,0)
		 FROM finding_flows ff LEFT JOIN flows f ON f.id = ff.flow_id
		 WHERE ff.finding_id=? ORDER BY ff.ord, ff.flow_id`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FindingFlow
	for rows.Next() {
		var ff FindingFlow
		var method, host, path *string
		var status *int
		var present bool
		var reqHash, resHash, originalReqHash, originalResHash string
		var reqLen, resLen int64
		if err := rows.Scan(&ff.FlowID, &ff.Ord, &ff.Note, &method, &host, &path, &status, &present, &reqHash, &resHash, &originalReqHash, &originalResHash, &reqLen, &resLen); err != nil {
			return nil, err
		}
		if method != nil {
			ff.Method = *method
		}
		if host != nil {
			ff.Host = *host
		}
		if path != nil {
			ff.Path = *path
		}
		if status != nil {
			ff.Status = *status
		}
		// A LEFT JOIN miss (flow purged via prune_history / GC) yields a NULL flow
		// id; the attachment row and its note survive but the evidence is gone.
		ff.Missing = !present
		ff.RawMissing = present && ((reqLen > 0 && reqHash == "") || (resLen > 0 && resHash == ""))
		for _, hash := range []string{reqHash, resHash, originalReqHash, originalResHash} {
			if hash != "" && !s.BodyExists(hash) {
				ff.RawMissing = true
			}
		}
		out = append(out, ff)
	}
	return out, rows.Err()
}

func scanFinding(sc scanner) (*Finding, error) {
	var f Finding
	if err := sc.Scan(&f.ID, &f.TS, &f.UpdatedTS, &f.Severity, &f.Status, &f.Source,
		&f.Title, &f.Summary, &f.Target, &f.Confidence, &f.Detail, &f.Evidence, &f.Fix, &f.Body, &f.Impact, &f.Why, &f.Cwe, &f.Environment, &f.Cvss,
		&f.VerificationInstructions, &f.Retest, &f.Targets, &f.ProofReview); err != nil {
		return nil, err
	}
	return &f, nil
}

const findingCols = `id, ts, updated_ts, severity, status, source, title, summary, target, confidence, detail, evidence, fix, body, impact, why, cwe, environment, cvss, verification_instructions, retest, targets, proof_review`

// GetFinding loads one finding with its narrative body blocks and PoC flow list.
func (s *Store) GetFinding(id int64) (*Finding, error) {
	f, err := scanFinding(s.db.QueryRow(`SELECT `+findingCols+` FROM findings WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	if f.Flows, err = s.findingFlows(id); err != nil {
		return nil, err
	}
	if f.Flows == nil {
		f.Flows = []FindingFlow{}
	}
	f.Blocks = buildBlocks(f.Body, f.Detail, f.Evidence, f.Flows)
	if f.Blocks == nil {
		f.Blocks = []FindingBlock{}
	}
	s.enrichImageBlocks(f.Blocks)
	if tags, err := s.FindingTags(id); err != nil {
		return nil, err
	} else {
		f.Tags = tags
	}
	if f.Tags == nil {
		f.Tags = []string{}
	}
	if v, err := s.GetFindingVerification(id); err == nil {
		f.Verification = v
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err := markMissingFindingReferences(s.db, f); err != nil {
		return nil, err
	}
	projectMissingFindingFlows(f)
	reconcileFindingImageReferences(f)
	f.EnrichCompleteness()
	return f, nil
}

// ListFindings returns findings ordered by severity (High→Info) then newest, each
// with its PoC flows and narrative blocks. Empty severity/status/tag means "any".
// When tag is set, only findings carrying that normalized tag are returned.
func (s *Store) ListFindings(severity, status, tag string) ([]Finding, error) {
	where := []string{"1=1"}
	args := []any{}
	if severity != "" {
		where = append(where, "severity=?")
		args = append(args, normalizeFindingSeverity(severity))
	}
	if status != "" {
		where = append(where, "status=?")
		args = append(args, normalizeFindingStatus(status))
	}
	if t := normalizeTag(tag); t != "" {
		where = append(where, "EXISTS (SELECT 1 FROM finding_tags ft WHERE ft.finding_id = findings.id AND ft.tag = ?)")
		args = append(args, t)
	}
	rows, err := s.db.Query(
		`SELECT `+findingCols+` FROM findings WHERE `+strings.Join(where, " AND ")+
			` ORDER BY CASE severity WHEN 'Critical' THEN 0 WHEN 'High' THEN 1 WHEN 'Medium' THEN 2 WHEN 'Low' THEN 3 ELSE 4 END, id DESC`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Finding
	for rows.Next() {
		f, err := scanFinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > 0 {
		ids := make([]int64, len(out))
		for i := range out {
			ids[i] = out[i].ID
		}
		flowsByID, err := s.findingFlowsForIDs(ids)
		if err != nil {
			return nil, err
		}
		tagsByID, err := s.TagsForFindings(ids)
		if err != nil {
			return nil, err
		}
		verByID, err := s.VerificationsForFindings(ids)
		if err != nil {
			return nil, err
		}
		for i := range out {
			out[i].Flows = flowsByID[out[i].ID]
			if out[i].Flows == nil {
				out[i].Flows = []FindingFlow{}
			}
			out[i].Tags = tagsByID[out[i].ID]
			if out[i].Tags == nil {
				out[i].Tags = []string{}
			}
			if v := verByID[out[i].ID]; v != nil {
				out[i].Verification = v
			}
			out[i].Blocks = buildBlocks(out[i].Body, out[i].Detail, out[i].Evidence, out[i].Flows)
			if out[i].Blocks == nil {
				out[i].Blocks = []FindingBlock{}
			}
			s.enrichImageBlocks(out[i].Blocks)
			if err := markMissingFindingReferences(s.db, &out[i]); err != nil {
				return nil, err
			}
			projectMissingFindingFlows(&out[i])
			reconcileFindingImageReferences(&out[i])
			out[i].EnrichCompleteness()
		}
	}
	return out, nil
}

// findingFlowsForIDs batch-loads PoC flows for many findings in one query.
func (s *Store) findingFlowsForIDs(findingIDs []int64) (map[int64][]FindingFlow, error) {
	out := make(map[int64][]FindingFlow, len(findingIDs))
	if len(findingIDs) == 0 {
		return out, nil
	}
	placeholders := strings.Repeat("?,", len(findingIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(findingIDs))
	for i, id := range findingIDs {
		args[i] = id
	}
	rows, err := s.db.Query(
		`SELECT ff.finding_id, ff.flow_id, ff.ord, ff.note, f.method, f.host, f.path, f.status, f.id IS NOT NULL, COALESCE(f.req_body_hash,''), COALESCE(f.res_body_hash,''), COALESCE(f.original_req_body_hash,''), COALESCE(f.original_res_body_hash,''), COALESCE(f.req_len,0), COALESCE(f.res_len,0)
		 FROM finding_flows ff LEFT JOIN flows f ON f.id = ff.flow_id
		 WHERE ff.finding_id IN (`+placeholders+`) ORDER BY ff.finding_id, ff.ord, ff.flow_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var findingID int64
		var ff FindingFlow
		var method, host, path *string
		var status *int
		var present bool
		var reqHash, resHash, originalReqHash, originalResHash string
		var reqLen, resLen int64
		if err := rows.Scan(&findingID, &ff.FlowID, &ff.Ord, &ff.Note, &method, &host, &path, &status, &present, &reqHash, &resHash, &originalReqHash, &originalResHash, &reqLen, &resLen); err != nil {
			return nil, err
		}
		if method != nil {
			ff.Method = *method
		}
		if host != nil {
			ff.Host = *host
		}
		if path != nil {
			ff.Path = *path
		}
		if status != nil {
			ff.Status = *status
		}
		ff.Missing = !present
		ff.RawMissing = present && ((reqLen > 0 && reqHash == "") || (resLen > 0 && resHash == ""))
		for _, hash := range []string{reqHash, resHash, originalReqHash, originalResHash} {
			if hash != "" && !s.BodyExists(hash) {
				ff.RawMissing = true
			}
		}
		out[findingID] = append(out[findingID], ff)
	}
	return out, rows.Err()
}

// Keep the legacy flow projection complete without recreating attachment rows
// that might bind a missing peer ID to unrelated local traffic.
func projectMissingFindingFlows(f *Finding) {
	byID := map[int64]int{}
	for i, flow := range f.Flows {
		byID[flow.FlowID] = i
	}
	ordinal := 0
	for _, block := range f.Blocks {
		if block.Type != "flow" {
			continue
		}
		if block.Missing {
			missing := FindingFlow{FlowID: block.FlowID, Ord: ordinal, Note: block.Note, Missing: true}
			if i, ok := byID[block.FlowID]; ok {
				f.Flows[i] = missing
			} else {
				byID[block.FlowID] = len(f.Flows)
				f.Flows = append(f.Flows, missing)
			}
		}
		if i, ok := byID[block.FlowID]; ok {
			f.Flows[i].Ord = ordinal
		}
		ordinal++
	}
	slices.SortStableFunc(f.Flows, func(a, b FindingFlow) int { return a.Ord - b.Ord })
}
