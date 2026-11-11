package proc

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLockFileExcludesASecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.lock")
	first, err := LockFile(path, time.Second)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	start := time.Now()
	if second, err := LockFile(path, 100*time.Millisecond); err == nil {
		second.Unlock()
		t.Fatal("a second holder got the lock while the first still held it")
	} else if !strings.Contains(err.Error(), "still held") {
		t.Fatalf("unexpected error: %v", err)
	}
	if waited := time.Since(start); waited < 90*time.Millisecond || waited > 2*time.Second {
		t.Fatalf("waited %s, want about the 100ms timeout", waited)
	}
	first.Unlock()
	first.Unlock() // idempotent
	third, err := LockFile(path, time.Second)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	third.Unlock()
	var nilLock *FileLock
	nilLock.Unlock()
}

func TestLockFileSerialisesHolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	var wg sync.WaitGroup
	inside, max := 0, 0
	var mu sync.Mutex
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := LockFile(path, 10*time.Second)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			inside++
			if inside > max {
				max = inside
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			inside--
			mu.Unlock()
			l.Unlock()
		}()
	}
	wg.Wait()
	if max != 1 {
		t.Fatalf("%d holders were inside the lock at once", max)
	}
}
