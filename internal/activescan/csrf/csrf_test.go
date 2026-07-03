package csrf

import "testing"

func TestXSRFFromCookieJSON(t *testing.T) {
	got := XSRFFromCookie(`XSRF-TOKEN=%7B%22token%22%3A%22abc123%22%7D; laravel_session=xyz`)
	if got != "abc123" {
		t.Fatalf("got %q", got)
	}
}

func TestFromFlowRequest(t *testing.T) {
	h := FromFlowRequest(map[string][]string{
		"Cookie":        {"XSRF-TOKEN=eyJ0; app_session=abc"},
		"X-Xsrf-Token":  {"decoded-token"},
	})
	if h.XSRFToken != "decoded-token" {
		t.Fatalf("xsrf %q", h.XSRFToken)
	}
}
