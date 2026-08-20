package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Veyal/interseptor/internal/store"
)

func TestRepeaterHistoryFiltersEndpointBeforeLimit(t *testing.T) {
	h, s, _ := newHub(t)
	// This matching flow is deliberately older than the default 100-row page.
	s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(1), Method: "GET", Scheme: "https", Host: "api.example.com", Port: 8443,
		Path: "/v1/users?id=1", Status: 500, Flags: store.FlagRepeater,
	})
	// The queryless endpoint identity includes scheme, host, port, and path. A
	// second query variant should still belong to the same endpoint, while each
	// of these near matches must be excluded.
	s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(2), Method: "GET", Scheme: "https", Host: "api.example.com", Port: 8443,
		Path: "/v1/users?sort=name", Status: 502, Flags: store.FlagRepeater,
	})
	s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(3), Method: "GET", Scheme: "http", Host: "api.example.com", Port: 8443,
		Path: "/v1/users?scheme=wrong", Status: 200, Flags: store.FlagRepeater,
	})
	s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(4), Method: "GET", Scheme: "https", Host: "api.example.com", Port: 443,
		Path: "/v1/users?port=wrong", Status: 200, Flags: store.FlagRepeater,
	})
	s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(5), Method: "GET", Scheme: "https", Host: "api.example.com", Port: 8443,
		Path: "/v1/users/details", Status: 200, Flags: store.FlagRepeater,
	})
	for i := 0; i < 100; i++ {
		s.InsertFlow(&store.Flow{
			TS: time.UnixMilli(int64(10 + i)), Method: "GET", Scheme: "https", Host: "other.example.com", Port: 443,
			Path: "/noise", Status: 200, Flags: store.FlagRepeater,
		})
	}
	s.InsertFlow(&store.Flow{
		TS: time.UnixMilli(200), Method: "GET", Scheme: "https", Host: "API.EXAMPLE.COM", Port: 8443,
		Path: "/v1/users?id=new", Status: 401, Flags: store.FlagRepeater,
	})

	ts := httptest.NewServer(h.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/api/repeater/history?scheme=https&host=api.example.com&port=8443&path=%2Fv1%2Fusers")
	if err != nil {
		t.Fatalf("GET repeater history: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Flows []struct {
			ID     int64  `json:"id"`
			Path   string `json:"path"`
			Status int    `json:"status"`
		} `json:"flows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if len(out.Flows) != 3 {
		t.Fatalf("filtered history returned %d flows, want 3: %+v", len(out.Flows), out.Flows)
	}
	for i, want := range []int{401, 502, 500} {
		if out.Flows[i].Status != want {
			t.Errorf("flow %d status = %d, want %d: %+v", i, out.Flows[i].Status, want, out.Flows)
		}
	}
}

func TestRepeaterHistoryEndpointRejectsAmbiguousOrIncompleteFilters(t *testing.T) {
	tests := []url.Values{
		{"endpoint": {"https://api.example.com/x"}, "host": {"api.example.com"}},
		{"scheme": {"https"}, "host": {"api.example.com"}, "port": {"443"}},
		{"scheme": {"ftp"}, "host": {"api.example.com"}, "port": {"21"}, "path": {"/x"}},
		{"scheme": {"https"}, "host": {"api.example.com"}, "port": {"not-a-port"}, "path": {"/x"}},
		{"scheme": {"https"}, "host": {"api.example.com"}, "port": {"0"}, "path": {"/x"}},
		{"scheme": {"https"}, "host": {"api.example.com"}, "port": {"65536"}, "path": {"/x"}},
		{"endpoint": {"https://api.example.com:65536/x"}},
	}
	for _, query := range tests {
		if endpoint, err := repeaterHistoryEndpoint(query); err == nil {
			t.Errorf("repeaterHistoryEndpoint(%q) = %+v, want error", query.Encode(), endpoint)
		}
	}
}

func TestRepeaterHistoryEndpointNormalizesIPv6AndDefaultPort(t *testing.T) {
	component, err := repeaterHistoryEndpoint(url.Values{
		"scheme": {"https"}, "host": {"[::1]"}, "port": {"443"}, "path": {"/x?q=1"},
	})
	if err != nil {
		t.Fatalf("component endpoint: %v", err)
	}
	absolute, err := repeaterHistoryEndpoint(url.Values{"endpoint": {"https://[::1]/x?q=2"}})
	if err != nil {
		t.Fatalf("absolute endpoint: %v", err)
	}
	if *component != *absolute || component.host != "::1" || component.port != 443 || component.path != "/x" {
		t.Fatalf("normalized endpoints differ: component=%+v absolute=%+v", component, absolute)
	}
}

func TestRepeaterHistoryLimitIsBounded(t *testing.T) {
	tests := []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{raw: "", want: 100},
		{raw: "1", want: 1},
		{raw: "5000", want: 5000},
		{raw: "0", wantErr: true},
		{raw: "-1", wantErr: true},
		{raw: "5001", wantErr: true},
		{raw: "many", wantErr: true},
	}
	for _, tc := range tests {
		got, err := repeaterHistoryLimit(tc.raw)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("repeaterHistoryLimit(%q) = (%d, %v), want (%d, error=%v)", tc.raw, got, err, tc.want, tc.wantErr)
		}
	}
}
