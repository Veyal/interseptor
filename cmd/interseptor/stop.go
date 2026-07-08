package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Veyal/interseptor/internal/proc"
)

func runStop(args []string) error {
	const defaultGrace = 6 * time.Second

	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "skip graceful shutdown and force-kill immediately")
	forceShort := fs.Bool("f", false, "shorthand for --force")
	timeout := fs.Duration("timeout", defaultGrace, "grace period before force-kill")
	if err := fs.Parse(args); err != nil {
		return err
	}
	doForce := *force || *forceShort
	graceWindow := *timeout
	if graceWindow < 0 {
		return fmt.Errorf("timeout must be >= 0")
	}

	procs, err := proc.List()
	if err != nil {
		return fmt.Errorf("find interseptor processes: %w", err)
	}
	if len(procs) == 0 {
		fmt.Println("no Interseptor process is running")
		return nil
	}

	fmt.Printf("stopping %d Interseptor process(es)…\n", len(procs))
	for _, p := range procs {
		fmt.Printf("  · PID %d  %s\n", p.PID, p.Path)
		if doForce {
			_ = proc.Force(p.PID)
			continue
		}
		_ = proc.Graceful(p.PID)
	}

	if doForce {
		time.Sleep(500 * time.Millisecond)
		fmt.Println("done")
		return nil
	}

	deadline := time.Now().Add(graceWindow)
	for time.Now().Before(deadline) {
		alive := false
		for _, p := range procs {
			if proc.Alive(p.PID) {
				alive = true
				break
			}
		}
		if !alive {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	var survivors []int
	for _, p := range procs {
		if proc.Alive(p.PID) {
			survivors = append(survivors, p.PID)
		}
	}
	for _, pid := range survivors {
		fmt.Printf("  · PID %d did not exit — force killing\n", pid)
		_ = proc.Force(pid)
	}

	if len(survivors) > 0 {
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Println("done")
	return nil
}
