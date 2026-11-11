package control

import (
	"bytes"
	"runtime"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

// A flow diff compares at most maxBytes of each response body, so it must not
// load whole bodies to do it: two large downloads used to be read into memory
// in full only to be cut to a few kilobytes.
func TestBuildFlowDiffReadsOnlyTheComparedPrefix(t *testing.T) {
	h, st, _ := newHub(t)
	const size = 24 << 20
	put := func(fill byte) *store.Flow {
		body := bytes.Repeat([]byte{fill}, size)
		copy(body, "first line\nsecond line "+string(fill)+"\n")
		bw, err := st.NewBodyWriter()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bw.Write(body); err != nil {
			t.Fatal(err)
		}
		hash, n, err := bw.Finalize()
		if err != nil {
			t.Fatal(err)
		}
		return &store.Flow{Status: 200, ResBodyHash: hash, ResLen: n}
	}
	fa, fb := put('a'), put('b')

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	d, err := (&flowAPI{h}).buildFlowDiff(fa, fb, diffDefaultMaxBytes)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if !d.BodyTruncated || d.BodySame || len(d.BodyDeltas) == 0 || d.BodyDeltas[0].Line != 2 {
		t.Fatalf("diff = %+v, want a truncated comparison differing from line 2", d)
	}
	if got := after.TotalAlloc - before.TotalAlloc; got > 4<<20 {
		t.Fatalf("diffing two %d MiB bodies at maxBytes=%d allocated %d MiB", size>>20, diffDefaultMaxBytes, got>>20)
	}
}
