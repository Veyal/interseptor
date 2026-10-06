package store

import (
	"errors"
	"fmt"
)

const (
	engagementBriefKey = "engagement.brief"
	// MaxEngagementFieldBytes bounds each free-text engagement metadata field.
	MaxEngagementFieldBytes = 16 << 10
)

// ErrInvalidEngagement marks a rejected brief or interception-setup payload.
var ErrInvalidEngagement = errors.New("invalid engagement metadata")

// EngagementBrief is the project-level authorisation and conduct statement
// agents read instead of being re-told on every run. Version increments only
// when the content changes, so reports can cite the authorisation they used.
type EngagementBrief struct {
	Version          int    `json:"version"`
	UpdatedAt        int64  `json:"updatedAt,omitempty"`
	Scope            string `json:"scope"`
	Authorisation    string `json:"authorisation"`
	ConductRules     string `json:"conductRules"`
	RateLimits       string `json:"rateLimits"`
	DoNotTouch       string `json:"doNotTouch"`
	CredentialPolicy string `json:"credentialPolicy"`
}

func (b *EngagementBrief) versionMeta() (*int, *int64) { return &b.Version, &b.UpdatedAt }

// IsEmpty reports whether the brief has no content.
func (b EngagementBrief) IsEmpty() bool {
	return b.Scope == "" && b.Authorisation == "" && b.ConductRules == "" &&
		b.RateLimits == "" && b.DoNotTouch == "" && b.CredentialPolicy == ""
}

func (b EngagementBrief) validate() error {
	fields := []struct{ name, v string }{
		{"scope", b.Scope}, {"authorisation", b.Authorisation}, {"conductRules", b.ConductRules},
		{"rateLimits", b.RateLimits}, {"doNotTouch", b.DoNotTouch}, {"credentialPolicy", b.CredentialPolicy},
	}
	for _, f := range fields {
		if len(f.v) > MaxEngagementFieldBytes {
			return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidEngagement, f.name, MaxEngagementFieldBytes)
		}
	}
	return nil
}

// GetEngagementBrief returns the stored brief (zero value, version 0, if unset).
func (s *Store) GetEngagementBrief() (EngagementBrief, error) {
	var b EngagementBrief
	_, err := loadVersionedSetting(s.db, engagementBriefKey, &b)
	return b, err
}

// SetEngagementBrief saves the brief, bumping the version only when content
// differs from what is stored. Version and UpdatedAt in the input are ignored.
func (s *Store) SetEngagementBrief(in EngagementBrief) (EngagementBrief, error) {
	if err := in.validate(); err != nil {
		return EngagementBrief{}, err
	}
	var out EngagementBrief
	err := s.saveVersionedSetting(engagementBriefKey, &out, func() {
		out.Scope, out.Authorisation, out.ConductRules = in.Scope, in.Authorisation, in.ConductRules
		out.RateLimits, out.DoNotTouch, out.CredentialPolicy = in.RateLimits, in.DoNotTouch, in.CredentialPolicy
	})
	return out, err
}
