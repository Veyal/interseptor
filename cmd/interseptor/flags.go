package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/Veyal/interseptor/internal/bind"
	"github.com/Veyal/interseptor/internal/store"
)

// normalizeCLIArgs maps underscore spellings to the flag names registered with
// flag.NewFlagSet (e.g. --control_port → --control-port).
func normalizeCLIArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		switch {
		case strings.HasPrefix(a, "--control_port="):
			a = "--control-port=" + strings.TrimPrefix(a, "--control_port=")
		case a == "--control_port":
			a = "--control-port"
		case strings.HasPrefix(a, "--control_addr="):
			a = "--control-addr=" + strings.TrimPrefix(a, "--control_addr=")
		case a == "--control_addr":
			a = "--control-addr"
		case strings.HasPrefix(a, "--proxy_port="):
			a = "--proxy-port=" + strings.TrimPrefix(a, "--proxy_port=")
		case a == "--proxy_port":
			a = "--proxy-port"
		case strings.HasPrefix(a, "--data_dir="):
			a = "--data-dir=" + strings.TrimPrefix(a, "--data_dir=")
		case a == "--data_dir":
			a = "--data-dir"
		}
		out[i] = a
	}
	return out
}

// reexecArgs builds the argument list for a project-switch restart: every flag
// the launcher was started with (ports, control address, data dir) is kept so
// the new process binds the same listeners, while --project is replaced by the
// target and --open is dropped (the open browser tab reloads itself; a second
// tab would be noise).
func reexecArgs(args []string, target string) []string {
	args = normalizeCLIArgs(args)
	out := make([]string, 0, len(args)+2)
	for i := 0; i < len(args); i++ {
		a := args[i]
		name := strings.TrimLeft(a, "-")
		if dashes := len(a) - len(name); dashes < 1 || dashes > 2 {
			out = append(out, a)
			continue
		}
		key, _, hasValue := strings.Cut(name, "=")
		switch key {
		case "project":
			if !hasValue {
				i++ // skip the separate value
			}
		case "open":
			// bool flag: never consumes a following argument
		default:
			out = append(out, a)
		}
	}
	return append(out, "--project", target)
}

func defaultControlHost() string {
	if h, _, err := net.SplitHostPort(defaultControlAddr); err == nil && h != "" {
		return h
	}
	return "127.0.0.1"
}

func controlAddrFromPort(port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid control port %d (want 1–65535)", port)
	}
	return net.JoinHostPort(defaultControlHost(), strconv.Itoa(port)), nil
}

// proxyAddrFromPort builds a loopback proxy listen address for --proxy-port.
func proxyAddrFromPort(port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid proxy port %d (want 1–65535)", port)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), nil
}

// resolveControlAddr picks the control-plane listen address: CLI override (wins),
// INTERSEPTOR_CONTROL_ADDR env, persisted control.addr, then defaultControlAddr.
// Non-loopback binds are allowed by default; set INTERSEPTOR_ALLOW_EXTERNAL_BIND=0
// to fall back to loopback-only.
func resolveControlAddr(st *store.Store, cliOverride string) string {
	addr := strings.TrimSpace(cliOverride)
	if addr == "" {
		addr = strings.TrimSpace(os.Getenv("INTERSEPTOR_CONTROL_ADDR"))
	}
	if addr == "" && st != nil {
		if v, ok, _ := st.GetSetting("control.addr"); ok && v != "" {
			addr = v
		}
	}
	if addr == "" {
		addr = defaultControlAddr
	}
	if !isLoopbackBind(addr) && !bind.ExternalBindAllowed() {
		log.Printf("control addr %q is non-loopback; ignoring (external bind disabled via INTERSEPTOR_ALLOW_EXTERNAL_BIND=0)", addr)
		return defaultControlAddr
	}
	return addr
}
