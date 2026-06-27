// Package harx converts captured flows to and from the HAR 1.2 (HTTP Archive)
// format for interop with browsers, Postman, and other tooling.
package harx

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/Veyal/interceptor/internal/store"
)

type har struct {
	Log harLog `json:"log"`
}
type harLog struct {
	Version string     `json:"version"`
	Creator harNamed   `json:"creator"`
	Entries []harEntry `json:"entries"`
}
type harNamed struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type harEntry struct {
	StartedDateTime string         `json:"startedDateTime"`
	Time            float64        `json:"time"`
	Request         harReq         `json:"request"`
	Response        harRes         `json:"response"`
	Cache           map[string]any `json:"cache"`
	Timings         harTimings     `json:"timings"`
}
type harReq struct {
	Method      string       `json:"method"`
	URL         string       `json:"url"`
	HTTPVersion string       `json:"httpVersion"`
	Headers     []harHeader  `json:"headers"`
	QueryString []harHeader  `json:"queryString"`
	Cookies     []any        `json:"cookies"`
	HeadersSize int          `json:"headersSize"`
	BodySize    int          `json:"bodySize"`
	PostData    *harPostData `json:"postData,omitempty"`
}
type harRes struct {
	Status      int         `json:"status"`
	StatusText  string      `json:"statusText"`
	HTTPVersion string      `json:"httpVersion"`
	Headers     []harHeader `json:"headers"`
	Cookies     []any       `json:"cookies"`
	Content     harContent  `json:"content"`
	RedirectURL string      `json:"redirectURL"`
	HeadersSize int         `json:"headersSize"`
	BodySize    int         `json:"bodySize"`
}
type harHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type harPostData struct {
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}
type harContent struct {
	Size     int    `json:"size"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}
type harTimings struct {
	Send    float64 `json:"send"`
	Wait    float64 `json:"wait"`
	Receive float64 `json:"receive"`
}

// Build serializes flows to a HAR document. body returns a flow body by hash.
func Build(flows []*store.Flow, body func(hash string) []byte) []byte {
	doc := har{Log: harLog{
		Version: "1.2",
		Creator: harNamed{Name: "Interceptor", Version: "0.1.0"},
	}}
	for _, f := range flows {
		reqBody := body(f.ReqBodyHash)
		resBody := body(f.ResBodyHash)
		entry := harEntry{
			StartedDateTime: f.TS.Format(time.RFC3339Nano),
			Time:            float64(f.DurationMs),
			Cache:           map[string]any{},
			Timings:         harTimings{Wait: float64(f.DurationMs)},
			Request: harReq{
				Method: f.Method, URL: flowURL(f), HTTPVersion: orVal(f.HTTPVersion, "HTTP/1.1"),
				Headers: headers(f.ReqHeaders), QueryString: []harHeader{}, Cookies: []any{},
				HeadersSize: -1, BodySize: len(reqBody),
			},
			Response: harRes{
				Status: f.Status, StatusText: "", HTTPVersion: orVal(f.HTTPVersion, "HTTP/1.1"),
				Headers: headers(f.ResHeaders), Cookies: []any{}, RedirectURL: "", HeadersSize: -1,
				BodySize: len(resBody),
				Content:  harContent{Size: len(resBody), MimeType: f.Mime, Text: string(resBody)},
			},
		}
		if len(reqBody) > 0 {
			entry.Request.PostData = &harPostData{MimeType: contentType(f.ReqHeaders), Text: string(reqBody)}
		}
		doc.Log.Entries = append(doc.Log.Entries, entry)
	}
	out, _ := json.MarshalIndent(doc, "", "  ")
	return out
}

// Entry is a parsed HAR entry ready to import as a flow.
type Entry struct {
	Method      string
	URL         string
	HTTPVersion string
	ReqHeaders  map[string][]string
	ReqBody     []byte
	Status      int
	ResHeaders  map[string][]string
	ResBody     []byte
	Mime        string
	TS          time.Time
	DurationMs  int64
}

// Parse reads a HAR document into importable entries.
func Parse(data []byte) ([]Entry, error) {
	var doc har
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(doc.Log.Entries))
	for _, e := range doc.Log.Entries {
		ts, _ := time.Parse(time.RFC3339Nano, e.StartedDateTime)
		en := Entry{
			Method: e.Request.Method, URL: e.Request.URL, HTTPVersion: e.Request.HTTPVersion,
			ReqHeaders: headerMap(e.Request.Headers), Status: e.Response.Status,
			ResHeaders: headerMap(e.Response.Headers), Mime: e.Response.Content.MimeType,
			TS: ts, DurationMs: int64(e.Time),
		}
		if e.Request.PostData != nil {
			en.ReqBody = []byte(e.Request.PostData.Text)
		}
		en.ResBody = []byte(e.Response.Content.Text)
		out = append(out, en)
	}
	return out, nil
}

func flowURL(f *store.Flow) string {
	scheme := orVal(f.Scheme, "http") // a flow errored before the scheme is known would otherwise yield "://host"
	host := f.Host
	if !((scheme == "https" && f.Port == 443) || (scheme == "http" && f.Port == 80) || f.Port == 0) {
		host = host + ":" + strconv.Itoa(f.Port)
	}
	return scheme + "://" + host + orVal(f.Path, "/")
}

func headers(h map[string][]string) []harHeader {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []harHeader
	for _, k := range keys {
		for _, v := range h[k] {
			out = append(out, harHeader{Name: k, Value: v})
		}
	}
	if out == nil {
		out = []harHeader{}
	}
	return out
}

func headerMap(hs []harHeader) map[string][]string {
	m := map[string][]string{}
	for _, h := range hs {
		m[h.Name] = append(m[h.Name], h.Value)
	}
	return m
}

func contentType(h map[string][]string) string {
	for k, v := range h {
		if len(v) > 0 && (k == "Content-Type" || k == "content-type") {
			return v[0]
		}
	}
	return ""
}

func orVal(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
