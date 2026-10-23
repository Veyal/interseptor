package version

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	sumA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sumB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestParseChecksumsExactName(t *testing.T) {
	data := sumA + "  other_interseptor_linux_amd64.tar.gz\n" + sumB + " *interseptor_linux_amd64.tar.gz\n"
	if got, ok := parseChecksums(data, "interseptor_linux_amd64.tar.gz"); !ok || got != sumB {
		t.Fatalf("got %q ok=%v, want the exact-name line", got, ok)
	}
	// A line for a different asset whose name merely ends with ours must not match.
	if _, ok := parseChecksums(sumA+"  other_interseptor_linux_amd64.tar.gz\n", "interseptor_linux_amd64.tar.gz"); ok {
		t.Fatal("suffix match picked another asset's checksum")
	}
}

func checksumServer(t *testing.T, body string, status int) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts.URL + "/checksums.txt"
}

func TestChecksumForFailsClosed(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("INTERSEPTOR_GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	asset := "interseptor_linux_amd64.tar.gz"
	if _, err := checksumFor(&releaseInfo{Tag: "v9.9.9"}, asset); err == nil {
		t.Fatal("no checksums.txt asset must be an error")
	}
	missing := &releaseInfo{Assets: []releaseAsset{{Name: "checksums.txt", URL: checksumServer(t, "", http.StatusNotFound)}}}
	if _, err := checksumFor(missing, asset); err == nil {
		t.Fatal("unavailable checksums.txt must be an error")
	}
	noEntry := &releaseInfo{Assets: []releaseAsset{{Name: "checksums.txt", URL: checksumServer(t, sumA+"  something_else.zip\n", http.StatusOK)}}}
	if _, err := checksumFor(noEntry, asset); err == nil {
		t.Fatal("checksums.txt without the asset must be an error")
	}
	ok := &releaseInfo{Assets: []releaseAsset{{Name: "checksums.txt", URL: checksumServer(t, sumA+"  "+asset+"\n", http.StatusOK)}}}
	if got, err := checksumFor(ok, asset); err != nil || got != sumA {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestLatestIsNewer(t *testing.T) {
	for _, tc := range []struct {
		cur, latest string
		want        bool
	}{
		{"2.17.3", "2.18.0", true},
		{"2.17.3", "2.17.3", false},
		{"2.19.0", "2.18.0", false}, // a newer local build is never downgraded
		{"2.18.0-local", "2.18.0", false},
		{"2.18.0-rc1", "2.18.0", true},
		{"2.18.0-rc1", "2.17.9", false},
		{"dev", "2.18.0", true},
		{"2.18.0", "garbage", false},
	} {
		if got := latestIsNewer(tc.cur, tc.latest); got != tc.want {
			t.Errorf("latestIsNewer(%q, %q) = %v, want %v", tc.cur, tc.latest, got, tc.want)
		}
	}
}

func TestUpdateDoesNotDowngradeNewerBuild(t *testing.T) {
	stubLatestRelease(t, "1.0.0") // older than the running build
	var out bytes.Buffer
	if err := Update(context.Background(), UpdateOptions{Out: &out}); err != nil {
		t.Fatalf("update: %v (output %q)", err, out.String())
	}
	if !strings.Contains(out.String(), "up to date") || strings.Contains(out.String(), "Downloading") {
		t.Fatalf("update offered a downgrade: %q", out.String())
	}
	out.Reset()
	if err := Update(context.Background(), UpdateOptions{Check: true, Out: &out}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "update available") {
		t.Fatalf("--check offered a downgrade: %q", out.String())
	}
}

func TestWindowsLegacyShimForwardsArgsRelatively(t *testing.T) {
	script := windowsUpdateScript(`C:\t\interseptor.exe.new`, `C:\t\interseptor.exe`, `C:\t\u.bat`, `C:\t\u.log`, `C:\Program Files!\interceptor.exe`)
	// The batch line must echo a literal %~dp0 and %* into the shim.
	if !strings.Contains(script, `echo "%%~dp0interseptor.exe" %%*>> "C:\Program Files!\interceptor.bat"`) {
		t.Fatalf("legacy shim line is mis-escaped:\n%s", script)
	}
	if strings.Contains(script, "EnableDelayedExpansion") || strings.Contains(script, "!TRY!") {
		t.Fatal("delayed expansion corrupts paths containing '!'")
	}
	if strings.Contains(script, "%!") || strings.Contains(script, "(MISSING)") {
		t.Fatalf("fmt verb leaked into the script:\n%s", script)
	}
}
