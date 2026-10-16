package store

import "fmt"

// NormalizeFindingTargets applies reviewer-approved path templates (by
// suggestion Index from PreviewFindingTargets) and re-runs the exact-key merge.
// Targets that differ in scheme, method, role, relation, variant or evidence
// exception never merge; flow and image references follow their target.
func NormalizeFindingTargets(targets FindingTargets, legacy string, approve []int) (FindingTargetPreview, error) {
	p, err := PreviewFindingTargets(targets, legacy)
	if err != nil {
		return p, err
	}
	templates := map[int]string{}
	for _, s := range p.Suggestions {
		templates[s.Index] = s.Template
	}
	for _, i := range approve {
		if _, ok := templates[i]; !ok {
			return p, fmt.Errorf("%w: no path template suggested for target %d", ErrInvalidFinding, i)
		}
	}
	for _, i := range approve {
		p.Targets[i].URL = templates[i]
	}
	return PreviewFindingTargets(p.Targets, "")
}
