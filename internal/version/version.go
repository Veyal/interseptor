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
)

// Version is the baked-in fallback version for dev builds. Release binaries
// report the real version from the module build info (the git tag) instead, so
// this constant deliberately tracks the last *published* release — bumping it
// ahead of the tag would break the update-check test, which verifies the named
// release actually exists on GitHub. See CONTRIBUTING.md §"Cutting a release".
const Version = "1.6.0"

// Repo is the GitHub owner/name used for the update check.
const Repo = "Veyal/interseptor"

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

// CheckLatest queries GitHub for the latest release tag and reports whether it
// is newer than the running version. Best-effort: any error (offline, rate
// limit, etc.) returns ("", false, err) for the caller to ignore quietly.
func CheckLatest(ctx context.Context) (latest string, newer bool, err error) {
	if c, ok := readLatestCache(); ok {
		return finishLatestCheck(c.Latest)
	}

	var apiErr error
	if githubToken() != "" {
		latest, err = checkLatestAPI(ctx)
		if err == nil {
			writeLatestCache(latest, "")
			return finishLatestCheck(latest)
		}
		apiErr = err
	}

	latest, err = checkLatestRedirect(ctx)
	if err != nil {
		if apiErr != nil {
			return "", false, apiErr
		}
		return "", false, err
	}
	writeLatestCache(latest, "")
	return finishLatestCheck(latest)
}

func finishLatestCheck(latest string) (string, bool, error) {
	cur := parseSemver(String())
	best := parseSemver(latest)
	if best == nil {
		return "", false, fmt.Errorf("github latest release: invalid tag %q", latest)
	}
	return latest, cur != nil && cmpSemver(best, cur) > 0, nil
}

func checkLatestAPI(ctx context.Context) (string, error) {
	req, err := newGitHubRequest(ctx, http.MethodGet, githubAPIRoot+"/releases/latest")
	if err != nil {
		return "", err
	}
	resp, err := githubHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", githubAPIError(resp, "github latest release")
	}
	var raw struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", err
	}
	latest := strings.TrimPrefix(strings.TrimSpace(raw.TagName), "v")
	if latest == "" || parseSemver(latest) == nil {
		return "", fmt.Errorf("github latest release: invalid tag %q", raw.TagName)
	}
	return latest, nil
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
