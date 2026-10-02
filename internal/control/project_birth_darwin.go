//go:build darwin

package control

import "syscall"

func birthUnix(stat *syscall.Stat_t) int64 {
	if stat == nil {
		return 0
	}
	return stat.Birthtimespec.Sec
}
