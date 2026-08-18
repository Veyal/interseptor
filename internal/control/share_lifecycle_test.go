package control

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/intruder"
	"github.com/Veyal/interseptor/internal/store"
	"github.com/Veyal/interseptor/internal/tunnel"
)

const controlLifecycleTestTimeout = 2 * time.Second

func awaitControlLifecycle(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(controlLifecycleTestTimeout):
		t.Fatalf("timed out waiting for %s", what)
	}
}

type lifecycleTunnel struct {
	calls []string
}

func (t *lifecycleTunnel) Status() tunnel.Status { return tunnel.Status{} }
func (t *lifecycleTunnel) Installed() bool       { return true }
func (t *lifecycleTunnel) SetOnURL(func(string)) {}
func (t *lifecycleTunnel) Start(context.Context) (tunnel.Status, error) {
	return tunnel.Status{}, nil
}
func (t *lifecycleTunnel) Stop() error {
	t.calls = append(t.calls, "stop")
	return nil
}
func (t *lifecycleTunnel) Close() {
	t.calls = append(t.calls, "close")
}

func TestHubCloseStopsThenClosesTunnelExactlyOnce(t *testing.T) {
	tun := &lifecycleTunnel{}
	h := &Hub{tun: tun}

	h.Close()
	h.Close()

	want := []string{"stop", "close"}
	if !reflect.DeepEqual(tun.calls, want) {
		t.Fatalf("tunnel lifecycle calls = %v, want %v", tun.calls, want)
	}
}

func TestHubCloseCancelsAndWaitsForActiveIntruder(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	forceRelease := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		select {
		case <-r.Context().Done():
			close(requestCanceled)
		case <-forceRelease:
		}
	}))
	defer target.Close()

	h, _, _ := newHub(t)
	if err := h.intr.Start(intruder.Spec{
		Target:     target.URL,
		Template:   "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n",
		AttackType: "repeat",
		Repeat:     1,
		Threads:    1,
	}); err != nil {
		t.Fatalf("start intruder: %v", err)
	}
	awaitControlLifecycle(t, requestStarted, "Intruder request")

	closeReturned := make(chan struct{})
	go func() {
		h.Close()
		close(closeReturned)
	}()
	select {
	case <-closeReturned:
	case <-time.After(controlLifecycleTestTimeout):
		close(forceRelease)
		awaitControlLifecycle(t, closeReturned, "Hub.Close after forced request release")
		t.Fatal("Hub.Close did not cancel the active Intruder request")
	}
	awaitControlLifecycle(t, requestCanceled, "Intruder request cancellation")

	if st := h.intr.State(); st.Running {
		t.Fatalf("Intruder still running after Hub.Close: %+v", st)
	}
}

func TestHubCloseWaitsForPurgeGC(t *testing.T) {
	h, _, _ := newHub(t)
	started := make(chan struct{})
	release := make(chan struct{})
	h.gcBodiesFn = func() (int64, int64, error) {
		close(started)
		<-release
		return 0, 0, nil
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/flows/purge", strings.NewReader(`{"hosts":[],"mode":"delete"}`))
	(&flowAPI{h}).purgeFlows(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("purge status = %d, want 200", rec.Code)
	}
	awaitControlLifecycle(t, started, "purge GC start")

	closeReturned := make(chan struct{})
	go func() {
		h.Close()
		close(closeReturned)
	}()
	select {
	case <-closeReturned:
		t.Fatal("Hub.Close returned while purge GC was still running")
	default:
	}
	close(release)
	awaitControlLifecycle(t, closeReturned, "Hub.Close after purge GC completion")
}

func TestHubCloseWaitsForRetentionGC(t *testing.T) {
	h, st, _ := newHub(t)
	for i := int64(0); i < 2; i++ {
		if _, err := st.InsertFlow(&store.Flow{TS: time.UnixMilli(i + 1), Method: "GET", Host: "example.com", Path: "/"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetSetting(retentionMaxFlowsKey, "1"); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	h.gcBodiesFn = func() (int64, int64, error) {
		close(started)
		<-release
		return 0, 0, nil
	}

	deleted, err := h.runRetentionOnce()
	if err != nil || deleted != 1 {
		t.Fatalf("runRetentionOnce() = (%d, %v), want (1, nil)", deleted, err)
	}
	awaitControlLifecycle(t, started, "retention GC start")

	closeReturned := make(chan struct{})
	go func() {
		h.Close()
		close(closeReturned)
	}()
	select {
	case <-closeReturned:
		t.Fatal("Hub.Close returned while retention GC was still running")
	default:
	}
	close(release)
	awaitControlLifecycle(t, closeReturned, "Hub.Close after retention GC completion")
}

func TestIntruderStartAfterHubCloseReturnsServiceUnavailable(t *testing.T) {
	h, _, _ := newHub(t)
	h.Close()
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	body, err := json.Marshal(map[string]any{
		"target":     "http://example.com",
		"template":   "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n",
		"attackType": "repeat",
		"repeat":     1,
		"threads":    1,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(ts.URL+"/api/intruder/start", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (error=%q)", resp.StatusCode, http.StatusServiceUnavailable, out.Error)
	}
	if out.Error != intruder.ErrClosed.Error() {
		t.Fatalf("error = %q, want %q", out.Error, intruder.ErrClosed)
	}
}
