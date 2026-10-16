package varstore

import "strings"

// KV is one ordered header/query/form entry. Disabled entries are not resolved.
type KV struct {
	Key      string
	Value    string
	Disabled bool
}

// Request is the structured request the resolver rewrites. Every string field
// is a template.
type Request struct {
	Method   string
	URL      string
	PathVars map[string]string // ":id" style path variables
	Query    []KV
	Headers  []KV
	Form     []KV
	Body     string
	Auth     map[string]string
}

// ResolveRequest resolves URL, path variables, query, header names and values,
// form keys, body and auth fields in one pass with a shared work budget. The
// returned Result aggregates uses and unresolved names (Field names where each
// was used) and applies the unresolved policy once.
func (r *Resolver) ResolveRequest(req Request, stack *Stack) (Request, Result) {
	x := r.newRun(stack)
	out := Request{}
	rs := func(field, tpl string) string {
		x.field = field
		return x.expand(tpl, 0, nil)
	}
	out.Method = rs("method", req.Method)
	pv := make(map[string]string, len(req.PathVars))
	for k, v := range req.PathVars {
		pv[k] = rs("path:"+k, v)
	}
	out.URL = SubstitutePath(rs("url", req.URL), pv)
	out.PathVars = pv
	kvs := func(field string, in []KV) []KV {
		if in == nil {
			return nil
		}
		o := make([]KV, len(in))
		for i, kv := range in {
			if kv.Disabled {
				o[i] = kv
				continue
			}
			o[i] = KV{Key: rs(field+".name", kv.Key), Value: rs(field+":"+kv.Key, kv.Value)}
		}
		return o
	}
	out.Query = kvs("query", req.Query)
	out.Headers = kvs("header", req.Headers)
	out.Form = kvs("form", req.Form)
	out.Body = rs("body", req.Body)
	if req.Auth != nil {
		out.Auth = make(map[string]string, len(req.Auth))
		for k, v := range req.Auth {
			out.Auth[k] = rs("auth:"+k, v)
		}
	}
	return out, x.finish(Result{})
}

// SubstitutePath replaces ":name" path segments with escaped values. Only the
// path is touched: a ":8080" port, the scheme and the query string are left
// alone, and names with no entry in vars stay as written.
func SubstitutePath(rawURL string, vars map[string]string) string {
	if len(vars) == 0 {
		return rawURL
	}
	start := 0
	if i := strings.Index(rawURL, "://"); i >= 0 {
		start = strings.IndexByte(rawURL[i+3:], '/')
		if start < 0 {
			return rawURL
		}
		start += i + 3
	} else if !strings.HasPrefix(rawURL, "/") {
		start = strings.IndexByte(rawURL, '/')
		if start < 0 {
			return rawURL
		}
	}
	end := len(rawURL)
	if i := strings.IndexAny(rawURL[start:], "?#"); i >= 0 {
		end = start + i
	}
	segs := strings.Split(rawURL[start:end], "/")
	for i, s := range segs {
		if name, ok := strings.CutPrefix(s, ":"); ok {
			if v, has := vars[name]; has {
				segs[i] = pathEscape(v)
			}
		}
	}
	return rawURL[:start] + strings.Join(segs, "/") + rawURL[end:]
}
