package tunnel

import (
	"io"
	"os/exec"
	"strings"
	"testing"
)

// The public URL is whatever cloudflared prints, so only a line that really
// names a quick-tunnel host may be published. cloudflared's own failure output
// mentions its API endpoint, and a longer host can merely start with the suffix.
func TestPublicURLOnlyFromAQuickTunnelHost(t *testing.T) {
	for _, line := range []string{
		`2024 ERR failed to request quick Tunnel: Post "https://api.trycloudflare.com/tunnel": dial tcp: i/o timeout`,
		`2024 INF Requesting new quick Tunnel on trycloudflare.com...`,
		`2024 ERR origin https://calm-sky-1.trycloudflare.com.attacker.example.com refused`,
		`2024 ERR origin https://calm-sky-1.trycloudflare.community refused`,
		`2024 INF https://-.trycloudflare.com`,
	} {
		if got := publicURL(line); got != "" {
			t.Errorf("publicURL(%q) = %q, want none", line, got)
		}
	}
	for line, want := range map[string]string{
		`2024 INF |  https://calm-forest-1234.trycloudflare.com   |`:       "https://calm-forest-1234.trycloudflare.com",
		`visit https://a-b-c.trycloudflare.com/ to reach the tunnel`:       "https://a-b-c.trycloudflare.com",
		`https://api.trycloudflare.com then https://x-y.trycloudflare.com`: "https://x-y.trycloudflare.com",
	} {
		if got := publicURL(line); got != want {
			t.Errorf("publicURL(%q) = %q, want %q", line, got, want)
		}
	}
}

// End to end through the scanner: a failed start must not publish the API host.
func TestScanForURLIgnoresTheAPIEndpoint(t *testing.T) {
	m := newTestManager(t)
	cmd := new(exec.Cmd)
	m.mu.Lock()
	m.cmd, m.generation, m.running = cmd, 1, true
	m.mu.Unlock()
	stderr := strings.Join([]string{
		`2024 INF Requesting new quick Tunnel on trycloudflare.com...`,
		`2024 ERR failed to request quick Tunnel: Post "https://api.trycloudflare.com/tunnel": context deadline exceeded`,
	}, "\n")
	m.scanForURL(cmd, 1, io.NopCloser(strings.NewReader(stderr)))
	if got := m.Status().URL; got != "" {
		t.Fatalf("published %q as the public URL of a tunnel that never came up", got)
	}
}
