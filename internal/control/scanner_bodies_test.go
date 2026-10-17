package control

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/scanner"
	"github.com/Veyal/interseptor/internal/store"
)

// Built-in checks inspect only the first scanner.MaxScanBytes of a body, yet the
// scan read every body whole: 120 flows with 4 MiB responses took 4-20 s instead
// of well under one. Without custom checks only the prefix is read; custom
// Starlark checks still receive whole bodies.
func TestScanBodyLoaderReadsOnlyThePrefixBuiltinsInspect(t *testing.T) {
	h, st, _ := newHub(t)
	w, err := st.NewBodyWriter()
	if err != nil {
		t.Fatal(err)
	}
	big := []byte(strings.Repeat("a", scanner.MaxScanBytes*3))
	if _, err := w.Write(big); err != nil {
		t.Fatal(err)
	}
	hash, _, err := w.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	f := &store.Flow{ResBodyHash: hash}
	if _, res, err := h.scanBodyLoader(false)(f); err != nil || len(res) != scanner.MaxScanBytes {
		t.Fatalf("built-in loader read %d bytes (err %v), want %d", len(res), err, scanner.MaxScanBytes)
	}
	if _, res, err := h.scanBodyLoader(true)(f); err != nil || len(res) != len(big) {
		t.Fatalf("custom-check loader read %d bytes (err %v), want the whole %d", len(res), err, len(big))
	}
	missing := &store.Flow{ResBodyHash: strings.Repeat("b", 64)}
	if _, _, err := h.scanBodyLoader(false)(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a missing blob must stay os.ErrNotExist so the flow is skipped, got %v", err)
	}
}
