package main

import (
	"log"
	"path/filepath"

	"github.com/Veyal/interseptor/internal/checkscript"
)

// migrateGlobalChecks merges any per-project checks folders into the global
// ~/.interseptor/checks directory (shared across all projects, like the CA).
func migrateGlobalChecks(globalDir, projectsDir string) {
	checksDir := filepath.Join(globalDir, "checks")
	for _, name := range listProjects(projectsDir) {
		src := filepath.Join(projectsDir, name, "checks")
		n, err := checkscript.MergeDir(src, checksDir)
		if err != nil {
			log.Printf("custom checks: merge from project %q: %v", name, err)
			continue
		}
		if n > 0 {
			log.Printf("custom checks: merged %d check(s) from project %q into %s", n, name, checksDir)
		}
	}
}
