package openapi

import (
	"encoding/json"
	"strings"
)

// body derives the request body: v3 requestBody or Swagger 2 body/formData
// parameters.
func (b *reqBuilder) body(ps []param) {
	if b.im.v2 {
		b.bodyV2(ps)
		return
	}
	rb, _, err := deref(b.im.root, b.o.op.get("requestBody"))
	m := asMap(rb)
	if m == nil {
		if err != nil {
			b.im.reportOnce(b.label, NeedsReview, b.label, "requestBody-ref", "requestBody reference could not be resolved ("+err.Error()+")", "")
		}
		return
	}
	media, mt := pickMedia(asMap(m.get("content")))
	if media == nil {
		return
	}
	var val any
	switch {
	case media.has("example"):
		val = media.get("example")
	case firstExample(b.im.root, media) != nil:
		val = firstExample(b.im.root, media)
	default:
		val = b.gen.example(media.get("schema"), 0)
	}
	b.render(mt, val, schemaOf(b.im.root, media.get("schema")))
}

func schemaOf(root any, s any) *omap {
	v, _, err := deref(root, s)
	if err != nil {
		return nil
	}
	return asMap(v)
}

// firstExample returns the value of the first entry of a media "examples" map.
func firstExample(root any, media *omap) any {
	ex := asMap(media.get("examples"))
	if ex == nil || len(ex.keys) == 0 {
		return nil
	}
	e, _, err := deref(root, ex.m[ex.keys[0]])
	if err != nil {
		return nil
	}
	return asMap(e).get("value")
}

func pickMedia(content *omap) (*omap, string) {
	if content == nil || len(content.keys) == 0 {
		return nil, ""
	}
	best := ""
	for _, k := range content.keys {
		if k == "application/json" {
			best = k
			break
		}
	}
	if best == "" {
		for _, k := range content.keys {
			if strings.Contains(k, "json") {
				best = k
				break
			}
		}
	}
	if best == "" {
		best = content.keys[0]
	}
	return asMap(content.m[best]), best
}

func (b *reqBuilder) render(mt string, val any, schema *omap) {
	lower := strings.ToLower(mt)
	m := newOmap()
	switch {
	case strings.Contains(lower, "json"):
		if val == nil {
			b.im.reportOnce(b.label+"|body", Degraded, b.label, "no-body-example", "no example or usable schema for the "+mt+" body; the body was left empty", "")
			return
		}
		m.set("mode", "raw")
		m.set("raw", jsonText(val))
		m.set("options", rawOpts("json"))
	case strings.HasPrefix(lower, "application/x-www-form-urlencoded"):
		m.set("mode", "urlencoded")
		m.set("urlencoded", formRows(val, nil, false))
	case strings.HasPrefix(lower, "multipart/"):
		m.set("mode", "formdata")
		m.set("formdata", formRows(val, schema, true))
	default:
		s, ok := val.(string)
		if !ok {
			b.im.reportOnce(b.label+"|body", Degraded, b.label, "body-"+mt, "no literal example for the "+mt+" body; the body was left empty", "")
			s = ""
		}
		m.set("mode", "raw")
		m.set("raw", s)
		if strings.Contains(lower, "xml") {
			m.set("options", rawOpts("xml"))
		}
	}
	b.it.Body = mustJSON(m)
	b.addHeader("Content-Type", mt)
}

func rawOpts(lang string) *omap {
	raw := newOmap()
	raw.set("language", lang)
	o := newOmap()
	o.set("raw", raw)
	return o
}

// jsonText renders a JSON example. A string that is itself valid JSON is used
// verbatim (authors often write examples as JSON text).
func jsonText(v any) string {
	if s, ok := v.(string); ok && json.Valid([]byte(s)) {
		return s
	}
	return prettyJSON(v)
}

// formRows turns an example object into form rows. For multipart, properties
// whose schema is binary become file rows.
func formRows(val any, schema *omap, multipart bool) []*omap {
	rows := []*omap{}
	obj := asMap(val)
	if obj == nil {
		return rows
	}
	props := asMap(schema.get("properties"))
	for _, k := range obj.keys {
		row := newOmap()
		row.set("key", k)
		if multipart && isBinary(asMap(props.get(k))) {
			row.set("type", "file")
			row.set("src", "")
		} else {
			if multipart {
				row.set("type", "text")
			}
			row.set("value", scalarString(obj.m[k]))
		}
		rows = append(rows, row)
	}
	return rows
}

func isBinary(s *omap) bool {
	if s == nil {
		return false
	}
	if asString(s.get("format")) == "binary" {
		return true
	}
	return asString(asMap(s.get("items")).get("format")) == "binary"
}

// bodyV2 handles Swagger 2 body and formData parameters.
func (b *reqBuilder) bodyV2(ps []param) {
	consumes := firstString(b.o.op.get("consumes"), b.im.root.get("consumes"))
	var form []param
	for _, p := range ps {
		switch p.in {
		case "body":
			mt := consumes
			if mt == "" {
				mt = "application/json"
			}
			var val any
			switch {
			case p.m.has("x-example"):
				val = p.m.get("x-example")
			default:
				val = b.gen.example(p.m.get("schema"), 0)
			}
			b.render(mt, val, schemaOf(b.im.root, p.m.get("schema")))
			return
		case "formData":
			form = append(form, p)
		}
	}
	if len(form) == 0 {
		return
	}
	hasFile := false
	obj := newOmap()
	props := newOmap()
	for _, p := range form {
		if asString(p.m.get("type")) == "file" {
			hasFile = true
			f := newOmap()
			f.set("format", "binary")
			props.set(p.name, f)
		}
		obj.set(p.name, b.paramValue(p))
	}
	mt := "application/x-www-form-urlencoded"
	if hasFile || strings.HasPrefix(consumes, "multipart/") {
		mt = "multipart/form-data"
	}
	sch := newOmap()
	sch.set("properties", props)
	b.render(mt, obj, sch)
}

func firstString(vs ...any) string {
	for _, v := range vs {
		for _, e := range asSlice(v) {
			if s := asString(e); s != "" {
				return s
			}
		}
	}
	return ""
}

// examples turns declared response examples into saved examples (Postman
// response[] shape).
func (b *reqBuilder) examples() json.RawMessage {
	responses := asMap(b.o.op.get("responses"))
	var out []*omap
	for i, code := range responses.keysOrNil() {
		if i >= MaxResponsesPerOp {
			b.im.reportOnce(b.label, Degraded, b.label, "too-many-responses", "only the first "+itoa(MaxResponsesPerOp)+" responses were scanned for examples", "")
			break
		}
		rv, _, err := deref(b.im.root, responses.m[code])
		r := asMap(rv)
		if err != nil || r == nil {
			b.im.reportOnce(b.label+"|"+code, NeedsReview, b.label, "response-ref", "response "+code+" could not be resolved ("+errString(err)+")", "")
			continue
		}
		if b.im.v2 {
			out = b.exampleV2(out, code, r)
		} else {
			out = b.exampleV3(out, code, r)
		}
	}
	if len(out) == 0 {
		return nil
	}
	b.im.res.Report.Stats.Examples += len(out)
	b.im.report(Converted, b.label, "response-examples", itoa(len(out))+" saved response example(s) from declared examples", "")
	return mustJSON(out)
}

func (b *reqBuilder) exampleV3(out []*omap, code string, r *omap) []*omap {
	content := asMap(r.get("content"))
	for _, mt := range content.keysOrNil() {
		media := asMap(content.m[mt])
		var named []struct {
			name string
			val  any
		}
		if media.has("example") {
			named = append(named, struct {
				name string
				val  any
			}{"", media.get("example")})
		}
		if ex := asMap(media.get("examples")); ex != nil {
			for _, k := range ex.keys {
				if len(named) >= MaxExamplesPerResp {
					break
				}
				e, _, err := deref(b.im.root, ex.m[k])
				em := asMap(e)
				if err != nil || em == nil {
					continue
				}
				if !em.has("value") {
					b.im.reportOnce(b.label+"|"+k, Degraded, b.label, "external-example", "example "+k+" uses externalValue, which is never fetched", "")
					continue
				}
				named = append(named, struct {
					name string
					val  any
				}{k, em.get("value")})
			}
		}
		for _, n := range named {
			out = append(out, savedExample(code, asString(r.get("description")), n.name, mt, n.val))
		}
	}
	return out
}

func (b *reqBuilder) exampleV2(out []*omap, code string, r *omap) []*omap {
	ex := asMap(r.get("examples"))
	for _, mt := range ex.keysOrNil() {
		out = append(out, savedExample(code, asString(r.get("description")), "", mt, ex.m[mt]))
	}
	return out
}

func savedExample(code, desc, name, mt string, val any) *omap {
	e := newOmap()
	label := code
	if name != "" {
		label += " " + name
	} else if desc != "" {
		label += " " + desc
	}
	e.set("name", label)
	e.set("status", desc)
	e.set("code", statusCode(code))
	hdr := newOmap()
	hdr.set("key", "Content-Type")
	hdr.set("value", mt)
	e.set("header", []any{hdr})
	lower := strings.ToLower(mt)
	switch {
	case strings.Contains(lower, "json"):
		e.set("body", jsonText(val))
		e.set("_postman_previewlanguage", "json")
	case strings.Contains(lower, "xml"):
		e.set("body", scalarString(val))
		e.set("_postman_previewlanguage", "xml")
	default:
		e.set("body", scalarString(val))
		e.set("_postman_previewlanguage", "text")
	}
	return e
}
