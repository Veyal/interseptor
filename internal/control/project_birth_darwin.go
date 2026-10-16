//go:build darwin

package control

import (
	"os"
	"syscall"
)

func createdUnix(info os.FileInfo) int64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return birthUnix(stat)
}

func birthUnix(stat *syscall.Stat_t) int64 {
	if stat == nil {
		return 0
	}
	return stat.Birthtimespec.Sec
}
