package httplines

import (
	"net/http"
	"testing"
)

func TestParseRawRequestKeepsTransferEncoding(t *testing.T) {
	raw := "POST /up HTTP/1.1\nHost: app.test\nTransfer-Encoding: chunked\nContent-Length: 99\n\n5\nhello\n0\n\n"
	_, _, hdrs, body, err := ParseRawRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := http.Header(hdrs).Values("Transfer-Encoding"); len(got) != 1 || got[0] != "chunked" {
		t.Fatalf("Transfer-Encoding = %v, want [chunked]", got)
	}
	if _, ok := hdrs["Content-Length"]; ok {
		t.Fatalf("Content-Length kept: %v", hdrs)
	}
	if string(body) != "5\r\nhello\r\n0\r\n\r\n" {
		t.Fatalf("body = %q", body)
	}
}
