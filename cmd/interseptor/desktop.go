package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/Veyal/interseptor/internal/desktopinstall"
)

type desktopInstaller interface {
	Install(ctx context.Context, opts desktopinstall.Options) (desktopinstall.Result, error)
}

// Test seams.
var (
	desktopOut          io.Writer = os.Stdout
	newDesktopInstaller           = func(out io.Writer) desktopInstaller { return desktopinstall.New(out) }
	openDesktopApp                = func(path string) error { return exec.Command("/usr/bin/open", path).Run() }
)

// runInstallDesktop implements `interseptor install-desktop`: download the
// macOS app from the GitHub release, verify it and install it into
// Applications. Ctrl-C cancels the download and removes partial files.
func runInstallDesktop(args []string) error {
	fs := flag.NewFlagSet("install-desktop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dir := fs.String("dir", "", "Applications folder (absolute; default /Applications if writable, else ~/Applications)")
	ver := fs.String("version", "", "install a specific release (e.g. 2.19.0) instead of the latest")
	force := fs.Bool("force", false, "reinstall the same version or replace a newer one")
	open := fs.Bool("open", false, "open the app after installing")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := newDesktopInstaller(desktopOut).Install(ctx, desktopinstall.Options{Dir: *dir, Version: *ver, Force: *force})
	if err != nil {
		return err
	}
	if *open {
		if err := openDesktopApp(res.Path); err != nil {
			return fmt.Errorf("open %s: %w", res.Path, err)
		}
		return nil
	}
	fmt.Fprintf(desktopOut, "Open it from Applications or run: open %s\n", res.Path)
	return nil
}
