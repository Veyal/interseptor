package checkscript

import (
	"os"
	"path/filepath"
)

// Exists reports whether <id>.star exists in dir.
func Exists(dir, id string) bool {
	if dir == "" || !ValidID(id) {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, id+".star"))
	return err == nil
}
