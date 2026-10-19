package control

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// uiAsset is one embedded UI file prepared for serving: its strong validator
// and, for text types, a gzip copy built once on first request. The embedded
// FS is immutable for the life of the process, so both are computed lazily and
// kept forever (about 0.7 MB of gzip copies for the whole UI).
type uiAsset struct {
	data   []byte
	etag   string // `"<sha256 prefix>"`
	gz     []byte // nil when gzip does not help or the type is not compressible
	gzETag string // `"<sha256 prefix>-gz"`
}

var uiAssets sync.Map // name -> *uiAsset

// loadUIAsset returns the prepared asset for an embedded path, or ok=false when
// the file does not exist.
func loadUIAsset(name string) (*uiAsset, bool) {
	if v, ok := uiAssets.Load(name); ok {
		return v.(*uiAsset), true
	}
	data, err := uiFS.ReadFile("ui/" + name)
	if err != nil {
		return nil, false
	}
	sum := sha256.Sum256(data)
	tag := hex.EncodeToString(sum[:12])
	a := &uiAsset{data: data, etag: `"` + tag + `"`}
	if uiCompressible(name) && len(data) > 512 {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if _, err := zw.Write(data); err == nil && zw.Close() == nil && buf.Len() < len(data) {
			a.gz = buf.Bytes()
			a.gzETag = `"` + tag + `-gz"`
		}
	}
	v, _ := uiAssets.LoadOrStore(name, a)
	return v.(*uiAsset), true
}

func uiCompressible(name string) bool {
	for _, ext := range []string{".html", ".js", ".css", ".svg", ".json", ".webmanifest", ".txt", ".md"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// acceptsGzip reports whether the Accept-Encoding header allows gzip (an
// explicit q=0 refuses it).
func acceptsGzip(h string) bool {
	for _, part := range strings.Split(h, ",") {
		fields := strings.Split(part, ";")
		coding := strings.ToLower(strings.TrimSpace(fields[0]))
		if coding != "gzip" && coding != "*" {
			continue
		}
		refused := false
		for _, p := range fields[1:] {
			p = strings.ToLower(strings.ReplaceAll(p, " ", ""))
			if strings.HasPrefix(p, "q=") {
				if q, err := strconv.ParseFloat(p[2:], 64); err == nil && q == 0 {
					refused = true
				}
			}
		}
		if !refused {
			return true
		}
	}
	return false
}

// etagMatches implements the weak comparison If-None-Match uses: any listed
// tag (with or without the W/ prefix) equal to etag, or "*", matches.
func etagMatches(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimSpace(t)
		if t == "*" || strings.TrimPrefix(t, "W/") == etag {
			return true
		}
	}
	return false
}

// writeUIAsset serves a prepared asset with revalidation and gzip negotiation.
// Cache-Control: no-cache keeps the upgrade guarantee the old no-store gave
// (the browser must ask before reusing a module that has no version hash) while
// a matching validator turns the answer into a body-less 304.
func writeUIAsset(w http.ResponseWriter, r *http.Request, name string, a *uiAsset) {
	h := w.Header()
	h.Set("Content-Type", uiContentType(name))
	h.Set("Cache-Control", "no-cache")
	body, etag := a.data, a.etag
	if a.gz != nil {
		h.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r.Header.Get("Accept-Encoding")) {
			body, etag = a.gz, a.gzETag
			h.Set("Content-Encoding", "gzip")
		}
	}
	h.Set("ETag", etag)
	if inm := r.Header.Get("If-None-Match"); inm != "" && etagMatches(inm, etag) {
		h.Del("Content-Encoding")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}
