package main

import "testing"

func TestUIWindowSkip(t *testing.T) {
	cases := []struct {
		name            string
		headless, force bool
		env             map[string]string
		skip            bool
	}{
		{"desktop default opens", false, false, nil, false},
		{"--headless", true, false, nil, true},
		{"INTERSEPTOR_HEADLESS", false, false, map[string]string{"INTERSEPTOR_HEADLESS": "1"}, true},
		{"INTERSEPTOR_NO_BROWSER beats --open", false, true, map[string]string{"INTERSEPTOR_NO_BROWSER": "1"}, true},
		{"--headless beats --open", true, true, nil, true},
		{"ssh session skips", false, false, map[string]string{"SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"}, true},
		{"ssh tty skips", false, false, map[string]string{"SSH_TTY": "/dev/ttys001"}, true},
		{"--open forces over ssh", false, true, map[string]string{"SSH_CONNECTION": "x"}, false},
		{"INTERSEPTOR_OPEN_BROWSER forces over ssh", false, false, map[string]string{"SSH_TTY": "x", "INTERSEPTOR_OPEN_BROWSER": "1"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			why := uiWindowSkip(c.headless, c.force, func(k string) string { return c.env[k] })
			if (why != "") != c.skip {
				t.Fatalf("uiWindowSkip = %q, want skip=%v", why, c.skip)
			}
		})
	}
}
