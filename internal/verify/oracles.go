package verify

import (
	"bytes"
	"regexp"
)

// VulnClass names the differential oracle to apply. Kept as string constants so
// callers (and the Phase-2 verifier) can pass a plain candidate class through.
type VulnClass string

const (
	ClassReflected VulnClass = "reflected-marker" // marker echoed in the payload response
	ClassError     VulnClass = "error-signature"  // interpreter/DB error surfaced by the payload
	ClassBoolean   VulnClass = "boolean-length"   // true≈baseline, false diverges (two-payload)
	ClassTiming    VulnClass = "timing"           // payload slow, control + baseline fast
)

// timingSlowMs is how slow (in ms) a timing payload's response must be to count
// as a delay; timingFastMs is the ceiling below which the baseline and zero-delay
// control must fall. Mirrors the active-scan timing check (payload ≥5s, control
// <3s), which uses a distinct injected sleep well above natural jitter.
const (
	timingSlowMs = 5000
	timingFastMs = 3000
)

// dbErrRe matches common DB/interpreter error signatures across engines. Kept in
// sync with the active-scan sqli error check so the verifier and the scanner
// agree on what counts as an error signature.
var dbErrRe = regexp.MustCompile(`(?i)(SQL syntax|mysql_fetch|valid MySQL result|ORA-\d{5}|PostgreSQL.{0,40}ERROR|SQLite[/.].{0,20}error|Unclosed quotation mark|quoted string not properly terminated|SQLSTATE\[|near ".{0,30}": syntax error|System\.Data\.SqlClient|Warning.{0,20}(mysqli|pg_|mysql_)|Microsoft OLE DB Provider|ODBC.{0,20}Driver|Fatal error|Traceback \(most recent call last\)|Unclosed.{0,20}mark)`)

// ReflectedMarkerHeld reports whether a unique marker is present in the payload
// response but absent from the baseline — the reflected-XSS/injection oracle. The
// marker must be non-empty; an empty marker never "reflects" (guards against a
// vacuously-true match). Pure and directly testable.
func ReflectedMarkerHeld(baseline, payload Exchange, marker string) bool {
	if marker == "" {
		return false
	}
	m := []byte(marker)
	return bytes.Contains(payload.Body, m) && !bytes.Contains(baseline.Body, m)
}

// ErrorSignatureHeld reports whether a DB/interpreter error signature appears in
// the payload response but not in the baseline. Requiring absence in the baseline
// avoids flagging an endpoint that always echoes such a string. Pure.
func ErrorSignatureHeld(baseline, payload Exchange) bool {
	if !payload.ok() {
		return false
	}
	return dbErrRe.Match(payload.Body) && !dbErrRe.Match(baseline.Body)
}

// BooleanLengthHeld models the two-payload boolean-SQLi oracle: a TRUE condition
// response should match the baseline length while a FALSE condition diverges
// clearly from the TRUE response. Guards:
//
//   - baseline must be non-trivial (>= booleanBaselineFloor bytes) — on tiny
//     bodies natural variation reads as a large relative divergence (false
//     positives), the same floor the active-scan boolean check applies;
//   - all three exchanges must have completed;
//   - true ≈ baseline within a small tolerance, AND false differs from true
//     beyond a larger floor.
//
// Pure; the three Exchange bodies are supplied by the caller.
func BooleanLengthHeld(baseline, payloadTrue, payloadFalse Exchange) bool {
	if !baseline.ok() || !payloadTrue.ok() || !payloadFalse.ok() {
		return false
	}
	lb := len(baseline.Body)
	lt := len(payloadTrue.Body)
	lf := len(payloadFalse.Body)
	if lb < booleanBaselineFloor {
		return false
	}
	if lt == 0 {
		return false
	}
	// true ≈ baseline
	if absdiff(lt, lb) > lb/20+8 {
		return false
	}
	// false clearly diverges from true
	return absdiff(lf, lt) >= lt/10+24
}

const booleanBaselineFloor = 64

// TimingHeld reports whether the timing oracle holds: the payload response is at
// least timingSlowMs, while both a zero-delay control and the baseline are fast
// (below timingFastMs) and actually completed. A blocked/errored control returns
// DurMs 0 and would falsely pass a naive "<threshold" test, so the control and
// baseline must both be ok(). Pure.
func TimingHeld(baseline, payload, control Exchange) bool {
	if !baseline.ok() || !payload.ok() || !control.ok() {
		return false
	}
	return payload.DurMs >= timingSlowMs &&
		control.DurMs < timingFastMs &&
		baseline.DurMs < timingFastMs
}

func absdiff(a, b int) int {
	if a < b {
		return b - a
	}
	return a - b
}
