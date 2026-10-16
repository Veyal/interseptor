// Package har imports HAR 1.2 files and Burp "Save items" XML as an editable
// collection (the alternative to importing them as History flows). Requests
// keep their exact method, URL, headers and body bytes; responses are not
// stored (use the History import for evidence). Nothing is sent or fetched.
package har

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Veyal/interseptor/internal/burpx"
	"github.com/Veyal/interseptor/internal/collimport/insomnia/impkit"
	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/harx"
	"github.com/Veyal/interseptor/internal/store"
)

// Limits for hostile input.
const (
	MaxInputBytes = impkit.MaxInputBytes
	MaxEntries    = 50000
	MaxBodyBytes  = 8 << 20
)

// Errors.
var (
	ErrTooLarge = errors.New("har: input exceeds 64 MiB")
	ErrEmpty    = errors.New("har: no requests found")
)

// Options tune a parse. NewID defaults to store.NewUID.
type Options struct {
	NewID       func() string
	Name        string
	NoGroupHost bool // keep a flat list instead of one folder per host
	Dedupe      bool // drop repeated method+URL+body requests
}

// Result is the parsed import, ready to preview and commit.
type Result struct {
	Collection store.Collection `json:"collection"`
	Items      []store.Item     `json:"items"`
	Report     impkit.Report    `json:"report"`
}

// Bundle returns the result as a store.CollectionsBundle.
func (r *Result) Bundle() store.CollectionsBundle {
	return store.CollectionsBundle{Version: store.CollectionsBundleVersion,
		Collections: []store.Collection{r.Collection}, Items: r.Items}
}

// request is the format-independent captured request.
type request struct {
	method  string
	url     string
	headers http.Header
	body    []byte
}

// ParseHAR imports a HAR document.
func ParseHAR(data []byte, opt Options) (*Result, error) {
	if len(data) > MaxInputBytes {
		return nil, ErrTooLarge
	}
	entries, err := harx.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("har: %w", err)
	}
	var reqs []request
	for _, e := range entries {
		reqs = append(reqs, request{e.Method, e.URL, http.Header(e.ReqHeaders), e.ReqBody})
	}
	return build(reqs, "HAR", "har", opt)
}

// ParseBurp imports Burp "Save items" XML (streamed, one item in memory).
func ParseBurp(r io.Reader, opt Options) (*Result, error) {
	var reqs []request
	over := false
	_, err := burpx.Parse(io.LimitReader(r, MaxInputBytes+1), func(e burpx.Entry) error {
		if len(reqs) >= MaxEntries {
			over = true
			return nil
		}
		reqs = append(reqs, request{e.Method, e.URL, e.ReqHeaders, e.ReqBody})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("burp: %w", err)
	}
	res, berr := build(reqs, "Burp", "burp", opt)
	if over && res != nil {
		res.Report.Entries = append(res.Report.Entries, impkit.Entry{Level: impkit.Blocked, Feature: "too-many-requests",
			Message: fmt.Sprintf("only the first %d items were imported", MaxEntries)})
		impkit.Finish(&res.Report, "Burp")
		res.Collection.ImportReport = impkit.MustJSON(res.Report)
	}
	return res, berr
}

func build(reqs []request, label, format string, opt Options) (*Result, error) {
	if opt.NewID == nil {
		opt.NewID = store.NewUID
	}
	name := opt.Name
	if name == "" {
		name = label + " import"
	}
	res := &Result{}
	res.Report.Format = format
	res.Collection = store.Collection{UID: opt.NewID(), Name: name, ScopePolicy: store.ScopePolicyBlock,
		Sidecar: impkit.MustJSON(postman.CollectionSidecar{Format: format})}
	folders := map[string]*store.Item{}
	prevByParent := map[string]string{}
	hostCount := map[string]int{}
	seen := map[string]bool{}
	dups, binary, oversize := 0, 0, 0
	if len(reqs) > MaxEntries {
		res.Report.Entries = append(res.Report.Entries, impkit.Entry{Level: impkit.Blocked, Feature: "too-many-requests",
			Message: fmt.Sprintf("only the first %d requests were imported", MaxEntries)})
		reqs = reqs[:MaxEntries]
	}
	for _, r := range reqs {
		if strings.TrimSpace(r.url) == "" {
			res.Report.Entries = append(res.Report.Entries, impkit.Entry{Level: impkit.Degraded, Feature: "no-url", Message: "an entry without a URL was skipped"})
			continue
		}
		if opt.Dedupe {
			key := r.method + " " + r.url + "\x00" + string(r.body)
			if seen[key] {
				dups++
				continue
			}
			seen[key] = true
		}
		host := hostOf(r.url)
		parent := ""
		if !opt.NoGroupHost {
			f := folders[host]
			if f == nil {
				f = &store.Item{UID: opt.NewID(), CollectionUID: res.Collection.UID, Kind: "folder", Name: host,
					Rank:    store.RankBetween(lastFolderRank(folders, prevByParent), ""),
					Sidecar: impkit.MustJSON(postman.ItemSidecar{})}
				folders[host] = f
				prevByParent[""] = f.Rank
				res.Items = append(res.Items, *f)
				res.Report.Stats.Folders++
			}
			parent = f.UID
		}
		it := store.Item{UID: opt.NewID(), CollectionUID: res.Collection.UID, ParentUID: parent, Kind: "request",
			Rank: store.RankBetween(prevByParent[parent], ""), Method: strings.ToUpper(r.method),
			Sidecar: impkit.MustJSON(postman.ItemSidecar{})}
		if it.Method == "" {
			it.Method = "GET"
		}
		prevByParent[parent] = it.Rank
		it.Name = impkit.Clip(it.Method+" "+impkit.HostAndPath(r.url), 120)
		it.URL = impkit.URLObject(r.url, nil)
		hs := headerRows(r.headers)
		path := host + "/" + it.Name
		impkit.ScanHeaderCredentials(&res.Report, path, it.UID, hs)
		it.Headers = impkit.Rows(hs)
		switch {
		case len(r.body) == 0:
		case len(r.body) > MaxBodyBytes:
			oversize++
		case !utf8.Valid(r.body):
			binary++
		default:
			it.Body = impkit.RawBody(string(r.body), impkit.LanguageFor(headerGet(r.headers, "Content-Type")))
		}
		hostCount[host]++
		res.Items = append(res.Items, it)
		res.Report.Stats.Requests++
	}
	add := func(l impkit.Level, f, m, s string) {
		res.Report.Entries = append(res.Report.Entries, impkit.Entry{Level: l, Feature: f, Message: m, Suggestion: s})
	}
	if dups > 0 {
		add(impkit.Converted, "deduplicated", fmt.Sprintf("%d repeated request(s) were dropped", dups), "")
	}
	if binary > 0 {
		add(impkit.NeedsReview, "binary-body", fmt.Sprintf("%d request(s) had a binary body that cannot be edited as text; the body was dropped", binary), "Use the History import to keep the exact bytes")
	}
	if oversize > 0 {
		add(impkit.Blocked, "body-too-large", fmt.Sprintf("%d request body(ies) over %d MiB were dropped", oversize, MaxBodyBytes>>20), "")
	}
	hosts := make([]string, 0, len(hostCount))
	for h := range hostCount {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		add(impkit.NeedsReview, "target-host", fmt.Sprintf("%d request(s) target %s; sends are blocked until the host is in scope", hostCount[h], h), "Add the host to the engagement scope")
	}
	add(impkit.Degraded, "responses-not-imported", "responses are not stored in the collection; header order is not preserved", "Import as History flows to keep evidence")
	impkit.Finish(&res.Report, label)
	res.Collection.ImportReport = impkit.MustJSON(res.Report)
	if res.Report.Stats.Requests == 0 {
		return res, ErrEmpty
	}
	return res, nil
}

func lastFolderRank(folders map[string]*store.Item, prev map[string]string) string { return prev[""] }

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return strings.ToLower(u.Host)
	}
	return "unknown-host"
}

func headerGet(h http.Header, k string) string {
	for name, v := range h {
		if strings.EqualFold(name, k) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

// headerRows flattens headers in a deterministic order, dropping HTTP/2
// pseudo-headers and Content-Length (recomputed on send).
func headerRows(h http.Header) []impkit.Row {
	names := make([]string, 0, len(h))
	for n := range h {
		if strings.HasPrefix(n, ":") || strings.EqualFold(n, "content-length") {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	var out []impkit.Row
	for _, n := range names {
		for _, v := range h[n] {
			out = append(out, impkit.Row{Key: n, Value: v})
		}
	}
	return out
}
