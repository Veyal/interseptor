package starx

import (
	"regexp"
	"strings"
)

// Metadata is the structured front-matter parsed from a check's leading `# key:
// value` comment block. It carries provenance (author, version, homepage) so a
// rule-pack installer can pin versions and show what a check does without reading
// its source. Unknown keys are kept in Extra for forward compatibility.
type Metadata struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Severity    string `json:"severity,omitempty"`
	Author      string `json:"author,omitempty"`
	Version     string `json:"version,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	License     string `json:"license,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

var metaLine = regexp.MustCompile(`^#\s*([A-Za-z][A-Za-z0-9 _-]*?)\s*:\s*(.*)$`)

// ParseMetadata reads a leading run of `#`-comment lines from src and collects
// any `# key: value` pairs. Parsing stops at the first non-comment line, so a
// check's in-code comments after the front-matter are left alone.
func ParseMetadata(src string) Metadata {
	var m Metadata
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimRight(line, "\r")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(trimmed), "#") {
			break
		}
		matches := metaLine.FindStringSubmatch(trimmed)
		if matches == nil {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(matches[1]))
		val := strings.TrimSpace(matches[2])
		assignMeta(&m, key, val)
	}
	return m
}

func assignMeta(m *Metadata, key, val string) {
	switch key {
	case "name":
		m.Name = val
	case "description":
		m.Description = val
	case "severity":
		m.Severity = val
	case "author":
		m.Author = val
	case "version":
		m.Version = val
	case "homepage":
		m.Homepage = val
	case "license":
		m.License = val
	default:
		if m.Extra == nil {
			m.Extra = map[string]string{}
		}
		m.Extra[key] = val
	}
}
