package control

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Veyal/interseptor/internal/burpx"
	"github.com/Veyal/interseptor/internal/store"
)

// importBurp streams a Burp Suite "Save items" XML export into History.
// Native .burp project files are intentionally rejected by burpx because
// PortSwigger does not publish that persistence format as an interchange API.
func (h *projectAPI) importBurp(w http.ResponseWriter, r *http.Request) {
	upload, ok := spoolBurpXML(w, r)
	if !ok {
		return
	}
	defer func() {
		name := upload.Name()
		_ = upload.Close()
		_ = os.Remove(name)
	}()
	if _, err := burpx.Parse(upload, nil); err != nil {
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := upload.Seek(0, io.SeekStart); err != nil {
		httpInternalErr(w, err)
		return
	}

	imported, skipped := 0, 0
	var importFailure error
	_, err := burpx.Parse(upload, func(e burpx.Entry) error {
		u, err := url.Parse(e.URL)
		if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			skipped++
			return nil
		}
		ts := e.TS
		if ts.IsZero() {
			ts = time.Now()
		}
		flow := &store.Flow{
			TS:          ts,
			Method:      orVal(strings.TrimSpace(e.Method), "GET"),
			Scheme:      u.Scheme,
			Host:        u.Hostname(),
			Port:        atoiOr(u.Port(), defaultPortFor(u.Scheme)),
			Path:        orVal(u.RequestURI(), "/"),
			HTTPVersion: orVal(e.HTTPVersion, "HTTP/1.1"),
			Status:      e.Status,
			ReqHeaders:  map[string][]string(e.ReqHeaders),
			ResHeaders:  map[string][]string(e.ResHeaders),
			Mime:        e.Mime,
			Flags:       store.FlagImported,
			Note:        e.Comment,
		}
		if err := h.insertImportedFlow(flow, e.ReqBody, e.ResBody); err != nil {
			importFailure = err
			return importFailure
		}
		imported++
		return nil
	})
	if err != nil {
		if importFailure != nil {
			httpInternalErr(w, importFailure)
			return
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpErr(w, http.StatusRequestEntityTooLarge, "Burp XML import exceeds the upload limit")
			return
		}
		httpErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if imported > 0 {
		h.epsCache.invalidate()
		h.broadcast(map[string]any{"type": "flow.new"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "skipped": skipped})
}

func spoolBurpXML(w http.ResponseWriter, r *http.Request) (*os.File, bool) {
	tmp, err := os.CreateTemp("", "interseptor-burp-*.xml")
	if err != nil {
		httpInternalErr(w, err)
		return nil, false
	}
	cleanup := func() {
		name := tmp.Name()
		_ = tmp.Close()
		_ = os.Remove(name)
	}
	if _, err := io.Copy(tmp, http.MaxBytesReader(w, r.Body, maxRequestBody)); err != nil {
		cleanup()
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpErr(w, http.StatusRequestEntityTooLarge, "Burp XML import exceeds the upload limit")
		} else {
			httpErr(w, http.StatusBadRequest, "could not read Burp XML import")
		}
		return nil, false
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		httpInternalErr(w, err)
		return nil, false
	}
	return tmp, true
}

// stageImportedBody protects a finalized body from concurrent garbage
// collection until InsertFlow publishes its hash. The returned writer remains
// useful only for Abort when a later body fails before InsertFlow is reached.
func stageImportedBody(st *store.Store, body []byte) (*store.BodyWriter, string, int64, error) {
	if len(body) == 0 {
		return nil, "", 0, nil
	}
	w, err := st.NewFlowBodyWriter()
	if err != nil {
		return nil, "", 0, err
	}
	if _, err := w.Write(body); err != nil {
		w.Abort()
		return nil, "", 0, err
	}
	hash, n, err := w.Finalize()
	if err != nil {
		w.Abort()
		return nil, "", 0, err
	}
	return w, hash, n, nil
}
