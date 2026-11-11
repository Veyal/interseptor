package store

import (
	"path/filepath"
	"testing"
)

// Two read-only opens of another project's database still built their SQLite
// URI from the raw path, so a directory containing '?' or '#' opened a
// different (missing) file: the collections merge failed and the default
// project's API keys were silently not carried into the global key store.
func TestR2PeerDatabasesOpenFromDirectoriesWithURIMetacharacters(t *testing.T) {
	for _, name := range []string{"client#2", "what?now", "100%done"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()

			// MergeCollectionsFrom: the peer lives in the awkward directory.
			peerDir := filepath.Join(root, name, "peer")
			peer, err := Open(peerDir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := peer.CreateCollection(Collection{Name: "Example API"}); err != nil {
				t.Fatal(err)
			}
			if err := peer.Close(); err != nil {
				t.Fatal(err)
			}
			local, err := Open(filepath.Join(root, "local"))
			if err != nil {
				t.Fatal(err)
			}
			defer local.Close()
			stats, err := local.MergeCollectionsFrom(filepath.Join(peerDir, currentDBName))
			if err != nil {
				t.Fatalf("MergeCollectionsFrom: %v", err)
			}
			cols, err := local.ListCollections()
			if err != nil || len(cols) != 1 || cols[0].Name != "Example API" {
				t.Fatalf("merged collections = %v (stats %+v, err %v), want the peer's one", cols, stats, err)
			}

			// AttachGlobalKeys: the global dir is the awkward directory and its
			// default project already holds a key.
			globalDir := filepath.Join(root, name, "global")
			def, err := Open(globalDir)
			if err != nil {
				t.Fatal(err)
			}
			token, _, err := def.CreateAPIKey("ci", ScopeFull, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := def.Close(); err != nil {
				t.Fatal(err)
			}
			proj, err := Open(filepath.Join(globalDir, "projects", "acme"))
			if err != nil {
				t.Fatal(err)
			}
			defer proj.Close()
			if err := proj.AttachGlobalKeys(globalDir); err != nil {
				t.Fatalf("AttachGlobalKeys: %v", err)
			}
			if ok, _, err := proj.VerifyAPIKeyScope(token); err != nil || !ok {
				t.Fatalf("the default project's key was not carried into the global key store (ok %v, err %v)", ok, err)
			}
		})
	}
}
