package version

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

// The self-update fallback must never hand `go install` a ref the toolchain
// rejects. This module declares github.com/Veyal/interseptor (no /v2 suffix) and
// is released as v2.x, so `go install …@v2.21.0` fails before any build starts:
//
//	invalid version: module contains a go.mod file, so module path must match
//	major version ("github.com/Veyal/interseptor/v2")
func TestInstallRefForRejectsTagWhoseMajorThePathCannotSatisfy(t *testing.T) {
	d := installRefFor("linux", "arm64", "v2.21.0", "github.com/Veyal/interseptor", "")
	if d.Kind != installNone {
		t.Fatalf("kind = %q, want %q (ref %q)", d.Kind, installNone, d.Ref)
	}
	if d.Ref != "" {
		t.Fatalf("ref = %q, want empty: a v2 tag must never reach `go install` for this path", d.Ref)
	}
	for _, want := range []string{
		"github.com/Veyal/interseptor",
		`"/v2"`,
		"v2.21.0",
		"module path must match major version",
	} {
		if !strings.Contains(d.Why, want) {
			t.Fatalf("why = %q, want it to mention %q", d.Why, want)
		}
	}
}

func TestInstallRefForAcceptsTagsMatchingThePathMajor(t *testing.T) {
	cases := []struct {
		name       string
		modulePath string
		tag        string
		wantKind   installKind
		wantRef    string
	}{
		{
			name:       "v1 tag on an unsuffixed path",
			modulePath: "github.com/Veyal/interseptor",
			tag:        "v1.8.1",
			wantKind:   installByTag,
			wantRef:    "github.com/Veyal/interseptor/cmd/interseptor@v1.8.1",
		},
		{
			name:       "v0 tag on an unsuffixed path",
			modulePath: "github.com/Veyal/interseptor",
			tag:        "v0.17.0",
			wantKind:   installByTag,
			wantRef:    "github.com/Veyal/interseptor/cmd/interseptor@v0.17.0",
		},
		{
			name:       "matching /v2 suffix",
			modulePath: "github.com/example/mod/v2",
			tag:        "v2.3.4",
			wantKind:   installByTag,
			wantRef:    "github.com/example/mod/v2/cmd/interseptor@v2.3.4",
		},
		{
			name:       "/v2 suffix, v3 tag is still refused",
			modulePath: "github.com/example/mod/v2",
			tag:        "v3.0.0",
			wantKind:   installNone,
		},
		{
			name:       "non-semver tag",
			modulePath: "github.com/Veyal/interseptor",
			tag:        "nightly",
			wantKind:   installNone,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := installRefFor("linux", "amd64", tc.tag, tc.modulePath, "")
			if d.Kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q (why %q)", d.Kind, tc.wantKind, d.Why)
			}
			if d.Ref != tc.wantRef {
				t.Fatalf("ref = %q, want %q", d.Ref, tc.wantRef)
			}
			if d.Kind == installNone && d.Why == "" {
				t.Fatal("a refusal must carry a reason")
			}
		})
	}
}

// A commit ref is the one form that always works for this layout: `go install
// pkg@<sha>` lets the toolchain derive the version itself, and it derives a
// pseudo-version whose major is the path's own (v0/v1 for an unsuffixed path),
// so the module-path/major check passes.
func TestInstallRefForPrefersCommitAndKeepsItsMajorCompatible(t *testing.T) {
	const full = "86a135cbc2be90756488ff8a054a7efd81ed27c7"
	for _, tc := range []struct {
		name       string
		modulePath string
		tag        string
	}{
		{"unsuffixed path, v2 tag", "github.com/Veyal/interseptor", "v2.21.0"},
		{"unsuffixed path, v1 tag", "github.com/Veyal/interseptor", "v1.8.1"},
		{"suffixed path", "github.com/example/mod/v2", "v2.3.4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := installRefFor("linux", "arm64", tc.tag, tc.modulePath, full)
			if d.Kind != installByCommit {
				t.Fatalf("kind = %q, want %q (why %q)", d.Kind, installByCommit, d.Why)
			}
			want := tc.modulePath + "/cmd/interseptor@" + full
			if d.Ref != want {
				t.Fatalf("ref = %q, want %q", d.Ref, want)
			}
			if d.Commit != full {
				t.Fatalf("commit = %q, want %q", d.Commit, full)
			}
		})
	}

	// An abbreviated id is a valid go-install ref too.
	d := installRefFor("darwin", "arm64", "v2.21.0", "github.com/Veyal/interseptor", "86a135c")
	if d.Kind != installByCommit || !strings.HasSuffix(d.Ref, "@86a135c") {
		t.Fatalf("short sha: kind = %q ref = %q", d.Kind, d.Ref)
	}

	// Junk in the commit slot falls back to the tag rules instead of producing
	// an unusable ref.
	for _, junk := range []string{"", "not-a-sha", "86A135C", "xyz1234", "86a135cbc2be90756488ff8a054a7efd81ed27c7extra"} {
		d := installRefFor("darwin", "arm64", "v2.21.0", "github.com/Veyal/interseptor", junk)
		if d.Kind != installNone || d.Ref != "" {
			t.Fatalf("commit %q: kind = %q ref = %q, want a refusal", junk, d.Kind, d.Ref)
		}
	}
}

func TestInstallRefForRejectsUnknownPlatformAndModule(t *testing.T) {
	for _, tc := range []struct{ goos, goarch, modulePath string }{
		{"", "amd64", "github.com/Veyal/interseptor"},
		{"linux", "", "github.com/Veyal/interseptor"},
		{"linux", "amd64", ""},
	} {
		d := installRefFor(tc.goos, tc.goarch, "v1.8.1", tc.modulePath, "86a135cbc2be")
		if d.Kind != installNone || d.Ref != "" || d.Why == "" {
			t.Fatalf("%s/%s %q: kind = %q ref = %q why = %q", tc.goos, tc.goarch, tc.modulePath, d.Kind, d.Ref, d.Why)
		}
	}
}

// The user-facing text is the whole point of the fix: it replaces a bare
// "go install: exit status 1" with commands that work.
func TestInstallGuidanceForUninstallableTag(t *testing.T) {
	d := installRefFor("darwin", "arm64", "v2.21.0", "github.com/Veyal/interseptor", "")
	msg := d.guidance("v2.21.0", "darwin", "arm64")

	for _, want := range []string{
		"no prebuilt binary for darwin/arm64",
		"v2.21.0",
		"module path must match major version",
		"git clone --depth 1 --branch v2.21.0 https://github.com/Veyal/interseptor.git",
		"cd interseptor",
		"CGO_ENABLED=0 go build -o interseptor ./cmd/interseptor",
		"keep a copy",
		"https://github.com/Veyal/interseptor/releases/tag/v2.21.0",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("guidance missing %q:\n%s", want, msg)
		}
	}
	// The ref the old code used must not be advertised as a working command.
	if strings.Contains(msg, "@/cmd/interseptor@v") || strings.Contains(msg, "go install github.com/Veyal/interseptor/cmd/interseptor@v") {
		t.Fatalf("guidance still suggests a tag install:\n%s", msg)
	}
}

func TestInstallGuidanceNamesTheBinaryForWindows(t *testing.T) {
	d := installRefFor("windows", "arm64", "v2.21.0", "github.com/Veyal/interseptor", "")
	msg := d.guidance("v2.21.0", "windows", "arm64")
	if !strings.Contains(msg, "no prebuilt binary for windows/arm64") {
		t.Fatalf("guidance does not name the platform:\n%s", msg)
	}
	if !strings.Contains(msg, `go build -o interseptor.exe ./cmd/interseptor`) {
		t.Fatalf("guidance does not build a .exe on windows:\n%s", msg)
	}
}

func TestInstallGuidanceAfterFailedCommitInstall(t *testing.T) {
	const commit = "86a135cbc2be90756488ff8a054a7efd81ed27c7"
	d := installRefFor("linux", "arm", "v2.21.0", "github.com/Veyal/interseptor", commit)
	msg := d.guidance("v2.21.0", "linux", "arm")
	if !strings.Contains(msg, "no prebuilt binary for linux/arm") {
		t.Fatalf("guidance does not name the platform:\n%s", msg)
	}
	if !strings.Contains(msg, commit) {
		t.Fatalf("guidance does not name the commit that was tried:\n%s", msg)
	}
	if !strings.Contains(msg, "CGO_ENABLED=0 go build -o interseptor ./cmd/interseptor") {
		t.Fatalf("guidance has no build commands:\n%s", msg)
	}
}

// The env for a source install must pin the requested platform, not the host,
// and must not leave a duplicate CGO_ENABLED/GOOS behind.
func TestGoInstallEnvPinsTargetPlatform(t *testing.T) {
	base := []string{"PATH=/bin", "CGO_ENABLED=1", "GOOS=windows", "GOARCH=386", "HOME=/home/x"}
	got := goInstallEnv(base, "linux", "arm64")
	want := map[string]string{"CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": "arm64"}
	seen := map[string]int{}
	for _, kv := range got {
		name, val, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("malformed env entry %q", kv)
		}
		seen[name]++
		if w, ok := want[name]; ok && val != w {
			t.Fatalf("%s = %q, want %q (env: %v)", name, val, w, got)
		}
	}
	for name, n := range seen {
		if n > 1 {
			t.Fatalf("%s set %d times: %v", name, n, got)
		}
	}
	if seen["PATH"] != 1 || seen["HOME"] != 1 {
		t.Fatalf("base env dropped: %v", got)
	}
}

func TestGoInstallEnvLeavesPlatformUnsetWhenUnknown(t *testing.T) {
	got := goInstallEnv([]string{"PATH=/bin"}, "", "")
	joined := strings.Join(got, "\x00")
	if strings.Contains(joined, "GOOS=") || strings.Contains(joined, "GOARCH=") {
		t.Fatalf("unknown platform must not be pinned: %v", got)
	}
	if !strings.Contains(joined, "CGO_ENABLED=0") {
		t.Fatalf("CGO_ENABLED=0 missing: %v", got)
	}
}

// releaseCommitSHA must peel an annotated tag to its commit through the commits
// endpoint, and must fail (not panic, not guess) on any other answer.
func TestReleaseCommitSHA(t *testing.T) {
	const sha = "86a135cbc2be90756488ff8a054a7efd81ed27c7"
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			http.Error(w, "bad accept", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sha":"` + sha + `","commit":{"message":"chore(release): 2.21.0"}}`))
	}))
	defer srv.Close()
	old := githubAPIRoot
	t.Cleanup(func() { githubAPIRoot = old })
	githubAPIRoot = srv.URL + "/repos/" + Repo

	got, err := releaseCommitSHA(context.Background(), "v2.21.0")
	if err != nil {
		t.Fatalf("releaseCommitSHA: %v", err)
	}
	if got != sha {
		t.Fatalf("sha = %q, want %q", got, sha)
	}
	if want := "/repos/" + Repo + "/commits/v2.21.0"; gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
}

func TestReleaseCommitSHARejectsBadAnswers(t *testing.T) {
	old := githubAPIRoot
	t.Cleanup(func() { githubAPIRoot = old })

	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"not found", http.StatusNotFound, `{"message":"Not Found"}`},
		{"rate limited", http.StatusForbidden, `{"message":"API rate limit exceeded"}`},
		{"no sha", http.StatusOK, `{}`},
		{"short sha", http.StatusOK, `{"sha":"86a1"}`},
		{"not json", http.StatusOK, `<html>nope</html>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			githubAPIRoot = srv.URL + "/repos/" + Repo
			if _, err := releaseCommitSHA(context.Background(), "v2.21.0"); err == nil {
				t.Fatal("want an error, got nil")
			}
		})
	}
}

// The release-tag path for platforms that DO have an asset must not be touched
// by the fallback logic: an asset still wins before any ref is considered.
func TestAssetStillWinsOverSourceInstall(t *testing.T) {
	rel := &releaseInfo{
		Tag:    "v2.21.0",
		Assets: []releaseAsset{{Name: "interseptor_2.21.0_" + platformTokensFor(runtime.GOOS, runtime.GOARCH) + ".tar.gz", URL: "https://example.com/a"}},
	}
	if name, url := pickAsset(rel, "2.21.0"); url == "" {
		t.Fatalf("asset path no longer picks a prebuilt archive (name %q)", name)
	}
}

// platformTokensFor renders the GoReleaser os_arch infix for goos/goarch.
func platformTokensFor(goos, goarch string) string {
	osToken, archToken := platformTokens(goos, goarch)
	return osToken + "_" + archToken
}
