package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
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

var (
	templateAnyRe = regexp.MustCompile(`\{\{[^{}]+\}\}`)
	// authSchemes are the words that may surround template references without
	// the value holding a literal secret ("Bearer {{token}}").
	authSchemes = map[string]bool{"bearer": true, "basic": true, "token": true, "digest": true, "apikey": true, "oauth": true, "negotiate": true, "ntlm": true}
)

// onlyTemplateRefs reports whether s is made of {{template}} references,
// separators and at most auth-scheme words: it carries no literal secret.
func onlyTemplateRefs(s string) bool {
	if !templateAnyRe.MatchString(s) {
		return false
	}
	rest := strings.NewReplacer(":", " ", ",", " ", ";", " ").Replace(templateAnyRe.ReplaceAllString(s, " "))
	for _, w := range strings.Fields(rest) {
		if !authSchemes[strings.ToLower(w)] {
			return false
		}
	}
	return true
}

func blankable(v any) bool {
	s, ok := v.(string)
	return ok && s != "" && !templateRefRe.MatchString(s) && !onlyTemplateRefs(s)
}

// scrubValue walks decoded JSON blanking literal secrets. A leaf is blanked
// when its own key is a secret name, or when it is the "value" of an object
// whose "key"/"name" is a secret name (Postman key/value pairs). When
// authCtx is true a pair keyed "value" is also blanked (apikey auth stores
// the secret under the literal key "value"). {{template}} references stay.
func scrubValue(v any, authCtx bool, n *tally) any {
	switch t := v.(type) {
	case []any:
		for i := range t {
			t[i] = scrubValue(t[i], authCtx, n)
		}
		return t
	case map[string]any:
		pairSecret, pairName := false, ""
		for _, kk := range []string{"key", "name"} {
			if s, ok := t[kk].(string); ok && (secretName(s) || (authCtx && kk == "key" && strings.EqualFold(s, "value"))) {
				pairSecret, pairName = true, s
			}
		}
		for k, val := range t {
			switch {
			case pairSecret && k == "value" && blankable(val):
				t[k] = ""
				n.hit(pairName)
			case secretName(k) && blankable(val):
				t[k] = ""
				n.hit(k)
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
	n := &tally{}
	v = scrubValue(v, authCtx, n)
	if n.n == 0 {
		return raw, 0
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw, 0
	}
	return string(out), n.n
}

// tally counts blanked credentials and, when collect is set, remembers the
// name of each field (never its value) so importers can report them.
type tally struct {
	n       int
	collect bool
	names   []string
}

func (t *tally) hit(name string) {
	t.n++
	if t.collect {
		t.names = append(t.names, name)
	}
}

func scrubRaw(r *json.RawMessage, authCtx bool) int {
	out, n := scrubJSON(string(*r), authCtx)
	if n > 0 {
		*r = json.RawMessage(out)
	}
	return n
}

// scrubRawNames is scrubRaw that also collects the credential field names.
func scrubRawNames(r *json.RawMessage, authCtx bool, t *tally) {
	var v any
	if len(*r) == 0 || json.Unmarshal(*r, &v) != nil {
		return
	}
	scrubValue(v, authCtx, t)
}

// scrubItemSecrets blanks literal credentials in an item's auth, headers,
// params, URL (userinfo and secret-named query values) and body.
// Returns the number of blanked values.
// The sidecar holds the imported document verbatim so Postman export can be
// byte-exact, and examples hold captured request/response pairs. Both therefore
// carry a second copy of any credential the live columns hold. They must be
// scrubbed with everything else: a user import now keeps credentials at rest,
// so an unwalked copy would reach archive, vault, bundle, peer merge and the AI
// read. A scrubbed export is meant to lose credentials, so losing byte-exactness
// here is the intended trade — the lossless round-trip is an unscrubbed-path
// guarantee.
func scrubItemSecrets(it *Item) int {
	return scrubRaw(&it.Auth, true) + scrubRaw(&it.Headers, false) + scrubRaw(&it.Params, false) +
		scrubURLSecrets(&it.URL) + scrubBodySecrets(&it.Body) +
		scrubRaw(&it.Sidecar, true) + scrubRaw(&it.Examples, true)
}

func scrubCollectionSecrets(c *Collection) int {
	return scrubRaw(&c.Auth, true) + scrubRaw(&c.Sidecar, true)
}

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
				if _, err := db.Exec(`UPDATE ix_items SET auth_json=?,headers_json=?,params_json=?,url_json=?,body_json=? WHERE uid=?`,
					string(it.Auth), string(it.Headers), string(it.Params), string(it.URL), string(it.Body), it.UID); err != nil {
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

// QuarantineImportedProject strips every grant a foreign project database
// carries: all script trust rows are deleted, collection capabilities reset to
// default-deny and a scope policy of "off" (or anything invalid) becomes
// "block". Restores and vault pulls call it on the staged file before it is
// installed, so a hand-built archive can never arrive with scripts already
// trusted (trust is a UI-only decision, mirroring MergeCollectionsFrom).
func QuarantineImportedProject(path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=DELETE`); err != nil {
		return err
	}
	has := func(t string) bool { ok, _ := peerHasTable(db, t); return ok }
	if has("ix_script_trust") {
		if _, err := db.Exec(`DELETE FROM ix_script_trust`); err != nil {
			return fmt.Errorf("clear imported script trust: %w", err)
		}
	}
	if has("ix_collections") {
		if _, err := db.Exec(`UPDATE ix_collections SET caps_json=''`); err != nil {
			return fmt.Errorf("reset imported capabilities: %w", err)
		}
		if _, err := db.Exec(`UPDATE ix_collections SET scope_policy='block' WHERE scope_policy NOT IN ('block','warn')`); err != nil {
			return fmt.Errorf("reset imported scope policy: %w", err)
		}
	}
	return nil
}

// ---- URL and body scrubbing ---------------------------------------------------

var (
	urlUserinfoRe = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*://[^/?#@:]*):([^/?#@]*)@`)
	jsonStrPairRe = regexp.MustCompile(`("(?:[^"\\]|\\.)*"\s*:\s*)"((?:[^"\\]|\\.)*)"`)
	xmlElemRe     = regexp.MustCompile(`<([A-Za-z_][\w.:-]*)>([^<>]*)</([A-Za-z_][\w.:-]*)>`)
)

func decodeQueryKey(k string) string {
	if d, err := url.QueryUnescape(k); err == nil {
		return d
	}
	return k
}

// scrubQueryString blanks the values of secret-named keys in a k=v&k=v string,
// keeping {{template}} references and every other pair byte for byte.
func scrubQueryString(q string, n *tally) string {
	parts := strings.Split(q, "&")
	for i, p := range parts {
		eq := strings.IndexByte(p, '=')
		if eq < 0 {
			continue
		}
		if secretName(decodeQueryKey(p[:eq])) && blankable(p[eq+1:]) {
			parts[i] = p[:eq+1]
			n.hit(decodeQueryKey(p[:eq]))
		}
	}
	return strings.Join(parts, "&")
}

// scrubURLString blanks literal credentials in a URL string: the userinfo
// password and the values of secret-named query keys. The fragment is kept.
func scrubURLString(s string, n *tally) string {
	if m := urlUserinfoRe.FindStringSubmatch(s); m != nil && blankable(m[2]) {
		s = m[1] + ":@" + s[len(m[0]):]
		n.hit("userinfo password")
	}
	qi := strings.IndexByte(s, '?')
	if qi < 0 {
		return s
	}
	frag := ""
	rest := s[qi+1:]
	if hi := strings.IndexByte(rest, '#'); hi >= 0 {
		rest, frag = rest[:hi], rest[hi:]
	}
	return s[:qi+1] + scrubQueryString(rest, n) + frag
}

// scrubBodyText blanks literal credentials in raw body text: JSON string
// members with a secret name, form-encoded pairs and simple XML elements.
func scrubBodyText(s string, n *tally) string {
	if strings.ContainsAny(s, "{[") {
		s = jsonStrPairRe.ReplaceAllStringFunc(s, func(m string) string {
			sm := jsonStrPairRe.FindStringSubmatch(m)
			var key string
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(sm[1]), ":"))), &key) != nil {
				return m
			}
			if secretName(key) && blankable(sm[2]) {
				n.hit(key)
				return sm[1] + `""`
			}
			return m
		})
	}
	if strings.Contains(s, "</") {
		s = xmlElemRe.ReplaceAllStringFunc(s, func(m string) string {
			sm := xmlElemRe.FindStringSubmatch(m)
			if sm[1] == sm[3] && secretName(sm[1]) && blankable(sm[2]) {
				n.hit(sm[1])
				return "<" + sm[1] + "></" + sm[3] + ">"
			}
			return m
		})
	} else if !strings.ContainsAny(s, "{[<\n") && strings.Contains(s, "=") {
		s = scrubQueryString(s, n)
	}
	return s
}

// scrubStringLeaves applies fn to the string members named in keys (and to a
// bare string root) anywhere inside decoded JSON.
func scrubStringLeaves(v any, keys map[string]bool, fn func(string, *tally) string, n *tally) any {
	switch t := v.(type) {
	case string:
		return fn(t, n)
	case []any:
		for i := range t {
			t[i] = scrubStringLeaves(t[i], keys, fn, n)
		}
	case map[string]any:
		for k, val := range t {
			if s, ok := val.(string); ok && keys[k] {
				t[k] = fn(s, n)
			} else if _, isStr := val.(string); !isStr {
				t[k] = scrubStringLeaves(val, keys, fn, n)
			}
		}
	}
	return v
}

func scrubStringLeavesRaw(r *json.RawMessage, keys map[string]bool, fn func(string, *tally) string) int {
	n := &tally{}
	scrubStringLeavesRawT(r, keys, fn, n)
	return n.n
}

// scrubStringLeavesRawT blanks (in r) and tallies credentials in the string
// members named in keys; r is rewritten only when something was blanked.
func scrubStringLeavesRawT(r *json.RawMessage, keys map[string]bool, fn func(string, *tally) string, n *tally) {
	if len(*r) == 0 {
		return
	}
	var v any
	if err := json.Unmarshal(*r, &v); err != nil {
		return
	}
	before := n.n
	v = scrubStringLeaves(v, keys, fn, n)
	if n.n == before {
		return
	}
	if out, err := json.Marshal(v); err == nil {
		*r = json.RawMessage(out)
	}
}

var (
	urlLeafKeys  = map[string]bool{"raw": true}
	bodyLeafKeys = map[string]bool{"raw": true, "text": true, "variables": true}
)

// scrubURLSecrets blanks credentials in an item URL (a string, or an object
// with raw and a query array).
func scrubURLSecrets(r *json.RawMessage) int {
	return scrubRaw(r, false) + scrubStringLeavesRaw(r, urlLeafKeys, scrubURLString)
}

// scrubBodySecrets blanks credentials in an item body: urlencoded/formdata
// pairs plus raw, text and graphql-variables text.
func scrubBodySecrets(r *json.RawMessage) int {
	return scrubRaw(r, false) + scrubStringLeavesRaw(r, bodyLeafKeys, scrubBodyText)
}

// CountURLBodyCredentials reports how many literal credentials a request's
// URL and body hold (secret-named query values, URL userinfo passwords, secret
// members of the body) without modifying its arguments. Importers use it to
// raise the same embedded-credential warning headers and auth already get.
func CountURLBodyCredentials(urlRaw, body json.RawMessage) int {
	return len(URLCredentialFields(urlRaw)) + len(BodyCredentialFields(body))
}

func uniqueNames(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// URLCredentialFields names (never the values) the literal credentials in a
// request URL: secret-named query keys and a userinfo password. The input is
// not modified. A URL object carries its query twice (raw and the parsed
// array); each representation is inspected and the names are merged.
func URLCredentialFields(urlRaw json.RawMessage) []string {
	u1 := append(json.RawMessage(nil), urlRaw...)
	u2 := append(json.RawMessage(nil), urlRaw...)
	t := &tally{collect: true}
	scrubRawNames(&u1, false, t)
	scrubStringLeavesRawT(&u2, urlLeafKeys, scrubURLString, t)
	return uniqueNames(t.names)
}

// BodyCredentialFields names the literal credentials in a request body (form
// pairs and secret members of raw, text and graphql-variables text).
func BodyCredentialFields(body json.RawMessage) []string {
	b1 := append(json.RawMessage(nil), body...)
	b2 := append(json.RawMessage(nil), body...)
	t := &tally{collect: true}
	scrubRawNames(&b1, false, t)
	scrubStringLeavesRawT(&b2, bodyLeafKeys, scrubBodyText, t)
	return uniqueNames(t.names)
}
