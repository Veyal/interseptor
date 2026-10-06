package report

import (
	"fmt"
	"strings"

	"github.com/Veyal/interseptor/internal/redact"
	"github.com/Veyal/interseptor/internal/store"
)

// secretLintNotice renders an HTML notice listing probable secrets written into
// finding text. It carries finding id, field, kind, length and the redacted
// replacement only, never the value, and escapes every interpolated string.
// Raw evidence (flow messages) is not scanned: it is evidence by design.
func secretLintNotice(findings []store.Finding) string {
	var items []string
	for _, f := range findings {
		for _, field := range findingTextFields(f) {
			for _, hit := range redact.Scan(field.text) {
				items = append(items, fmt.Sprintf("<li>#%d %s &middot; %s &middot; %s (%d chars) &rarr; <code>%s</code></li>",
					f.ID, htmlEsc(f.Title), htmlEsc(field.name), htmlEsc(hit.Kind), hit.Len, htmlEsc(hit.Suggest)))
			}
		}
	}
	if len(items) == 0 {
		return ""
	}
	return `<aside class="secret-lint" role="note"><strong>Secret lint: probable secrets in finding text.</strong> ` +
		`Replace each with the redacted form (MCP <code>redact_value</code>) before sharing this report.<ul>` +
		strings.Join(items, "") + `</ul></aside>` + "\n"
}

type textField struct{ name, text string }

func findingTextFields(f store.Finding) []textField {
	fields := []textField{
		{"summary", f.Summary}, {"impact", f.Impact}, {"why", f.Why}, {"fix", f.Fix}, {"retest", f.Retest},
		{"detail", f.Detail}, {"evidence", f.Evidence}, {"verificationInstructions", f.VerificationInstructions},
	}
	var blocks []string
	for _, b := range f.Blocks {
		blocks = append(blocks, b.MD, b.Note, b.Caption, b.Proof)
	}
	return append(fields, textField{"blocks", strings.Join(blocks, "\n")})
}
