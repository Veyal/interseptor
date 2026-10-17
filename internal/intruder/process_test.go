package intruder

import "testing"

// TestProcessPayloadTransforms covers each supported transform and ordered
// chaining, so the new hashing/encoding ops stay correct.
func TestProcessPayloadTransforms(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		rules []string
		want  string
	}{
		{"urlencode", "a b&c", []string{"urlencode"}, "a+b%26c"},
		{"base64", "foo", []string{"base64"}, "Zm9v"},
		{"base64url", "\xff\xff\xff", []string{"base64url"}, "____"},
		{"hex", "AB", []string{"hex"}, "4142"},
		{"htmlencode", `<a href="x">'&'`, []string{"htmlencode"}, "&lt;a href=&#34;x&#34;&gt;&#39;&amp;&#39;"},
		{"md5", "foo", []string{"md5"}, "acbd18db4cc2f85cedef654fccc4a4d8"},
		{"sha1", "foo", []string{"sha1"}, "0beec7b5ea3f0fdbc95d0dd47f3c5bc275da8a33"},
		{"sha256", "foo", []string{"sha256"}, "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae"},
		{"upper", "aB", []string{"upper"}, "AB"},
		{"lower", "aB", []string{"lower"}, "ab"},
		{"prefix", "x", []string{"prefix:id="}, "id=x"},
		{"suffix", "x", []string{"suffix:--"}, "x--"},
		{"blank skipped", "x", []string{"", "upper", ""}, "X"},
		{"chain prefix then base64", "x", []string{"prefix:a=", "base64"}, "YT14"},
		{"chain hash then upper", "foo", []string{"sha1", "upper"}, "0BEEC7B5EA3F0FDBC95D0DD47F3C5BC275DA8A33"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := processPayload(tc.in, tc.rules); got != tc.want {
				t.Fatalf("processPayload(%q, %v) = %q, want %q", tc.in, tc.rules, got, tc.want)
			}
		})
	}
}

// TestValidateProcessRules proves an unknown transform is rejected (the former
// silent no-op) while valid rules, blanks, and prefix/suffix pass.
func TestValidateProcessRules(t *testing.T) {
	ok := [][]string{
		nil,
		{""},
		{"urlencode", "base64", "base64url", "hex", "htmlencode", "md5", "sha1", "sha256", "upper", "lower"},
		{"prefix:id=", "suffix:--"},
		{"prefix:"},
	}
	for _, rules := range ok {
		if err := validateProcessRules(rules); err != nil {
			t.Fatalf("validateProcessRules(%v) = %v, want nil", rules, err)
		}
	}
	bad := [][]string{
		{"md55"},
		{"base644"},
		{"sha512"},
		{"gzip"},
		{"urlencode", "nope"},
	}
	for _, rules := range bad {
		if err := validateProcessRules(rules); err == nil {
			t.Fatalf("validateProcessRules(%v) = nil, want error", rules)
		}
	}
}
