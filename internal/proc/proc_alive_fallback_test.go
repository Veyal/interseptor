package proc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// proc.go calls aliveInterseptor(pid), but the only implementation compiled on
// a GOOS outside the release matrix (anything but darwin/linux/windows) lives in
// proc_alive_fallback.go behind `//go:build !windows && !linux && !darwin`.
// No test on a normal CI host compiles that file, so a name mismatch there is
// invisible until somebody builds for that GOOS — where it is a hard build
// failure ("undefined: aliveInterseptor"). This guard checks the declared
// symbol by parsing the file, which works on every platform.
func TestFallbackAliveInterseptorIsDeclared(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "proc_alive_fallback.go", nil, 0)
	if err != nil {
		t.Fatalf("parse proc_alive_fallback.go: %v", err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}
		if fn.Name.Name == "aliveInterseptor" {
			return
		}
	}
	t.Fatal("proc_alive_fallback.go must declare aliveInterseptor(pid int) bool — proc.go calls it on GOOS outside darwin/linux/windows")
}
