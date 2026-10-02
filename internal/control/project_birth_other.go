//go:build !darwin

package control

import "syscall"

func birthUnix(stat *syscall.Stat_t) int64 { return 0 }
