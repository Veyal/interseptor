package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Veyal/interseptor/internal/proc"
)

var (
	stopList     = proc.List
	stopBootout  = proc.BootoutLaunchdServer
	stopGraceful = proc.Graceful
	stopForce    = proc.Force
	stopAlive    = proc.AliveInterseptor
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

	procs, err := stopList()
	if err != nil {
		return fmt.Errorf("find interseptor processes: %w", err)
	}
	if len(procs) == 0 {
		fmt.Println("no Interseptor process is running")
		return nil
	}

	var candidates []proc.Proc
	for _, p := range procs {
		if p.Stoppable() {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		fmt.Println("no stoppable Interseptor server process is running")
		return nil
	}
	if len(candidates) > 1 {
		return fmt.Errorf("refusing to stop %d Interseptor server processes; use a targeted stop", len(candidates))
	}
	procs = candidates

	fmt.Printf("stopping %d Interseptor process(es)…\n", len(procs))
	for _, p := range procs {
		fmt.Printf("  · PID %d  %s\n", p.PID, p.Path)
		if label, booted, err := stopBootout(p.PID); err != nil {
			return fmt.Errorf("stop launchd-managed server: %w", err)
		} else if booted {
			fmt.Printf("  · booted out launchd job %s\n", label)
		}
		if doForce {
			_ = stopForce(p.PID)
			continue
		}
		_ = stopGraceful(p.PID)
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
			if stopAlive(p.PID) {

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
		if stopAlive(p.PID) {
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
