package main

import (
	"reflect"
	"testing"
)

// Without a Chromium browser for the app window, the UI opens in the default
// browser rather than only logging the URL.
func TestDefaultBrowserCommand(t *testing.T) {
	const u = "http://127.0.0.1:9966/"
	for goos, want := range map[string][]string{
		"darwin":  {"open", u},
		"linux":   {"xdg-open", u},
		"windows": {"rundll32", "url.dll,FileProtocolHandler", u},
	} {
		name, args, ok := defaultBrowserCommand(goos, u)
		if !ok || !reflect.DeepEqual(append([]string{name}, args...), want) {
			t.Errorf("%s: %s %v (ok=%v), want %v", goos, name, args, ok, want)
		}
	}
	if _, _, ok := defaultBrowserCommand("plan9", u); ok {
		t.Error("an unknown OS must not claim a default-browser command")
	}
	if _, _, ok := defaultBrowserCommand("darwin", "file:///etc/passwd"); ok {
		t.Error("only http(s) URLs are opened")
	}
}
