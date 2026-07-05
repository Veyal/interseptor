package tunnel

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestQuickTunnelURLRegex(t *testing.T) {
	line := `2024-06-01T00:00:00Z INF |  https://calm-forest-1234.trycloudflare.com   |`
	if got := quickTunnelURL.FindString(line); got != "https://calm-forest-1234.trycloudflare.com" {
		t.Fatalf("URL parse = %q", got)
	}
	if quickTunnelURL.FindString("no url here") != "" {
		t.Fatal("expected no match on a plain line")
	}
}

func TestScanForURLSetsURLAndNotifies(t *testing.T) {
	m := New(func() string { return "9966" })
	got := make(chan string, 1)
	m.SetOnURL(func(u string) { got <- u })

	stderr := strings.Join([]string{
		"2024 INF Thank you for trying Cloudflare Tunnel.",
		"2024 INF +----------------------------------+",
		"2024 INF |  https://happy-sky-9.trycloudflare.com  |",
		"2024 INF +----------------------------------+",
	}, "\n")
	m.scanForURL(io.NopCloser(strings.NewReader(stderr)))

	select {
	case u := <-got:
		if u != "https://happy-sky-9.trycloudflare.com" {
			t.Fatalf("callback URL = %q", u)
		}
	default:
		t.Fatal("onURL was not called")
	}
	if st := m.Status(); st.URL != "https://happy-sky-9.trycloudflare.com" {
		t.Fatalf("Status.URL = %q", st.URL)
	}
}

func TestInstalledUsesLookPath(t *testing.T) {
	m := New(func() string { return "9966" })
	m.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if m.Installed() {
		t.Fatal("Installed should be false when lookPath fails")
	}
	m.lookPath = func(string) (string, error) { return "/usr/bin/cloudflared", nil }
	if !m.Installed() {
		t.Fatal("Installed should be true when lookPath succeeds")
	}
}

func TestStartFailsWithoutBinary(t *testing.T) {
	m := New(func() string { return "9966" })
	m.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	st, err := m.Start(context.Background())
	if err == nil {
		t.Fatal("expected an error when cloudflared is absent")
	}
	if st.Running {
		t.Fatal("status must not be running when start failed")
	}
	if st.Err == "" {
		t.Fatal("expected a lastErr message")
	}
}
