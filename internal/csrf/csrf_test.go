package csrf

import "testing"

func TestXSRFFromCookie(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "XSRF-TOKEN=abc; session=xyz", want: "abc"},
		{name: "encoded json", in: "XSRF-TOKEN=%7B%22token%22%3A%22abc%22%7D", want: "abc"},
		{name: "missing", in: "session=xyz", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := XSRFFromCookie(tc.in); got != tc.want {
				t.Fatalf("XSRFFromCookie(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
