package proxy

import "testing"

// Go's default MaxIdleConnsPerHost is 2. A browser or mobile app opening ten
// parallel requests to one API host would keep two connections and tear the rest
// down, paying a TCP and TLS handshake again on the next request to that host.
// The sender already sets its own pool; the proxy carries far more hosts, so it
// must be at least as generous.
func TestTransportKeepsIdleConnectionsPerHost(t *testing.T) {
	tr := New(nil, nil, nil, nil, nil).tr
	if tr.MaxIdleConnsPerHost < 64 {
		t.Errorf("MaxIdleConnsPerHost = %d, want at least 64 (0 means Go's default of 2)", tr.MaxIdleConnsPerHost)
	}
	if tr.MaxIdleConns < tr.MaxIdleConnsPerHost {
		t.Errorf("MaxIdleConns = %d is below the per-host cap %d", tr.MaxIdleConns, tr.MaxIdleConnsPerHost)
	}
}
