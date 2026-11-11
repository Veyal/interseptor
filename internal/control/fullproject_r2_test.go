package control

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

// C7: the read-only SQLite handles were opened with "file:"+path+"?mode=ro". A
// project directory whose name contains '?' or '#' cut the URI there, so the
// handle pointed at a different (missing) file.
func TestValidateImportedProjectPathWithURIMetacharacters(t *testing.T) {
	for _, name := range []string{"client#2", "what?now", "a?b#c", "100%done", "plain"} {
		// Build the database under a plain path, then place it (under the name an
		// archive carries) in the directory with the awkward name.
		src := t.TempDir()
		st, err := store.Open(src)
		if err != nil {
			t.Fatalf("%s: open store: %v", name, err)
		}
		if err := st.SetSetting(proxyAuthEnabledKey, "1"); err != nil {
			t.Fatal(err)
		}
		st.Close()
		dir := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(src, openedProjectDBName), filepath.Join(dir, archiveDBName)); err != nil {
			t.Fatal(err)
		}

		if err := validateImportedProject(dir); err != nil {
			t.Errorf("%s: validateImportedProject: %v", name, err)
		}
		// The directory must not have gained a stray truncated sibling.
		entries, _ := os.ReadDir(filepath.Dir(dir))
		if len(entries) != 1 {
			t.Errorf("%s: %d entries next to the project dir, want 1", name, len(entries))
		}
		// The settings read sees the real database: auth on, no password.
		if gaps := restoredCredentialGaps(dir); len(gaps) != 1 || gaps[0] != "proxyAuthPassword" {
			t.Errorf("%s: restoredCredentialGaps = %v, want [proxyAuthPassword]", name, gaps)
		}
	}
}

// End to end: restoring a full archive into a project named with '#' or '?'.
func TestImportFullIntoProjectNamedWithURIMetacharacters(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "app.example.com", Port: 443, Path: "/", HTTPVersion: "HTTP/1.1", Status: 200}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	er, err := http.Get(ts.URL + "/api/export/full")
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := io.ReadAll(er.Body)
	er.Body.Close()

	for _, name := range []string{"client#2", "what?now"} {
		h2, _, _ := newHub(t)
		h2.GlobalDir = t.TempDir()
		ts2 := httptest.NewServer(h2.Handler())
		ir, err := http.Post(ts2.URL+"/api/import/full?name="+url.QueryEscape(name), "application/zip", bytes.NewReader(archive))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(ir.Body)
		ir.Body.Close()
		ts2.Close()
		if ir.StatusCode != http.StatusOK {
			t.Errorf("import into project %q: status %d %s", name, ir.StatusCode, body)
		}
	}
}
