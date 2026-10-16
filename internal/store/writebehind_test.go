package store

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnqueueFlowDoesNotBlockWhileDBLocked(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO settings(key, value) VALUES('wb-lock', '1')`); err != nil {
		t.Fatalf("lock: %v", err)
	}

	f := &Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: "/locked", ClientAddr: "203.0.113.5"}
	start := time.Now()
	ok, err := s.EnqueueInsertFlow(f)
	elapsed := time.Since(start)
	if err != nil || !ok {
		t.Fatalf("enqueue insert: ok=%v err=%v", ok, err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("enqueue blocked for %s while the database was locked", elapsed)
	}
	f.Status = 200
	start = time.Now()
	ok, err = s.EnqueueUpdateFlow(f)
	elapsed = time.Since(start)
	if err != nil || !ok {
		t.Fatalf("enqueue update: ok=%v err=%v", ok, err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("enqueue update blocked for %s while the database was locked", elapsed)
	}
	start = time.Now()
	ok, err = s.EnqueueAddFlowTags(f.ID, []string{"auth"})
	elapsed = time.Since(start)
	if err != nil || !ok {
		t.Fatalf("enqueue tags: ok=%v err=%v", ok, err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("enqueue tags blocked for %s while the database was locked", elapsed)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := s.FlushFlows(); err != nil {
		t.Fatalf("FlushFlows: %v", err)
	}
	got, err := s.GetFlow(f.ID)
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	if got.Status != 200 || got.Path != "/locked" || got.ClientAddr != "203.0.113.5" {
		t.Fatalf("persisted %+v, want status 200 path /locked", got)
	}
	tags, err := s.FlowTags(f.ID)
	if err != nil || len(tags) != 1 || tags[0] != "auth" {
		t.Fatalf("tags = %v, err=%v, want [auth]", tags, err)
	}
}

func TestFlushFlowsPersistsInsertThenUpdateInOrder(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	f := &Flow{
		TS:         time.UnixMilli(1),
		Method:     "POST",
		Scheme:     "https",
		Host:       "example.com",
		Path:       "/ordered",
		ClientAddr: "203.0.113.9",
		Note:       "from-insert",
	}
	if ok, err := s.EnqueueInsertFlow(f); err != nil || !ok {
		t.Fatalf("enqueue insert: ok=%v err=%v", ok, err)
	}
	f.Status = 201
	f.ResLen = 4
	if ok, err := s.EnqueueUpdateFlow(f); err != nil || !ok {
		t.Fatalf("enqueue update: ok=%v err=%v", ok, err)
	}
	if err := s.FlushFlows(); err != nil {
		t.Fatalf("FlushFlows: %v", err)
	}
	got, err := s.GetFlow(f.ID)
	if err != nil {
		t.Fatalf("GetFlow: %v", err)
	}
	if got.Status != 201 || got.ResLen != 4 || got.Note != "from-insert" || got.ClientAddr != "203.0.113.9" || got.Scheme != "https" {
		t.Fatalf("row = status %d resLen %d note %q client %q scheme %q", got.Status, got.ResLen, got.Note, got.ClientAddr, got.Scheme)
	}
}

func TestSetFlowNoteWaitsForQueuedInsert(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	f := &Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: "/noted"}
	if ok, err := s.EnqueueInsertFlow(f); err != nil || !ok {
		t.Fatalf("enqueue: ok=%v err=%v", ok, err)
	}
	if err := s.SetFlowNote(f.ID, "kept"); err != nil {
		t.Fatalf("SetFlowNote: %v", err)
	}
	got, err := s.GetFlow(f.ID)
	if err != nil || got.Note != "kept" {
		t.Fatalf("note before update = %q, err=%v", noteOf(got), err)
	}
	f.Status = 204
	if ok, err := s.EnqueueUpdateFlow(f); err != nil || !ok {
		t.Fatalf("enqueue update: ok=%v err=%v", ok, err)
	}
	if err := s.FlushFlows(); err != nil {
		t.Fatalf("FlushFlows: %v", err)
	}
	got, err = s.GetFlow(f.ID)
	if err != nil || got.Note != "kept" || got.Status != 204 {
		t.Fatalf("after update note=%q status=%d err=%v", noteOf(got), statusOf(got), err)
	}
}

func TestDirectInsertSerializesWithQueuedFlow(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	queued := &Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: "/queued"}
	if ok, err := s.EnqueueInsertFlow(queued); err != nil || !ok {
		t.Fatalf("enqueue: ok=%v err=%v", ok, err)
	}
	syncID, err := s.InsertFlow(&Flow{TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "example.com", Path: "/sync", Status: 200})
	if err != nil {
		t.Fatalf("InsertFlow: %v", err)
	}
	queued.Status = 202
	if ok, err := s.EnqueueUpdateFlow(queued); err != nil || !ok {
		t.Fatalf("enqueue update: ok=%v err=%v", ok, err)
	}
	if err := s.FlushFlows(); err != nil {
		t.Fatalf("FlushFlows: %v", err)
	}
	if syncID == queued.ID {
		t.Fatalf("sync and queued flows share id %d", syncID)
	}
	q, err := s.GetFlow(queued.ID)
	if err != nil || q.Status != 202 || q.Path != "/queued" {
		t.Fatalf("queued flow = %+v err=%v", q, err)
	}
	sy, err := s.GetFlow(syncID)
	if err != nil || sy.Path != "/sync" || sy.Status != 200 {
		t.Fatalf("sync flow = %+v err=%v", sy, err)
	}
}

func TestEnqueueFlowDropsOldestWhenBufferFull(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	s.flowMu.Lock()
	s.flowPaused = true
	s.flowMu.Unlock()

	const extra = 5
	for i := 0; i < flowBufCap+extra; i++ {
		f := &Flow{TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/drop", Note: strconv.Itoa(i)}
		if ok, err := s.EnqueueInsertFlow(f); err != nil || !ok {
			t.Fatalf("enqueue %d: ok=%v err=%v", i, ok, err)
		}
	}
	if got := s.FlowsDropped(); got != extra {
		t.Fatalf("dropped %d, want %d", got, extra)
	}
	if err := s.FlushFlows(); err != nil {
		t.Fatalf("FlushFlows: %v", err)
	}
	got, err := s.QueryFlows(flowBufCap + extra)
	if err != nil {
		t.Fatalf("QueryFlows: %v", err)
	}
	if len(got) != flowBufCap {
		t.Fatalf("persisted %d, want %d", len(got), flowBufCap)
	}
	newest, oldest := got[0].Note, got[len(got)-1].Note
	if newest != strconv.Itoa(flowBufCap+extra-1) || oldest != strconv.Itoa(extra) {
		t.Fatalf("kept notes %q..%q, want %q..%q", oldest, newest, strconv.Itoa(extra), strconv.Itoa(flowBufCap+extra-1))
	}
}

func TestCloseDrainsPendingFlows(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for i := 0; i < 3; i++ {
		f := &Flow{TS: time.UnixMilli(int64(i + 1)), Method: "GET", Host: "example.com", Path: "/close", Note: strconv.Itoa(i)}
		if ok, err := s.EnqueueInsertFlow(f); err != nil || !ok {
			t.Fatalf("enqueue: ok=%v err=%v", ok, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got, err := s2.QueryFlows(10)
	if err != nil {
		t.Fatalf("QueryFlows: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("drained %d flows, want 3", len(got))
	}
}

func TestCloseStopsFlowDrainAfterDeadline(t *testing.T) {
	old := flowApplyDelayNS.Load()
	oldTimeout := flowDrainTimeout
	flowApplyDelayNS.Store(int64(time.Second))
	flowDrainTimeout = 40 * time.Millisecond
	t.Cleanup(func() {
		flowApplyDelayNS.Store(old)
		flowDrainTimeout = oldTimeout
	})

	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f := &Flow{TS: time.UnixMilli(1), Method: "GET", Host: "example.com", Path: "/slow"}
	if ok, err := s.EnqueueInsertFlow(f); err != nil || !ok {
		t.Fatalf("enqueue: ok=%v err=%v", ok, err)
	}
	start := time.Now()
	err = s.Close()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Close took %s, want to stop within the drain deadline", elapsed)
	}
	if err == nil || !errors.Is(err, errFlowDrainTimeout) {
		t.Fatalf("Close err = %v, want drain timeout", err)
	}
}

func TestEnqueueFlowConcurrentWithSlowWriter(t *testing.T) {
	old := flowApplyDelayNS.Load()
	flowApplyDelayNS.Store(int64(2 * time.Millisecond))
	t.Cleanup(func() { flowApplyDelayNS.Store(old) })

	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const goroutines, each = 32, 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := make(map[int64]struct{}, goroutines*each)
	var blocked atomic.Int64
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				f := &Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: "/burst"}
				t0 := time.Now()
				ok, err := s.EnqueueInsertFlow(f)
				if time.Since(t0) > 200*time.Millisecond {
					blocked.Add(1)
				}
				if err != nil || !ok {
					t.Errorf("enqueue insert: ok=%v err=%v", ok, err)
					return
				}
				f.Status = 200
				f.ResLen = int64(i)
				t0 = time.Now()
				ok, err = s.EnqueueUpdateFlow(f)
				if time.Since(t0) > 200*time.Millisecond {
					blocked.Add(1)
				}
				if err != nil || !ok {
					t.Errorf("enqueue update: ok=%v err=%v", ok, err)
					return
				}
				mu.Lock()
				ids[f.ID] = struct{}{}
				mu.Unlock()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			if _, err := s.InsertFlow(&Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "example.com", Path: "/direct", Status: 200}); err != nil {
				t.Errorf("InsertFlow: %v", err)
				return
			}
		}
	}()
	wg.Wait()
	if n := blocked.Load(); n != 0 {
		t.Fatalf("%d enqueues blocked longer than 200ms behind the writer", n)
	}
	if err := s.FlushFlows(); err != nil {
		t.Fatalf("FlushFlows: %v", err)
	}
	if dropped := s.FlowsDropped(); dropped != 0 {
		t.Fatalf("dropped %d, want 0", dropped)
	}
	got, err := s.QueryFlows(goroutines*each + 30 + 10)
	if err != nil {
		t.Fatalf("QueryFlows: %v", err)
	}
	if len(got) != goroutines*each+30 {
		t.Fatalf("persisted %d, want %d", len(got), goroutines*each+30)
	}
	for _, f := range got {
		if f.Status != 200 {
			t.Fatalf("flow %d status %d, want 200", f.ID, f.Status)
		}
	}
	if len(ids) != goroutines*each {
		t.Fatalf("queued ids %d, want %d", len(ids), goroutines*each)
	}
}

func noteOf(f *Flow) string {
	if f == nil {
		return ""
	}
	return f.Note
}

func statusOf(f *Flow) int {
	if f == nil {
		return -1
	}
	return f.Status
}
