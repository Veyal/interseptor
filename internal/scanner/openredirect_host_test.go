package scanner

import "testing"

func TestOpenRedirectParamComparesParsedHost(t *testing.T) {
	for _, tc := range []struct {
		name, path, location string
		want                 bool
	}{
		{"suffix-domain bypass", "/go?next=https://target.com.evil.net/", "https://target.com.evil.net/", true},
		{"target in query", "/go?next=https://evil.net/?r=target.com", "https://evil.net/?r=target.com", true},
		{"protocol-relative", "/go?next=//evil.net/x", "//evil.net/x", true},
		{"same host", "/go?next=https://target.com/home", "https://target.com/home", false},
		{"same host, case and trailing dot", "/go?next=https://TARGET.com./home", "https://TARGET.com./home", false},
		{"subdomain", "/go?next=https://app.target.com/x", "https://app.target.com/x", false},
	} {
		_, _, ok := openRedirectParam("target.com", tc.path, "", tc.location)
		if ok != tc.want {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.want)
		}
	}
}
