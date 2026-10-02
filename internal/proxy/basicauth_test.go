package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBasicAuth_disabledForwardsWithoutCredentials(t *testing.T) {
	var calls int
	handler := (&BasicAuth{}).Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodConnect, "http://example.com", nil)
	request.Header.Set("Proxy-Authorization", "Basic aWdub3JlZA==")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent || calls != 1 {
		t.Fatalf("status=%d calls=%d, want 204/1", recorder.Code, calls)
	}
}

func TestBasicAuth_requiresConfiguredCredentialsAndStripsHeader(t *testing.T) {
	auth := &BasicAuth{}
	auth.Set(true, "proxy", "secret")
	var calls int
	handler := auth.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("Proxy-Authorization"); got != "" {
			t.Errorf("forwarded Proxy-Authorization = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, test := range []struct {
		name, header string
		want         int
	}{
		{name: "missing", want: http.StatusProxyAuthRequired},
		{name: "wrong", header: "Basic d3Jvbmc6c2VjcmV0", want: http.StatusProxyAuthRequired},
		{name: "authorization header ignored", header: "", want: http.StatusProxyAuthRequired},
		{name: "match", header: "Basic cHJveHk6c2VjcmV0", want: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
			if test.header != "" {
				request.Header.Set("Proxy-Authorization", test.header)
			}
			if test.name == "authorization header ignored" {
				request.SetBasicAuth("proxy", "secret")
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d", recorder.Code, test.want)
			}
			if test.want == http.StatusProxyAuthRequired && recorder.Header().Get("Proxy-Authenticate") != `Basic realm="interseptor"` {
				t.Fatalf("Proxy-Authenticate = %q", recorder.Header().Get("Proxy-Authenticate"))
			}
		})
	}
	if calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}
}

func TestBasicAuth_emptyStoredSecretFailsClosed(t *testing.T) {
	auth := &BasicAuth{}
	auth.Set(true, "proxy", "")
	handler := auth.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler invoked")
	}))
	request := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	request.Header.Set("Proxy-Authorization", "Basic cHJveHk6")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusProxyAuthRequired {
		t.Fatalf("status = %d, want 407", recorder.Code)
	}
}

func TestBasicAuth_setDisablesWithoutRebuildingHandler(t *testing.T) {
	auth := &BasicAuth{}
	auth.Set(true, "proxy", "secret")
	handler := auth.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	auth.Set(false, "proxy", "secret")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://example.com", nil))

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
}
