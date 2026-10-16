// Package postman imports Postman collections (v2.0/v2.1), environments and
// globals into the collection model without loss: every v2.1 key is mapped to
// a store column, preserved in a sidecar (so the exporter re-emits it) or
// reported. Parsing is data-only: nothing is executed, fetched or sent.
// Scripts are kept verbatim (exec line arrays) and are QUARANTINED: this
// package never writes script trust, so imported scripts do not run until the
// owner trusts them through the UI.
package postman

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Veyal/interseptor/internal/store"
)

// Limits for hostile input.
const (
	MaxInputBytes  = 64 << 20
	MaxFolderDepth = 64
	MaxItems       = 200000
)

// Errors.
var (
	ErrTooLarge    = errors.New("postman: input exceeds 64 MiB")
	ErrUnsupported = errors.New("postman: unsupported format")
	ErrNotPostman  = errors.New("postman: not a Postman collection or environment")
)

// Kind says what a parsed file was.
type Kind string

const (
	KindCollection  Kind = "collection"
	KindEnvironment Kind = "environment"
	KindGlobals     Kind = "globals"
)

// Options tune a parse. NewID defaults to store.NewUID (tests inject a counter).
type Options struct {
	NewID func() string
}

// SecretValue is a non-blank value that a secret-type variable carried. It is
// handed to the caller to store as a local current value; it is never part of
// the collection rows, the report, JSON output or logs.
type SecretValue struct {
	OwnerKind string
	OwnerUID  string
	Key       string
	Value     string
}

// EnvImport is a parsed environment or globals file.
type EnvImport struct {
	Environment store.Environment `json:"environment"`
	Variables   []store.Variable  `json:"variables"`
	// Sidecar holds the non-modelled parts (ids, export stamps, per-variable
	// extras) for the exporter. store.Environment has no sidecar column, so a
	// caller that persists environments loses it; exports then regenerate it.
	Sidecar json.RawMessage `json:"sidecar,omitempty"`
}

// Result is a parsed Postman file, ready to preview and commit.
type Result struct {
	Kind         Kind             `json:"kind"`
	Collection   store.Collection `json:"collection"`
	Items        []store.Item     `json:"items"`
	Variables    []store.Variable `json:"variables"`
	Environments []EnvImport      `json:"environments,omitempty"`
	Report       Report           `json:"report"`
	SecretValues []SecretValue    `json:"-"`
}

// Bundle returns the collection (if any) and environments as a
// store.CollectionsBundle for the exporter or a store commit.
func (r *Result) Bundle() store.CollectionsBundle {
	b := store.CollectionsBundle{Version: store.CollectionsBundleVersion}
	if r.Kind == KindCollection {
		b.Collections = []store.Collection{r.Collection}
		b.Items = append(b.Items, r.Items...)
		b.Variables = append(b.Variables, r.Variables...)
	}
	for _, e := range r.Environments {
		b.Environments = append(b.Environments, e.Environment)
		b.Variables = append(b.Variables, e.Variables...)
	}
	return b
}

// CollectionSidecar is everything about a collection that has no column.
type CollectionSidecar struct {
	Format         string                     `json:"format"`
	Wrapped        bool                       `json:"wrapped,omitempty"`
	Info           map[string]json.RawMessage `json:"info,omitempty"`
	InfoKeyOrder   []string                   `json:"infoKeyOrder,omitempty"`
	DescriptionRaw json.RawMessage            `json:"descriptionRaw,omitempty"`
	Variable       json.RawMessage            `json:"variable,omitempty"`
	Extra          map[string]json.RawMessage `json:"extra,omitempty"`
}

// ItemSidecar is everything about an item that has no column.
type ItemSidecar struct {
	ID                 string                     `json:"id,omitempty"`
	DescriptionRaw     json.RawMessage            `json:"descriptionRaw,omitempty"`
	RequestDescription json.RawMessage            `json:"requestDescription,omitempty"`
	HeaderString       bool                       `json:"headerString,omitempty"`
	DescInRequest      bool                       `json:"descInRequest,omitempty"`
	Shorthand          bool                       `json:"shorthand,omitempty"`
	RequestKeys        []string                   `json:"requestKeys,omitempty"`
	RequestExtra       map[string]json.RawMessage `json:"requestExtra,omitempty"`
	Variable           json.RawMessage            `json:"variable,omitempty"`
	Extra              map[string]json.RawMessage `json:"extra,omitempty"`
}

// EnvSidecar is the same for environment/globals files.
type EnvSidecar struct {
	Keys   []string                   `json:"keys,omitempty"`
	Extra  map[string]json.RawMessage `json:"extra,omitempty"`
	Values json.RawMessage            `json:"values,omitempty"`
}

type parser struct {
	opt     Options
	res     *Result
	count   int
	text    string
	envVars []store.Variable
}

// Parse reads a Postman collection (optionally wrapped as {"collection":...}
// with an {"environment":...} sibling) or a bare environment/globals file.
func Parse(data []byte, opt Options) (*Result, error) {
	if len(data) > MaxInputBytes {
		return nil, ErrTooLarge
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // UTF-8 BOM
	if opt.NewID == nil {
		opt.NewID = store.NewUID
	}
	root, err := ParseOMap(data)
	if err != nil {
		rep := Report{Format: "unknown"}
		if looksLikeV3YAML(data) {
			rep.add(Entry{Level: Unsupported, Feature: "postman-v3", Message: "Postman v3 (YAML directory) collections are not supported in v1", Suggestion: "Export from Postman as Collection v2.1 JSON"})
			rep.finish()
			return &Result{Report: rep}, fmt.Errorf("%w: Postman v3 YAML", ErrUnsupported)
		}
		return nil, fmt.Errorf("%w: %v", ErrNotPostman, err)
	}
	p := &parser{opt: opt, res: &Result{}, text: string(data)}
	p.res.Report.Format = "postman"
	switch {
	case root.Has("collection") && !isNull(root.Get("collection")):
		err = p.parseWrapped(root)
	case root.Has("info") && root.Has("item"):
		err = p.parseCollection(root, false)
	case root.Has("requests") && (root.Has("info") || root.Has("order")):
		p.unsupportedV1()
		err = fmt.Errorf("%w: Postman collection v1", ErrUnsupported)
	case root.Has("collections") && !root.Has("info") && !root.Has("item"):
		p.res.Report.add(Entry{Level: Unsupported, Feature: "postman-data-dump", Message: "this is a Postman data dump (several collections and environments in one file); it cannot be imported as a whole", Suggestion: "Export each collection individually from Postman as Collection v2.1, and each environment separately"})
		err = fmt.Errorf("%w: this is a Postman data dump; export collections and environments individually from Postman (Collection v2.1)", ErrUnsupported)
	case root.Has("values"):
		err = p.parseEnvFile(root)
	default:
		return nil, ErrNotPostman
	}
	p.res.Report.finish()
	if p.res.Kind == KindCollection {
		p.res.Collection.ImportReport = mustJSON(p.res.Report)
	}
	if err != nil {
		return p.res, err
	}
	return p.res, nil
}

func looksLikeV3YAML(b []byte) bool {
	s := string(b)
	return strings.Contains(s, "$kind:") || strings.Contains(s, "type: collection") || strings.Contains(s, "$kind\":")
}

func (p *parser) unsupportedV1() {
	p.res.Report.add(Entry{Level: Unsupported, Feature: "postman-v1", Message: "Postman collection v1 (requests/order) is not supported", Suggestion: "Import into Postman and export as Collection v2.1"})
}

func (p *parser) parseWrapped(root *OMap) error {
	if err := p.parseCollection(mustOMap(root.Get("collection")), true); err != nil {
		return err
	}
	if env := root.Get("environment"); !isNull(env) {
		if m, err := ParseOMap(env); err == nil {
			return p.parseEnvFile(m)
		}
	}
	return nil
}

func mustOMap(raw json.RawMessage) *OMap {
	m, err := ParseOMap(raw)
	if err != nil {
		return NewOMap()
	}
	return m
}

func (p *parser) parseCollection(root *OMap, wrapped bool) error {
	info, err := ParseOMap(root.Get("info"))
	if err != nil {
		return fmt.Errorf("%w: info is not an object", ErrNotPostman)
	}
	schema := jsonString(info.Get("schema"))
	if strings.Contains(schema, "/v3") || strings.Contains(schema, "collection/v3") {
		p.res.Report.add(Entry{Level: Unsupported, Feature: "postman-v3", Message: "Postman collection schema v3 is not supported in v1: " + schema, Suggestion: "Export as Collection v2.1 JSON"})
		return fmt.Errorf("%w: Postman v3 (%s)", ErrUnsupported, schema)
	}
	if strings.Contains(schema, "v1.0.0") {
		p.unsupportedV1()
		return fmt.Errorf("%w: Postman v1", ErrUnsupported)
	}
	p.res.Kind = KindCollection
	c := &p.res.Collection
	c.UID = p.opt.NewID()
	c.Name = jsonString(info.Get("name"))
	if c.Name == "" {
		c.Name = "Imported collection"
	}
	c.ScopePolicy = store.ScopePolicyBlock
	c.KeyOrder = mustJSON(root.Keys())
	side := CollectionSidecar{Format: "postman-" + schemaVersion(schema), Wrapped: wrapped, InfoKeyOrder: info.Keys(),
		Info: info.Rest("name", "description")}
	c.Description, side.DescriptionRaw = description(info.Get("description"))
	if a := root.Get("auth"); !isNull(a) {
		c.Auth = a
		p.checkAuth(a, c.Name, "")
	}
	if ev := root.Get("event"); !isNull(ev) {
		c.Events = ev
		p.checkEvents(ev, c.Name, "")
	}
	if ppb := root.Get("protocolProfileBehavior"); !isNull(ppb) {
		c.Settings = ppb
		p.res.Report.add(Entry{Level: Converted, Path: c.Name, Feature: "protocolProfileBehavior", Message: "stored in collection settings"})
	}
	if v := root.Get("variable"); !isNull(v) {
		blanked := p.importVars(v, store.VarOwnerCollection, c.UID, c.Name, "", false)
		side.Variable = blanked
	}
	side.Extra = root.Rest("info", "item", "event", "variable", "auth", "protocolProfileBehavior")
	p.reportExtras(side.Extra, c.Name, "collection")
	if len(side.Extra) == 0 {
		side.Extra = nil
	}
	c.Sidecar = mustJSON(side)
	if err := p.walkItems(root.Get("item"), "", c.Name, 0); err != nil {
		return err
	}
	p.scanDynamicVars()
	return nil
}

func schemaVersion(schema string) string {
	switch {
	case strings.Contains(schema, "v2.1"):
		return "2.1"
	case strings.Contains(schema, "v2.0"):
		return "2.0"
	}
	return "2.1"
}

func (p *parser) reportExtras(extra map[string]json.RawMessage, path, what string) {
	for k := range extra {
		lvl := PreservedInert
		msg := "unknown " + what + " key kept verbatim and re-exported; no effect when sending"
		if strings.HasPrefix(k, "_postman") || strings.HasPrefix(k, "x-") || strings.HasPrefix(k, "_") {
			msg = "vendor/extension key kept verbatim and re-exported"
		}
		p.res.Report.add(Entry{Level: lvl, Path: path, Feature: "key:" + k, Message: msg})
	}
}

func (p *parser) walkItems(raw json.RawMessage, parent, path string, depth int) error {
	if isNull(raw) {
		return nil
	}
	if depth > MaxFolderDepth {
		return fmt.Errorf("%w: folders nested deeper than %d", ErrUnsupported, MaxFolderDepth)
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return fmt.Errorf("%w: item is not an array", ErrNotPostman)
	}
	ranks := store.EvenRanks(len(elems))
	for i, e := range elems {
		p.count++
		if p.count > MaxItems {
			return fmt.Errorf("%w: more than %d items", ErrUnsupported, MaxItems)
		}
		rank := ranks[i]
		if err := p.parseItem(e, parent, path, rank, depth); err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) parseItem(raw json.RawMessage, parent, parentPath, rank string, depth int) error {
	m, err := ParseOMap(raw)
	if err != nil {
		return fmt.Errorf("%w: item is not an object", ErrNotPostman)
	}
	it := store.Item{UID: p.opt.NewID(), CollectionUID: p.res.Collection.UID, ParentUID: parent, Rank: rank,
		Name: jsonString(m.Get("name")), KeyOrder: mustJSON(m.Keys())}
	if it.Name == "" {
		it.Name = "Untitled"
	}
	path := parentPath + "/" + it.Name
	side := ItemSidecar{ID: jsonString(m.Get("id"))}
	it.DescriptionMD, side.DescriptionRaw = description(m.Get("description"))
	folder := m.Has("item")
	known := []string{"id", "name", "description", "item", "request", "response", "event", "variable", "protocolProfileBehavior"}
	if folder {
		known = append(known, "auth")
		it.Kind = "folder"
		p.res.Report.Stats.Folders++
	} else {
		it.Kind = "request"
		p.res.Report.Stats.Requests++
	}
	if ev := m.Get("event"); !isNull(ev) {
		it.Events = ev
		p.checkEvents(ev, path, it.UID)
	}
	if ppb := m.Get("protocolProfileBehavior"); !isNull(ppb) {
		it.Settings = ppb
		p.res.Report.add(Entry{Level: Converted, Path: path, Item: it.UID, Feature: "protocolProfileBehavior", Message: "stored in item settings"})
	}
	if v := m.Get("variable"); !isNull(v) {
		owner := store.VarOwnerFolder
		if !folder {
			owner = store.VarOwnerRequest
		}
		side.Variable = p.importVars(v, owner, it.UID, path, it.UID, false)
	}
	if folder {
		if a := m.Get("auth"); !isNull(a) {
			it.Auth = a
			p.checkAuth(a, path, it.UID)
		}
	} else if err := p.parseRequest(m, &it, &side, path); err != nil {
		return err
	}
	side.Extra = m.Rest(known...)
	p.reportExtras(side.Extra, path, "item")
	p.checkUnsupportedProtocols(side.Extra, path, it.UID)
	if len(side.Extra) == 0 {
		side.Extra = nil
	}
	if ex := m.Get("response"); !isNull(ex) {
		it.Examples = ex
		var arr []json.RawMessage
		if json.Unmarshal(ex, &arr) == nil && len(arr) > 0 {
			p.res.Report.Stats.Examples += len(arr)
			p.res.Report.add(Entry{Level: Converted, Path: path, Item: it.UID, Feature: "response-examples", Message: fmt.Sprintf("%d saved response example(s) stored", len(arr))})
		}
	}
	it.Sidecar = mustJSON(side)
	p.res.Items = append(p.res.Items, it)
	if folder {
		return p.walkItems(m.Get("item"), it.UID, path, depth+1)
	}
	return nil
}

func (p *parser) checkUnsupportedProtocols(extra map[string]json.RawMessage, path, uid string) {
	for _, k := range []string{"grpc", "protocol", "mqtt", "websocket", "socketio"} {
		if _, ok := extra[k]; ok {
			p.res.Report.add(Entry{Level: Unsupported, Path: path, Item: uid, Feature: "protocol:" + k, Message: "non-HTTP protocol item is kept verbatim but cannot be sent (gRPC/MQTT/WebSocket are out of v1)"})
		}
	}
}

func (p *parser) parseRequest(m *OMap, it *store.Item, side *ItemSidecar, path string) error {
	it.Method = "GET"
	reqRaw := m.Get("request")
	if isNull(reqRaw) {
		return nil
	}
	if s := jsonString(reqRaw); s != "" || strings.HasPrefix(strings.TrimSpace(string(reqRaw)), `"`) {
		side.Shorthand = true
		it.URL = reqRaw
		return nil
	}
	rm, err := ParseOMap(reqRaw)
	if err != nil {
		return fmt.Errorf("%w: request of %q is neither string nor object", ErrNotPostman, path)
	}
	side.RequestKeys = rm.Keys()
	if mt := jsonString(rm.Get("method")); mt != "" {
		it.Method = mt
	}
	if u := rm.Get("url"); !isNull(u) {
		it.URL = u
	}
	if h := rm.Get("header"); !isNull(h) {
		it.Headers, side.HeaderString = p.normalizeHeaders(h, path, it.UID)
		p.countDisabled(it.Headers)
		p.checkHeaderCreds(it.Headers, path, it.UID)
	}
	if b := rm.Get("body"); !isNull(b) {
		it.Body = b
		p.checkBody(b, path, it.UID)
	}
	for _, f := range store.URLCredentialFields(it.URL) {
		p.credential(path, it.UID, "URL "+urlFieldLabel(f))
	}
	for _, f := range store.BodyCredentialFields(it.Body) {
		p.credential(path, it.UID, "body field "+f)
	}
	if a := rm.Get("auth"); !isNull(a) {
		it.Auth = a
		p.checkAuth(a, path, it.UID)
	}
	p.countURLDisabled(it.URL)
	if d := rm.Get("description"); !isNull(d) {
		side.RequestDescription = d
		if it.DescriptionMD == "" && side.DescriptionRaw == nil && !m.Has("description") {
			it.DescriptionMD, _ = description(d)
			side.DescInRequest = true
		}
	}
	side.RequestExtra = rm.Rest("method", "header", "body", "url", "auth", "description")
	if _, ok := side.RequestExtra["certificate"]; ok {
		p.res.Report.add(Entry{Level: PreservedInert, Path: path, Item: it.UID, Feature: "request.certificate", Message: "client certificate settings kept verbatim; certificate files are never read from disk", Suggestion: "Configure client certificates in Interseptor settings"})
	}
	if _, ok := side.RequestExtra["proxy"]; ok {
		p.res.Report.add(Entry{Level: PreservedInert, Path: path, Item: it.UID, Feature: "request.proxy", Message: "per-request proxy kept verbatim and not applied"})
	}
	p.reportExtras(side.RequestExtra, path, "request")
	if len(side.RequestExtra) == 0 {
		side.RequestExtra = nil
	}
	return nil
}

// description returns the markdown text and, when the source was an object
// ({content,type,version}), the raw value for lossless re-export.
func description(raw json.RawMessage) (string, json.RawMessage) {
	if isNull(raw) {
		return "", nil
	}
	if s := jsonString(raw); s != "" || strings.HasPrefix(strings.TrimSpace(string(raw)), `"`) {
		return s, nil
	}
	m, err := ParseOMap(raw)
	if err != nil {
		return "", nil
	}
	return jsonString(m.Get("content")), raw
}

func (p *parser) normalizeHeaders(h json.RawMessage, path, uid string) (json.RawMessage, bool) {
	s := jsonString(h)
	if s == "" && !strings.HasPrefix(strings.TrimSpace(string(h)), `"`) {
		return h, false
	}
	var rows []map[string]string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if k, v, ok := strings.Cut(line, ":"); ok {
			rows = append(rows, map[string]string{"key": strings.TrimSpace(k), "value": strings.TrimSpace(v)})
		}
	}
	p.res.Report.add(Entry{Level: Degraded, Path: path, Item: uid, Feature: "header-string", Message: "v2.0 header string converted to a header list"})
	return mustJSON(rows), true
}

func (p *parser) countDisabled(rows json.RawMessage) {
	var arr []map[string]json.RawMessage
	if json.Unmarshal(rows, &arr) != nil {
		return
	}
	for _, r := range arr {
		if string(r["disabled"]) == "true" {
			p.res.Report.Stats.DisabledRows++
		}
	}
}

func (p *parser) countURLDisabled(u json.RawMessage) {
	m, err := ParseOMap(u)
	if err != nil {
		return
	}
	if q := m.Get("query"); !isNull(q) {
		p.countDisabled(q)
	}
}

func (p *parser) checkBody(b json.RawMessage, path, uid string) {
	m, err := ParseOMap(b)
	if err != nil {
		return
	}
	mode := jsonString(m.Get("mode"))
	switch mode {
	case "raw":
		lang := ""
		if o, err := ParseOMap(m.Get("options")); err == nil {
			if r, err := ParseOMap(o.Get("raw")); err == nil {
				lang = jsonString(r.Get("language"))
			}
		}
		if lang != "" {
			p.res.Report.add(Entry{Level: Converted, Path: path, Item: uid, Feature: "body.raw.language", Message: "raw body language " + lang + " preserved"})
		}
	case "urlencoded":
		p.countDisabled(m.Get("urlencoded"))
	case "formdata":
		p.countDisabled(m.Get("formdata"))
		var rows []map[string]json.RawMessage
		_ = json.Unmarshal(m.Get("formdata"), &rows)
		for _, r := range rows {
			if jsonString(r["type"]) == "file" {
				p.res.Report.Stats.NeedsAsset++
				p.res.Report.add(Entry{Level: NeedsReview, Path: path, Item: uid, Feature: "needs-asset", Message: "form-data file field " + jsonString(r["key"]) + " references a local file; the file is never read from disk", Suggestion: "Attach the file to the request as an asset"})
			}
		}
	case "file":
		p.res.Report.Stats.NeedsAsset++
		p.res.Report.add(Entry{Level: NeedsReview, Path: path, Item: uid, Feature: "needs-asset", Message: "file body references a local file; the file is never read from disk", Suggestion: "Attach the file to the request as an asset"})
	case "graphql":
		p.res.Report.add(Entry{Level: Converted, Path: path, Item: uid, Feature: "body.graphql", Message: "GraphQL query and variables preserved"})
	case "":
	default:
		p.res.Report.add(Entry{Level: Unsupported, Path: path, Item: uid, Feature: "body.mode:" + mode, Message: "unknown body mode kept verbatim; it is not sent"})
	}
	if string(m.Get("disabled")) == "true" {
		p.res.Report.Stats.DisabledRows++
	}
}

// inertAuth lists auth types that are kept and re-exported but have no
// authenticator: the request is sent without them (the pipeline warns).
var inertAuth = map[string]bool{"hawk": true, "ntlm": true, "oauth1": true, "edgegrid": true, "akamai": true, "asap": true}

// appliedAuth lists the extra auth types the send pipeline implements.
var appliedAuth = map[string]bool{"digest": true, "awsv4": true, "jwt": true}

func urlFieldLabel(f string) string {
	if f == "userinfo password" {
		return f
	}
	return "query parameter " + f
}

// credential records one literal credential by field name; the value is never
// in the report.
func (p *parser) credential(path, uid, what string) {
	p.res.Report.Stats.EmbeddedCredentials++
	p.res.Report.add(Entry{Level: NeedsReview, Path: path, Item: uid, Feature: "embedded-credential", Message: what + " holds a literal credential (value not shown)", Suggestion: "Lift it to a secret variable and reference it as {{name}}"})
}

var knownAuth = map[string]bool{"noauth": true, "bearer": true, "basic": true, "apikey": true, "oauth2": true}

// credential keys per auth type that hold literal secrets.
var credKeys = map[string]bool{"token": true, "password": true, "secret": true, "accessToken": true, "client_secret": true, "clientSecret": true, "accessKey": true, "secretKey": true, "sessionToken": true, "privateKey": true, "authKey": true, "value": true}

func (p *parser) checkAuth(a json.RawMessage, path, uid string) {
	m, err := ParseOMap(a)
	if err != nil {
		return
	}
	typ := jsonString(m.Get("type"))
	switch {
	case knownAuth[typ]:
		lvl := Converted
		msg := "auth type " + typ + " supported"
		if typ == "oauth2" {
			msg = "oauth2: a manual access token is applied; token fetch flows are not run at import"
		}
		p.res.Report.add(Entry{Level: lvl, Path: path, Item: uid, Feature: "auth:" + typ, Message: msg})
	case appliedAuth[typ]:
		p.res.Report.add(Entry{Level: Converted, Path: path, Item: uid, Feature: "auth:" + typ, Message: "auth type " + typ + " is applied when sending"})
	case inertAuth[typ]:
		p.res.Report.add(Entry{Level: PreservedInert, Path: path, Item: uid, Feature: "auth:" + typ, Message: "auth type " + typ + " is kept and re-exported but has no authenticator: the request is sent without it", Suggestion: "Add the header manually or use a pre-request script once trusted"})
	case typ == "":
	default:
		p.res.Report.add(Entry{Level: Unsupported, Path: path, Item: uid, Feature: "auth:" + typ, Message: "unknown auth type " + typ + " kept verbatim"})
	}
	var entries []map[string]json.RawMessage
	if json.Unmarshal(m.Get(typ), &entries) != nil {
		var obj map[string]json.RawMessage // object form: {"bearer":{"token":"x"}}
		if json.Unmarshal(m.Get(typ), &obj) == nil {
			keys := make([]string, 0, len(obj))
			for k := range obj {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				entries = append(entries, map[string]json.RawMessage{"key": mustJSON(k), "value": obj[k]})
			}
		}
	}
	for _, e := range entries {
		k := jsonString(e["key"])
		v := jsonString(e["value"])
		if (credKeys[k] || store.IsSecretName(k)) && v != "" && !strings.Contains(v, "{{") {
			p.credential(path, uid, "auth "+typ+"."+k)
		}
	}
}

func (p *parser) checkHeaderCreds(h json.RawMessage, path, uid string) {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(h, &rows) != nil {
		return
	}
	for _, r := range rows {
		k := strings.ToLower(jsonString(r["key"]))
		v := jsonString(r["value"])
		if (k == "authorization" || k == "x-api-key" || k == "proxy-authorization" || k == "cookie" || store.IsSecretName(k)) && v != "" && !strings.Contains(v, "{{") {
			p.credential(path, uid, "header "+jsonString(r["key"]))
		}
	}
}

// checkEvents analyses scripts and records the quarantine. It never executes.
func (p *parser) checkEvents(raw json.RawMessage, path, uid string) {
	var evs []map[string]json.RawMessage
	if json.Unmarshal(raw, &evs) != nil {
		return
	}
	for _, e := range evs {
		listen := jsonString(e["listen"])
		if listen != "prerequest" && listen != "test" {
			p.res.Report.add(Entry{Level: NeedsReview, Path: path, Item: uid, Feature: "event:" + listen, Message: "unknown event listener kept verbatim"})
		}
		if string(e["disabled"]) == "true" {
			p.res.Report.Stats.DisabledRows++
		}
		sm, err := ParseOMap(e["script"])
		if err != nil {
			continue
		}
		if t := jsonString(sm.Get("type")); t != "" && t != "text/javascript" {
			p.res.Report.add(Entry{Level: Unsupported, Path: path, Item: uid, Feature: "script.type:" + t, Message: "non-JavaScript script kept verbatim and never run"})
			continue
		}
		src := execSource(sm.Get("exec"))
		if sm.Has("src") && !isNull(sm.Get("src")) {
			p.res.Report.add(Entry{Level: Unsupported, Path: path, Item: uid, Feature: "script.src", Message: "external script src is never fetched"})
		}
		if strings.TrimSpace(src) == "" {
			continue
		}
		p.recordScript(src, listen, path, uid)
	}
}

func (p *parser) recordScript(src, listen, path, uid string) {
	a := analyzeScript(src)
	info := ScriptInfo{Path: path, Item: uid, Listen: listen, SourceHash: a.hash, Lines: a.lines, APIs: a.apis,
		Modules: a.modules, Hosts: a.hosts, Flags: a.flags, Status: a.status(), Quarantine: true}
	r := &p.res.Report
	r.ScriptList = append(r.ScriptList, info)
	r.Scripts.Total++
	switch info.Status {
	case "supported":
		r.Scripts.Supported++
	case "partial":
		r.Scripts.Partial++
	default:
		r.Scripts.Unsupported++
	}
	r.add(Entry{Level: Blocked, Path: path, Item: uid, Feature: "script-quarantined", Message: fmt.Sprintf("%s script (%d lines, %s) will not run until the owner trusts it in the UI", listen, a.lines, info.Status), Suggestion: "Review the source, hash " + a.hash[:12] + ", then trust it"})
	for f, line := range a.unsupported {
		r.add(Entry{Level: Unsupported, Path: path, Item: uid, Feature: "api:" + f, Line: line, Message: f + " is not available; the script reports 'unsupported' at run time instead of a false pass"})
	}
	for f, line := range a.deferred {
		r.add(Entry{Level: Degraded, Path: path, Item: uid, Feature: "api:" + f, Line: line, Message: f + " is not bundled yet; the script reports 'unsupported' when it is used"})
	}
	for _, f := range a.flags {
		r.add(Entry{Level: NeedsReview, Path: path, Item: uid, Feature: "script-flag:" + f, Message: "script flagged: " + f, Suggestion: "Read the source carefully before trusting"})
	}
}

// execSource joins an exec value (line array or single string).
func execSource(raw json.RawMessage) string {
	var lines []string
	if json.Unmarshal(raw, &lines) == nil {
		return strings.Join(lines, "\n")
	}
	return jsonString(raw)
}

// importVars maps a variable[] array to rows, flags secrets and returns the
// original array with secret values blanked (for the sidecar).
func (p *parser) importVars(raw json.RawMessage, ownerKind, ownerUID, path, itemUID string, env bool) json.RawMessage {
	var elems []json.RawMessage
	if json.Unmarshal(raw, &elems) != nil {
		return nil
	}
	seen := map[string]bool{}
	var kept []json.RawMessage
	for _, e := range elems {
		m, err := ParseOMap(e)
		if err != nil {
			continue
		}
		key := jsonString(m.Get("key"))
		if key == "" {
			key = jsonString(m.Get("id"))
		}
		typ := jsonString(m.Get("type"))
		v := store.Variable{OwnerKind: ownerKind, OwnerUID: ownerUID, Key: key, Type: mapVarType(typ), Enabled: true}
		if string(m.Get("disabled")) == "true" || string(m.Get("enabled")) == "false" {
			v.Enabled = false
			p.res.Report.Stats.DisabledRows++
		}
		val := valueString(m.Get("value"))
		if v.Type == store.VarTypeSecret {
			p.res.Report.Stats.SecretVariables++
			lvl, msg := Converted, "secret variable flagged; no initial value is stored"
			if val != "" {
				p.res.SecretValues = append(p.res.SecretValues, SecretValue{OwnerKind: ownerKind, OwnerUID: ownerUID, Key: key, Value: val})
				lvl, msg = NeedsReview, "secret variable carried a value; it was moved out of the shareable initial value and is offered as a local current value only"
			}
			p.res.Report.add(Entry{Level: lvl, Path: path, Item: itemUID, Feature: "secret-variable", Message: msg + ": " + key})
			m.SetValue("value", "")
		} else {
			v.InitialValue = val
		}
		kept = append(kept, mustJSON(m))
		if key == "" {
			p.res.Report.add(Entry{Level: Degraded, Path: path, Item: itemUID, Feature: "variable-no-key", Message: "variable without a key skipped in the variable table (kept in the sidecar)"})
			continue
		}
		if seen[key] {
			p.res.Report.add(Entry{Level: Degraded, Path: path, Item: itemUID, Feature: "variable-duplicate", Message: "duplicate variable key " + key + "; the first definition is used"})
			continue
		}
		seen[key] = true
		p.res.Report.Stats.Variables++
		if env {
			p.res.Report.Stats.EnvironmentVariables++
		}
		if !env {
			p.res.Variables = append(p.res.Variables, v)
		} else {
			p.envVars = append(p.envVars, v)
		}
	}
	return mustJSON(kept)
}

func mapVarType(t string) string {
	switch t {
	case "secret":
		return store.VarTypeSecret
	case "any":
		return store.VarTypeAny
	}
	return store.VarTypeDefault
}

func valueString(raw json.RawMessage) string {
	if isNull(raw) {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

func jsonString(raw json.RawMessage) string {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

func mustJSON(v any) json.RawMessage {
	b, err := Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
