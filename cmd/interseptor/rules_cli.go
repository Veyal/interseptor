package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Veyal/interseptor/internal/rules"
)

// `interseptor rules` — share, install, and manage Starlark rule packs (the
// ecosystem layer over per-check authoring). All subcommands are offline; they
// operate on ~/.interseptor and don't need a running server.
//
//	interseptor rules create --name <n> [--version <v>] [--out file] <dir>   build a pack
//	interseptor rules install <file.tar.gz>                                  install + verify
//	interseptor rules list                                                   installed packs
//	interseptor rules info <name>                                            show a pack's record
//	interseptor rules remove <name>                                          uninstall a pack
func runRules(args []string) error {
	if len(args) == 0 {
		return errors.New("rules: expected create|install|list|info|remove (see `interseptor help`)")
	}
	switch args[0] {
	case "create":
		return rulesCreate(args[1:])
	case "install":
		return rulesInstall(args[1:])
	case "list":
		return rulesList(args[1:])
	case "info":
		return rulesInfo(args[1:])
	case "remove":
		return rulesRemove(args[1:])
	}
	return fmt.Errorf("rules: unknown action %q", args[0])
}

func rulesCreate(args []string) error {
	fs := flag.NewFlagSet("rules create", flag.ContinueOnError)
	name := fs.String("name", "", "pack name (required)")
	version := fs.String("version", "0.1.0", "pack version")
	desc := fs.String("description", "", "short description")
	author := fs.String("author", "", "author")
	out := fs.String("out", "", "output file (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" || fs.NArg() < 1 {
		return errors.New("rules create: --name and a source <dir> are required")
	}
	srcDir := fs.Arg(0)
	var w io.Writer = os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	m, err := rules.BuildPack(srcDir, rules.Manifest{
		Name: *name, Version: *version, Description: *desc, Author: *author,
	}, w)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "built pack %q v%s with %d check(s)\n", m.Name, m.Version, len(m.Entries))
	return nil
}

func rulesInstall(args []string) error {
	if len(args) != 1 {
		return errors.New("rules install: expected <file.tar.gz>")
	}
	root, err := dataRoot()
	if err != nil {
		return err
	}
	reg := rules.NewRegistry(root)
	m, n, err := reg.InstallFile(args[0], filepath.Join(root, "checks"), filepath.Join(root, "active-checks"))
	if err != nil {
		return err
	}
	fmt.Printf("installed pack %q v%s — %d check(s)\n", m.Name, m.Version, n)
	return nil
}

func rulesList(args []string) error {
	root, err := dataRoot()
	if err != nil {
		return err
	}
	packs, err := rules.NewRegistry(root).List()
	if err != nil {
		return err
	}
	if len(packs) == 0 {
		fmt.Println("no packs installed")
		return nil
	}
	for _, p := range packs {
		fmt.Printf("%-20s v%-10s  %d check(s)\n", p.Name, p.Version, len(p.IDs))
	}
	return nil
}

func rulesInfo(args []string) error {
	if len(args) != 1 {
		return errors.New("rules info: expected <name>")
	}
	root, err := dataRoot()
	if err != nil {
		return err
	}
	rec, ok, err := rules.NewRegistry(root).Get(args[0])
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("rules: pack %q is not installed", args[0])
	}
	out, _ := json.MarshalIndent(rec, "", "  ")
	fmt.Println(string(out))
	return nil
}

func rulesRemove(args []string) error {
	if len(args) != 1 {
		return errors.New("rules remove: expected <name>")
	}
	root, err := dataRoot()
	if err != nil {
		return err
	}
	n, err := rules.NewRegistry(root).Remove(args[0], filepath.Join(root, "checks"), filepath.Join(root, "active-checks"))
	if err != nil {
		return err
	}
	fmt.Printf("removed pack %q — %d check(s) deleted\n", args[0], n)
	return nil
}
