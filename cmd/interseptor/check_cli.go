package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Veyal/interseptor/internal/activescript"
	"github.com/Veyal/interseptor/internal/checkscript"
)

// `interseptor check` — author-time tooling for Starlark checks, usable without
// a running server (ideal for CI gates and editor workflows):
//
//	interseptor check new <id> [--active]      scaffold a check template
//	interseptor check validate [files...]      compile every check (CI gate)
//	interseptor check lint     [files...]      alias of validate
//	interseptor check test <file> --flow-json f   compile + run against a flow
//
// `new`/`validate` (no files) operate on ~/.interseptor/checks (passive) and,
// with --active, ~/.interseptor/active-checks.
func runCheck(args []string) error {
	if len(args) == 0 {
		return errors.New("check: expected new|validate|lint|test (see `interseptor help`)")
	}
	active := false
	rest := args[1:]
	for len(rest) > 0 && strings.HasPrefix(rest[0], "--") {
		if rest[0] == "--active" {
			active = true
			rest = rest[1:]
			continue
		}
		return fmt.Errorf("check %s: unknown flag %s", args[0], rest[0])
	}
	switch args[0] {
	case "new":
		return checkNew(rest, active)
	case "validate", "lint":
		return checkValidate(rest, active)
	case "test":
		return checkTest(rest, active)
	}
	return fmt.Errorf("check: unknown action %q (want new, validate, lint, or test)", args[0])
}

func globalChecksDir(active bool) (string, error) {
	root, err := dataRoot()
	if err != nil {
		return "", err
	}
	if active {
		return filepath.Join(root, "active-checks"), nil
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
	activeTemplate = `# %s — custom ACTIVE check (fires only on an armed active scan).
# Send real mutated requests with probe(payload); compare against ` + "`baseline`" + `.
# Docs: docs/custom-active-checks.md · builtins: probe, finding, re_search, json_*, b64_*, url_*, hash, hmac.
def check(point, baseline, probe):
    r = probe("test")  # sends one real, scope-enforced, budget-counted request
    if False:
        return [finding("high", "%s", evidence=r.body[:120])]
    return []
`
)

func checkNew(args []string, active bool) error {
	if len(args) != 1 {
		return errors.New("check new: expected exactly one <id>")
	}
	id := args[0]
	dir, err := globalChecksDir(active)
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
	tpl := passiveTemplate
	if active {
		tpl = activeTemplate
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf(tpl, id, id)), 0o644); err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// compileFile picks the right engine by --active (a passive check defines
// check(flow); an active one defines check(point, baseline, probe)).
func compileFile(path string, active bool) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	id := strings.TrimSuffix(filepath.Base(path), ".star")
	if active {
		_, err = activescript.Compile(id, string(src))
	} else {
		_, err = checkscript.Compile(id, string(src))
	}
	return err
}

func checkValidate(args []string, active bool) error {
	files := args
	if len(files) == 0 {
		dir, err := globalChecksDir(active)
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
		if err := compileFile(f, active); err != nil {
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
func checkTest(args []string, active bool) error {
	if active {
		return errors.New("check test: active checks send real traffic — use the in-UI Test button or the test_active_check MCP tool against a live engine")
	}
	if len(args) < 1 {
		return errors.New("check test: expected <file> (--flow-json <path> or stdin)")
	}
	flowJSON := "-"
	src, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("read %s: %w", args[0], err)
	}
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "--flow-json=") {
			flowJSON = strings.TrimPrefix(a, "--flow-json=")
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
