package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The conditional-intercept filter auto-saves while the operator types, so its
// response must not carry every held message's raw bytes (held responses can be
// up to 64 MB each). It returns the summary the SSE stream already uses: ids,
// lengths and labels; the UI loads a raw body on demand from /held/{id}/raw.
func TestInterceptFilterResponseOmitsHeldRaw(t *testing.T) {
	h, _, eng := newHub(t)
	eng.SetEnabled(true)
	req, raw, flow := newHeldRequest(t, "/held-for-filter")
	go eng.Hold(flow, req, raw)
	waitEngQueue(t, h, 1)
	id := eng.Snapshot().Queue[0].ID
	t.Cleanup(func() { _ = eng.Drop(id) })

	rec := httptest.NewRecorder()
	(&interceptAPI{h}).setInterceptFilter(rec, httptest.NewRequest(http.MethodPost, "/api/intercept/filter",
		strings.NewReader(`{"enabled":true,"target":"url","pattern":"admin"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Seq           int64  `json:"seq"`
		FilterPattern string `json:"filterPattern"`
		Queue         []struct {
			ID  int64  `json:"id"`
			Len int    `json:"len"`
			Raw string `json:"raw"`
		} `json:"queue"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.FilterPattern != "admin" || len(out.Queue) != 1 {
		t.Fatalf("summary = %+v", out)
	}
	if out.Queue[0].Raw != "" || out.Queue[0].Len != len(raw) || out.Queue[0].ID != id {
		t.Fatalf("held item = %+v, want id %d, len %d and no raw", out.Queue[0], id, len(raw))
	}
}
