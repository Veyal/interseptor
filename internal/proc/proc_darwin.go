//go:build darwin

package proc

import "os"

// List returns every running interceptor process (excluding the caller).
// macOS has no /proc — use pgrep directly.
func List() ([]Proc, error) {
	return listViaPgrep(os.Getpid())
}
