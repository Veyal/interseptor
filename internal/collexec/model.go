package collexec

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Veyal/interseptor/internal/collection"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/varstore"
)

// Body modes.
const (
	BodyNone       = "none"
	BodyRaw        = "raw"
	BodyURLEncoded = "urlencoded"
	BodyFormData   = "formdata"
	BodyGraphQL    = "graphql"
	BodyFile       = "file"
)

// Part is one multipart form part. File parts are not supported: collections
// never read local paths (assets are content-addressed, a later work package).
type Part struct {
	Key         string
	Value       string
	Type        string // text | file
	ContentType string
	Disabled    bool
}

// BodyModel is the structured request body. Every string is a template until
// resolved.
type BodyModel struct {
	Mode        string
	Raw         string
	Language    string // raw language hint: json, xml, text, html, javascript
	Form        []varstore.KV
	Parts       []Part
	GraphQLQ    string
	GraphQLVars string
}

// AuthModel is one effective auth configuration. Fields are templates.
type AuthModel struct {
	Type   string // none | inherit | basic | bearer | apikey | oauth2 | <other>
	Fields map[string]string
}

// RequestModel is the mutable request handed to pre-request scripts and then
// resolved. It is intentionally structured (not wire text) so scripts can
// edit one part without reparsing.
type RequestModel struct {
	Method   string
	URL      string
	PathVars map[string]string
	// Query is authoritative when QueryFromParams is true (the URL's own query
	// string is dropped); otherwise the URL keeps its query verbatim.
	Query           []varstore.KV
	QueryFromParams bool
	Headers         []varstore.KV
	Body            BodyModel
	Auth            AuthModel
}

// Clone deep-copies the model.
func (m *RequestModel) Clone() *RequestModel {
	c := *m
	c.PathVars = copyMap(m.PathVars)
	c.Query = append([]varstore.KV(nil), m.Query...)
	c.Headers = append([]varstore.KV(nil), m.Headers...)
	c.Body.Form = append([]varstore.KV(nil), m.Body.Form...)
	c.Body.Parts = append([]Part(nil), m.Body.Parts...)
	c.Auth.Fields = copyMap(m.Auth.Fields)
	return &c
}

func copyMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Settings are per-collection/folder/item execution settings; a later level
// overrides an earlier one field by field.
type Settings struct {
	FollowRedirects *bool  `json:"followRedirects"`
	MaxRedirects    int    `json:"maxRedirects"`
	TimeoutMs       *int64 `json:"timeoutMs"`
	VerifyTLS       *bool  `json:"verifyTls"`
	// Unresolved is block | literal | empty (default block).
	Unresolved string `json:"unresolved"`
	// UseSession opts a collection item into the global session headers
	// (default false: reproducible requests).
	UseSession *bool `json:"useSession"`
	// RawHeaders forces the ordered raw-header wire path.
	RawHeaders *bool `json:"rawHeaders"`
	// Codec: "" auto (apply_on_send codecs that match), "off", or a codec id
	// applied directly to the item body (plaintext in, wire body out).
	Codec string `json:"codec"`
}

func (s *Settings) overlay(o Settings) {
	if o.FollowRedirects != nil {
		s.FollowRedirects = o.FollowRedirects
	}
	if o.MaxRedirects > 0 {
		s.MaxRedirects = o.MaxRedirects
	}
	if o.TimeoutMs != nil {
		s.TimeoutMs = o.TimeoutMs
	}
	if o.VerifyTLS != nil {
		s.VerifyTLS = o.VerifyTLS
	}
	if o.Unresolved != "" {
		s.Unresolved = o.Unresolved
	}
	if o.UseSession != nil {
		s.UseSession = o.UseSession
	}
	if o.RawHeaders != nil {
		s.RawHeaders = o.RawHeaders
	}
	if o.Codec != "" {
		s.Codec = o.Codec
	}
}

func parseSettings(raw json.RawMessage) Settings {
	var s Settings
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// ---- JSON column parsing ----------------------------------------------------

type kvJSON struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Disabled    bool   `json:"disabled"`
	Enabled     *bool  `json:"enabled"`
	Type        string `json:"type"`
	ContentType string `json:"contentType"`
}

func (k kvJSON) off() bool { return k.Disabled || (k.Enabled != nil && !*k.Enabled) }

func (k kvJSON) value() string {
	switch v := k.Value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool, float64, json.Number:
		return fmt.Sprint(v)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func parseKVs(raw json.RawMessage) []kvJSON {
	var out []kvJSON
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func toKV(in []kvJSON) []varstore.KV {
	if len(in) == 0 {
		return nil
	}
	out := make([]varstore.KV, len(in))
	for i, k := range in {
		out[i] = varstore.KV{Key: k.Key, Value: k.value(), Disabled: k.off()}
	}
	return out
}

// parseURL accepts a JSON string or a Postman-style {raw, variable[]} object.
func parseURL(raw json.RawMessage) (string, map[string]string) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var o struct {
		Raw      string   `json:"raw"`
		Variable []kvJSON `json:"variable"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return "", nil
	}
	var pv map[string]string
	for _, v := range o.Variable {
		if v.Key == "" || v.off() {
			continue
		}
		if pv == nil {
			pv = map[string]string{}
		}
		pv[v.Key] = v.value()
	}
	return o.Raw, pv
}

func parseBody(raw json.RawMessage) BodyModel {
	b := BodyModel{Mode: BodyNone}
	if len(raw) == 0 {
		return b
	}
	var o struct {
		Mode       string          `json:"mode"`
		Raw        string          `json:"raw"`
		URLEncoded []kvJSON        `json:"urlencoded"`
		FormData   []kvJSON        `json:"formdata"`
		GraphQL    json.RawMessage `json:"graphql"`
		Options    struct {
			Raw struct {
				Language string `json:"language"`
			} `json:"raw"`
		} `json:"options"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return b
	}
	b.Mode = strings.ToLower(o.Mode)
	if b.Mode == "" {
		b.Mode = BodyNone
	}
	b.Raw = o.Raw
	b.Language = strings.ToLower(o.Options.Raw.Language)
	b.Form = toKV(o.URLEncoded)
	for _, p := range o.FormData {
		typ := p.Type
		if typ == "" {
			typ = "text"
		}
		b.Parts = append(b.Parts, Part{Key: p.Key, Value: p.value(), Type: typ, ContentType: p.ContentType, Disabled: p.off()})
	}
	if len(o.GraphQL) > 0 {
		var g struct {
			Query     string `json:"query"`
			Variables any    `json:"variables"`
		}
		if json.Unmarshal(o.GraphQL, &g) == nil {
			b.GraphQLQ = g.Query
			switch v := g.Variables.(type) {
			case nil:
			case string:
				b.GraphQLVars = v
			default:
				vb, _ := json.Marshal(v)
				b.GraphQLVars = string(vb)
			}
		}
	}
	return b
}

// parseAuth reads {"type":"bearer","bearer":{"token":"x"}} or the Postman
// array form {"type":"bearer","bearer":[{"key":"token","value":"x"}]}.
func parseAuth(raw json.RawMessage) (AuthModel, bool) {
	if len(raw) == 0 {
		return AuthModel{}, false
	}
	var o map[string]json.RawMessage
	if json.Unmarshal(raw, &o) != nil {
		return AuthModel{}, false
	}
	var typ string
	_ = json.Unmarshal(o["type"], &typ)
	typ = strings.ToLower(typ)
	if typ == "noauth" {
		typ = "none"
	}
	a := AuthModel{Type: typ, Fields: map[string]string{}}
	if sub, ok := o[typ]; ok {
		var arr []kvJSON
		if json.Unmarshal(sub, &arr) == nil {
			for _, kv := range arr {
				a.Fields[kv.Key] = kv.value()
			}
		} else {
			var m map[string]any
			if json.Unmarshal(sub, &m) == nil {
				for k, v := range m {
					a.Fields[k] = kvJSON{Value: v}.value()
				}
			}
		}
	}
	return a, typ != ""
}

// ---- chain ------------------------------------------------------------------

// Chain is the ancestor path of one request: collection, folders outer to
// inner, then the request item itself.
type Chain struct {
	Collection store.Collection
	Folders    []store.Item
	Item       store.Item
}

// ChainFromItems resolves itemUID's ancestors from a flat item list.
func ChainFromItems(coll store.Collection, items []store.Item, itemUID string) (Chain, error) {
	by := make(map[string]store.Item, len(items))
	for _, it := range items {
		by[it.UID] = it
	}
	it, ok := by[itemUID]
	if !ok {
		return Chain{}, fmt.Errorf("collexec: item %q not found", itemUID)
	}
	if it.Kind == "folder" {
		return Chain{}, fmt.Errorf("collexec: item %q is a folder", itemUID)
	}
	c := Chain{Collection: coll, Item: it}
	seen := map[string]bool{it.UID: true}
	for p := it.ParentUID; p != ""; {
		f, ok := by[p]
		if !ok || seen[p] {
			break
		}
		seen[p] = true
		c.Folders = append([]store.Item{f}, c.Folders...)
		p = f.ParentUID
	}
	return c, nil
}

// ChainRepo is the persistence a chain loader needs; *store.Store and
// collection.Repo satisfy it.
type ChainRepo interface {
	GetItem(uid string) (*store.Item, error)
	GetCollection(uid string) (*store.Collection, error)
	ListItems(collectionUID string) ([]store.Item, error)
}

// LoadChain loads the chain for an item uid.
func LoadChain(r ChainRepo, itemUID string) (Chain, error) {
	it, err := r.GetItem(itemUID)
	if err != nil {
		return Chain{}, err
	}
	coll, err := r.GetCollection(it.CollectionUID)
	if err != nil {
		return Chain{}, err
	}
	items, err := r.ListItems(it.CollectionUID)
	if err != nil {
		return Chain{}, err
	}
	return ChainFromItems(*coll, items, itemUID)
}

// settings merges settings from collection down to the item.
func (c Chain) settings() Settings {
	s := parseSettings(c.Collection.Settings)
	for _, f := range c.Folders {
		s.overlay(parseSettings(f.Settings))
	}
	s.overlay(parseSettings(c.Item.Settings))
	return s
}

// effectiveAuth returns the nearest non-inherit auth walking item to
// collection. "none" stops inheritance.
func (c Chain) effectiveAuth() AuthModel {
	if a, ok := parseAuth(c.Item.Auth); ok && a.Type != "inherit" {
		return a
	}
	for i := len(c.Folders) - 1; i >= 0; i-- {
		if a, ok := parseAuth(c.Folders[i].Auth); ok && a.Type != "inherit" {
			return a
		}
	}
	if a, ok := parseAuth(c.Collection.Auth); ok && a.Type != "inherit" {
		return a
	}
	return AuthModel{Type: "none"}
}

// model builds the request model from the item columns and effective auth.
func (c Chain) model() *RequestModel {
	url, pv := parseURL(c.Item.URL)
	params := parseKVs(c.Item.Params)
	m := &RequestModel{
		Method:          strings.ToUpper(strings.TrimSpace(c.Item.Method)),
		URL:             url,
		PathVars:        pv,
		Query:           toKV(params),
		QueryFromParams: len(params) > 0,
		Headers:         toKV(parseKVs(c.Item.Headers)),
		Body:            parseBody(c.Item.Body),
		Auth:            c.effectiveAuth(),
	}
	if m.Method == "" {
		m.Method = "GET"
	}
	return m
}

// capSet parses a collection's capability set (array of names or an object of
// name->bool) into sorted names, the form bound into script trust hashes.
func capSet(raw json.RawMessage) []string {
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		return arr
	}
	var m map[string]bool
	if json.Unmarshal(raw, &m) == nil {
		var out []string
		for k, v := range m {
			if v {
				out = append(out, k)
			}
		}
		return out
	}
	return nil
}

// ---- scripts ----------------------------------------------------------------

// Script listen phases.
const (
	ListenPre  = "prerequest"
	ListenTest = "test"
)

// scriptRef is one script located in the chain.
type scriptRef struct {
	Owner  string // collection | folder | request
	Name   string
	Listen string
	Source string
	Hash   string
}

type eventJSON struct {
	Listen   string `json:"listen"`
	Disabled bool   `json:"disabled"`
	Script   struct {
		Exec json.RawMessage `json:"exec"`
	} `json:"script"`
}

func eventScripts(owner, name string, events json.RawMessage, caps []string, listen string) []scriptRef {
	var evs []eventJSON
	if len(events) == 0 || json.Unmarshal(events, &evs) != nil {
		return nil
	}
	var out []scriptRef
	for _, e := range evs {
		if e.Listen != listen || e.Disabled {
			continue
		}
		var lines []string
		if json.Unmarshal(e.Script.Exec, &lines) != nil {
			var one string
			if json.Unmarshal(e.Script.Exec, &one) != nil {
				continue
			}
			lines = []string{one}
		}
		src := strings.Join(lines, "\n")
		if strings.TrimSpace(src) == "" {
			continue
		}
		// The hash input matches collection.EventSources ("<listen>\n<src>") so
		// trust rows written elsewhere line up.
		out = append(out, scriptRef{Owner: owner, Name: name, Listen: listen, Source: src,
			Hash: collection.ScriptHash(listen+"\n"+src, nil, caps)})
	}
	return out
}

// scripts lists scripts for a listen phase, outer (collection) to inner.
func (c Chain) scripts(listen string) []scriptRef {
	caps := capSet(c.Collection.Caps)
	out := eventScripts("collection", c.Collection.Name, c.Collection.Events, caps, listen)
	for _, f := range c.Folders {
		out = append(out, eventScripts("folder", f.Name, f.Events, caps, listen)...)
	}
	return append(out, eventScripts("request", c.Item.Name, c.Item.Events, caps, listen)...)
}
