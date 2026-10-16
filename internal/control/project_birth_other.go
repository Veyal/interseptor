//go:build !darwin

package control

import "os"

func createdUnix(os.FileInfo) int64 { return 0 }
