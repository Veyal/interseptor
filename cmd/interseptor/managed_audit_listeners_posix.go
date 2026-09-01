//go:build !windows

package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

func loadManagedUIAuditListeners(controlAddr string, proxyAddrs []string) (*managedUIAuditListeners, error) {
	if os.Getenv(managedUIAuditEnv) != "1" {
		return nil, nil
	}
	if len(proxyAddrs) != 1 {
		return nil, fmt.Errorf("exactly one proxy listener is required, got %d", len(proxyAddrs))
	}
	control, err := inheritedTCPListener(managedUIAuditControlFDEnv, controlAddr)
	if err != nil {
		return nil, err
	}
	proxy, err := inheritedTCPListener(managedUIAuditProxyFDEnv, proxyAddrs[0])
	if err != nil {
		_ = control.Close()
		return nil, err
	}
	return &managedUIAuditListeners{control: control, proxies: []net.Listener{proxy}}, nil
}

func inheritedTCPListener(envName, expectedAddr string) (net.Listener, error) {
	raw := strings.TrimSpace(os.Getenv(envName))
	fd, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || fd < 3 {
		return nil, fmt.Errorf("%s must name an inherited descriptor", envName)
	}
	file := os.NewFile(uintptr(fd), envName)
	if file == nil {
		return nil, fmt.Errorf("%s descriptor is unavailable", envName)
	}
	listener, listenErr := net.FileListener(file)
	closeErr := file.Close()
	if listenErr != nil {
		return nil, fmt.Errorf("open %s: %w", envName, listenErr)
	}
	if closeErr != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("close inherited %s: %w", envName, closeErr)
	}
	actual, ok := listener.Addr().(*net.TCPAddr)
	expected, resolveErr := net.ResolveTCPAddr("tcp", expectedAddr)
	if !ok || resolveErr != nil || actual.IP == nil || !actual.IP.IsLoopback() || expected.IP == nil || !expected.IP.IsLoopback() || actual.Port != expected.Port || !actual.IP.Equal(expected.IP) {
		_ = listener.Close()
		return nil, fmt.Errorf("%s is not the expected loopback TCP listener", envName)
	}
	return listener, nil
}
