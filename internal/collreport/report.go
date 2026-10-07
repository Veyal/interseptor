// Package collreport renders a collrun.Report as CLI text, JSON, JUnit XML or
// a self-contained HTML page. All formats come from the one Report model and
// all go through the same scrub pass first: every string is scrubbed (known
// secret values, credential-shaped text) before a single byte is written, so
// no format can leak what another masks.
package collreport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Veyal/interseptor/internal/collrun"
	"github.com/Veyal/interseptor/internal/redact"
)

// Formats the package can render.
const (
	FormatCLI   = "cli"
	FormatJSON  = "json"
	FormatJUnit = "junit"
	FormatHTML  = "html"
)

// SchemaVersion identifies the JSON report layout.
const SchemaVersion = "interseptor.run.v1"

// Options control rendering.
type Options struct {
	// Scrub masks one string. Nil uses redact.Text (credential-shaped text).
	// Callers with a secret registry pass its Mask composed with redact.Text.
	Scrub func(string) string
	// Hostname goes into JUnit testsuite attributes (default "interseptor").
	Hostname string
}

func (o Options) scrub() func(string) string {
	if o.Scrub != nil {
		return o.Scrub
	}
	return redact.Text
}

// Formats lists the names Write accepts.
func Formats() []string { return []string{FormatCLI, FormatJSON, FormatJUnit, FormatHTML} }

// Write renders rep in the named format to w.
func Write(w io.Writer, format string, rep *collrun.Report, o Options) error {
	safe, err := Scrub(rep, o.scrub())
	if err != nil {
		return err
	}
	switch strings.ToLower(format) {
	case FormatCLI, "text":
		return writeText(w, safe)
	case FormatJSON:
		return writeJSON(w, safe)
	case FormatJUnit, "xml":
		return writeJUnit(w, safe, o)
	case FormatHTML:
		return writeHTML(w, safe)
	}
	return fmt.Errorf("collreport: unknown report format %q (want cli, json, junit or html)", format)
}

// Render is Write into a byte slice.
func Render(format string, rep *collrun.Report, o Options) ([]byte, error) {
	var b bytes.Buffer
	if err := Write(&b, format, rep, o); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Scrub returns a deep copy of rep with every string value passed through
// scrub. It works on the JSON form so a field added to the model later is
// covered without touching this code.
func Scrub(rep *collrun.Report, scrub func(string) string) (*collrun.Report, error) {
	raw, err := json.Marshal(rep)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	generic = scrubValue(generic, scrub)
	out, err := json.Marshal(generic)
	if err != nil {
		return nil, err
	}
	var clean collrun.Report
	if err := json.Unmarshal(out, &clean); err != nil {
		return nil, err
	}
	if clean.Items == nil {
		clean.Items = []collrun.ItemResult{}
	}
	return &clean, nil
}

func scrubValue(v any, scrub func(string) string) any {
	switch t := v.(type) {
	case string:
		return scrub(t)
	case []any:
		for i := range t {
			t[i] = scrubValue(t[i], scrub)
		}
		return t
	case map[string]any:
		for k := range t {
			t[k] = scrubValue(t[k], scrub)
		}
		return t
	}
	return v
}

// envelope is the JSON report document.
type envelope struct {
	Schema   string          `json:"schema"`
	ExitCode int             `json:"exitCode"`
	Report   *collrun.Report `json:"report"`
}

func writeJSON(w io.Writer, rep *collrun.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(envelope{Schema: SchemaVersion, ExitCode: rep.ExitCode(), Report: rep})
}
