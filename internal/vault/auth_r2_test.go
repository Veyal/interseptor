package vault

import (
	"os"
	"strings"
	"sync"
	"testing"
)

// tokens.json is the vault's only credential store. A reader (or a process
// that starts while another is saving) must see either the previous token set
// or the new one, never an empty or half-written file: an empty file reads as
// "no tokens" and makes the vault mint a fresh bootstrap token, and a partial
// one stops the vault from starting.
func TestR2TokenFileIsReplacedAtomically(t *testing.T) {
	dir := t.TempDir()
	a, err := OpenAuth(dir)
	if err != nil {
		t.Fatalf("OpenAuth: %v", err)
	}
	const seeded = 400
	for i := 0; i < seeded; i++ {
		if _, _, err := a.Create(ScopeRead, "seed"); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var readErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			r, err := OpenAuth(dir)
			if err != nil {
				readErr = err
				return
			}
			if n := len(r.toks); n < seeded {
				readErr = &shortRead{n}
				return
			}
		}
	}()
	for i := 0; i < 300; i++ {
		if _, _, err := a.Create(ScopeRead, "live"); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	if readErr != nil {
		t.Fatalf("a concurrent reader saw a torn token file: %v", readErr)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "tokens.json" {
			t.Errorf("leftover file %q next to tokens.json", e.Name())
		}
	}
	info, err := os.Stat(a.path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("tokens.json mode = %o, want 600", perm)
	}
	if b, _ := os.ReadFile(a.path); strings.Count(string(b), `"hash"`) < seeded+300 {
		t.Errorf("tokens.json holds fewer than %d tokens", seeded+300)
	}
}

type shortRead struct{ n int }

func (e *shortRead) Error() string { return "token file read back with only " + itoa(e.n) + " tokens" }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
