package store

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

type FindingTargetSuggestion struct {
	Index    int    `json:"index"`
	Original string `json:"original"`
	Template string `json:"template"`
}
type FindingTargetPreview struct {
	Targets     FindingTargets            `json:"targets"`
	Suggestions []FindingTargetSuggestion `json:"suggestions"`
	Removed     int                       `json:"removed"`
}

var targetIDSegment = regexp.MustCompile(`^(?:[0-9]+|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`)
var legacyTargetSeparator = regexp.MustCompile(`;\s*(https?://)`)

// PreviewFindingTargets never mutates its input or applies inferred templates.
// Exact duplicates combine evidence, retaining method/role/variant distinctions.
func PreviewFindingTargets(targets FindingTargets, legacy string) (FindingTargetPreview, error) {
	var p FindingTargetPreview
	raw, _ := json.Marshal(targets)
	_ = json.Unmarshal(raw, &p.Targets)
	if len(p.Targets) == 0 && strings.TrimSpace(legacy) != "" {
		for _, part := range strings.Split(legacyTargetSeparator.ReplaceAllString(legacy, "\n$1"), "\n") {
			if strings.TrimSpace(part) != "" {
				p.Targets = append(p.Targets, FindingTarget{URL: strings.TrimSpace(part)})
			}
		}
	}
	var expanded FindingTargets
	for _, target := range p.Targets {
		parts := strings.Split(legacyTargetSeparator.ReplaceAllString(target.URL, "\n$1"), "\n")
		for _, part := range parts {
			if strings.TrimSpace(part) != "" {
				copyTarget := target
				copyTarget.URL = strings.TrimSpace(part)
				expanded = append(expanded, copyTarget)
			}
		}
	}
	p.Targets = expanded
	before := len(p.Targets)
	f := Finding{Targets: p.Targets}
	if err := normalizeFindingAssessment(&f); err != nil {
		return p, err
	}
	p.Targets = f.Targets
	output := FindingTargets{}
	indices := map[string]int{}
	for _, t := range p.Targets {
		if u, err := url.Parse(t.URL); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			u.Scheme = strings.ToLower(u.Scheme)
			u.Host = strings.ToLower(u.Host)
			// Canonicalize the authority only. Preserve the raw path spelling,
			// including reviewer-approved {id} templates and escaped segments.
			separator := strings.Index(t.URL, "://")
			authorityStart := separator + 3
			authorityEnd := len(t.URL)
			if offset := strings.IndexAny(t.URL[authorityStart:], "/?#"); offset >= 0 {
				authorityEnd = authorityStart + offset
			}
			authority := t.URL[authorityStart:authorityEnd]
			userEnd := strings.LastIndex(authority, "@") + 1
			t.URL = strings.ToLower(t.URL[:separator]) + "://" + authority[:userEnd] + strings.ToLower(authority[userEnd:]) + t.URL[authorityEnd:]
		}
		methods := slices.Clone(t.Methods)
		slices.Sort(methods)
		key, _ := json.Marshal([]any{t.URL, methods, t.Role, t.Relation, t.Variant, t.EvidenceException})
		if i, ok := indices[string(key)]; ok {
			d := &output[i]
			for _, id := range t.FlowIDs {
				if !slices.Contains(d.FlowIDs, id) {
					d.FlowIDs = append(d.FlowIDs, id)
				}
			}
			for _, id := range t.MissingFlowIDs {
				if !slices.Contains(d.MissingFlowIDs, id) {
					d.MissingFlowIDs = append(d.MissingFlowIDs, id)
				}
			}
			for _, hash := range t.ImageHashes {
				if !slices.Contains(d.ImageHashes, hash) {
					d.ImageHashes = append(d.ImageHashes, hash)
				}
			}
			if t.Note != "" && t.Note != d.Note {
				if d.Note != "" {
					d.Note += "\n\n"
				}
				d.Note += t.Note
			}
		} else {
			indices[string(key)] = len(output)
			output = append(output, t)
		}
	}
	f.Targets = output
	if err := normalizeFindingAssessment(&f); err != nil {
		return p, err
	}
	p.Targets = f.Targets
	p.Removed = before - len(output)
	p.Suggestions = []FindingTargetSuggestion{}
	for i, t := range p.Targets {
		u, err := url.Parse(t.URL)
		if err != nil || u.Host == "" {
			continue
		}
		segments := strings.Split(u.EscapedPath(), "/")
		changed := false
		for j, segment := range segments {
			if targetIDSegment.MatchString(segment) {
				segments[j] = "{id}"
				changed = true
			}
		}
		if changed {
			template := u.Scheme + "://" + u.Host + strings.Join(segments, "/")
			if u.RawQuery != "" {
				template += "?" + u.RawQuery
			}
			if u.Fragment != "" {
				template += "#" + u.EscapedFragment()
			}
			p.Suggestions = append(p.Suggestions, FindingTargetSuggestion{i, t.URL, template})
		}
	}
	if len(p.Targets) > 64 {
		return p, fmt.Errorf("%w: too many targets", ErrInvalidFinding)
	}
	return p, nil
}
