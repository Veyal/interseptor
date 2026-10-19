package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/intercept"
)

// A drop scheduled in the UI (Undo window) is marked on the server, so turning
// intercept off from any client (another window, MCP, REST) drops it instead of
// sending it, a Forward is refused, and Undo (pending:false) restores Forward.
func TestInterceptDropPendingWinsOverInterceptOff(t *testing.T) {
	h, _, eng := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	eng.SetEnabled(true)
	req, raw, flow := newHeldRequest(t, "/drop-me")
	got := make(chan intercept.Decision, 1)
	go func() { got <- eng.Hold(flow, req, raw) }()
	waitEngQueue(t, h, 1)
	id := eng.Snapshot().Queue[0].ID

	if code, _ := postJSON(t, fmt.Sprintf("%s/api/intercept/%d/drop-pending", ts.URL, id), `{"pending":true}`); code != http.StatusNoContent {
		t.Fatalf("mark status = %d", code)
	}
	resp, err := http.Get(ts.URL + "/api/intercept")
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Queue []struct {
			ID          int64 `json:"id"`
			DropPending bool  `json:"dropPending"`
		} `json:"queue"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(st.Queue) != 1 || !st.Queue[0].DropPending {
		t.Fatalf("GET /api/intercept queue = %+v, want dropPending", st.Queue)
	}
	if code, _ := postJSON(t, fmt.Sprintf("%s/api/intercept/%d/forward", ts.URL, id), ``); code != http.StatusConflict {
		t.Fatalf("forward of a drop-pending item status = %d, want 409", code)
	}
	if code, _ := postJSON(t, ts.URL+"/api/intercept/toggle", `{"enabled":false}`); code != http.StatusOK {
		t.Fatalf("toggle status = %d", code)
	}
	select {
	case d := <-got:
		if !d.Drop {
			t.Fatalf("intercept off released the drop-pending request as %+v, want drop", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("held request never released")
	}
	if code, _ := postJSON(t, fmt.Sprintf("%s/api/intercept/%d/drop-pending", ts.URL, id), `{"pending":false}`); code != http.StatusNotFound {
		t.Fatalf("unmark of a released item status = %d, want 404", code)
	}
}

func TestInterceptResponseDropPendingWinsOverResponseInterceptOff(t *testing.T) {
	h, _, eng := newHub(t)
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	eng.SetResponseEnabled(true)
	_, _, flow := newHeldRequest(t, "/resp")
	got := make(chan intercept.ResponseDecision, 1)
	go func() { got <- eng.HoldResponse(flow, []byte("HTTP/1.1 200 OK\r\n\r\n")) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(eng.ResponseQueue()) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	id := eng.ResponseQueue()[0].ID
	if code, _ := postJSON(t, fmt.Sprintf("%s/api/intercept/response/%d/drop-pending", ts.URL, id), `{"pending":true}`); code != http.StatusNoContent {
		t.Fatalf("mark status = %d", code)
	}
	if code, _ := postJSON(t, ts.URL+"/api/intercept/response/toggle", `{"enabled":false}`); code != http.StatusOK {
		t.Fatalf("toggle status = %d", code)
	}
	select {
	case d := <-got:
		if !d.Drop {
			t.Fatalf("response intercept off released the drop-pending response as %+v, want drop", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("held response never released")
	}
}
