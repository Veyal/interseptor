package main

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Veyal/interseptor/internal/store"
)

func TestProxyListenerAuth_deniesHTTPAndCONNECTBeforeHandler(t *testing.T) {
	// Given
	var calls atomic.Int32
	handler := proxyListenerHandler(&net.TCPAddr{IP: net.IPv4zero, Port: 8080}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}), func(string) (bool, string, error) {
		return false, "", nil
	})

	for _, method := range []string{http.MethodGet, http.MethodConnect} {
		t.Run(method, func(t *testing.T) {
			// When
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(method, "http://example.com", nil))

			// Then
			if recorder.Code != http.StatusProxyAuthRequired {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusProxyAuthRequired)
			}
			if got := recorder.Header().Get("Proxy-Authenticate"); got != `Basic realm="interseptor"` {
				t.Fatalf("Proxy-Authenticate = %q", got)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("handler calls = %d, want 0", calls.Load())
	}
}

func TestProxyListenerAuth_acceptsOnlyFullScopeBasicPasswordAndRemovesHeader(t *testing.T) {
	// Given
	var calls atomic.Int32
	handler := proxyListenerHandler(&net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 8080}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("Proxy-Authorization"); got != "" {
			t.Errorf("forwarded Proxy-Authorization = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}), func(token string) (bool, string, error) {
		return token == "full-key", store.ScopeFull, nil
	})

	tests := []struct {
		name       string
		authorize  func(*http.Request)
		wantStatus int
	}{
		{name: "missing", authorize: func(*http.Request) {}, wantStatus: http.StatusProxyAuthRequired},
		{name: "bearer", authorize: func(r *http.Request) { r.Header.Set("Authorization", "Bearer full-key") }, wantStatus: http.StatusProxyAuthRequired},
		{name: "read scope", authorize: func(r *http.Request) { r.SetBasicAuth("user", "read-key") }, wantStatus: http.StatusProxyAuthRequired},
		{name: "full scope", authorize: func(r *http.Request) { r.SetBasicAuth("user", "full-key") }, wantStatus: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// When
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
			test.authorize(request)
			if username, password, ok := request.BasicAuth(); ok {
				request.Header.Del("Authorization")
				request.SetBasicAuth(username, password)
				request.Header.Set("Proxy-Authorization", request.Header.Get("Authorization"))
				request.Header.Del("Authorization")
			}
			handler.ServeHTTP(recorder, request)

			// Then
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
		})
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls = %d, want 1", calls.Load())
	}
}

func TestProxyListenerAuth_failsClosedOnVerifierError(t *testing.T) {
	// Given
	handler := proxyListenerHandler(&net.TCPAddr{IP: net.IPv4zero, Port: 8080}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler invoked")
	}), func(string) (bool, string, error) {
		return false, "", errors.New("database unavailable")
	})
	request := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	request.Header.Set("Proxy-Authorization", "Basic dXNlcjprZXk=")

	// When
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	// Then
	if recorder.Code != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusProxyAuthRequired)
	}
}

func TestProxyListenerRequiresAuth_classifiesActualIPv4AndIPv6Address(t *testing.T) {
	tests := []struct {
		name string
		addr net.Addr
		want bool
	}{
		{name: "IPv4 loopback", addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1")}, want: false},
		{name: "IPv6 loopback", addr: &net.TCPAddr{IP: net.ParseIP("::1")}, want: false},
		{name: "IPv4 wildcard", addr: &net.TCPAddr{IP: net.IPv4zero}, want: true},
		{name: "IPv6 wildcard", addr: &net.TCPAddr{IP: net.IPv6zero}, want: true},
		{name: "IPv4 non-loopback", addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.10")}, want: true},
		{name: "IPv6 non-loopback", addr: &net.TCPAddr{IP: net.ParseIP("2001:db8::10")}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := proxyListenerRequiresAuth(test.addr); got != test.want {
				t.Fatalf("proxyListenerRequiresAuth(%s) = %v, want %v", test.addr, got, test.want)
			}
		})
	}
}

func TestProxyManager_Rebind_reclassifiesListenerAuthentication(t *testing.T) {
	// Given
	wildcardListener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	wildcardAddr := wildcardListener.Addr().String()
	wildcardListener.Close()
	loopbackAddr := freeAddr(t)
	manager := &proxyManager{
		handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
		verifyAPIKeyScope: func(string) (bool, string, error) { return false, "", nil },
	}
	if err := manager.Start(wildcardAddr); err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())

	// When
	wildcardResponse, err := http.Get("http://127.0.0.1:" + wildcardAddr[strings.LastIndexByte(wildcardAddr, ':')+1:])
	if err != nil {
		t.Fatal(err)
	}
	wildcardResponse.Body.Close()
	if err := manager.Rebind(loopbackAddr); err != nil {
		t.Fatal(err)
	}
	loopbackResponse, err := http.Get("http://" + loopbackAddr)
	if err != nil {
		t.Fatal(err)
	}
	loopbackResponse.Body.Close()

	// Then
	if wildcardResponse.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("wildcard status = %d, want %d", wildcardResponse.StatusCode, http.StatusProxyAuthRequired)
	}
	if loopbackResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("loopback status = %d, want %d", loopbackResponse.StatusCode, http.StatusNoContent)
	}
}

func TestProxyListenerAuth_usesStoreExpiryAndScope(t *testing.T) {
	// Given
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	full, _, err := st.CreateAPIKey("full", store.ScopeFull, 0)
	if err != nil {
		t.Fatal(err)
	}
	expired, _, err := st.CreateAPIKey("expired", store.ScopeFull, 1)
	if err != nil {
		t.Fatal(err)
	}
	read, _, err := st.CreateAPIKey("read", store.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}
	handler := proxyListenerHandler(&net.TCPAddr{IP: net.IPv4zero}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), st.VerifyAPIKeyScope)

	for _, test := range []struct {
		name, token string
		want        int
	}{
		{name: "full", token: full, want: http.StatusNoContent},
		{name: "expired", token: expired, want: http.StatusProxyAuthRequired},
		{name: "read", token: read, want: http.StatusProxyAuthRequired},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
			request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:"+test.token)))
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
		})
	}
}
