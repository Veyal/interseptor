//go:build !linux

package proc

func procFromProcFS(pid int) (Proc, bool) {
	return Proc{}, false
}
