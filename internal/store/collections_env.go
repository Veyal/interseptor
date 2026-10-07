package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Variable owner kinds.
const (
	VarOwnerGlobal      = "global"
	VarOwnerCollection  = "collection"
	VarOwnerFolder      = "folder"
	VarOwnerRequest     = "request"
	VarOwnerEnvironment = "environment"
)

// Variable types.
const (
	VarTypeDefault = "default"
	VarTypeSecret  = "secret"
	VarTypeAny     = "any"
)

// Environment is a named variable set (kind env) or the globals holder.
type Environment struct {
	UID           string `json:"uid"`
	Name          string `json:"name"`
	Kind          string `json:"kind"` // env | globals
	CollectionUID string `json:"collectionUid,omitempty"`
	BoundIdentity string `json:"boundIdentity,omitempty"`
	BaseTargetPin string `json:"baseTargetPin,omitempty"`
	Rev           int64  `json:"rev"`
	TS            int64  `json:"ts"`
}

// Variable is a declared variable with its shareable initial value. Secret
// variables never carry an initial value: it is forced blank on write.
type Variable struct {
	OwnerKind    string `json:"ownerKind"`
	OwnerUID     string `json:"ownerUid"`
	Key          string `json:"key"`
	Type         string `json:"type"`
	InitialValue string `json:"initialValue"`
	Enabled      bool   `json:"enabled"`
}

// VarCurrent is the local-only current value of a variable.
type VarCurrent struct {
	OwnerKind string `json:"ownerKind"`
	OwnerUID  string `json:"ownerUid"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedBy string `json:"updatedBy,omitempty"`
	TS        int64  `json:"ts"`
}

func validVarOwner(k string) bool {
	switch k {
	case VarOwnerGlobal, VarOwnerCollection, VarOwnerFolder, VarOwnerRequest, VarOwnerEnvironment:
		return true
	}
	return false
}

// CreateEnvironment inserts an environment (empty UID gets a ULID).
func (s *Store) CreateEnvironment(e Environment) (*Environment, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(e.Name) == "" {
		return nil, fmt.Errorf("%w: name required", ErrCollInvalid)
	}
	if e.Kind == "" {
		e.Kind = "env"
	}
	if e.Kind != "env" && e.Kind != "globals" {
		return nil, fmt.Errorf("%w: environment kind %q", ErrCollInvalid, e.Kind)
	}
	if e.UID == "" {
		e.UID = NewUID()
	}
	e.Rev, e.TS = 1, nowMs()
	_, err := s.db.Exec(`INSERT INTO ix_environments(uid,name,kind,collection_uid,bound_identity,base_target_pin,rev,ts) VALUES(?,?,?,?,?,?,?,?)`,
		e.UID, e.Name, e.Kind, e.CollectionUID, e.BoundIdentity, e.BaseTargetPin, e.Rev, e.TS)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

const envCols = `uid,name,kind,collection_uid,bound_identity,base_target_pin,rev,ts`

func scanEnv(r rowScanner) (*Environment, error) {
	var e Environment
	err := r.Scan(&e.UID, &e.Name, &e.Kind, &e.CollectionUID, &e.BoundIdentity, &e.BaseTargetPin, &e.Rev, &e.TS)
	return &e, err
}

// GetEnvironment returns one environment or ErrCollNotFound.
func (s *Store) GetEnvironment(uid string) (*Environment, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	e, err := scanEnv(s.db.QueryRow(`SELECT `+envCols+` FROM ix_environments WHERE uid=?`, uid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCollNotFound
	}
	return e, err
}

// ListEnvironments returns all environments ordered by name.
func (s *Store) ListEnvironments() ([]Environment, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT ` + envCols + ` FROM ix_environments ORDER BY name COLLATE NOCASE, uid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Environment{}
	for rows.Next() {
		e, err := scanEnv(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// UpdateEnvironment updates name, binding and pin. A non-zero e.Rev must match.
func (s *Store) UpdateEnvironment(e Environment) (*Environment, error) {
	cur, err := s.GetEnvironment(e.UID)
	if err != nil {
		return nil, err
	}
	if e.Rev != 0 && e.Rev != cur.Rev {
		return nil, ErrCollConflict
	}
	if strings.TrimSpace(e.Name) == "" {
		return nil, fmt.Errorf("%w: name required", ErrCollInvalid)
	}
	e.Kind, e.CollectionUID = cur.Kind, cur.CollectionUID
	e.Rev, e.TS = cur.Rev+1, nowMs()
	res, err := s.db.Exec(`UPDATE ix_environments SET name=?,bound_identity=?,base_target_pin=?,rev=?,ts=? WHERE uid=? AND rev=?`,
		e.Name, e.BoundIdentity, e.BaseTargetPin, e.Rev, e.TS, e.UID, cur.Rev)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrCollConflict
	}
	return &e, nil
}

// DeleteEnvironment removes an environment with its variables and current values.
func (s *Store) DeleteEnvironment(uid string) error {
	if _, err := s.GetEnvironment(uid); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM ix_variables WHERE owner_kind='environment' AND owner_uid=?`,
		`DELETE FROM ix_var_current WHERE owner_kind='environment' AND owner_uid=?`,
		`DELETE FROM ix_environments WHERE uid=?`,
	} {
		if _, err := tx.Exec(q, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetVariables replaces all declared variables of one owner atomically.
// Secret variables have their initial value blanked. Current values of keys
// that no longer exist are dropped.
func (s *Store) SetVariables(ownerKind, ownerUID string, vars []Variable) error {
	if err := s.ensureCollections(); err != nil {
		return err
	}
	if !validVarOwner(ownerKind) {
		return fmt.Errorf("%w: owner kind %q", ErrCollInvalid, ownerKind)
	}
	seen := map[string]bool{}
	for _, v := range vars {
		if v.Key == "" || seen[v.Key] {
			return fmt.Errorf("%w: empty or duplicate variable key %q", ErrCollInvalid, v.Key)
		}
		seen[v.Key] = true
		if v.Type != "" && v.Type != VarTypeDefault && v.Type != VarTypeSecret && v.Type != VarTypeAny {
			return fmt.Errorf("%w: variable type %q", ErrCollInvalid, v.Type)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM ix_variables WHERE owner_kind=? AND owner_uid=?`, ownerKind, ownerUID); err != nil {
		return err
	}
	for _, v := range vars {
		typ, init := v.Type, v.InitialValue
		if typ == "" {
			typ = VarTypeDefault
		}
		if typ == VarTypeSecret {
			init = ""
		}
		if _, err := tx.Exec(`INSERT INTO ix_variables(owner_kind,owner_uid,key,type,initial_value,enabled) VALUES(?,?,?,?,?,?)`,
			ownerKind, ownerUID, v.Key, typ, init, b2i(v.Enabled)); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT key FROM ix_var_current WHERE owner_kind=? AND owner_uid=?`, ownerKind, ownerUID)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return err
		}
		if !seen[k] {
			stale = append(stale, k)
		}
	}
	rows.Close()
	for _, k := range stale {
		if _, err := tx.Exec(`DELETE FROM ix_var_current WHERE owner_kind=? AND owner_uid=? AND key=?`, ownerKind, ownerUID, k); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ListVariables returns one owner's variables ordered by key.
func (s *Store) ListVariables(ownerKind, ownerUID string) ([]Variable, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT owner_kind,owner_uid,key,type,initial_value,enabled FROM ix_variables WHERE owner_kind=? AND owner_uid=? ORDER BY key`, ownerKind, ownerUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Variable{}
	for rows.Next() {
		var v Variable
		var en int
		if err := rows.Scan(&v.OwnerKind, &v.OwnerUID, &v.Key, &v.Type, &v.InitialValue, &en); err != nil {
			return nil, err
		}
		v.Enabled = en != 0
		out = append(out, v)
	}
	return out, rows.Err()
}

// SetCurrentValue stores the local current value of a declared variable.
func (s *Store) SetCurrentValue(ownerKind, ownerUID, key, value, updatedBy string) error {
	if err := s.ensureCollections(); err != nil {
		return err
	}
	if !validVarOwner(ownerKind) || key == "" {
		return fmt.Errorf("%w: variable owner/key", ErrCollInvalid)
	}
	_, err := s.db.Exec(`INSERT INTO ix_var_current(owner_kind,owner_uid,key,value,updated_by,ts) VALUES(?,?,?,?,?,?)
 ON CONFLICT(owner_kind,owner_uid,key) DO UPDATE SET value=excluded.value,updated_by=excluded.updated_by,ts=excluded.ts`,
		ownerKind, ownerUID, key, value, updatedBy, nowMs())
	return err
}

// ListCurrentValues returns one owner's current values ordered by key.
func (s *Store) ListCurrentValues(ownerKind, ownerUID string) ([]VarCurrent, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT owner_kind,owner_uid,key,value,updated_by,ts FROM ix_var_current WHERE owner_kind=? AND owner_uid=? ORDER BY key`, ownerKind, ownerUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VarCurrent{}
	for rows.Next() {
		var v VarCurrent
		if err := rows.Scan(&v.OwnerKind, &v.OwnerUID, &v.Key, &v.Value, &v.UpdatedBy, &v.TS); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ResetCurrentValues drops an owner's current values (reset to initial).
func (s *Store) ResetCurrentValues(ownerKind, ownerUID string) error {
	if err := s.ensureCollections(); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM ix_var_current WHERE owner_kind=? AND owner_uid=?`, ownerKind, ownerUID)
	return err
}

// ---- cookies, tokens ----

// CollCookie is a jar cookie (partition "default" or an identity name).
type CollCookie struct {
	Partition string `json:"partition"`
	Domain    string `json:"domain"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Value     string `json:"value"`
	Flags     string `json:"flags,omitempty"`
	Expires   int64  `json:"expires,omitempty"`
}

// PutCookie upserts a cookie.
func (s *Store) PutCookie(c CollCookie) error {
	if err := s.ensureCollections(); err != nil {
		return err
	}
	if c.Domain == "" || c.Name == "" {
		return fmt.Errorf("%w: cookie domain/name", ErrCollInvalid)
	}
	if c.Partition == "" {
		c.Partition = "default"
	}
	if c.Path == "" {
		c.Path = "/"
	}
	_, err := s.db.Exec(`INSERT INTO ix_cookies(partition,domain,path,name,value,flags,expires) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(partition,domain,path,name) DO UPDATE SET value=excluded.value,flags=excluded.flags,expires=excluded.expires`,
		c.Partition, c.Domain, c.Path, c.Name, c.Value, c.Flags, c.Expires)
	return err
}

// ListCookies returns a partition's cookies.
func (s *Store) ListCookies(partition string) ([]CollCookie, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	if partition == "" {
		partition = "default"
	}
	rows, err := s.db.Query(`SELECT partition,domain,path,name,value,flags,expires FROM ix_cookies WHERE partition=? ORDER BY domain,path,name`, partition)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CollCookie{}
	for rows.Next() {
		var c CollCookie
		if err := rows.Scan(&c.Partition, &c.Domain, &c.Path, &c.Name, &c.Value, &c.Flags, &c.Expires); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CollToken is a cached OAuth2 token record (opaque JSON).
type CollToken struct {
	UID           string `json:"uid"`
	CollectionUID string `json:"collectionUid,omitempty"`
	Name          string `json:"name"`
	TokenJSON     string `json:"tokenJson"`
	Expires       int64  `json:"expires,omitempty"`
}

// PutToken upserts a token record.
func (s *Store) PutToken(t CollToken) (*CollToken, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	if t.UID == "" {
		t.UID = NewUID()
	}
	if t.TokenJSON != "" && !jsonValidString(t.TokenJSON) {
		return nil, fmt.Errorf("%w: token is not valid JSON", ErrCollInvalid)
	}
	_, err := s.db.Exec(`INSERT INTO ix_tokens(uid,collection_uid,name,token_json,expires,ts) VALUES(?,?,?,?,?,?)
 ON CONFLICT(uid) DO UPDATE SET collection_uid=excluded.collection_uid,name=excluded.name,token_json=excluded.token_json,expires=excluded.expires,ts=excluded.ts`,
		t.UID, t.CollectionUID, t.Name, t.TokenJSON, t.Expires, nowMs())
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// DeleteToken removes one named token record; a missing one is not an error.
func (s *Store) DeleteToken(collectionUID, name string) error {
	if err := s.ensureCollections(); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM ix_tokens WHERE collection_uid=? AND name=?`, collectionUID, name)
	return err
}

// ListTokens returns a collection's token records.
func (s *Store) ListTokens(collectionUID string) ([]CollToken, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT uid,collection_uid,name,token_json,expires FROM ix_tokens WHERE collection_uid=? ORDER BY name,uid`, collectionUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CollToken{}
	for rows.Next() {
		var t CollToken
		if err := rows.Scan(&t.UID, &t.CollectionUID, &t.Name, &t.TokenJSON, &t.Expires); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
