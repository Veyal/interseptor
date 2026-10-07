package jsrt

import (
	"context"
	"testing"
)

func BenchmarkNewRuntimeEval(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rt := New(Options{})
		if _, err := rt.Eval(context.Background(), "b", `1+1`); err != nil {
			b.Fatal(err)
		}
		rt.Close()
	}
}
