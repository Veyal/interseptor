package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// serverBinaryName is the real interseptor executable that this launcher
// supervises. Inside the bundle it sits next to the launcher in Contents/MacOS.
const serverBinaryName = "interseptor"

const defaultControlAddr = "127.0.0.1:9966"

// resolveBinary locates the interseptor server binary, preferring the copy
// shipped alongside the launcher inside the bundle so a double-clicked app
// never silently runs a different build that happens to be on PATH.
func resolveBinary(exeDir string, lookPath func(string) (string, error)) (string, error) {
	sibling := filepath.Join(exeDir, serverBinaryName)
	if fi, err := os.Stat(sibling); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
		return sibling, nil
	}
	path, err := lookPath(serverBinaryName)
	if err != nil {
		return "", fmt.Errorf("%s not found next to the launcher (%s) or on PATH: %w", serverBinaryName, sibling, err)
	}
	return path, nil
}

// controlURL turns a control listen address into a loopback URL the launcher
// can probe and hand to the browser. A wildcard or empty bind host is rewritten
// to 127.0.0.1, since that is what the local UI actually connects to.
func controlURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = defaultControlAddr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + defaultControlAddr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// interseptorRealm is the authentication realm the control plane advertises when
// it refuses an unauthenticated request.
const interseptorRealm = `realm="interseptor"`

// isInterseptorUp reports whether the server at base is specifically Interseptor,
// not merely something answering HTTP.
//
// Identity matters in both directions. On the pre-flight "already running" check
// a false positive is worst: the launcher would decide Interseptor was up, point
// a browser at an unrelated service, and never start the real server — silently.
// Accepting any HTTP response, as an earlier version did, made any process
// holding the default control port trigger exactly that.
//
// Two signals count. A 200 from /api/version carrying the control plane's own
// fields, or — when local auth gates that endpoint — the realm it advertises on
// the 401, which still proves Interseptor is what is listening.
func isInterseptorUp(base string, client *http.Client) bool {
	req, err := http.NewRequest(http.MethodGet, base+"/api/version", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if strings.Contains(resp.Header.Get("WWW-Authenticate"), interseptorRealm) {
		return true
	}
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var v struct {
		Version string `json:"version"`
		Repo    string `json:"repo"`
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return false
	}
	if json.Unmarshal(body, &v) != nil {
		return false
	}
	return v.Version != "" && v.Repo != ""
}

// waitReady polls ready until it returns true or ctx is done.
func waitReady(ctx context.Context, ready func() bool, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ready() {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("timed out waiting for the interseptor control UI to accept connections")
		case <-ticker.C:
		}
	}
}
