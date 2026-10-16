package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/testbrowser"
)

func TestTestBrowserLaunchUsesSeamAndReportsResult(t *testing.T) {
	h, _, _ := newHub(t)
	var gotProxy string
	h.launchTestBrowser = func(proxy string) (testbrowser.LaunchResult, error) {
		gotProxy = proxy
		return testbrowser.LaunchResult{Browser: "/fake/chrome", ProfileDir: "/tmp/p", Proxy: proxy, PID: 99}, nil
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/test-browser/launch", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["browser"] != "/fake/chrome" || out["pid"].(float64) != 99 {
		t.Fatalf("unexpected body: %v", out)
	}
	if gotProxy == "" || !strings.Contains(gotProxy, ":") {
		t.Fatalf("launcher got proxy %q, want a host:port", gotProxy)
	}
}

func TestTestBrowserLaunchReturnsHonestErrorWhenNoBrowser(t *testing.T) {
	h, _, _ := newHub(t)
	h.launchTestBrowser = func(string) (testbrowser.LaunchResult, error) {
		return testbrowser.LaunchResult{}, errors.New("no headless Chrome found: set INTERSEPTOR_CHROME")
	}
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/test-browser/launch", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestTestBrowserAvailabilityReported(t *testing.T) {
	h, _, _ := newHub(t)
	h.testBrowserAvailable = func() bool { return true }
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/test-browser")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["available"] != true {
		t.Fatalf("available = %v, want true", out["available"])
	}
	if s, _ := out["proxy"].(string); !strings.Contains(s, ":") {
		t.Fatalf("proxy = %v, want a host:port", out["proxy"])
	}
}
