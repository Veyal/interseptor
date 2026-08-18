package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Veyal/interseptor/internal/checkscript"
)

// `interseptor check` — author-time tooling for Starlark checks, usable without
// a running server (ideal for CI gates and editor workflows):
//
//	interseptor check new <id>                  scaffold a check template
//	interseptor check validate [files...]      compile every check (CI gate)
//	interseptor check lint     [files...]      alias of validate
//	interseptor check test <file> --flow-json f   compile + run against a flow
//
// `new`/`validate` (no files) operate on ~/.interseptor/checks.
func printCheckUsage() {
	fmt.Fprintln(os.Stdout, "Usage: interseptor check new|validate|lint|test [options]")
}

func runCheck(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printCheckUsage()
		return nil
	}
	rest := args[1:]
	for len(rest) > 0 && strings.HasPrefix(rest[0], "--") {
		return fmt.Errorf("check %s: unknown flag %s", args[0], rest[0])
	}
	switch args[0] {
	case "new":
		return checkNew(rest)
	case "validate", "lint":
		return checkValidate(rest)
	case "test":
		return checkTest(rest)
	}
	return fmt.Errorf("check: unknown action %q (want new, validate, lint, or test)", args[0])
}

func globalChecksDir() (string, error) {
	root, err := dataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "checks"), nil
}

// dataRoot resolves the global interseptor data dir (where checks, the CA, and
// the pack registry live): --data-dir / INTERSEPTOR_DATA_DIR, else ~/.interseptor.
func dataRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if root := strings.TrimSpace(os.Getenv("INTERSEPTOR_DATA_DIR")); root != "" {
		return root, nil
	}
	return filepath.Join(home, newDataDirName), nil
}

const (
	passiveTemplate = `# %s — custom passive check (runs on every scan).
# Inspect ` + "`flow`" + ` and return a list of finding(...), or [] for nothing found.
# Docs: docs/custom-checks.md · builtins: finding, re_search, json_decode/encode,
# b64decode/encode, url_decode/encode, hash, hmac.
def check(flow):
    if False:  # replace with your condition
        return [finding("info", "%s", evidence="")]
    return []
`
)

func checkNew(args []string) error {
	if len(args) != 1 {
		return errors.New("check new: expected exactly one <id>")
	}
	id := args[0]
	if !checkscript.ValidID(id) {
		return fmt.Errorf("check new: invalid check id %q (use letters, digits, - or _)", id)
	}
	dir, err := globalChecksDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, id+".star")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("check new: %s already exists (delete it first)", path)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf(passiveTemplate, id, id)), 0o644); err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

func compileFile(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	id := strings.TrimSuffix(filepath.Base(path), ".star")
	_, err = checkscript.Compile(id, string(src))
	return err
}

func checkValidate(args []string) error {
	files := args
	if len(files) == 0 {
		dir, err := globalChecksDir()
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("check validate: read %s: %w (pass file paths, or run `interseptor` once to create the dir)", dir, err)
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".star") {
				files = append(files, filepath.Join(dir, e.Name()))
			}
		}
		if len(files) == 0 {
			fmt.Printf("no .star files in %s\n", dir)
			return nil
		}
	}
	var bad int
	for _, f := range files {
		if err := compileFile(f); err != nil {
			fmt.Printf("FAIL %s\n      %v\n", f, err)
			bad++
			continue
		}
		fmt.Printf("ok   %s\n", f)
	}
	if bad > 0 {
		return fmt.Errorf("check validate: %d file(s) failed to compile", bad)
	}
	return nil
}

// checkTest compiles a passive check and runs it against a flow supplied as JSON
// (--flow-json path, or "-" / omitted for stdin). The JSON shape matches the
// `flow` object: method/scheme/host/port/path/status/mime, req_body/res_body,
// req_headers/res_headers (each a {name: [values]} map).
func checkTest(args []string) error {
	if len(args) < 1 {
		return errors.New("check test: expected <file> (--flow-json <path> or stdin)")
	}
	flowJSON := "-"
	src, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("read %s: %w", args[0], err)
	}
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--flow-json="):
			flowJSON = strings.TrimPrefix(a, "--flow-json=")
		case a == "--flow-json":
			if i+1 == len(args) {
				return errors.New("check test: --flow-json requires a path")
			}
			i++
			flowJSON = args[i]
		default:
			return fmt.Errorf("check test: unknown argument %q", a)
		}
	}
	var raw []byte
	if flowJSON == "-" || flowJSON == "" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(flowJSON)
	}
	if err != nil {
		return fmt.Errorf("read flow: %w", err)
	}
	var f checkscript.Flow
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("parse flow json: %w", err)
	}
	c, err := checkscript.Compile(strings.TrimSuffix(filepath.Base(args[0]), ".star"), string(src))
	if err != nil {
		return err
	}
	issues, err := c.Run(f)
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(issues, "", "  ")
	fmt.Println(string(out))
	return nil
}
