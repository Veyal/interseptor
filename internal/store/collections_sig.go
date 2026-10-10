package store

import (
	"encoding/json"
	"strings"
)

// MergeSkip names one incoming request that was not imported because an
// identical one (same place, name, method, URL and body) already exists. It
// never carries a URL or body, so it cannot leak a credential.
type MergeSkip struct {
	Item   string `json:"item"` // incoming item uid
	Path   string `json:"path"` // Folder/Sub/Name
	Method string `json:"method,omitempty"`
	Reason string `json:"reason"`
}

// maxMergeSkips bounds the named skips of one merge; the count in
// CollectionMergeStats.ItemsSkipped stays exact.
const maxMergeSkips = 200

// EffectiveURL returns the URL a stored url_json value denotes. A string is
// itself. A Postman-style object yields its raw text, except that a non-empty
// query array is authoritative (a disabled row must not go out even though raw
// still lists it), and an object with no raw is rebuilt from protocol, host,
// port, path and query.
func EffectiveURL(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		Raw      string          `json:"raw"`
		Protocol string          `json:"protocol"`
		Host     json.RawMessage `json:"host"`
		Port     json.RawMessage `json:"port"`
		Path     json.RawMessage `json:"path"`
		Query    []struct {
			Key      string          `json:"key"`
			Value    json.RawMessage `json:"value"`
			Disabled bool            `json:"disabled"`
			Enabled  *bool           `json:"enabled"`
		} `json:"query"`
		Hash string `json:"hash"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return ""
	}
	base, frag := o.Raw, o.Hash
	if base != "" {
		if i := strings.IndexByte(base, '#'); i >= 0 {
			base, frag = base[:i], base[i+1:]
		}
		if len(o.Query) == 0 {
			if frag != "" && o.Hash == "" {
				return base + "#" + frag
			}
			return o.Raw
		}
		if i := strings.IndexByte(base, '?'); i >= 0 {
			base = base[:i]
		}
	} else {
		base = buildURLBase(o.Protocol, o.Host, o.Port, o.Path)
	}
	var q []string
	for _, p := range o.Query {
		if p.Disabled || (p.Enabled != nil && !*p.Enabled) {
			continue
		}
		v, has := jsonScalar(p.Value)
		if p.Key == "" && v == "" {
			continue
		}
		if !has {
			q = append(q, p.Key)
		} else {
			q = append(q, p.Key+"="+v)
		}
	}
	out := base
	if len(q) > 0 {
		out += "?" + strings.Join(q, "&")
	}
	if frag != "" {
		out += "#" + frag
	}
	return out
}

func buildURLBase(protocol string, host, port, path json.RawMessage) string {
	var sb strings.Builder
	if protocol != "" {
		sb.WriteString(strings.TrimSuffix(protocol, "://") + "://")
	}
	sb.WriteString(joinSegments(host, "."))
	if p, ok := jsonScalar(port); ok && p != "" {
		sb.WriteString(":" + p)
	}
	if segs := joinSegments(path, "/"); segs != "" {
		sb.WriteString("/" + strings.TrimPrefix(segs, "/"))
	}
	return sb.String()
}

// jsonScalar renders a JSON string/number/bool; ok is false for null/absent.
func jsonScalar(raw json.RawMessage) (string, bool) {
	t := strings.TrimSpace(string(raw))
	if t == "" || t == "null" {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	return t, true
}

// joinSegments accepts a string or an array of strings / {value} objects.
func joinSegments(raw json.RawMessage, sep string) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) != nil {
		return ""
	}
	parts := make([]string, 0, len(arr))
	for _, e := range arr {
		if v, ok := jsonScalar(e); ok && !strings.HasPrefix(v, "{") {
			parts = append(parts, v)
			continue
		}
		var o struct {
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(e, &o) == nil {
			v, _ := jsonScalar(o.Value)
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, sep)
}

// canonJSON re-encodes a JSON column with sorted keys and no insignificant
// whitespace so two spellings of the same value compare equal.
func canonJSON(raw json.RawMessage) string {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return strings.TrimSpace(string(raw))
	}
	out, err := json.Marshal(v)
	if err != nil {
		return strings.TrimSpace(string(raw))
	}
	return string(out)
}

// itemSig identifies an item when uids do not match: its collection, its
// (local) parent, kind, name and for requests the method, the effective URL and
// canonical body and auth. Literal credentials are scrubbed from the signature
// inputs so the same request matches whether or not its secrets were blanked
// on the way in (peer bundles are scrubbed, user imports are not).
func itemSig(coll, parent string, it *Item) string {
	var sb strings.Builder
	sb.WriteString(coll + "\x00" + parent + "\x00" + it.Kind + "\x00" + it.Name)
	if it.Kind == "folder" {
		return sb.String()
	}
	u := append(json.RawMessage(nil), it.URL...)
	scrubURLSecrets(&u)
	body := append(json.RawMessage(nil), it.Body...)
	scrubBodySecrets(&body)
	auth := append(json.RawMessage(nil), it.Auth...)
	scrubRaw(&auth, true)
	sb.WriteString("\x00" + it.Method + "\x00" + strings.TrimSpace(EffectiveURL(u)) + "\x00" + canonJSON(body) + "\x00" + canonJSON(auth))
	return sb.String()
}
