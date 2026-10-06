package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// versionedDoc is a JSON project-metadata document stored in the settings
// table whose Version bumps only when its content changes.
type versionedDoc interface {
	// versionMeta exposes the Version and UpdatedAt fields.
	versionMeta() (*int, *int64)
}

type settingQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// loadVersionedSetting unmarshals a JSON setting into dst; reports presence.
func loadVersionedSetting(q settingQuerier, key string, dst any) (bool, error) {
	var raw string
	err := q.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), dst)
}

// saveVersionedSetting loads the stored document into dst, lets mutate set the
// desired content, and persists it in one transaction. The version increments
// (and UpdatedAt refreshes) only when content, compared with the version
// fields zeroed, differs from what was stored or nothing was stored yet.
func (s *Store) saveVersionedSetting(key string, dst versionedDoc, mutate func()) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := loadVersionedSetting(tx, key, dst); err != nil {
		return err
	}
	ver, ts := dst.versionMeta()
	oldVer, oldTS := *ver, *ts
	before, err := contentWithoutMeta(dst)
	if err != nil {
		return err
	}
	mutate()
	*ver, *ts = oldVer, oldTS // callers must not control version fields
	after, err := contentWithoutMeta(dst)
	if err != nil {
		return err
	}
	if oldVer > 0 && string(before) == string(after) {
		return tx.Commit()
	}
	*ver, *ts = oldVer+1, time.Now().UnixMilli()
	raw, err := json.Marshal(dst)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

func contentWithoutMeta(d versionedDoc) ([]byte, error) {
	ver, ts := d.versionMeta()
	ov, ot := *ver, *ts
	*ver, *ts = 0, 0
	raw, err := json.Marshal(d)
	*ver, *ts = ov, ot
	return raw, err
}
