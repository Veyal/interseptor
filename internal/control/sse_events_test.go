package control

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// collectEvents reads SSE frames off /api/events and returns the decoded JSON
// objects. stop must be deferred before the test server's Close: the SSE handler
// blocks until its client disconnects, so closing the server first would wait on
// it forever.
func collectEvents(t *testing.T, base string) (events func() []map[string]any, ready func(), stop func()) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	var mu sync.Mutex
	var got []map[string]any
	hello := make(chan struct{})
	var once sync.Once
	go func() {
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "event: hello") {
				once.Do(func() { close(hello) })
				continue
			}
			payload, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue
			}
			var m map[string]any
			if json.Unmarshal([]byte(payload), &m) != nil {
				continue
			}
			mu.Lock()
			got = append(got, m)
			mu.Unlock()
		}
	}()
	return func() []map[string]any {
			mu.Lock()
			defer mu.Unlock()
			return append([]map[string]any(nil), got...)
		}, func() {
			select {
			case <-hello:
			case <-time.After(5 * time.Second):
				t.Fatal("no SSE hello frame")
			}
		}, func() { resp.Body.Close() }
}
