package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
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
	all := fs.Bool("all", false, "also stop MCP bridges, the launcher and the vault")
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

	candidates, left := stopTargets(procs, *all)
	if left != "" {
		fmt.Printf("left running: %s (use --all to stop them too)\n", left)
	}
	if len(candidates) == 0 {
		fmt.Println("no stoppable Interseptor server process is running")
		return nil
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

// stopTargets picks the processes a stop signals and summarises the rest.
// Without all it takes every server; processes whose mode cannot be read
// (RoleUnknown, e.g. on Windows) are taken only when one is the sole
// candidate, since several may include agents' MCP bridges.
func stopTargets(procs []proc.Proc, all bool) ([]proc.Proc, string) {
	if all {
		return procs, ""
	}
	var servers, unknown []proc.Proc
	counts := map[proc.Role]int{}
	for _, p := range procs {
		switch p.Role {
		case proc.RoleServer:
			servers = append(servers, p)
		case proc.RoleUnknown:
			unknown = append(unknown, p)
		default:
			counts[p.Role]++
		}
	}
	if len(servers) == 0 && len(unknown) == 1 {
		servers, unknown = unknown, nil
	}
	counts[proc.RoleUnknown] = len(unknown)
	var parts []string
	for _, r := range []struct {
		role      proc.Role
		one, many string
	}{
		{proc.RoleMCP, "MCP bridge", "MCP bridges"},
		{proc.RoleLauncher, "launcher", "launchers"},
		{proc.RoleVault, "vault", "vaults"},
		{proc.RoleTool, "CLI command", "CLI commands"},
		{proc.RoleUnknown, "unidentified process", "unidentified processes"},
	} {
		if n := counts[r.role]; n == 1 {
			parts = append(parts, "1 "+r.one)
		} else if n > 1 {
			parts = append(parts, fmt.Sprintf("%d %s", n, r.many))
		}
	}
	return servers, strings.Join(parts, ", ")
}
