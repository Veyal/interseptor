package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Veyal/interseptor/internal/vault"
)

func runVault(args []string) error {
	fs := flag.NewFlagSet("vault", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dirFlag := fs.String("dir", "", "vault data directory (default ~/.interseptor/vault; also INTERSEPTOR_VAULT_DIR)")
	addrFlag := fs.String("addr", "127.0.0.1:9977", "listen address (tailscale serve proxies here)")
	keepFlag := fs.Int("keep", 10, "max revisions kept per project")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dir := strings.TrimSpace(*dirFlag)
	if dir == "" {
		dir = strings.TrimSpace(os.Getenv("INTERSEPTOR_VAULT_DIR"))
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(home, ".interseptor", "vault")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	st, err := vault.Open(dir, *keepFlag)
	if err != nil {
		return err
	}
	auth, err := vault.OpenAuth(dir)
	if err != nil {
		return err
	}
	raw, created, err := auth.EnsureBootstrap()
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(os.Stderr, "vault: bootstrap token (also written to %s/vault.token):\n  %s\n", dir, raw)
		fmt.Fprintf(os.Stderr, "vault: use Authorization: Bearer <token> from clients\n")
	}

	addr := vault.NormalizeAddr(*addrFlag)
	srv := vault.NewServer(st, auth)
	fmt.Fprintf(os.Stderr, "vault: listening on http://%s\n", addr)
	fmt.Fprintf(os.Stderr, "vault: data dir %s (keep=%d)\n", dir, *keepFlag)
	fmt.Fprintf(os.Stderr, "vault: Tailscale Serve example:\n  tailscale serve --bg --https=9977 http://%s\n", addr)
	log.SetFlags(0)
	return srv.ListenAndServe(addr)
}
