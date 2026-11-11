package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/harx"
	"github.com/Veyal/interseptor/internal/store"
)

// GET /api/export/har?inScope=1 applied the scope filter after the row limit,
// so in-scope flows older than the newest `limit` rows were silently dropped.
func TestExportHARInScopeFiltersBeforeLimit(t *testing.T) {
	h, st, _ := newHub(t)
	if _, err := st.CreateScopeRule(&store.ScopeRule{Enabled: true, Action: "include", Host: "target.example.com"}); err != nil {
		t.Fatal(err)
	}
	h.refreshScope()
	insert := func(host string, i int) {
		t.Helper()
		if _, err := st.InsertFlow(&store.Flow{
			TS: time.UnixMilli(int64(i + 1)), Method: "GET", Scheme: "https", Host: host, Port: 443,
			Path: fmt.Sprintf("/p%d", i), HTTPVersion: "HTTP/1.1", Status: 200,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		insert("target.example.com", i) // the oldest rows
	}
	for i := 0; i < 20; i++ {
		insert("noise.example.net", 100+i) // newer out-of-scope traffic
	}

	rec := httptest.NewRecorder()
	(&projectAPI{h}).exportHAR(rec, httptest.NewRequest(http.MethodGet, "/api/export/har?inScope=1&limit=5", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	entries, err := harx.Parse(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("parse exported HAR: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("in-scope export returned %d entries, want the 3 in-scope flows", len(entries))
	}
	for _, e := range entries {
		if !strings.Contains(e.URL, "target.example.com") {
			t.Fatalf("out-of-scope entry exported: %s", e.URL)
		}
	}

	// The limit still bounds the in-scope result.
	rec = httptest.NewRecorder()
	(&projectAPI{h}).exportHAR(rec, httptest.NewRequest(http.MethodGet, "/api/export/har?inScope=1&limit=2", nil))
	if entries, err = harx.Parse(rec.Body.Bytes()); err != nil || len(entries) != 2 {
		t.Fatalf("limit=2 in-scope export: %d entries, err %v", len(entries), err)
	}
}

// A project merge adds flows; the cached /api/endpoints aggregate must be
// dropped, like every other import path does.
func TestMergeFileInvalidatesEndpointsCache(t *testing.T) {
	// Peer project with one flow, archived.
	peer, peerStore, _ := newHub(t)
	if _, err := peerStore.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "merged.example.com", Port: 443,
		Path: "/from-peer", HTTPVersion: "HTTP/1.1", Status: 200,
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := peer.snapshotDB()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(snap)
	var archive bytes.Buffer
	if err := buildFullArchive(&archive, snap, peerStore.BodiesDir(), filepath.Join(t.TempDir(), "codecs")); err != nil {
		t.Fatal(err)
	}

	h, _, _ := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	endpointCount := func() int {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/endpoints")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			Endpoints []json.RawMessage `json:"endpoints"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return len(out.Endpoints)
	}
	if c := endpointCount(); c != 0 { // primes the cache while empty
		t.Fatalf("endpoints before merge = %d, want 0", c)
	}
	resp, err := http.Post(ts.URL+"/api/merge/file?label=peer", "application/zip", bytes.NewReader(archive.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("merge status %d", resp.StatusCode)
	}
	if c := endpointCount(); c != 1 {
		t.Fatalf("endpoints after merge = %d, want 1 (stale cached aggregate)", c)
	}
}

// Importing an archive over the project that is currently open swapped its
// directory out from under the running store: the live database was deleted
// when the import finished. It must be refused.
func TestImportFullRefusesOverwritingActiveProject(t *testing.T) {
	src, srcStore, _ := newHub(t)
	if _, err := srcStore.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "archive.example.com", Port: 443, Path: "/", Status: 200,
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := src.snapshotDB()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(snap)
	var archive bytes.Buffer
	if err := buildFullArchive(&archive, snap, srcStore.BodiesDir(), filepath.Join(t.TempDir(), "codecs")); err != nil {
		t.Fatal(err)
	}

	global := t.TempDir()
	activeDir := filepath.Join(global, "projects", "acme")
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(activeDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := New(st, nil, nil, nil, nil)
	t.Cleanup(h.Close)
	h.GlobalDir, h.ProjectDir, h.ProjectName = global, activeDir, "acme"
	if _, err := st.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "live.example.com", Port: 443, Path: "/", Status: 200,
	}); err != nil {
		t.Fatal(err)
	}
	dbInfo, err := os.Stat(filepath.Join(activeDir, openedProjectDBName))
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/import/full?name=ACME&overwrite=1", bytes.NewReader(archive.Bytes()))
	(&projectAPI{h}).importFull(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "currently open") {
		t.Fatalf("overwrite of the open project: status %d body %s, want 400 naming the open project", rec.Code, rec.Body.String())
	}
	after, err := os.Stat(filepath.Join(activeDir, openedProjectDBName))
	if err != nil || !os.SameFile(dbInfo, after) {
		t.Fatalf("active project database was replaced (err %v)", err)
	}

	// Another name still imports.
	rec = httptest.NewRecorder()
	(&projectAPI{h}).importFull(rec, httptest.NewRequest(http.MethodPost, "/api/import/full?name=copy", bytes.NewReader(archive.Bytes())))
	if rec.Code != http.StatusOK {
		t.Fatalf("import under a new name: status %d body %s", rec.Code, rec.Body.String())
	}
}

// A project that has been opened holds interseptor.db (store.Open renames the
// archive's interceptor.db). dirHasProject only looked for the archive name, so
// importing onto such a project skipped the 409 and overwrite=1 failed.
func TestImportFullSeesOpenedProjectAsExisting(t *testing.T) {
	src, srcStore, _ := newHub(t)
	if _, err := srcStore.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "archive.example.com", Port: 443, Path: "/", Status: 200,
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := src.snapshotDB()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(snap)
	var archive bytes.Buffer
	if err := buildFullArchive(&archive, snap, srcStore.BodiesDir(), filepath.Join(t.TempDir(), "codecs")); err != nil {
		t.Fatal(err)
	}

	h, _, _ := newHub(t)
	h.GlobalDir = t.TempDir()
	dest := filepath.Join(h.GlobalDir, "projects", "acme")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	opened, err := store.Open(dest) // an existing project that has been used
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opened.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "old.example.com", Port: 443, Path: "/", Status: 200,
	}); err != nil {
		t.Fatal(err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	post := func(query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		(&projectAPI{h}).importFull(rec, httptest.NewRequest(http.MethodPost, "/api/import/full?"+query, bytes.NewReader(archive.Bytes())))
		return rec
	}
	if rec := post("name=acme"); rec.Code != http.StatusConflict {
		t.Fatalf("import onto an opened project without overwrite: status %d body %s, want 409", rec.Code, rec.Body.String())
	}
	if rec := post("name=acme&overwrite=1"); rec.Code != http.StatusOK {
		t.Fatalf("overwrite of an opened project: status %d body %s, want 200", rec.Code, rec.Body.String())
	}
	replaced, err := store.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer replaced.Close()
	flows, err := replaced.QueryFlows(10)
	if err != nil || len(flows) != 1 || flows[0].Host != "archive.example.com" {
		t.Fatalf("project after overwrite: %d flows (err %v), want only the archived flow", len(flows), err)
	}
}
