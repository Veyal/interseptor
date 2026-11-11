package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/Veyal/interseptor/internal/chromemgr"
)

var newCLIChromeInstaller = chromemgr.NewInstaller

func runInstallChrome(args []string) error {
	fs := flag.NewFlagSet("install-chrome", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDirFlag := fs.String("data-dir", "", "global data directory (default ~/.interseptor; also INTERSEPTOR_DATA_DIR)")
	force := fs.Bool("force", false, "reinstall even if already installed")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	globalDir := strings.TrimSpace(*dataDirFlag)
	if globalDir == "" {
		globalDir = strings.TrimSpace(os.Getenv("INTERSEPTOR_DATA_DIR"))
	}
	targetDir := chromemgr.Dir(globalDir)

	if !*force {
		if existing, err := chromemgr.BinaryPath(targetDir, runtime.GOOS, runtime.GOARCH); err == nil && existing != "" {
			fmt.Printf("Chrome is already installed at %s (pass --force to reinstall)\n", existing)
			return nil
		}
	}

	installer := newCLIChromeInstaller()

	goos := installer.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := installer.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}

	platform, err := chromemgr.PlatformKey(goos, goarch)
	if err != nil {
		return fmt.Errorf("platform check: %w", err)
	}

	fmt.Printf("Installing Chrome for Testing for %s into %s…\n", platform, targetDir)

	var lastPct int = -1
	progress := func(done, total int64) {
		if total <= 0 {
			return
		}
		pct := int((done * 100) / total)
		if pct != lastPct && pct%10 == 0 {
			lastPct = pct
			fmt.Printf("Downloading Chrome: %d%% (%d / %d MB)\n", pct, done/(1024*1024), total/(1024*1024))
		}
	}

	logStep := func(format string, a ...any) {
		fmt.Printf(format+"\n", a...)
	}

	// Ctrl-C (or a termination request) cancels the download and extraction, so
	// the temporary archive and the staging directory are removed on the way out
	// instead of being left behind by a process killed mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	path, err := installer.Install(ctx, targetDir, progress, logStep)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("install Chrome: interrupted, nothing was installed: %w", context.Canceled)
		}
		return fmt.Errorf("install Chrome: %w", err)
	}

	fmt.Printf("Chrome installed successfully at %s\n", path)
	return nil
}

// isInterrupted reports whether err is an install the operator interrupted.
func isInterrupted(err error) bool { return errors.Is(err, context.Canceled) }
