package main

import (
	"strings"
	"testing"
)

func TestReexecArgsKeepsLaunchFlags(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"none", nil, []string{"--project", "b"}},
		{"ports kept", []string{"--proxy-port", "18092", "--control-port=19972"}, []string{"--proxy-port", "18092", "--control-port=19972", "--project", "b"}},
		{"old project dropped", []string{"--project", "a", "--data-dir", "/tmp/x"}, []string{"--data-dir", "/tmp/x", "--project", "b"}},
		{"equals project dropped", []string{"-project=a", "--control-addr=127.0.0.1:1"}, []string{"--control-addr=127.0.0.1:1", "--project", "b"}},
		{"open not repeated", []string{"--open", "--proxy_port=5"}, []string{"--proxy-port=5", "--project", "b"}},
		{"open=true not repeated", []string{"-open=true"}, []string{"--project", "b"}},
	}
	for _, c := range cases {
		got := reexecArgs(c.in, "b")
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
