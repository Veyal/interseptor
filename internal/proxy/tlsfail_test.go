package proxy

import (
	"errors"
	"strings"
	"testing"
)

func TestClassifyTLSError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err    error
		want   string
		noWant string
	}{
		{errors.New("EOF"), "pinning or untrusted CA", ""},
		{errors.New("read tcp: connection reset by peer"), "pinning or untrusted CA", ""},
		{errors.New("tls: bad certificate"), "certificate rejected", ""},
		{errors.New("some other failure"), "handshake failed", "pinning"},
	}
	for _, tc := range cases {
		got := classifyTLSError(tc.err)
		if !strings.Contains(got, tc.want) {
			t.Fatalf("classifyTLSError(%v) = %q, want substring %q", tc.err, got, tc.want)
		}
		if tc.noWant != "" && strings.Contains(got, tc.noWant) {
			t.Fatalf("classifyTLSError(%v) = %q, must not contain %q", tc.err, got, tc.noWant)
		}
	}
}
