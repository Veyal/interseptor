package proxy

import "strings"

// browserTelemetryHosts is the set of exact hostnames that Chrome and Firefox
// use for background telemetry, crash reporting, safe-browsing lookups, update
// pings, and captive-portal detection. Requests to these hosts are suppressed
// from history and the intercept gate when SuppressBrowserTelemetry is on.
var browserTelemetryHosts = map[string]struct{}{
	// Firefox — telemetry & crash
	"incoming.telemetry.mozilla.org": {},
	"telemetry.mozilla.org":          {},
	"crash-reports.mozilla.com":      {},
	"crash-stats.mozilla.com":        {},

	// Firefox — update & remote settings (Normandy / Balrog)
	"aus5.mozilla.org":                            {},
	"normandy.cdn.mozilla.net":                    {},
	"normandy.services.mozilla.com":               {},
	"firefox.settings.services.mozilla.com":       {},
	"remotesettings.services.mozilla.com":         {},
	"firefox-settings-attachments.cdn.mozilla.net": {},

	// Firefox — push & portal detection
	"push.services.mozilla.com": {},
	"detectportal.firefox.com":  {},

	// Firefox — tracking-protection list updates (Safe Browsing)
	"shavar.services.mozilla.com":    {},
	"tracking-protection.cdn.mozilla.net": {},

	// Firefox — client classification & geolocation
	"prod.classify-client.services.mozilla.com": {},
	"location.services.mozilla.com":             {},

	// Chrome / Chromium — Safe Browsing
	"safebrowsing.googleapis.com": {},

	// Chrome — updates
	"update.googleapis.com": {},

	// Chrome — field trials & optimisation hints
	"chrome-variations.googleapis.com":   {},
	"optimizationguide-pa.googleapis.com": {},

	// Chrome — crash reports
	"chromecrashreports-pa.googleapis.com": {},
	"crash.chromium.org":                   {},

	// Chrome — connectivity probe
	"connectivity.gstatic.com": {},
}

// isBrowserTelemetry reports whether host is a known Chrome or Firefox
// background telemetry / update / crash-reporting endpoint.
func isBrowserTelemetry(host string) bool {
	// Strip port, lowercase.
	if i := strings.LastIndexByte(host, ':'); i != -1 {
		host = host[:i]
	}
	_, ok := browserTelemetryHosts[strings.ToLower(host)]
	return ok
}
