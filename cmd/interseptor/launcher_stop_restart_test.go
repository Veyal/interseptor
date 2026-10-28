package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/launcher"
)

// The stop goroutine's registry cleanup runs after the old process dies; if the
// project was restarted meanwhile, the new instance's entry must survive.
func TestHandleStopKeepsRestartedInstanceEntry(t *testing.T) {
	origAlive, origGraceful, origForce := launcherAlive, launcherGraceful, launcherForce
	t.Cleanup(func() {
		launcherAlive, launcherGraceful, launcherForce = origAlive, origGraceful, origForce
	})
	const oldPID, newPID = 4343, 5454

	dir := t.TempDir()
	reg, err := launcher.Open(filepath.Join(dir, "instances.json"))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	if err := reg.Upsert(launcher.Instance{Project: "demo", PID: oldPID}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var mu sync.Mutex
	oldAlive := true
	polled := make(chan struct{}, 16)
	launcherAlive = func(pid int) bool {
		mu.Lock()
		defer mu.Unlock()
		if pid == oldPID {
			select {
			case polled <- struct{}{}:
			default:
			}
			return oldAlive
		}
		return true
	}
	launcherGraceful = func(pid int) error {
		mu.Lock()
		oldAlive = false
		mu.Unlock()
		// The project is restarted before the stop goroutine cleans up.
		return reg.Upsert(launcher.Instance{Project: "demo", PID: newPID})
	}
	launcherForce = func(int) error { return nil }

	lh := &launcherServer{globalDir: dir, projectsDir: filepath.Join(dir, "projects"), logsDir: filepath.Join(dir, "logs"), exe: "interceptor", reg: reg}
	req := httptest.NewRequest(http.MethodPost, launcherTestOrigin+"/api/instances/demo/stop", nil)
	req.SetPathValue("project", "demo")
	rec := httptest.NewRecorder()
	lh.handleStop(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}

	// Wait for the stop goroutine's poll of the old PID, then give its cleanup time to run.
	for i := 0; i < 2; i++ {
		select {
		case <-polled:
		case <-time.After(3 * time.Second):
			t.Fatal("stop goroutine never polled the old pid")
		}
	}
	time.Sleep(300 * time.Millisecond)
	if got, ok := reg.Get("demo"); !ok || got.PID != newPID {
		t.Fatalf("restarted instance unregistered by the old stop: %+v %v", got, ok)
	}
}
