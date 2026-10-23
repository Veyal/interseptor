package main

import (
	"path/filepath"
	"testing"
)

func TestRestartProjectTarget(t *testing.T) {
	global := filepath.FromSlash("/home/u/.interseptor")
	cases := []struct{ name, dir, want string }{
		{"default", global, "default"},
		{"acme", filepath.Join(global, "projects", "acme"), "acme"},
		{"acme", filepath.FromSlash("/clients/acme"), filepath.FromSlash("/clients/acme")},
		{"x y", filepath.Join(global, "projects", "x y"), "x y"},
	}
	for _, c := range cases {
		if got := restartProjectTarget(global, c.name, c.dir); got != c.want {
			t.Errorf("restartProjectTarget(%q, %q) = %q, want %q", c.name, c.dir, got, c.want)
		}
	}
}
