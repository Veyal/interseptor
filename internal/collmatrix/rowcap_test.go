package collmatrix

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// The MaxRows column cap must be reported once, not once per dropped row per
// identity: on a large collection the duplicate warnings dominated the matrix
// JSON.
func TestRunMatrixRowCapWarningIsEmittedOnce(t *testing.T) {
	const extra = 10
	names := make([]string, 0, MaxRows+extra)
	for i := 0; i < MaxRows+extra; i++ {
		names = append(names, fmt.Sprintf("r%03d", i))
	}
	be := newFake(names...)
	svc := testService(be, []Identity{
		{Name: "u1", Headers: []Header{{"Authorization", "t"}}},
		{Name: "u2", Headers: []Header{{"Authorization", "t"}}},
	})
	m, err := svc.RunMatrix(context.Background(), MatrixRequest{CollectionUID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Rows) != MaxRows {
		t.Fatalf("rows = %d, want %d", len(m.Rows), MaxRows)
	}
	var rpc []string
	for _, w := range m.Warnings {
		if strings.Contains(w, "extra rows dropped") {
			rpc = append(rpc, w)
		}
	}
	if len(rpc) != 1 {
		t.Fatalf("row-cap warning emitted %d times, want 1: %q", len(rpc), rpc)
	}
}
