package control

import (
	"bytes"
	"os"
	"runtime"
	"testing"
)

// Intruder grep inspects at most intruder.MaxGrepBytes of a response, but the
// control side handed it the whole stored body, so a run against large
// responses still held every body in memory. The prefix reader stops at the
// limit and reports the cut.
func TestBodyPrefixReadsOnlyTheRequestedBytes(t *testing.T) {
	h, _, _ := newHub(t)
	const size = 24 << 20
	payload := bytes.Repeat([]byte("abcdefgh"), size/8)
	hash, n := (&projectAPI{h}).storeBody(payload)
	if n != int64(size) {
		t.Fatalf("stored %d bytes, want %d", n, size)
	}
	payload = nil

	b, truncated, err := h.bodyPrefix(hash, 1<<20)
	if err != nil || !truncated || len(b) != 1<<20 {
		t.Fatalf("prefix: len %d truncated %v err %v, want 1 MiB truncated", len(b), truncated, err)
	}
	if !bytes.HasPrefix(b, []byte("abcdefghabcdefgh")) {
		t.Fatal("prefix does not start with the stored bytes")
	}
	whole, truncated, err := h.bodyPrefix(hash, size)
	if err != nil || truncated || len(whole) != size {
		t.Fatalf("exact limit: len %d truncated %v err %v, want the whole body untruncated", len(whole), truncated, err)
	}
	whole = nil
	if b, truncated, err := h.bodyPrefix("", 10); err != nil || truncated || b != nil {
		t.Fatalf("empty hash: %v %v %v", b, truncated, err)
	}
	if _, _, err := h.bodyPrefix("0000000000000000000000000000000000000000000000000000000000000000", 10); err == nil {
		t.Fatal("a missing body must be an error, not an empty match")
	}

	// The read itself is bounded: asking for 64 KiB of a 24 MiB body must not
	// allocate anything like the body.
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < 8; i++ {
		if _, _, err := h.bodyPrefix(hash, 64<<10); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 8<<20 {
		t.Fatalf("eight 64 KiB prefix reads allocated %d MiB", grew>>20)
	}
}

func TestIntruderEngineIsWiredToTheBoundedBodyReader(t *testing.T) {
	src, err := os.ReadFile("control.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(src, []byte("h.intr.SetBodyPrefixReader(h.bodyPrefix)")) {
		t.Fatal("the Intruder engine must read response bodies through the bounded prefix reader")
	}
}
