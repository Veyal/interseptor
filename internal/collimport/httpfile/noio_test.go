package httpfile

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A .http file names URLs and can reference external body files, and resolving
// either during a parse would turn an import into an SSRF or a file read.
// Parsing is pure: it works on the bytes it was handed and nothing else.
//
// The check covers this package's direct imports only -- it deliberately proves
// nothing about the transitive closure, which legitimately pulls in os and net
// through internal/store. Any path under a forbidden root counts, so a future
// net/http/httputil or os/user cannot slip past an exact-match list; net/url is
// allowed because it parses without dialling.
func TestNoNetworkOrDiskImports(t *testing.T) {
	forbiddenRoots := []string{"net", "os", "io/ioutil", "io/fs", "syscall"}
	allowed := map[string]bool{"net/url": true}
	files, _ := filepath.Glob("*.go")
	if len(files) == 0 {
		t.Fatal("no source files found; the glob is relative to the package directory")
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if allowed[p] {
				continue
			}
			for _, root := range forbiddenRoots {
				if p == root || strings.HasPrefix(p, root+"/") {
					t.Errorf("%s imports %s", f, p)
				}
			}
		}
	}
}
