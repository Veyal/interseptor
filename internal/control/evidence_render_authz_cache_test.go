package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func authzBody(rows, cols int, errText string) []byte {
	var runs []authzRunOut
	for r := 0; r < rows; r++ {
		run := authzRunOut{FlowID: int64(r + 1), Method: "GET", Host: "example.com", Path: fmt.Sprintf("/r/%d", r)}
		for c := 0; c < cols; c++ {
			run.Results = append(run.Results, authzResult{Name: fmt.Sprintf("id%d", c), Status: 200, Length: 10, Error: errText, Mime: "text/html", BodyHash: strings.Repeat("a", 64)})
		}
		runs = append(runs, run)
	}
	b, _ := json.Marshal(map[string]any{"runs": runs, "summary": map[string]any{"endpoints": rows}})
	return b
}

func TestAuthzCaptureCachesBoundedCopyAndKeepsResponse(t *testing.T) {
	h, _, _ := newHub(t)
	ev := newEvidenceAPI(h)
	body := authzBody(150, 40, "")
	wrapped := ev.captureAuthzRun(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	rec := httptest.NewRecorder()
	wrapped(rec, httptest.NewRequest("POST", "/api/authz/run", nil))
	var out struct {
		RunID string          `json:"runId"`
		Runs  []authzRunOut   `json:"runs"`
		Sum   json.RawMessage `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.RunID == "" {
		t.Fatalf("response: %v %.200s", err, rec.Body.String())
	}
	if len(out.Runs) != 150 || len(out.Runs[0].Results) != 40 {
		t.Fatalf("the client must still get the full response: %d runs, %d results", len(out.Runs), len(out.Runs[0].Results))
	}
	_, cached, ok := ev.authz.get(out.RunID)
	if !ok || len(cached) != authzCacheRows || len(cached[0].Results) != authzCacheCols {
		t.Fatalf("cache must be bounded to %dx%d, got %d rows x %d cols", authzCacheRows, authzCacheCols, len(cached), len(cached[0].Results))
	}
	if cached[0].Results[0].BodyHash != "" || cached[0].Results[0].Mime != "" {
		t.Fatalf("cache keeps only the fields the matrix needs: %+v", cached[0].Results[0])
	}
}

func TestAuthzCaptureOversizedResponsePassesThroughUncached(t *testing.T) {
	h, _, _ := newHub(t)
	ev := newEvidenceAPI(h)
	big := authzBody(60, 30, strings.Repeat("e", 6000)) // well over the capture cap
	if len(big) <= maxAuthzCaptureBytes {
		big = append(big, bytes.Repeat([]byte(" "), maxAuthzCaptureBytes+1-len(big))...)
	}
	wrapped := ev.captureAuthzRun(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom", "kept")
		w.WriteHeader(http.StatusOK)
		// stream in chunks like a real handler
		for off := 0; off < len(big); off += 64 << 10 {
			end := min(off+64<<10, len(big))
			_, _ = w.Write(big[off:end])
		}
	})
	rec := httptest.NewRecorder()
	wrapped(rec, httptest.NewRequest("POST", "/api/authz/run", nil))
	if !bytes.Equal(rec.Body.Bytes(), big) {
		t.Fatalf("oversized response must pass through byte for byte (%d vs %d)", rec.Body.Len(), len(big))
	}
	if rec.Header().Get("X-Custom") != "kept" || rec.Code != 200 {
		t.Fatalf("headers/status lost: %v %d", rec.Header(), rec.Code)
	}
	if len(ev.authz.order) != 0 {
		t.Fatal("oversized run must not be cached")
	}
}

func TestBufferedResponseSupportsFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	b := &bufferedResponse{dst: rec, header: http.Header{}, limit: 1 << 20}
	if _, ok := any(b).(http.Flusher); !ok {
		t.Fatal("wrapped writer must keep http.Flusher so streaming handlers still work")
	}
	_, _ = b.Write([]byte("abc"))
	b.Flush()
	if rec.Body.String() != "abc" || !b.overflow {
		t.Fatalf("flush must stream what was buffered: %q", rec.Body.String())
	}
}
