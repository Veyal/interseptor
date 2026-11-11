package intruder

import (
	"os"
	"testing"
)

// failChunk makes the write of chunk i fail (its path is a directory), which is
// what a full or read-only temp dir looks like to the log.
func failChunk(t *testing.T, l *resultLog, i int) {
	t.Helper()
	if err := os.Mkdir(l.chunkPath(i), 0o700); err != nil {
		t.Fatal(err)
	}
}

// When a chunk cannot be written the log keeps the results in memory. Reads
// must still address them by their real offset: the buffer then holds more
// than one chunk, so indexing it with offset%chunkSize returned the wrong rows
// (a results page repeated earlier rows and the run record was scrambled).
func TestResultLogReadsCorrectlyAfterFlushFailure(t *testing.T) {
	for _, okChunks := range []int{0, 2} {
		l := newResultLog(t.TempDir(), 3, 5)
		failChunk(t, l, okChunks)
		for i := 1; i <= 20; i++ {
			l.append(Result{ID: i, Status: 200, Length: 10})
		}
		if l.count() != 20 {
			t.Fatalf("okChunks=%d: count=%d", okChunks, l.count())
		}
		all := l.all()
		if len(all) != 20 {
			t.Fatalf("okChunks=%d: all() returned %d results: %v", okChunks, len(all), ids(all))
		}
		for i, r := range all {
			if r.ID != i+1 {
				t.Fatalf("okChunks=%d: all() = %v, want 1..20 in order", okChunks, ids(all))
			}
		}
		for _, off := range []int{0, 1, 4, 6, 7, 11, 17} {
			win := l.readRange(off, 3)
			if len(win) != 3 || win[0].ID != off+1 || win[2].ID != off+3 {
				t.Fatalf("okChunks=%d: readRange(%d,3) = %v, want %d..%d", okChunks, off, ids(win), off+1, off+3)
			}
		}
	}
}

// A failed flush must not be retried later in the run. The buffer has grown
// past one chunk by then, so a retry that succeeds writes an oversized chunk
// file and every offset after it resolves to the wrong rows; retrying on each
// append also re-marshals the whole growing buffer (quadratic on a full disk).
func TestResultLogDoesNotRetryFailedFlush(t *testing.T) {
	dir := t.TempDir()
	l := newResultLog(dir, 3, 5)
	failChunk(t, l, 0)
	for i := 1; i <= 5; i++ {
		l.append(Result{ID: i, Status: 200})
	}
	if err := os.Remove(l.chunkPath(0)); err != nil { // the disk "recovers"
		t.Fatal(err)
	}
	for i := 6; i <= 20; i++ {
		l.append(Result{ID: i, Status: 200})
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("flush retried after a failure: %d chunk file(s) written", len(ents))
	}
	all := l.all()
	if len(all) != 20 {
		t.Fatalf("all() = %v", ids(all))
	}
	for i, r := range all {
		if r.ID != i+1 {
			t.Fatalf("all() = %v, want 1..20 in order", ids(all))
		}
	}
	if win := l.readRange(13, 4); len(win) != 4 || win[0].ID != 14 || win[3].ID != 17 {
		t.Fatalf("readRange(13,4) = %v, want 14..17", ids(win))
	}
}

func BenchmarkResultLogAppendDiskFailing(b *testing.B) {
	for n := 0; n < b.N; n++ {
		l := newResultLog(b.TempDir(), 1000, 2000)
		if err := os.Mkdir(l.chunkPath(0), 0o700); err != nil {
			b.Fatal(err)
		}
		for i := 1; i <= 6000; i++ {
			l.append(Result{ID: i, Status: 200, Length: 10, Payload: "payload-value"})
		}
	}
}
