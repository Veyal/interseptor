// Package version is the single source of truth for the build version and a
// best-effort "is a newer release out?" check against the GitHub tags.
package version

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// Version is the baked-in release version — keep it in sync with the git tag.
const Version = "0.12.0"

// Repo is the GitHub owner/name used for the update check.
const Repo = "Veyal/interceptor"

// String returns the running version: the module version when installed via
// `go install …@vX.Y.Z` (authoritative), otherwise the baked-in constant. It
// deliberately ignores "(devel)" and pseudo-versions (vX.Y.Z-0.<ts>-<hash>) from
// between-tags local builds, which would otherwise look ugly in the UI/logs.
func String() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; isReleaseVersion(v) {
			return strings.TrimPrefix(v, "v")
		}
	}
	return Version
}

// isReleaseVersion reports whether v is a clean release tag (vX.Y.Z, optionally
// with a +dirty / build-metadata suffix) rather than "(devel)" or a pseudo-version.
func isReleaseVersion(v string) bool {
	base := strings.SplitN(strings.TrimPrefix(v, "v"), "+", 2)[0]
	return base != "" && !strings.Contains(base, "-") && parseSemver(base) != nil
}

// CheckLatest queries GitHub for the highest released tag and reports whether it
// is newer than the running version. Best-effort: any error (offline, rate
// limit, etc.) returns ("", false, err) for the caller to ignore quietly.
func CheckLatest(ctx context.Context) (latest string, newer bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+Repo+"/tags?per_page=30", nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("github tags: HTTP %d", resp.StatusCode)
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "", false, err
	}
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	l, n := pickLatest(names, String())
	return l, n, nil
}

// pickLatest returns the highest semver tag (without the leading v) and whether
// it is strictly newer than current. Non-semver tags are ignored.
func pickLatest(tags []string, current string) (string, bool) {
	cur := parseSemver(current)
	var best []int
	latest := ""
	for _, t := range tags {
		v := parseSemver(t)
		if v == nil {
			continue
		}
		if best == nil || cmpSemver(v, best) > 0 {
			best, latest = v, strings.TrimPrefix(strings.TrimSpace(t), "v")
		}
	}
	if latest == "" {
		return "", false
	}
	return latest, cur != nil && cmpSemver(best, cur) > 0
}

// parseSemver parses "vX.Y.Z" (prerelease/build suffix ignored) into [3]int.
func parseSemver(s string) []int {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s = strings.SplitN(s, "-", 2)[0]
	s = strings.SplitN(s, "+", 2)[0]
	parts := strings.Split(s, ".")
	if len(parts) == 0 || parts[0] == "" {
		return nil
	}
	out := []int{0, 0, 0}
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

func cmpSemver(a, b []int) int {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] > b[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}
