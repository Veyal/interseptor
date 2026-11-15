package store

import (
	"bytes"
	"strings"
	"testing"
)

func TestIsSoft404ContentMatchesRegexAcrossVariants(t *testing.T) {
	samples := []string{
		"",
		`{"users":[{"id":1}],"results":[]}`,
		`{"status":"found","page":"next","url":"https://example.com"}`,
		`404`,
		`x404`,
		`404x`,
		`404_`,
		`_404`,
		`error 404 here`,
		"HTTP/2 404\t\nNot\tFound",
		"Page   does\nnot\r\nexist",
		"page does not exist",
		"doesn't exist",
		"DOESNT EXIST",
		"no\t\nsuch file",
		"couldn't find record",
		"Could Not Find",
		"couldnt\ffind",
		"not\vfound", // \v is not RE2 \s
		"no\u00a0results",
		"nothing\t\nfound",
		"Resource Not Found",
		"Invalid   URL",
		"Unknown\tPage",
		"no results",
		"results here, and later no results",
		"İnvalid url",
		"no result\u017f",   // long s 'ſ'
		"un\u212anown page", // kelvin sign 'K'
		"prefix 40404 404",
		strings.Repeat("lorem ipsum ", 500) + " unknown\npage",
		strings.Repeat("lorem ipsum ", 500) + " valid content",
	}
	for _, s := range samples {
		in := []byte(s)
		want := soft404Re.Match(in)
		if got := isSoft404Content(in); got != want {
			t.Fatalf("isSoft404Content(%q) = %v, want regex %v", s, got, want)
		}
		cp := append([]byte(nil), in...)
		if got := isSoft404Mutable(cp); got != want {
			t.Fatalf("isSoft404Mutable(%q) = %v, want regex %v", s, got, want)
		}
	}
	// Ensure isSoft404Content never mutates its caller's slice.
	orig := []byte("HELLO PAGE NOT FOUND WORLD")
	before := bytes.Clone(orig)
	if !isSoft404Content(orig) {
		t.Fatal("expected match")
	}
	if !bytes.Equal(orig, before) {
		t.Fatal("isSoft404Content mutated caller buffer")
	}
}
