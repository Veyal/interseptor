//go:build darwin

package proc

import (
	"reflect"
	"testing"
)

func TestParsePSLineHandlesSpacesInPath(t *testing.T) {
	for _, tc := range []struct {
		line string
		pid  int
		path string
		args []string
		ok   bool
	}{
		{"  123 /usr/local/bin/interseptor serve --project demo", 123, "/usr/local/bin/interseptor", []string{"/usr/local/bin/interseptor", "serve", "--project", "demo"}, true},
		{"456 /Applications/My Tools/Interseptor.app/Contents/MacOS/interseptor --data-dir /tmp/x", 456, "/Applications/My Tools/Interseptor.app/Contents/MacOS/interseptor", []string{"/Applications/My Tools/Interseptor.app/Contents/MacOS/interseptor", "--data-dir", "/tmp/x"}, true},
		{"789 /Users/a b/bin/interseptor", 789, "/Users/a b/bin/interseptor", []string{"/Users/a b/bin/interseptor"}, true},
		{"42 /usr/bin/vim notes.txt", 0, "", nil, false},
		{"garbage", 0, "", nil, false},
	} {
		pid, path, args, ok := parsePSLine(tc.line)
		if ok != tc.ok || pid != tc.pid || path != tc.path || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("parsePSLine(%q) = %d %q %q %v, want %d %q %q %v", tc.line, pid, path, args, ok, tc.pid, tc.path, tc.args, tc.ok)
		}
	}
}
