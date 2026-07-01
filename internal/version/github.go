package version

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// githubAPIRoot is the repos API base (overridable in tests).
var githubAPIRoot = "https://api.github.com/repos/" + Repo

// githubReleasesLatest is the public latest-release URL (no API quota; overridable in tests).
var githubReleasesLatest = "https://github.com/" + Repo + "/releases/latest"

// githubHTTP is the shared client for GitHub API calls (update check + release fetch).
var githubHTTP = &http.Client{Timeout: 30 * time.Second}

// newGitHubRequest builds a GitHub REST request with the headers GitHub requires.
// Unauthenticated calls are rate-limited (~60/h per IP); set GITHUB_TOKEN or
// INTERCEPTOR_GITHUB_TOKEN for a higher quota.
func newGitHubRequest(ctx context.Context, method, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "interceptor/"+String()+" (https://github.com/"+Repo+")")
	if tok := githubToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

func githubToken() string {
	for _, k := range []string{"GITHUB_TOKEN", "INTERCEPTOR_GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func githubAPIError(resp *http.Response, what string) error {
	switch resp.StatusCode {
	case http.StatusForbidden:
		hint := "GitHub API access denied (HTTP 403)"
		if githubToken() == "" {
			hint += " — unauthenticated rate limit may be exhausted; set GITHUB_TOKEN and retry"
		}
		return fmt.Errorf("%s: %s", what, hint)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%s: GitHub API rate limit (HTTP 429); retry later or set GITHUB_TOKEN", what)
	default:
		return fmt.Errorf("%s: HTTP %d", what, resp.StatusCode)
	}
}

// checkLatestRedirect follows github.com/…/releases/latest and parses the tag
// from the redirect target (/releases/tag/vX.Y.Z).
func checkLatestRedirect(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubReleasesLatest, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "interceptor/"+String()+" (https://github.com/"+Repo+")")
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github releases page: HTTP %d", resp.StatusCode)
	}
	path := resp.Request.URL.Path
	const prefix = "/releases/tag/"
	if i := strings.Index(path, prefix); i >= 0 {
		tag := strings.TrimPrefix(path[i+len(prefix):], "v")
		if parseSemver(tag) != nil {
			return tag, nil
		}
	}
	return "", fmt.Errorf("github releases page: could not parse tag from %s", resp.Request.URL)
}
