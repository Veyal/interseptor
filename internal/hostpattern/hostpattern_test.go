package hostpattern

import "testing"

func TestMatchHost(t *testing.T) {
	cases := map[string]map[string]bool{
		"*.acme.com": {
			"acme.com":          true,
			"api.acme.com":      true,
			"evil-acme.com":     false,
			"acme.com.evil.com": false,
		},
		"api.test": {
			"api.test":     true,
			"www.api.test": false,
		},
		"": {
			"anything.test": true,
		},
		// A fully-qualified trailing dot names the same host and must not
		// escape include/exclude rules.
		"example.com": {
			"example.com.":  true,
			"EXAMPLE.COM.":  true,
			"example.com..": true,
		},
		"*.example.com": {
			"api.example.com.": true,
			"example.com.":     true,
		},
		"Example.COM.": {
			"example.com": true,
		},
	}
	for pattern, hosts := range cases {
		for host, want := range hosts {
			if got := MatchHost(pattern, host); got != want {
				t.Fatalf("MatchHost(%q, %q) = %v, want %v", pattern, host, got, want)
			}
		}
	}
}
