package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ScrubOptions controls scrubSecrets. IncludeSecrets keeps variable current
// values, tokens and cookies and any literal credentials in auth/headers; the
// caller must have obtained explicit confirmation and record it in the
// manifest. Script trust is reset in every case: trust never travels.
type ScrubOptions struct {
	IncludeSecrets bool
}

// ScrubReport says what a scrub did, for the export manifest.
type ScrubReport struct {
	IncludedSecrets bool `json:"includedSecrets"`
	SecretsBlanked  int  `json:"secretsBlanked"`
	RowsDeleted     int  `json:"rowsDeleted"`
}

var templateRefRe = regexp.MustCompile(`^\s*\{\{[^{}]+\}\}\s*$`)

// nonSecretSuffixes mark names that hold addresses or labels, not secrets.
var nonSecretSuffixes = []string{"url", "uri", "type", "prefix", "name", "location", "placement", "endpoint", "scheme", "algorithm", "expiry", "expires"}

var secretNameParts = []string{"token", "secret", "password", "passwd", "passphrase", "apikey", "api-key", "api_key",
	"authorization", "cookie", "privatekey", "private_key", "session", "signature", "credential"}

// IsSecretName reports whether a variable, header or parameter name designates
// a secret (token, password, signature, ...). It is the single name heuristic of
// the scrub; callers that declare variables on a script's behalf use it so a
// credential a script writes is stored as a secret, not a shareable value.
func IsSecretName(name string) bool { return secretName(name) }

// secretName reports whether a key/header/param name designates a secret.
func secretName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	for _, suf := range nonSecretSuffixes {
		if strings.HasSuffix(n, suf) {
			return false
		}
	}
	for _, p := range secretNameParts {
		if strings.Contains(n, p) {
			return true
		}
	}
	return false
}

func blankable(v any) bool {
	s, ok := v.(string)
	return ok && s != "" && !templateRefRe.MatchString(s)
}

// scrubValue walks decoded JSON blanking literal secrets. A leaf is blanked
// when its own key is a secret name, or when it is the "value" of an object
// whose "key"/"name" is a secret name (Postman key/value pairs). When
// authCtx is true a pair keyed "value" is also blanked (apikey auth stores
// the secret under the literal key "value"). {{template}} references stay.
func scrubValue(v any, authCtx bool, n *int) any {
	switch t := v.(type) {
	case []any:
		for i := range t {
			t[i] = scrubValue(t[i], authCtx, n)
		}
		return t
	case map[string]any:
		pairSecret := false
		for _, kk := range []string{"key", "name"} {
			if s, ok := t[kk].(string); ok && (secretName(s) || (authCtx && kk == "key" && strings.EqualFold(s, "value"))) {
				pairSecret = true
			}
		}
		for k, val := range t {
			switch {
			case pairSecret && k == "value" && blankable(val):
				t[k] = ""
				*n++
			case secretName(k) && blankable(val):
				t[k] = ""
				*n++
			default:
				t[k] = scrubValue(val, authCtx, n)
			}
		}
		return t
	}
	return v
}

// scrubJSON scrubs one stored JSON column; invalid or empty input is
// returned unchanged. It is the single JSON-level scrub used by archive,
// vault, bundle and merge paths.
func scrubJSON(raw string, authCtx bool) (string, int) {
	if raw == "" {
		return raw, 0
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw, 0
	}
	n := 0
	v = scrubValue(v, authCtx, &n)
	if n == 0 {
		return raw, 0
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw, 0
	}
	return string(out), n
}

func scrubRaw(r *json.RawMessage, authCtx bool) int {
	out, n := scrubJSON(string(*r), authCtx)
	if n > 0 {
		*r = json.RawMessage(out)
	}
	return n
}

// scrubItemSecrets blanks literal credentials in an item's auth, headers and
// params. Returns the number of blanked values.
func scrubItemSecrets(it *Item) int {
	return scrubRaw(&it.Auth, true) + scrubRaw(&it.Headers, false) + scrubRaw(&it.Params, false)
}

func scrubCollectionSecrets(c *Collection) int { return scrubRaw(&c.Auth, true) }

func scrubVariables(vs []Variable) int {
	n := 0
	for i := range vs {
		if vs[i].Type == VarTypeSecret && vs[i].InitialValue != "" {
			vs[i].InitialValue = ""
			n++
		}
	}
	return n
}

// scrubSecrets is THE secret scrub for a project database snapshot. Every
// export path (full archive, vault push, portable export) must run exactly
// this on its copy before the copy leaves the machine. It deletes variable
// current values, OAuth tokens and cookies, blanks secret initial values and
// literal credentials in auth/header/param JSON (including stored item
// revisions), and always resets script trust. db must be a private snapshot.
func scrubSecrets(db *sql.DB, opt ScrubOptions) (ScrubReport, error) {
	rep := ScrubReport{IncludedSecrets: opt.IncludeSecrets}
	has := func(t string) bool { ok, _ := peerHasTable(db, t); return ok }
	del := func(table string) error {
		if !has(table) {
			return nil
		}
		res, err := db.Exec(`DELETE FROM ` + table)
		if err == nil {
			n, _ := res.RowsAffected()
			rep.RowsDeleted += int(n)
		}
		return err
	}
	if err := del("ix_script_trust"); err != nil {
		return rep, err
	}
	if opt.IncludeSecrets {
		return rep, nil
	}
	for _, t := range []string{"ix_var_current", "ix_tokens", "ix_cookies"} {
		if err := del(t); err != nil {
			return rep, err
		}
	}
	if has("ix_variables") {
		res, err := db.Exec(`UPDATE ix_variables SET initial_value='' WHERE type='secret' AND initial_value<>''`)
		if err != nil {
			return rep, err
		}
		n, _ := res.RowsAffected()
		rep.SecretsBlanked += int(n)
	}
	if has("ix_collections") {
		cols, err := loadCollections(db)
		if err != nil {
			return rep, err
		}
		for _, c := range cols {
			if n := scrubCollectionSecrets(&c); n > 0 {
				rep.SecretsBlanked += n
				if _, err := db.Exec(`UPDATE ix_collections SET auth_json=? WHERE uid=?`, string(c.Auth), c.UID); err != nil {
					return rep, err
				}
			}
		}
	}
	if has("ix_items") {
		rows, err := db.Query(`SELECT ` + itemCols + ` FROM ix_items`)
		if err != nil {
			return rep, err
		}
		var items []Item
		for rows.Next() {
			it, err := scanItem(rows)
			if err != nil {
				rows.Close()
				return rep, err
			}
			items = append(items, *it)
		}
		rows.Close()
		for _, it := range items {
			if n := scrubItemSecrets(&it); n > 0 {
				rep.SecretsBlanked += n
				if _, err := db.Exec(`UPDATE ix_items SET auth_json=?,headers_json=?,params_json=? WHERE uid=?`,
					string(it.Auth), string(it.Headers), string(it.Params), it.UID); err != nil {
					return rep, err
				}
			}
		}
	}
	if has("ix_item_revisions") {
		if err := scrubRevisions(db, &rep); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func scrubRevisions(db *sql.DB, rep *ScrubReport) error {
	rows, err := db.Query(`SELECT id,snapshot FROM ix_item_revisions`)
	if err != nil {
		return err
	}
	type upd struct {
		id   int64
		snap string
	}
	var ups []upd
	for rows.Next() {
		var id int64
		var snap string
		if err := rows.Scan(&id, &snap); err != nil {
			rows.Close()
			return err
		}
		var it Item
		if json.Unmarshal([]byte(snap), &it) != nil {
			// Unparseable snapshot: cannot prove it is clean, drop it.
			ups = append(ups, upd{id, ""})
			continue
		}
		if n := scrubItemSecrets(&it); n > 0 {
			b, _ := json.Marshal(&it)
			ups = append(ups, upd{id, string(b)})
			rep.SecretsBlanked += n
		}
	}
	rows.Close()
	for _, u := range ups {
		var err error
		if u.snap == "" {
			_, err = db.Exec(`DELETE FROM ix_item_revisions WHERE id=?`, u.id)
		} else {
			_, err = db.Exec(`UPDATE ix_item_revisions SET snapshot=? WHERE id=?`, u.snap, u.id)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ScrubSnapshotFile opens a private DB snapshot at path, runs scrubSecrets
// and vacuums with secure_delete so no scrubbed byte survives in free pages.
func ScrubSnapshotFile(path string, opt ScrubOptions) (ScrubReport, error) {
	var rep ScrubReport
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return rep, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, p := range []string{`PRAGMA journal_mode=DELETE`, `PRAGMA secure_delete=ON`} {
		if _, err := db.Exec(p); err != nil {
			return rep, err
		}
	}
	rep, err = scrubSecrets(db, opt)
	if err != nil {
		return rep, err
	}
	if _, err := db.Exec(`VACUUM`); err != nil {
		return rep, fmt.Errorf("vacuum scrubbed snapshot: %w", err)
	}
	return rep, nil
}

// BackupToScrubbed is BackupTo followed by ScrubSnapshotFile on the copy. On
// any scrub failure the half-scrubbed copy is removed. Archive and vault
// callers must use this (or ScrubSnapshotFile) instead of BackupTo.
func (s *Store) BackupToScrubbed(destPath string, opt ScrubOptions) (ScrubReport, error) {
	if err := s.BackupTo(destPath); err != nil {
		return ScrubReport{}, err
	}
	rep, err := ScrubSnapshotFile(destPath, opt)
	if err != nil {
		os.Remove(destPath)
		return rep, err
	}
	return rep, nil
}
