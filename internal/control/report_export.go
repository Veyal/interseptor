package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Veyal/interseptor/internal/pdfexport"
	"github.com/Veyal/interseptor/internal/report"
)

// reportFormats are the accepted ?format= values of GET /api/findings/report.
// "" is Markdown, the historical default.
var reportFormats = map[string]bool{
	"": true, "md": true, "markdown": true, "html": true, "json": true,
	"pdf": true, "sarif": true,
}

const reportFormatError = "format must be md, html, json, pdf, or sarif"

// boolParam reads a query flag written as 1/true/yes/on in any case.
func boolParam(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// writeSARIF writes a SARIF log under its registered media type, so a consumer
// that routes on Content-Type recognises the document for what it is.
func writeSARIF(w http.ResponseWriter, log report.SARIFLog) {
	w.Header().Set("Content-Type", "application/sarif+json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(log)
}

// reportPDFRenderer prints the HTML report document to PDF. control talks to
// the browser through this interface so the dependency can be replaced in tests
// and so nothing in control imports a browser driver directly.
type reportPDFRenderer interface {
	Render(ctx context.Context, html []byte) ([]byte, error)
}

// newReportPDFRenderer is a seam for tests; production always resolves a real
// headless Chrome through internal/pdfexport.
var newReportPDFRenderer = func() reportPDFRenderer { return pdfexport.New() }

// writeReportPDF prints an already-rendered HTML report and writes the PDF.
//
// A failure is reported as a failure. There is no fallback to HTML and no
// partially written body: the status and message name the missing browser or
// quote what the browser said, because a report that silently arrives in the
// wrong format — or arrives truncated — is worse than one that did not arrive.
func (h *findingsAPI) writeReportPDF(w http.ResponseWriter, r *http.Request, html, mode string) {
	pdf, err := newReportPDFRenderer().Render(r.Context(), []byte(html))
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, pdfexport.ErrNoBrowser) {
			// The capability is absent, not broken: say so, and say how to fix it.
			code = http.StatusServiceUnavailable
		}
		httpErr(w, code, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", reportDisposition("pdf", mode))
	w.Write(pdf)
}
