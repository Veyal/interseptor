package netutil

import "testing"

func TestURLAuthority(t *testing.T) {
	tests := []struct {
		scheme string
		host   string
		port   int
		want   string
	}{
		{"https", "example.com", 443, "example.com"},
		{"https", "example.com", 8443, "example.com:8443"},
		{"http", "2001:db8::1", 80, "[2001:db8::1]"},
		{"https", "[2001:db8::1]", 8443, "[2001:db8::1]:8443"},
		{"http", "fe80::1%en0", 8080, "[fe80::1%25en0]:8080"},
	}
	for _, tt := range tests {
		if got := URLAuthority(tt.scheme, tt.host, tt.port); got != tt.want {
			t.Errorf("URLAuthority(%q, %q, %d) = %q, want %q", tt.scheme, tt.host, tt.port, got, tt.want)
		}
	}
	if got := URLHost("http", "fe80::1%en0", 8080); got != "[fe80::1%en0]:8080" {
		t.Fatalf("URLHost scoped IPv6 = %q", got)
	}
}
