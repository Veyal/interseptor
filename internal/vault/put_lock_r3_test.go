package vault

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// r3SlowUpload yields the archive, then blocks before EOF until released: an
// upload that is still arriving.
type r3SlowUpload struct {
	data    *bytes.Reader
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (u *r3SlowUpload) Read(p []byte) (int, error) {
	if u.data.Len() > 0 {
		return u.data.Read(p)
	}
	u.once.Do(func() { close(u.started) })
	<-u.release
	return 0, io.EOF
}

// One upload still in flight must not block reads, listings or another
// project's upload: Put holds the store lock only to commit the revision.
func TestR3PutDoesNotHoldTheStoreLockDuringUpload(t *testing.T) {
	st, err := Open(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	zip := writeMiniZip(t)
	if _, err := st.Put("alpha", "first", "seed", bytes.NewReader(zip)); err != nil {
		t.Fatal(err)
	}
	slow := &r3SlowUpload{data: bytes.NewReader(zip), started: make(chan struct{}), release: make(chan struct{})}
	putDone := make(chan error, 1)
	go func() {
		_, err := st.Put("alpha", "slow", "test", slow)
		putDone <- err
	}()
	<-slow.started

	others := make(chan error, 1)
	go func() {
		if _, err := st.List(); err != nil {
			others <- err
			return
		}
		if revs, err := st.Revisions("alpha"); err != nil || len(revs) != 1 {
			others <- fmt.Errorf("revisions during upload = %v, %v; want the one committed revision", revs, err)
			return
		}
		rc, _, err := st.OpenRev("alpha", 0)
		if err != nil {
			others <- err
			return
		}
		rc.Close()
		_ = st.Status()
		info, err := st.Put("beta", "other", "test", bytes.NewReader(zip))
		if err != nil || info.Rev != 1 {
			others <- fmt.Errorf("put of another project during upload = %+v, %v", info, err)
			return
		}
		// A second upload of the same project commits first and takes rev 2.
		info, err = st.Put("alpha", "fast", "test", bytes.NewReader(zip))
		if err != nil || info.Rev != 2 {
			others <- fmt.Errorf("concurrent put of the same project = %+v, %v; want rev 2", info, err)
			return
		}
		others <- nil
	}()
	select {
	case err := <-others:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(slow.release)
		t.Fatal("vault reads and another upload blocked behind an upload that was still in flight")
	}
	close(slow.release)
	if err := <-putDone; err != nil {
		t.Fatalf("slow put: %v", err)
	}
	revs, err := st.Revisions("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 3 || revs[2].Rev != 3 || revs[2].Label != "slow" || revs[1].Label != "fast" {
		t.Fatalf("revisions = %+v, want first(1), fast(2), slow(3)", revs)
	}
	for _, r := range revs {
		rc, info, err := st.OpenRev("alpha", r.Rev)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(rc)
		rc.Close()
		if !bytes.Equal(got, zip) || info.Size != int64(len(zip)) {
			t.Fatalf("rev %d does not hold the uploaded archive", r.Rev)
		}
	}
	r3NoTempFiles(t, st.Dir)
}

func r3NoTempFiles(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && strings.HasPrefix(info.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", path)
		}
		return nil
	})
}

// Many uploads of one project at once each get their own revision number and
// their own bytes.
func TestR3ConcurrentPutsGetDistinctRevisions(t *testing.T) {
	st, err := Open(t.TempDir(), 50)
	if err != nil {
		t.Fatal(err)
	}
	zip := writeMiniZip(t)
	const n = 16
	var wg sync.WaitGroup
	revs := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			info, err := st.Put("gamma", fmt.Sprint(i), "test", bytes.NewReader(zip))
			if err != nil {
				t.Errorf("put %d: %v", i, err)
				return
			}
			revs[i] = info.Rev
		}(i)
	}
	wg.Wait()
	seen := map[int]bool{}
	for _, r := range revs {
		if r < 1 || r > n || seen[r] {
			t.Fatalf("revision numbers %v are not 1..%d exactly once", revs, n)
		}
		seen[r] = true
	}
	list, err := st.Revisions("gamma")
	if err != nil || len(list) != n {
		t.Fatalf("stored %d revisions (err %v), want %d", len(list), err, n)
	}
	r3NoTempFiles(t, st.Dir)
}

// A rejected or failed upload leaves neither a revision nor a temp file.
func TestR3FailedPutLeavesNothingBehind(t *testing.T) {
	st, err := Open(t.TempDir(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put("delta", "", "", strings.NewReader("not a zip")); err == nil {
		t.Fatal("a non-zip upload was stored")
	}
	if _, err := st.Put("delta", "", "", strings.NewReader("")); err == nil {
		t.Fatal("an empty upload was stored")
	}
	if _, err := st.Put("delta", "", "", io.MultiReader(bytes.NewReader(writeMiniZip(t)[:20]), r3ErrReader{})); err == nil {
		t.Fatal("an upload that failed mid-stream was stored")
	}
	if revs, _ := st.Revisions("delta"); len(revs) != 0 {
		t.Fatalf("failed uploads left revisions %+v", revs)
	}
	r3NoTempFiles(t, st.Dir)
}

type r3ErrReader struct{}

func (r3ErrReader) Read([]byte) (int, error) { return 0, fmt.Errorf("connection reset") }

// Open removes the temp file of an upload that died long ago and leaves a
// recent one (an upload that may still be running in another process) alone.
func TestR3OpenRemovesOnlyStaleUploadTempFiles(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "projects", "acme")
	if err := os.MkdirAll(pdir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale, fresh := filepath.Join(pdir, tmpRevPrefix+"old.zip"), filepath.Join(pdir, tmpRevPrefix+"new.zip")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * staleUploadAge)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale upload temp file survived Open: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("recent upload temp file was removed: %v", err)
	}
}
