package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/Veyal/interseptor/internal/abilities"
)

const abilityUsage = `Usage: interseptor ability <subcommand> [options]

Subcommands:
  list                          List installed abilities (--json for JSON output)
  install <archive-or-dir>      Install a .tar.gz/.zip archive or local folder (always quarantined)
  trust <id>                    Approve an ability's SHA-256 digest (--enable=true|false)
  enable <id>                   Enable a trusted ability
  disable <id>                  Disable an ability without revoking its digest
  scaffold <id>                 Create a starter ability directory (--name, --group, --desc, --scope)
  export <id>                   Export an ability as a deterministic .tar.gz (-o out.tar.gz)
  remove <id>                   Delete an ability directory and clear its trust state

Common options:
  --data-dir <path>             Global data directory (default ~/.interseptor; also INTERSEPTOR_DATA_DIR)
  --project <name|dir>          Project name or directory (also INTERSEPTOR_PROJECT)
  --scope <global|project>      Target scope for install/scaffold (default project when --project is set, else global)
`

func runAbilityCmd(args []string) error {
	return runAbilityCLI(args, os.Stdout, os.Stderr)
}

func runAbilityCLI(args []string, stdout, stderr io.Writer) error {
	preDataDir, preProject, rest, helpRequested, err := extractLeadingAbilityFlags(args)
	if err != nil {
		return err
	}
	if helpRequested || len(rest) == 0 {
		fmt.Fprint(stdout, abilityUsage)
		return nil
	}

	sub := rest[0]
	subArgs := rest[1:]
	switch sub {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, abilityUsage)
		return nil
	case "list":
		return runAbilityList(subArgs, preDataDir, preProject, stdout, stderr)
	case "install":
		return runAbilityInstall(subArgs, preDataDir, preProject, stdout, stderr)
	case "trust":
		return runAbilityTrust(subArgs, preDataDir, preProject, stdout, stderr)
	case "enable":
		return runAbilitySetEnabled(subArgs, preDataDir, preProject, true, stdout, stderr)
	case "disable":
		return runAbilitySetEnabled(subArgs, preDataDir, preProject, false, stdout, stderr)
	case "scaffold":
		return runAbilityScaffold(subArgs, preDataDir, preProject, stdout, stderr)
	case "export":
		return runAbilityExport(subArgs, preDataDir, preProject, stdout, stderr)
	case "remove":
		return runAbilityRemove(subArgs, preDataDir, preProject, stdout, stderr)
	default:
		return fmt.Errorf("unknown ability subcommand %q (see `interseptor ability help`)", sub)
	}
}

func extractLeadingAbilityFlags(args []string) (dataDir, project string, rest []string, help bool, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			return dataDir, project, args[i:], false, nil
		}
		if a == "-h" || a == "--help" || a == "-help" {
			return dataDir, project, nil, true, nil
		}
		key, val, hasEq := strings.Cut(strings.TrimLeft(a, "-"), "=")
		switch key {
		case "data-dir", "data_dir":
			if !hasEq {
				if i+1 >= len(args) {
					return "", "", nil, false, fmt.Errorf("flag %s requires a value", a)
				}
				i++
				val = args[i]
			}
			dataDir = val
		case "project":
			if !hasEq {
				if i+1 >= len(args) {
					return "", "", nil, false, fmt.Errorf("flag %s requires a value", a)
				}
				i++
				val = args[i]
			}
			project = val
		default:
			return "", "", nil, false, fmt.Errorf("unknown flag %s before subcommand (see `interseptor ability help`)", a)
		}
	}
	return dataDir, project, nil, false, nil
}

func newAbilityFlagSet(name, defaultDataDir, defaultProject string, dataDir, project *string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(dataDir, "data-dir", defaultDataDir, "global data directory")
	fs.StringVar(project, "project", defaultProject, "project name or directory")
	return fs
}

func parseInterleavedAbilityFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	isBool := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			isBool[f.Name] = true
		}
	})

	var flagArgs, posArgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			posArgs = append(posArgs, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			name := strings.TrimLeft(a, "-")
			if strings.Contains(name, "=") || isBool[name] {
				flagArgs = append(flagArgs, a)
				continue
			}
			flagArgs = append(flagArgs, a)
			if i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
			continue
		}
		posArgs = append(posArgs, a)
	}
	if err := fs.Parse(append(flagArgs, posArgs...)); err != nil {
		return nil, err
	}
	return fs.Args(), nil
}

func openAbilityManager(dataDirArg, projectArg string) (mgr *abilities.Manager, globalDir, projectDir string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", "", err
	}
	globalDir = strings.TrimSpace(dataDirArg)
	if globalDir == "" {
		globalDir = strings.TrimSpace(os.Getenv("INTERSEPTOR_DATA_DIR"))
	}
	if globalDir == "" {
		globalDir = filepath.Join(home, newDataDirName)
	}

	proj := strings.TrimSpace(projectArg)
	if proj == "" {
		proj = strings.TrimSpace(os.Getenv("INTERSEPTOR_PROJECT"))
	}
	if proj != "" {
		if strings.EqualFold(proj, "default") {
			projectDir = globalDir
		} else {
			_, projectDir = resolveProjectDir(filepath.Join(globalDir, "projects"), proj, home)
		}
	}

	mgr, err = abilities.NewManager(abilities.Config{
		GlobalDir:  globalDir,
		ProjectDir: projectDir,
	})
	if err != nil {
		return nil, "", "", err
	}
	return mgr, globalDir, projectDir, nil
}

func resolveCLIAbilityScope(scopeFlag string, mgr *abilities.Manager, projectDir string) (string, error) {
	scope := strings.TrimSpace(strings.ToLower(scopeFlag))
	switch scope {
	case "":
		if mgr != nil && mgr.HasProjectScope() {
			return abilities.ScopeProject, nil
		}
		return abilities.ScopeGlobal, nil
	case abilities.ScopeGlobal:
		return abilities.ScopeGlobal, nil
	case abilities.ScopeProject:
		if projectDir == "" {
			return "", errors.New("--scope project requires --project <name|dir>")
		}
		if mgr == nil || !mgr.HasProjectScope() {
			return "", errors.New("--scope project is unavailable when --project aliases the global data directory; use global scope or a named project")
		}
		return abilities.ScopeProject, nil
	default:
		return "", fmt.Errorf("invalid --scope %q (want %q or %q)", scopeFlag, abilities.ScopeGlobal, abilities.ScopeProject)
	}
}

func runAbilityList(args []string, preDataDir, preProject string, stdout, stderr io.Writer) error {
	var dataDir, project string
	var asJSON bool
	fs := newAbilityFlagSet("ability list", preDataDir, preProject, &dataDir, &project, stderr)
	fs.BoolVar(&asJSON, "json", false, "output JSON")
	pos, err := parseInterleavedAbilityFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fmt.Errorf("list takes no positional arguments (got %v)", pos)
	}

	mgr, _, _, err := openAbilityManager(dataDir, project)
	if err != nil {
		return err
	}
	defer mgr.Close()
	list := mgr.List()
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"abilities": list})
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "No abilities installed.")
		return nil
	}

	tw := tabwriter.NewWriter(stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSCOPE\tVERSION\tSTATUS\tNAME\tDIGEST")
	for _, ab := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			ab.ID, ab.Scope, ab.Manifest.Version, formatAbilityStatus(ab), ab.Manifest.Name, shortAbilityDigest(ab.Digest))
	}
	return tw.Flush()
}

func formatAbilityStatus(ab abilities.Ability) string {
	switch {
	case ab.Drift:
		return "drifted"
	case ab.LoadError != "":
		return "error"
	case ab.Quarantined || !ab.Trusted:
		return "quarantined"
	case ab.Enabled:
		return "active"
	default:
		return "approved (off)"
	}
}

func shortAbilityDigest(d string) string {
	if strings.HasPrefix(d, "sha256:") && len(d) > 19 {
		return d[:19]
	}
	if d == "" {
		return "-"
	}
	return d
}

func runAbilityInstall(args []string, preDataDir, preProject string, stdout, stderr io.Writer) error {
	var dataDir, project, scopeFlag string
	fs := newAbilityFlagSet("ability install", preDataDir, preProject, &dataDir, &project, stderr)
	fs.StringVar(&scopeFlag, "scope", "", "target scope: global or project")
	pos, err := parseInterleavedAbilityFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: interseptor ability install <path-to-archive-or-dir> [--scope global|project]")
	}
	srcPath := pos[0]

	mgr, _, projectDir, err := openAbilityManager(dataDir, project)
	if err != nil {
		return err
	}
	defer mgr.Close()
	scope, err := resolveCLIAbilityScope(scopeFlag, mgr, projectDir)
	if err != nil {
		return err
	}

	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}

	var ab *abilities.Ability
	if info.IsDir() {
		ab, err = installAbilityDir(mgr, srcPath, scope)
	} else {
		f, openErr := os.Open(srcPath)
		if openErr != nil {
			return openErr
		}
		ab, err = mgr.InstallArchive(f, 0, scope)
		_ = f.Close()
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Installed ability %q (scope=%s, digest=%s, quarantined=true)\n", ab.ID, ab.Scope, ab.Digest)
	return nil
}

// installAbilityDir validates and packs a local ability directory into a
// temporary .tar.gz archive so installation goes through abilities.Manager's
// atomic quarantine pipeline.
func installAbilityDir(mgr *abilities.Manager, srcDir, scope string) (*abilities.Ability, error) {
	manifest, _, err := abilities.ValidateDir(srcDir)
	if err != nil {
		return nil, err
	}
	rootReal, err := filepath.EvalSymlinks(srcDir)
	if err != nil {
		return nil, err
	}

	tmp, err := os.CreateTemp("", "interseptor-ability-dir-*.tar.gz")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	if err := packAbilityDirTarGz(tmp, rootReal, manifest.ID); err != nil {
		return nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return mgr.InstallArchive(tmp, 0, scope)
}

func packAbilityDirTarGz(w io.Writer, rootReal, id string) error {
	root, err := os.OpenRoot(rootReal)
	if err != nil {
		return err
	}
	defer root.Close()

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	var count int
	var totalBytes int64
	err = fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || rel == "." {
			return walkErr
		}
		if d.Name() == ".DS_Store" || strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		name := filepath.FromSlash(rel)
		info, err := root.Stat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular file %q not allowed", rel)
		}
		count++
		if count > abilities.MaxArchiveFiles {
			return fmt.Errorf("ability exceeds %d files", abilities.MaxArchiveFiles)
		}
		if info.Size() > abilities.MaxFileBytes {
			return fmt.Errorf("file %q exceeds %d bytes", rel, abilities.MaxFileBytes)
		}
		totalBytes += info.Size()
		if totalBytes > abilities.MaxAbilityBytes {
			return fmt.Errorf("ability exceeds %d bytes", abilities.MaxAbilityBytes)
		}

		fh, err := root.Open(name)
		if err != nil {
			return err
		}
		openedInfo, err := fh.Stat()
		if err != nil {
			_ = fh.Close()
			return err
		}
		if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
			_ = fh.Close()
			return fmt.Errorf("file %q changed before packing", rel)
		}

		hdr := &tar.Header{
			Name:     id + "/" + rel,
			Mode:     0o644,
			Size:     openedInfo.Size(),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			_ = fh.Close()
			return err
		}
		n, copyErr := io.Copy(tw, io.LimitReader(fh, openedInfo.Size()+1))
		closeErr := fh.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != openedInfo.Size() {
			return fmt.Errorf("file %q size changed during pack", rel)
		}
		return nil
	})
	if err != nil {
		_ = tw.Close()
		_ = gz.Close()
		return err
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return err
	}
	return gz.Close()
}

func runAbilityTrust(args []string, preDataDir, preProject string, stdout, stderr io.Writer) error {
	var dataDir, project string
	enable := true
	fs := newAbilityFlagSet("ability trust", preDataDir, preProject, &dataDir, &project, stderr)
	fs.BoolVar(&enable, "enable", true, "enable ability immediately after trusting")
	pos, err := parseInterleavedAbilityFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: interseptor ability trust <id> [--enable=true|false]")
	}

	mgr, _, _, err := openAbilityManager(dataDir, project)
	if err != nil {
		return err
	}
	defer mgr.Close()
	ab, err := mgr.TrustAndEnable(pos[0], enable)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Trusted ability %q (scope=%s, enabled=%v, digest=%s)\n", ab.ID, ab.Scope, ab.Enabled, ab.Digest)
	return nil
}

func runAbilitySetEnabled(args []string, preDataDir, preProject string, enabled bool, stdout, stderr io.Writer) error {
	verb := "enable"
	if !enabled {
		verb = "disable"
	}
	var dataDir, project string
	fs := newAbilityFlagSet("ability "+verb, preDataDir, preProject, &dataDir, &project, stderr)
	pos, err := parseInterleavedAbilityFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: interseptor ability %s <id>", verb)
	}

	mgr, _, _, err := openAbilityManager(dataDir, project)
	if err != nil {
		return err
	}
	defer mgr.Close()
	ab, err := mgr.SetEnabled(pos[0], enabled)
	if err != nil {
		return err
	}
	state := "disabled"
	if ab.Enabled {
		state = "enabled"
	}
	fmt.Fprintf(stdout, "Ability %q (%s) is now %s\n", ab.ID, ab.Scope, state)
	return nil
}

func runAbilityScaffold(args []string, preDataDir, preProject string, stdout, stderr io.Writer) error {
	var dataDir, project, name, group, desc, author, scopeFlag string
	fs := newAbilityFlagSet("ability scaffold", preDataDir, preProject, &dataDir, &project, stderr)
	fs.StringVar(&name, "name", "", "display name (defaults to id)")
	fs.StringVar(&group, "group", "abilities", "sidebar group (capture|test|recon|report|configure|abilities)")
	fs.StringVar(&desc, "desc", "", "short description")
	fs.StringVar(&desc, "description", "", "short description")
	fs.StringVar(&author, "author", "", "author name")
	fs.StringVar(&scopeFlag, "scope", "", "target scope: global or project")
	pos, err := parseInterleavedAbilityFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: interseptor ability scaffold <id> [--name ...] [--group ...] [--desc ...]")
	}

	id := pos[0]
	if strings.TrimSpace(name) == "" {
		name = id
	}
	mgr, _, projectDir, err := openAbilityManager(dataDir, project)
	if err != nil {
		return err
	}
	defer mgr.Close()
	scope, err := resolveCLIAbilityScope(scopeFlag, mgr, projectDir)
	if err != nil {
		return err
	}

	ab, err := mgr.Scaffold(abilities.ScaffoldRequest{
		ID:          id,
		Name:        name,
		Description: desc,
		Author:      author,
		Scope:       scope,
		Group:       group,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Scaffolded ability %q at %s (scope=%s, quarantined=true)\n", ab.ID, ab.Dir, ab.Scope)
	return nil
}

func runAbilityExport(args []string, preDataDir, preProject string, stdout, stderr io.Writer) error {
	var dataDir, project, outPath string
	fs := newAbilityFlagSet("ability export", preDataDir, preProject, &dataDir, &project, stderr)
	fs.StringVar(&outPath, "o", "", "output archive path (default <id>.tar.gz; '-' for stdout)")
	fs.StringVar(&outPath, "out", "", "output archive path (default <id>.tar.gz; '-' for stdout)")
	pos, err := parseInterleavedAbilityFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: interseptor ability export <id> [-o out.tar.gz]")
	}
	id := pos[0]

	mgr, _, _, err := openAbilityManager(dataDir, project)
	if err != nil {
		return err
	}
	defer mgr.Close()
	ab, ok := mgr.Get(id)
	if !ok {
		return fmt.Errorf("%w: %s", abilities.ErrNotFound, id)
	}

	if outPath == "-" {
		return mgr.ExportArchive(id, stdout)
	}
	if strings.TrimSpace(outPath) == "" {
		outPath = ab.ID + ".tar.gz"
	}

	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".export-*.tar.gz")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if err := mgr.ExportArchive(id, tmp); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		return err
	}
	cleanup = false
	fmt.Fprintf(stdout, "Exported ability %q to %s\n", ab.ID, outPath)
	return nil
}

func runAbilityRemove(args []string, preDataDir, preProject string, stdout, stderr io.Writer) error {
	var dataDir, project string
	fs := newAbilityFlagSet("ability remove", preDataDir, preProject, &dataDir, &project, stderr)
	pos, err := parseInterleavedAbilityFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: interseptor ability remove <id>")
	}

	mgr, _, _, err := openAbilityManager(dataDir, project)
	if err != nil {
		return err
	}
	defer mgr.Close()
	if err := mgr.Remove(pos[0]); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Removed ability %q\n", pos[0])
	return nil
}
