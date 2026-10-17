package store

import "strings"

// Browser background traffic is what a proxied Chrome sends on its own: component
// and extension updates, field-trial seeds, account sync checks, autofill votes,
// Safe Browsing and connectivity probes. History can hide it as a display filter
// (FlowFilter.HideBrowserNoise). Unlike capture-time telemetry suppression in
// internal/proxy, nothing is dropped: the rows stay in the project and reappear
// when the filter is off, so the list may name hosts a tester could, rarely,
// target. Keep it exact; js/proxy-filters.js mirrors it for live rows and
// TestBrowserNoiseRulesMatchTheUIMirror keeps the two in step.
var browserNoiseHosts = []string{
	// Chrome: component updater, extension updates and the Web Store
	"clients2.google.com",
	"clients2.googleusercontent.com",
	"update.googleapis.com",
	"chromewebstore.googleapis.com",
	"edgedl.me.gvt1.com",
	"redirector.gvt1.com",
	// Chrome: variations seed, autofill votes, optimization hints
	"clientservices.googleapis.com",
	"chrome-variations.googleapis.com",
	"content-autofill.googleapis.com",
	"optimizationguide-pa.googleapis.com",
	// Chrome: Safe Browsing, crash reports, connectivity probe
	"safebrowsing.googleapis.com",
	"safebrowsing.google.com",
	"sb-ssl.google.com",
	"chromecrashreports-pa.googleapis.com",
	"crash.chromium.org",
	"connectivity.gstatic.com",
}

// browserNoiseSuffixes: gvt1.com is Google's download CDN for Chrome component
// and extension payloads (rrN---sn-*.gvt1.com mirrors).
var browserNoiseSuffixes = []string{".gvt1.com"}

// browserNoisePaths are host + path-prefix pairs on shared Google hosts, where
// only the browser's own endpoint is noise.
var browserNoisePaths = [][2]string{
	{"accounts.google.com", "/ListAccounts"},
	{"www.google.com", "/async/folae"},
	{"www.googleapis.com", "/chromewebstore/"},
}

// browserNoisePredicate is the SQL that matches browser background rows.
func browserNoisePredicate() (string, []any) {
	var (
		parts []string
		args  []any
	)
	ph := make([]string, len(browserNoiseHosts))
	for i, h := range browserNoiseHosts {
		ph[i] = "?"
		args = append(args, h)
	}
	parts = append(parts, "lower(host) IN ("+strings.Join(ph, ",")+")")
	for _, suf := range browserNoiseSuffixes {
		parts = append(parts, "substr(lower(host), -?) = ?")
		args = append(args, len(suf), suf)
	}
	for _, hp := range browserNoisePaths {
		parts = append(parts, "(lower(host) = ? AND substr(path, 1, ?) = ?)")
		args = append(args, hp[0], len(hp[1]), hp[1])
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}
