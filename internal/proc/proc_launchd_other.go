//go:build !darwin

package proc

func BootoutLaunchdServer(pid int) (string, bool, error) {
	return "", false, nil
}
