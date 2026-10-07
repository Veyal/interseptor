package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Scope policies for a collection.
const (
	ScopePolicyBlock = "block"
	ScopePolicyWarn  = "warn"
	ScopePolicyOff   = "off"
)

const maxItemRevisions = 50

// Collection is a saved-request tree root. JSON columns are opaque to store.
type Collection struct {
	UID          string          `json:"uid"`
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	Auth         json.RawMessage `json:"auth,omitempty"`
	Events       json.RawMessage `json:"events,omitempty"`
	Settings     json.RawMessage `json:"settings,omitempty"`
	Caps         json.RawMessage `json:"caps,omitempty"`
	ScopePolicy  string          `json:"scopePolicy"`
	Sidecar      json.RawMessage `json:"sidecar,omitempty"`
	KeyOrder     json.RawMessage `json:"keyOrder,omitempty"`
	ImportReport json.RawMessage `json:"importReport,omitempty"`
	Rev          int64           `json:"rev"`
	TS           int64           `json:"ts"`
}

// Item is a folder or request inside a collection.
type Item struct {
	UID           string          `json:"uid"`
	CollectionUID string          `json:"collectionUid"`
	ParentUID     string          `json:"parentUid,omitempty"`
	Kind          string          `json:"kind"` // folder | request
	Rank          string          `json:"rank"`
	Name          string          `json:"name"`
	DescriptionMD string          `json:"descriptionMd,omitempty"`
	Method        string          `json:"method,omitempty"`
	URL           json.RawMessage `json:"url,omitempty"`
	Headers       json.RawMessage `json:"headers,omitempty"` // ordered, duplicates allowed
	Params        json.RawMessage `json:"params,omitempty"`
	Body          json.RawMessage `json:"body,omitempty"`
	Auth          json.RawMessage `json:"auth,omitempty"`
	Events        json.RawMessage `json:"events,omitempty"`
	Vars          json.RawMessage `json:"vars,omitempty"`
	Assertions    json.RawMessage `json:"assertions,omitempty"`
	Settings      json.RawMessage `json:"settings,omitempty"`
	Examples      json.RawMessage `json:"examples,omitempty"`
	Tags          json.RawMessage `json:"tags,omitempty"`
	Sidecar       json.RawMessage `json:"sidecar,omitempty"`
	KeyOrder      json.RawMessage `json:"keyOrder,omitempty"`
	Rev           int64           `json:"rev"`
	TS            int64           `json:"ts"`
}

// CollChange identifies who changed an item (recorded in its revisions).
type CollChange struct {
	Actor  string `json:"actor"`
	Source string `json:"source"`
}

// ItemRevision is a prior state of an item.
type ItemRevision struct {
	ID       int64  `json:"id"`
	ItemUID  string `json:"itemUid"`
	Rev      int64  `json:"rev"`
	TS       int64  `json:"ts"`
	Actor    string `json:"actor"`
	Source   string `json:"source"`
	Snapshot *Item  `json:"snapshot,omitempty"`
}

const collCols = `uid,name,description,auth_json,events_json,settings_json,caps_json,scope_policy,sidecar_json,key_order_json,import_report_json,rev,ts`

func (c *Collection) rawCols() []*json.RawMessage {
	return []*json.RawMessage{&c.Auth, &c.Events, &c.Settings, &c.Caps, &c.Sidecar, &c.KeyOrder, &c.ImportReport}
}

var collRawNames = []string{"auth", "events", "settings", "caps", "sidecar", "keyOrder", "importReport"}

type rowScanner interface{ Scan(...any) error }

func scanCollection(r rowScanner) (*Collection, error) {
	var c Collection
	var raws [7]string
	if err := r.Scan(&c.UID, &c.Name, &c.Description, &raws[0], &raws[1], &raws[2], &raws[3], &c.ScopePolicy,
		&raws[4], &raws[5], &raws[6], &c.Rev, &c.TS); err != nil {
		return nil, err
	}
	for i, p := range c.rawCols() {
		*p = rawOut(raws[i])
	}
	return &c, nil
}

func validScopePolicy(p string) bool {
	return p == ScopePolicyBlock || p == ScopePolicyWarn || p == ScopePolicyOff
}

func collArgs(c *Collection) ([]any, error) {
	args := []any{c.UID, c.Name, c.Description}
	cols := c.rawCols()
	vals := make([]string, len(cols))
	for i, p := range cols {
		v, err := rawArg(collRawNames[i], *p)
		if err != nil {
			return nil, err
		}
		vals[i] = v
	}
	args = append(args, vals[0], vals[1], vals[2], vals[3], c.ScopePolicy, vals[4], vals[5], vals[6])
	return args, nil
}

// CreateCollection inserts a collection. Empty UID gets a new ULID; empty
// ScopePolicy defaults to "block" (the safe default for runner/CLI/MCP).
func (s *Store) CreateCollection(c Collection) (*Collection, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.Name) == "" {
		return nil, fmt.Errorf("%w: name required", ErrCollInvalid)
	}
	if c.UID == "" {
		c.UID = NewUID()
	}
	if c.ScopePolicy == "" {
		c.ScopePolicy = ScopePolicyBlock
	}
	if !validScopePolicy(c.ScopePolicy) {
		return nil, fmt.Errorf("%w: scope policy %q", ErrCollInvalid, c.ScopePolicy)
	}
	args, err := collArgs(&c)
	if err != nil {
		return nil, err
	}
	c.Rev, c.TS = 1, nowMs()
	args = append(args, c.Rev, c.TS)
	if _, err := s.db.Exec(`INSERT INTO ix_collections(`+collCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...); err != nil {
		return nil, err
	}
	return &c, nil
}

// GetCollection returns one collection or ErrCollNotFound.
func (s *Store) GetCollection(uid string) (*Collection, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	c, err := scanCollection(s.db.QueryRow(`SELECT `+collCols+` FROM ix_collections WHERE uid=?`, uid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCollNotFound
	}
	return c, err
}

// ListCollections returns all collections ordered by name.
func (s *Store) ListCollections() ([]Collection, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT ` + collCols + ` FROM ix_collections ORDER BY name COLLATE NOCASE, uid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Collection{}
	for rows.Next() {
		c, err := scanCollection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// UpdateCollection replaces a collection's mutable fields. If c.Rev is
// non-zero it must match the stored rev (optimistic concurrency).
func (s *Store) UpdateCollection(c Collection) (*Collection, error) {
	cur, err := s.GetCollection(c.UID)
	if err != nil {
		return nil, err
	}
	if c.Rev != 0 && c.Rev != cur.Rev {
		return nil, ErrCollConflict
	}
	if c.ScopePolicy == "" {
		c.ScopePolicy = cur.ScopePolicy
	}
	if !validScopePolicy(c.ScopePolicy) || strings.TrimSpace(c.Name) == "" {
		return nil, fmt.Errorf("%w: name/scope policy", ErrCollInvalid)
	}
	args, err := collArgs(&c)
	if err != nil {
		return nil, err
	}
	c.Rev, c.TS = cur.Rev+1, nowMs()
	res, err := s.db.Exec(`UPDATE ix_collections SET name=?,description=?,auth_json=?,events_json=?,settings_json=?,caps_json=?,scope_policy=?,sidecar_json=?,key_order_json=?,import_report_json=?,rev=?,ts=? WHERE uid=? AND rev=?`,
		append(args[1:], c.Rev, c.TS, c.UID, cur.Rev)...)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrCollConflict
	}
	return &c, nil
}

// DeleteCollection removes a collection and everything hanging off it
// (items, revisions, bound environments and their variables, trust, tokens,
// datasets, assets, import log). Runs are kept as history.
func (s *Store) DeleteCollection(uid string) error {
	if _, err := s.GetCollection(uid); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`DELETE FROM ix_item_revisions WHERE item_uid IN (SELECT uid FROM ix_items WHERE collection_uid=?1)`,
		`DELETE FROM ix_variables WHERE owner_kind='environment' AND owner_uid IN (SELECT uid FROM ix_environments WHERE collection_uid=?1)`,
		`DELETE FROM ix_var_current WHERE owner_kind='environment' AND owner_uid IN (SELECT uid FROM ix_environments WHERE collection_uid=?1)`,
		`DELETE FROM ix_variables WHERE (owner_kind IN ('collection') AND owner_uid=?1) OR (owner_kind IN ('folder','request') AND owner_uid IN (SELECT uid FROM ix_items WHERE collection_uid=?1))`,
		`DELETE FROM ix_var_current WHERE (owner_kind IN ('collection') AND owner_uid=?1) OR (owner_kind IN ('folder','request') AND owner_uid IN (SELECT uid FROM ix_items WHERE collection_uid=?1))`,
		`DELETE FROM ix_environments WHERE collection_uid=?1`,
		`DELETE FROM ix_items WHERE collection_uid=?1`,
		`DELETE FROM ix_script_trust WHERE collection_uid=?1`,
		`DELETE FROM ix_tokens WHERE collection_uid=?1`,
		`DELETE FROM ix_datasets WHERE collection_uid=?1`,
		`DELETE FROM ix_assets WHERE collection_uid=?1`,
		`DELETE FROM ix_import_log WHERE collection_uid=?1`,
		`DELETE FROM ix_collections WHERE uid=?1`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- items ----

const itemCols = `uid,collection_uid,parent_uid,kind,rank,name,description_md,method,url_json,headers_json,params_json,body_json,auth_json,events_json,vars_json,assertions_json,settings_json,examples_json,tags_json,sidecar_json,key_order_json,rev,ts`

var itemRawNames = []string{"url", "headers", "params", "body", "auth", "events", "vars", "assertions", "settings", "examples", "tags", "sidecar", "keyOrder"}

func (i *Item) rawCols() []*json.RawMessage {
	return []*json.RawMessage{&i.URL, &i.Headers, &i.Params, &i.Body, &i.Auth, &i.Events, &i.Vars,
		&i.Assertions, &i.Settings, &i.Examples, &i.Tags, &i.Sidecar, &i.KeyOrder}
}

func scanItem(r rowScanner) (*Item, error) {
	var it Item
	var raws [13]string
	dest := []any{&it.UID, &it.CollectionUID, &it.ParentUID, &it.Kind, &it.Rank, &it.Name, &it.DescriptionMD, &it.Method}
	for i := range raws {
		dest = append(dest, &raws[i])
	}
	dest = append(dest, &it.Rev, &it.TS)
	if err := r.Scan(dest...); err != nil {
		return nil, err
	}
	for i, p := range it.rawCols() {
		*p = rawOut(raws[i])
	}
	return &it, nil
}

func itemArgs(it *Item) ([]any, error) {
	args := []any{it.UID, it.CollectionUID, it.ParentUID, it.Kind, it.Rank, it.Name, it.DescriptionMD, it.Method}
	for i, p := range it.rawCols() {
		v, err := rawArg(itemRawNames[i], *p)
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	return args, nil
}

func (s *Store) lastSiblingRank(q interface {
	QueryRow(string, ...any) *sql.Row
}, coll, parent string) string {
	var r sql.NullString
	_ = q.QueryRow(`SELECT MAX(rank) FROM ix_items WHERE collection_uid=? AND parent_uid=?`, coll, parent).Scan(&r)
	return r.String
}

func (s *Store) validateItemPlacement(tx *sql.Tx, it *Item) error {
	if it.Kind != "folder" && it.Kind != "request" {
		return fmt.Errorf("%w: kind %q", ErrCollInvalid, it.Kind)
	}
	if it.ParentUID == "" {
		return nil
	}
	var kind, coll string
	err := tx.QueryRow(`SELECT kind, collection_uid FROM ix_items WHERE uid=?`, it.ParentUID).Scan(&kind, &coll)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: parent not found", ErrCollInvalid)
	}
	if err != nil {
		return err
	}
	if kind != "folder" || coll != it.CollectionUID {
		return fmt.Errorf("%w: parent must be a folder in the same collection", ErrCollInvalid)
	}
	// Moving a folder under itself or a descendant would orphan a cycle.
	if it.UID != "" {
		var n int
		err = tx.QueryRow(`WITH RECURSIVE anc(uid) AS (SELECT ?1 UNION SELECT i.parent_uid FROM ix_items i JOIN anc ON i.uid=anc.uid WHERE i.parent_uid<>'')
 SELECT COUNT(*) FROM anc WHERE uid=?2`, it.ParentUID, it.UID).Scan(&n)
		if err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: cannot move an item under itself", ErrCollInvalid)
		}
	}
	return nil
}

// CreateItem inserts an item at the end of its parent unless Rank is set.
func (s *Store) CreateItem(it Item) (*Item, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	if it.UID == "" {
		it.UID = NewUID()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM ix_collections WHERE uid=?`, it.CollectionUID).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("%w: collection not found", ErrCollNotFound)
	}
	if err := s.validateItemPlacement(tx, &Item{UID: "", CollectionUID: it.CollectionUID, ParentUID: it.ParentUID, Kind: it.Kind}); err != nil {
		return nil, err
	}
	if it.Rank == "" {
		it.Rank = RankBetween(s.lastSiblingRank(tx, it.CollectionUID, it.ParentUID), "")
	}
	args, err := itemArgs(&it)
	if err != nil {
		return nil, err
	}
	it.Rev, it.TS = 1, nowMs()
	args = append(args, it.Rev, it.TS)
	if _, err := tx.Exec(`INSERT INTO ix_items(`+itemCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...); err != nil {
		return nil, err
	}
	return &it, tx.Commit()
}

// GetItem returns one item or ErrCollNotFound.
func (s *Store) GetItem(uid string) (*Item, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	it, err := scanItem(s.db.QueryRow(`SELECT `+itemCols+` FROM ix_items WHERE uid=?`, uid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCollNotFound
	}
	return it, err
}

// ListItems returns every item of a collection ordered parent, rank, uid.
func (s *Store) ListItems(collectionUID string) ([]Item, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT `+itemCols+` FROM ix_items WHERE collection_uid=? ORDER BY parent_uid, rank, uid`, collectionUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

// UpdateItem replaces an item's mutable fields (including parent/rank, so it
// doubles as move/reorder). The previous state is saved as a revision.
// CollectionUID and Kind are immutable. A non-zero it.Rev must match.
func (s *Store) UpdateItem(it Item, ch CollChange) (*Item, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	cur, err := scanItem(tx.QueryRow(`SELECT `+itemCols+` FROM ix_items WHERE uid=?`, it.UID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCollNotFound
	}
	if err != nil {
		return nil, err
	}
	if it.Rev != 0 && it.Rev != cur.Rev {
		return nil, ErrCollConflict
	}
	it.CollectionUID, it.Kind = cur.CollectionUID, cur.Kind
	if it.Rank == "" {
		it.Rank = cur.Rank
	}
	if err := s.validateItemPlacement(tx, &it); err != nil {
		return nil, err
	}
	args, err := itemArgs(&it)
	if err != nil {
		return nil, err
	}
	snap, _ := json.Marshal(cur)
	if _, err := tx.Exec(`INSERT INTO ix_item_revisions(item_uid,rev,ts,actor,source,snapshot) VALUES(?,?,?,?,?,?)`,
		cur.UID, cur.Rev, nowMs(), ch.Actor, ch.Source, string(snap)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM ix_item_revisions WHERE item_uid=? AND id NOT IN (SELECT id FROM ix_item_revisions WHERE item_uid=? ORDER BY id DESC LIMIT ?)`,
		cur.UID, cur.UID, maxItemRevisions); err != nil {
		return nil, err
	}
	it.Rev, it.TS = cur.Rev+1, nowMs()
	// args[0] is uid, args[1] collection_uid: keep them out of the SET list.
	set := append(append([]any{}, args[2:]...), it.Rev, it.TS, it.UID)
	if _, err := tx.Exec(`UPDATE ix_items SET parent_uid=?,kind=?,rank=?,name=?,description_md=?,method=?,url_json=?,headers_json=?,params_json=?,body_json=?,auth_json=?,events_json=?,vars_json=?,assertions_json=?,settings_json=?,examples_json=?,tags_json=?,sidecar_json=?,key_order_json=?,rev=?,ts=? WHERE uid=?`, set...); err != nil {
		return nil, err
	}
	return &it, tx.Commit()
}

// DeleteItem removes an item and, for folders, all its descendants.
func (s *Store) DeleteItem(uid string) error {
	if _, err := s.GetItem(uid); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const sub = `WITH RECURSIVE d(uid) AS (SELECT ?1 UNION SELECT i.uid FROM ix_items i JOIN d ON i.parent_uid=d.uid) `
	for _, q := range []string{
		sub + `DELETE FROM ix_item_revisions WHERE item_uid IN (SELECT uid FROM d)`,
		sub + `DELETE FROM ix_variables WHERE owner_kind IN ('folder','request') AND owner_uid IN (SELECT uid FROM d)`,
		sub + `DELETE FROM ix_var_current WHERE owner_kind IN ('folder','request') AND owner_uid IN (SELECT uid FROM d)`,
		sub + `DELETE FROM ix_items WHERE uid IN (SELECT uid FROM d)`,
	} {
		if _, err := tx.Exec(q, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListItemRevisions returns saved prior states of an item, newest first.
func (s *Store) ListItemRevisions(itemUID string) ([]ItemRevision, error) {
	if err := s.ensureCollections(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id,item_uid,rev,ts,actor,source,snapshot FROM ix_item_revisions WHERE item_uid=? ORDER BY id DESC`, itemUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ItemRevision{}
	for rows.Next() {
		var r ItemRevision
		var snap string
		if err := rows.Scan(&r.ID, &r.ItemUID, &r.Rev, &r.TS, &r.Actor, &r.Source, &snap); err != nil {
			return nil, err
		}
		var it Item
		if json.Unmarshal([]byte(snap), &it) == nil {
			r.Snapshot = &it
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
