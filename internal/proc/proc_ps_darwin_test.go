//go:build darwin

package proc

import (
	"io/fs"
	"os"
	"reflect"
	"testing"
	"time"
)

type fakeFileInfo struct{ mode fs.FileMode }

func (f fakeFileInfo) Name() string       { return "interseptor" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeFileInfo) Sys() any           { return nil }

// stubStat replaces statPath so only the given paths exist, with the given modes.
func stubStat(t *testing.T, files map[string]fs.FileMode) {
	t.Helper()
	orig := statPath
	statPath = func(name string) (fs.FileInfo, error) {
		if mode, ok := files[name]; ok {
			return fakeFileInfo{mode: mode}, nil
		}
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() { statPath = orig })
}

func TestParsePSLineHandlesSpacesInPath(t *testing.T) {
	stubStat(t, map[string]fs.FileMode{
		"/Applications/My Tools/Interseptor.app/Contents/MacOS/interseptor": 0o755,
		"/Users/a b/bin/interseptor":                                        0o755,
		"/Users/a b/data/interseptor":                                       0o644,
		"/Users/a b/dir/interseptor":                                        fs.ModeDir | 0o755,
	})
	for _, tc := range []struct {
		line string
		pid  int
		path string
		args []string
		ok   bool
	}{
		{"  123 /usr/local/bin/interseptor serve --project demo", 123, "/usr/local/bin/interseptor", []string{"/usr/local/bin/interseptor", "serve", "--project", "demo"}, true},
		{"124 interseptor --project demo", 124, "interseptor", []string{"interseptor", "--project", "demo"}, true},
		{"456 /Applications/My Tools/Interseptor.app/Contents/MacOS/interseptor --data-dir /tmp/x", 456, "/Applications/My Tools/Interseptor.app/Contents/MacOS/interseptor", []string{"/Applications/My Tools/Interseptor.app/Contents/MacOS/interseptor", "--data-dir", "/tmp/x"}, true},
		{"789 /Users/a b/bin/interseptor", 789, "/Users/a b/bin/interseptor", []string{"/Users/a b/bin/interseptor"}, true},
		{"42 /usr/bin/vim notes.txt", 0, "", nil, false},
		{"garbage", 0, "", nil, false},
		// Commands whose arguments merely end in "interseptor" must not be
		// mistaken for an Interseptor server.
		{"501 vim /Users/des/Documents/interseptor", 0, "", nil, false},
		{"502 go run ./cmd/interseptor", 0, "", nil, false},
		{"503 less /var/log/interseptor", 0, "", nil, false},
		{"504 /usr/bin/less /var/log/interseptor", 0, "", nil, false},
		{"505 /usr/bin/vim /Users/a b/data/interseptor", 0, "", nil, false},
		{"506 /bin/ls /Users/a b/dir/interseptor", 0, "", nil, false},
	} {
		pid, path, args, ok := parsePSLine(tc.line)
		if ok != tc.ok || pid != tc.pid || path != tc.path || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("parsePSLine(%q) = %d %q %q %v, want %d %q %q %v", tc.line, pid, path, args, ok, tc.pid, tc.path, tc.args, tc.ok)
		}
	}
}
