package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// CollectionsBundleVersion is the portable bundle schema version.
const CollectionsBundleVersion = 1

// CollectionsBundle is the portable collections section of a project bundle
// (and the native interchange between peers). Variables carry initial values
// only; current values, cookies, tokens, trust and runs never travel.
type CollectionsBundle struct {
	Version      int           `json:"version"`
	Collections  []Collection  `json:"collections"`
	Items        []Item        `json:"items"`
	Environments []Environment `json:"environments"`
	Variables    []Variable    `json:"variables"`
}

type collQuerier interface {
	Query(string, ...any) (*sql.Rows, error)
}

func loadCollections(q collQuerier) ([]Collection, error) {
	rows, err := q.Query(`SELECT ` + collCols + ` FROM ix_collections ORDER BY uid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Collection
	for rows.Next() {
		c, err := scanCollection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// loadCollBundle reads the collections sections from a database, tolerating
// peers that lack any of the tables (older versions).
func loadCollBundle(db *sql.DB) (CollectionsBundle, error) {
	b := CollectionsBundle{Version: CollectionsBundleVersion}
	var err error
	if ok, _ := peerHasTable(db, "ix_collections"); ok {
		if b.Collections, err = loadCollections(db); err != nil {
			return b, err
		}
	}
	if ok, _ := peerHasTable(db, "ix_items"); ok {
		rows, err := db.Query(`SELECT ` + itemCols + ` FROM ix_items ORDER BY collection_uid, parent_uid, rank, uid`)
		if err != nil {
			return b, err
		}
		for rows.Next() {
			it, err := scanItem(rows)
			if err != nil {
				rows.Close()
				return b, err
			}
			b.Items = append(b.Items, *it)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return b, err
		}
		rows.Close()
	}
	if ok, _ := peerHasTable(db, "ix_environments"); ok {
		rows, err := db.Query(`SELECT ` + envCols + ` FROM ix_environments ORDER BY uid`)
		if err != nil {
			return b, err
		}
		for rows.Next() {
			e, err := scanEnv(rows)
			if err != nil {
				rows.Close()
				return b, err
			}
			b.Environments = append(b.Environments, *e)
		}
		rows.Close()
	}
	if ok, _ := peerHasTable(db, "ix_variables"); ok {
		rows, err := db.Query(`SELECT owner_kind,owner_uid,key,type,initial_value,enabled FROM ix_variables ORDER BY owner_kind,owner_uid,key`)
		if err != nil {
			return b, err
		}
		for rows.Next() {
			var v Variable
			var en int
			if err := rows.Scan(&v.OwnerKind, &v.OwnerUID, &v.Key, &v.Type, &v.InitialValue, &en); err != nil {
				rows.Close()
				return b, err
			}
			v.Enabled = en != 0
			b.Variables = append(b.Variables, v)
		}
		rows.Close()
	}
	return b, nil
}

// scrubBundle applies the shared scrub to an in-memory bundle.
func scrubBundle(b *CollectionsBundle) {
	for i := range b.Collections {
		scrubCollectionSecrets(&b.Collections[i])
	}
	for i := range b.Items {
		scrubItemSecrets(&b.Items[i])
	}
	scrubVariables(b.Variables)
}

// ExportCollectionsBundle returns the portable collections section with
// secrets scrubbed unless opt.IncludeSecrets (which needs caller-side
// confirmation).
func (s *Store) ExportCollectionsBundle(opt ScrubOptions) (CollectionsBundle, error) {
	if err := s.ensureCollections(); err != nil {
		return CollectionsBundle{}, err
	}
	b, err := loadCollBundle(s.db)
	if err != nil {
		return b, err
	}
	if !opt.IncludeSecrets {
		scrubBundle(&b)
	}
	return b, nil
}

// ImportCollectionsBundle merges a bundle (possibly empty or from an older
// version that had no collections section) into this project using the same
// rules as project merge: imported scripts are quarantined (trust is not
// carried), caps are default-deny, scope policy off is downgraded to block.
func (s *Store) ImportCollectionsBundle(b CollectionsBundle) (CollectionMergeStats, error) {
	if b.Version > CollectionsBundleVersion {
		return CollectionMergeStats{}, fmt.Errorf("collections bundle version %d is newer than supported %d", b.Version, CollectionsBundleVersion)
	}
	st, _, err := s.mergeCollBundle(b, true)
	return st, err
}

// ImportUserCollectionsBundle merges a bundle the user deliberately imported
// from their own file (Postman, Insomnia, Bruno, HAR, Burp, OpenAPI, curl).
// Unlike ImportCollectionsBundle it keeps literal credentials: they are the
// user's own data, real secret values already live at rest in ix_var_current,
// and blanking them on the way in silently turns every authenticated request
// into a 401. Every export path still scrubs (ExportCollectionsBundle,
// BackupToScrubbed), so keeping them at rest does not widen what leaves the
// project. The skips name the requests that were not imported because an
// identical one already exists. Everything else (quarantined scripts, default-deny caps, scope
// policy off downgraded to block) is identical to ImportCollectionsBundle.
func (s *Store) ImportUserCollectionsBundle(b CollectionsBundle) (CollectionMergeStats, []MergeSkip, error) {
	if b.Version > CollectionsBundleVersion {
		return CollectionMergeStats{}, nil, fmt.Errorf("collections bundle version %d is newer than supported %d", b.Version, CollectionsBundleVersion)
	}
	return s.mergeCollBundle(b, false)
}

// DecodeCollectionsBundle parses a bundle section; null/empty yields an empty
// bundle so old project bundles import cleanly.
func DecodeCollectionsBundle(raw json.RawMessage) (CollectionsBundle, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return CollectionsBundle{Version: CollectionsBundleVersion}, nil
	}
	var b CollectionsBundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return b, fmt.Errorf("invalid collections bundle: %w", err)
	}
	return b, nil
}
