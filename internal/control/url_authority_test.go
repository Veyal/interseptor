package control

import (
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestFlowURLsFormatIPv6Authority(t *testing.T) {
	f := &store.Flow{Scheme: "https", Host: "2001:db8::1", Port: 8443, Path: "/v1"}
	want := "https://[2001:db8::1]:8443/v1"
	if got := flowURLStr(f); got != want {
		t.Fatalf("flowURLStr = %q, want %q", got, want)
	}
	if got := analyzeURL(f); got != want {
		t.Fatalf("analyzeURL = %q, want %q", got, want)
	}
}
