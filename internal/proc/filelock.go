package proc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// FileLock is an exclusive advisory lock held on a lock file, used to serialise
// a read-modify-write of shared on-disk state between Interseptor processes
// (the launcher's instance registry, a managed Chrome install). The kernel
// releases it when the holder exits, so a crashed process never leaves it
// stuck; the lock file itself is left in place and reused.
type FileLock struct {
	f *os.File
}

// ErrLockHeld is returned (wrapped) by LockFile when another process still
// holds the lock at the end of the timeout.
var ErrLockHeld = errors.New("still held by another process")

// lockRetryInterval is how often a contended lock is retried.
const lockRetryInterval = 10 * time.Millisecond

// LockFile takes the exclusive lock at path, creating the file (0600) and its
// directory if needed. It waits up to timeout for another holder to release it
// and returns an error rather than proceeding unlocked.
func LockFile(path string, timeout time.Duration) (*FileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		locked, err := tryLockFile(f)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if locked {
			return &FileLock{f: f}, nil
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w after %s", path, ErrLockHeld, timeout)
		}
		time.Sleep(lockRetryInterval)
	}
}

// Unlock releases the lock. It is safe to call on a nil lock and more than once.
func (l *FileLock) Unlock() {
	if l == nil || l.f == nil {
		return
	}
	_ = unlockFile(l.f)
	_ = l.f.Close()
	l.f = nil
}
