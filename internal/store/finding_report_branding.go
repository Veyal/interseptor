package store

import (
	"fmt"
	"strings"
	"time"
)

// White-label report metadata: the assessing firm, the client, the assessment
// window, who led it, and an optional operator-authored cover page. It is a
// versioned document in the settings table, like the engagement brief, so an
// exported report can cite the settings it was produced under and no schema
// migration is needed.

const reportBrandingKey = "report.branding"

const (
	// MaxReportBrandingFieldBytes bounds each single-line branding field.
	MaxReportBrandingFieldBytes = 1 << 10
	// MaxReportCoverPageBytes bounds the operator-authored cover page markdown.
	MaxReportCoverPageBytes = 32 << 10
	// MaxReportLogoBytes bounds one embedded logo. A logo is inlined into every
	// HTML and PDF export, so it has to stay small enough to carry.
	MaxReportLogoBytes = 512 << 10
)

// ReportBranding is the project's report identity. Every field is optional; the
// zero value renders exactly the report Interseptor produced before this
// existed.
type ReportBranding struct {
	Version   int   `json:"version"`
	UpdatedAt int64 `json:"updatedAt,omitempty"`

	// ReportTitle replaces the default "Interseptor — Engagement Report".
	ReportTitle string `json:"reportTitle,omitempty"`
	// CompanyName is the assessing firm; ClientName is who the work was for.
	CompanyName string `json:"companyName,omitempty"`
	ClientName  string `json:"clientName,omitempty"`
	// CompanyLogo and ClientLogo are data: image URIs, embedded in the HTML and
	// PDF renders. A remote URL is refused: a delivered report must not fetch
	// anything, and an image that loads from the network is also a read receipt.
	CompanyLogo string `json:"companyLogo,omitempty"`
	ClientLogo  string `json:"clientLogo,omitempty"`
	// EngagementName names this piece of work, e.g. "Q1 external assessment".
	EngagementName string `json:"engagementName,omitempty"`
	// AssessmentStart and AssessmentEnd are ISO YYYY-MM-DD dates. They are
	// stored and rendered in that form rather than reformatted, because a date
	// in a client deliverable must not be ambiguous.
	AssessmentStart string `json:"assessmentStart,omitempty"`
	AssessmentEnd   string `json:"assessmentEnd,omitempty"`
	// LeadAssessor and Contact identify who stands behind the report.
	LeadAssessor string `json:"leadAssessor,omitempty"`
	Contact      string `json:"contact,omitempty"`
	// Classification is a handling label, e.g. "Confidential".
	Classification string `json:"classification,omitempty"`
	// CoverPage is operator-authored Markdown that replaces the generated cover
	// table. It is rendered as Markdown, so it can carry whatever structure the
	// deliverable needs.
	CoverPage string `json:"coverPage,omitempty"`
}

func (b *ReportBranding) versionMeta() (*int, *int64) { return &b.Version, &b.UpdatedAt }

// IsEmpty reports whether the branding has nothing to render.
func (b ReportBranding) IsEmpty() bool {
	return b.ReportTitle == "" && b.CompanyName == "" && b.ClientName == "" &&
		b.CompanyLogo == "" && b.ClientLogo == "" && b.EngagementName == "" &&
		b.AssessmentStart == "" && b.AssessmentEnd == "" && b.LeadAssessor == "" &&
		b.Contact == "" && b.Classification == "" && b.CoverPage == ""
}

// reportLogoMIMEs are the image types a logo may use. SVG is excluded on
// purpose: it is a document format that can carry script, and nothing about a
// logo needs it.
var reportLogoMIMEs = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

func (b ReportBranding) validate() error {
	lines := []struct{ name, v string }{
		{"reportTitle", b.ReportTitle}, {"companyName", b.CompanyName}, {"clientName", b.ClientName},
		{"engagementName", b.EngagementName}, {"leadAssessor", b.LeadAssessor},
		{"contact", b.Contact}, {"classification", b.Classification},
	}
	for _, f := range lines {
		if len(f.v) > MaxReportBrandingFieldBytes {
			return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidEngagement, f.name, MaxReportBrandingFieldBytes)
		}
		if strings.ContainsAny(f.v, "\r\n") {
			return fmt.Errorf("%w: %s must be a single line", ErrInvalidEngagement, f.name)
		}
	}
	if len(b.CoverPage) > MaxReportCoverPageBytes {
		return fmt.Errorf("%w: coverPage exceeds %d bytes", ErrInvalidEngagement, MaxReportCoverPageBytes)
	}
	for _, f := range []struct{ name, v string }{{"companyLogo", b.CompanyLogo}, {"clientLogo", b.ClientLogo}} {
		if err := validateReportLogo(f.name, f.v); err != nil {
			return err
		}
	}
	for _, f := range []struct{ name, v string }{{"assessmentStart", b.AssessmentStart}, {"assessmentEnd", b.AssessmentEnd}} {
		if err := validateISODate(f.name, f.v); err != nil {
			return err
		}
	}
	if b.AssessmentStart != "" && b.AssessmentEnd != "" && b.AssessmentEnd < b.AssessmentStart {
		return fmt.Errorf("%w: assessmentEnd is before assessmentStart", ErrInvalidEngagement)
	}
	return nil
}

func validateReportLogo(name, v string) error {
	if v == "" {
		return nil
	}
	if len(v) > MaxReportLogoBytes {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidEngagement, name, MaxReportLogoBytes)
	}
	meta, _, ok := strings.Cut(v, ",")
	if !ok || !strings.HasPrefix(meta, "data:") {
		return fmt.Errorf("%w: %s must be a data: image URI, not a URL — a delivered report fetches nothing", ErrInvalidEngagement, name)
	}
	if !strings.HasSuffix(meta, ";base64") {
		return fmt.Errorf("%w: %s must be base64-encoded", ErrInvalidEngagement, name)
	}
	mime := strings.TrimSuffix(strings.TrimPrefix(meta, "data:"), ";base64")
	for _, allowed := range reportLogoMIMEs {
		if strings.EqualFold(mime, allowed) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s type %q is not one of %s", ErrInvalidEngagement, name, mime, strings.Join(reportLogoMIMEs, ", "))
}

func validateISODate(name, v string) error {
	if v == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return fmt.Errorf("%w: %s must be an ISO date (YYYY-MM-DD), got %q", ErrInvalidEngagement, name, v)
	}
	return nil
}

// GetReportBranding returns the stored branding (zero value, version 0, if unset).
func (s *Store) GetReportBranding() (ReportBranding, error) {
	var b ReportBranding
	_, err := loadVersionedSetting(s.db, reportBrandingKey, &b)
	return b, err
}

// SetReportBranding saves the branding, bumping the version only when content
// differs from what is stored. Version and UpdatedAt in the input are ignored.
func (s *Store) SetReportBranding(in ReportBranding) (ReportBranding, error) {
	if err := in.validate(); err != nil {
		return ReportBranding{}, err
	}
	var out ReportBranding
	err := s.saveVersionedSetting(reportBrandingKey, &out, func() {
		out.ReportTitle, out.CompanyName, out.ClientName = in.ReportTitle, in.CompanyName, in.ClientName
		out.CompanyLogo, out.ClientLogo = in.CompanyLogo, in.ClientLogo
		out.EngagementName = in.EngagementName
		out.AssessmentStart, out.AssessmentEnd = in.AssessmentStart, in.AssessmentEnd
		out.LeadAssessor, out.Contact, out.Classification = in.LeadAssessor, in.Contact, in.Classification
		out.CoverPage = in.CoverPage
	}, nil)
	return out, err
}
