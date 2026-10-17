package proxy

import "testing"

// An intercept edit that retargets a request to an alias of loopback on one of
// our own ports must be refused like the literal 127.0.0.1 form.
func TestIsOwnListenerTargetCoversLoopbackAliases(t *testing.T) {
	s := &Server{SelfPorts: []int{9966}}
	for _, host := range []string{"127.0.0.1", "localhost", "0.0.0.0", "::", "[::]", "::ffff:127.0.0.1", "localhost.", "LOCALHOST", "app.localhost"} {
		if !s.isOwnListenerTarget(host, 9966) {
			t.Errorf("isOwnListenerTarget(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"example.com", "localhost.example.com", "10.0.0.1"} {
		if s.isOwnListenerTarget(host, 9966) {
			t.Errorf("isOwnListenerTarget(%q) = true, want false", host)
		}
	}
	if s.isOwnListenerTarget("0.0.0.0", 8081) {
		t.Error("a non-own port must not be refused")
	}
}
