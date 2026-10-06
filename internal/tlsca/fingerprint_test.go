package tlsca

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestFingerprintIsColonSeparatedSHA256OfDER(t *testing.T) {
	ca, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(ca.cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	want := strings.Join(parts, ":")
	if got := ca.Fingerprint(); got != want {
		t.Fatalf("Fingerprint = %q, want %q", got, want)
	}
}
