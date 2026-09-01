package proxy

import (
	"net"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// browserTelemetryHosts is the set of exact hostnames that Chrome and Firefox
// use for background telemetry and browser-managed services such as crash
// reporting, Safe Browsing, updates, remote settings, suggestions, sponsored
// new-tab content, and connectivity probes. Requests to these hosts are
// forwarded untouched but suppressed from History and both intercept gates
// when SuppressBrowserTelemetry is on.
//
// Keep this list exact and curated. Broad suffixes such as *.mozilla.org or
// *.googleapis.com would hide ordinary target traffic from a security tester.
var browserTelemetryHosts = map[string]struct{}{
	// Firefox — telemetry, private measurement, crash, and coverage
	"incoming.telemetry.mozilla.org": {},
	"telemetry.mozilla.org":          {},
	"crash-reports.mozilla.com":      {},
	"crash-stats.mozilla.com":        {},
	"crash-stats.mozilla.org":        {},
	"dap.services.mozilla.com":       {},
	"dap-09-3.api.divviup.org":       {},
	"coverage.mozilla.org":           {},

	// Older Firefox/ESR telemetry endpoints still seen in long-lived labs.
	"fhr.data.mozilla.com":                 {},
	"metrics.services.mozilla.com":         {},
	"telemetry-experiment.cdn.mozilla.net": {},
	"crash-reports-xpsp2.mozilla.com":      {},

	// Firefox — update & remote settings (Normandy / Balrog)
	"aus.mozilla.org":                                 {},
	"aus3.mozilla.org":                                {},
	"aus4.mozilla.org":                                {},
	"aus5.mozilla.org":                                {},
	"normandy.cdn.mozilla.net":                        {},
	"normandy.services.mozilla.com":                   {},
	"firefox.settings.services.mozilla.com":           {},
	"remotesettings.services.mozilla.com":             {},
	"webextensions.settings.services.mozilla.com":     {},
	"firefox-settings-attachments.cdn.mozilla.net":    {},
	"content-signature-2.cdn.mozilla.net":             {},
	"firefox-settings.mozilla-backup.org":             {},
	"firefox-settings-attachments.mozilla-backup.org": {},
	"content-signature-2-cdn.mozilla-backup.org":      {},
	"aus5.mozilla-backup.org":                         {},

	// Firefox — push, portal/connectivity, and browser security services
	"push.services.mozilla.com":          {},
	"detectportal.firefox.com":           {},
	"firefox-portal-detection.com":       {},
	"mitmdetection.services.mozilla.com": {},

	// Firefox — tracking-protection list updates (Safe Browsing)
	"shavar.services.mozilla.com":         {},
	"tracking-protection.cdn.mozilla.net": {},

	// Firefox — client classification & geolocation
	"classify-client.services.mozilla.com":      {},
	"prod.classify-client.services.mozilla.com": {},
	"location.services.mozilla.com":             {},

	// Firefox — suggestions, sponsored new-tab content, and OHTTP relays.
	// These are not all telemetry, but they are browser-managed background
	// services and otherwise dominate History during application testing.
	"merino.services.mozilla.com":                            {},
	"ohttp-gateway-merino.services.mozilla.com":              {},
	"ohttp-merino.mozilla.fastly-edge.com":                   {},
	"mozilla-ohttp.fastly-edge.com":                          {},
	"mozilla-ohttp-dap.mozilla.fastly-edge.com":              {},
	"prod.ohttp-gateway.prod.webservices.mozgcp.net":         {},
	"prod-games-particle.merino.prod.webservices.mozgcp.net": {},
	"prod-images.merino.prod.webservices.mozgcp.net":         {},
	"contile.services.mozilla.com":                           {},
	"contile-images.services.mozilla.com":                    {},
	"topsites.services.mozilla.com":                          {},
	"ads.mozilla.org":                                        {},
	"ads-img.mozilla.org":                                    {},
	"spocs.getpocket.com":                                    {},

	// Chrome / Chromium — Safe Browsing
	"safebrowsing.googleapis.com": {},
	"safebrowsing.google.com":     {},
	"sb-ssl.google.com":           {},

	// Chrome — updates
	"update.googleapis.com": {},

	// Chrome — field trials & optimisation hints
	"chrome-variations.googleapis.com":    {},
	"optimizationguide-pa.googleapis.com": {},

	// Chrome — crash reports
	"chromecrashreports-pa.googleapis.com": {},
	"crash.chromium.org":                   {},

	// Chrome — connectivity probe
	"connectivity.gstatic.com": {},
}

// androidTelemetryHosts is the set of exact hostnames that Android OS, Google
// Play Services, Firebase Analytics/Crashlytics SDKs, and related ad/measurement
// stacks use for background phone-home. Suppressed from history and the
// intercept gate when SuppressAndroidTelemetry is on.
//
// Intentionally excludes app backends (firebase.googleapis.com, Firestore),
// auth (accounts.google.com), and FCM push (mtalk.google.com) so mobile
// engagements still see traffic the target app needs.
var androidTelemetryHosts = map[string]struct{}{
	// Play / GMS check-in & store APIs (very noisy on a proxied device)
	"android.clients.google.com": {},
	"android.googleapis.com":     {},
	"play.googleapis.com":        {},

	// Captive-portal / connectivity probes
	"connectivitycheck.gstatic.com": {},
	"connectivitycheck.android.com": {},
	"clients3.google.com":           {},

	// Crashlytics reporting (exact hosts; suffix list covers the rest)
	"crashlyticsreports-pa.googleapis.com":      {},
	"firebasecrashlyticssymbols.googleapis.com": {},
	"mobilecrashreporting.googleapis.com":       {},

	// Firebase Analytics / Clearcut-style logging (not Firestore/RTDB backends)
	"firebaselogging.googleapis.com":    {},
	"firebaselogging-pa.googleapis.com": {},
	"app-measurement.com":               {},
	"www.google-analytics.com":          {},
	"ssl.google-analytics.com":          {},
	"google-analytics.com":              {},
	"region1.google-analytics.com":      {},
	"analytics.google.com":              {},

	// Ads / measurement noise common on Android browsers & WebViews
	"googleads.g.doubleclick.net":   {},
	"ad.doubleclick.net":            {},
	"pagead2.googlesyndication.com": {},
	"www.googletagmanager.com":      {},
	"www.googletagservices.com":     {},
}

// androidTelemetrySuffixes matches any host under these DNS suffixes (leading
// dot required). Used for Crashlytics and App Measurement regional endpoints.
var androidTelemetrySuffixes = []string{
	".crashlytics.com",
	".app-measurement.com",
}

// telemetryHost normalises an HTTP authority / flow host for telemetry
// matching. It safely removes an optional port, brackets around IPv6 literals,
// surrounding whitespace, case differences, and a DNS trailing dot.
func telemetryHost(host string) string {
	host = strings.TrimSpace(host)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(host)), ".")
}

// isBrowserTelemetry reports whether host is a known Chrome or Firefox
// browser-managed background-service endpoint. Its legacy name is retained to
// match the public suppression setting.
func isBrowserTelemetry(host string) bool {
	return telemetryHostMatches(telemetryHost(host), browserTelemetryHosts, nil)
}

// isAndroidTelemetry reports whether host is a known Android OS / GMS /
// Crashlytics / Analytics background telemetry endpoint.
func isAndroidTelemetry(host string) bool {
	return telemetryHostMatches(telemetryHost(host), androidTelemetryHosts, androidTelemetrySuffixes)
}

func telemetryHostMatches(host string, exact map[string]struct{}, suffixes []string) bool {
	if _, ok := exact[host]; ok {
		return true
	}
	for _, suf := range suffixes {
		if host == suf[1:] || strings.HasSuffix(host, suf) {
			return true
		}
	}
	return false
}

// isSuppressedTelemetry reports whether flow belongs to a currently enabled
// browser/Android background-traffic suppression category. Keeping this policy
// in one place prevents request and response interception from diverging from
// History/body persistence.
func (s *Server) isSuppressedTelemetry(flow *store.Flow) bool {
	if flow == nil {
		return false
	}
	return (s.suppressTelemetry.Load() && isBrowserTelemetry(flow.Host)) ||
		(s.suppressAndroidTelemetry.Load() && isAndroidTelemetry(flow.Host))
}
