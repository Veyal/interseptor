package version

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// githubAPIRoot is the repos API base (overridable in tests).
var githubAPIRoot = "https://api.github.com/repos/" + Repo

// githubReleasesLatest is the public latest-release URL (no API quota; overridable in tests).
var githubReleasesLatest = "https://github.com/" + Repo + "/releases/latest"

// githubReleasesDownload is the direct asset base (no API quota; overridable in tests).
var githubReleasesDownload = "https://github.com/" + Repo + "/releases/download"

// githubHTTP is the shared client for GitHub API calls (update check + release fetch).
var githubHTTP = &http.Client{Timeout: 30 * time.Second}

// githubWebHTTP fetches github.com pages and release assets (not api.github.com).
var githubWebHTTP = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

// newGitHubRequest builds a GitHub REST request with the headers GitHub requires.
// Unauthenticated calls are rate-limited (~60/h per IP); set GITHUB_TOKEN or
// INTERSEPTOR_GITHUB_TOKEN for a higher quota.
func newGitHubRequest(ctx context.Context, method, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "interseptor/"+String()+" (https://github.com/"+Repo+")")
	if tok := githubToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

func githubToken() string {
	for _, k := range []string{"GITHUB_TOKEN", "INTERSEPTOR_GITHUB_TOKEN", "GH_TOKEN"} {
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
		if d := retryAfterHint(resp); d != "" {
			hint += "; retry after " + d
		}
		return fmt.Errorf("%s: %s", what, hint)
	case http.StatusTooManyRequests:
		msg := fmt.Sprintf("%s: GitHub API rate limit (HTTP 429)", what)
		if d := retryAfterHint(resp); d != "" {
			msg += "; retry after " + d
		}
		msg += "; set GITHUB_TOKEN for a higher quota"
		return fmt.Errorf("%s", msg)
	default:
		return fmt.Errorf("%s: HTTP %d", what, resp.StatusCode)
	}
}

func retryAfterHint(resp *http.Response) string {
	if ra := strings.TrimSpace(resp.Header.Get("Retry-After")); ra != "" {
		if sec, err := strconv.Atoi(ra); err == nil && sec > 0 {
			if sec < 120 {
				return fmt.Sprintf("%ds", sec)
			}
			return fmt.Sprintf("%dm", (sec+59)/60)
		}
		return ra
	}
	if reset := strings.TrimSpace(resp.Header.Get("X-RateLimit-Reset")); reset != "" {
		if unix, err := strconv.ParseInt(reset, 10, 64); err == nil && unix > 0 {
			until := time.Until(time.Unix(unix, 0))
			if until > 0 {
				if until < 2*time.Minute {
					return fmt.Sprintf("%ds", int(until.Seconds())+1)
				}
				return fmt.Sprintf("%dm", int(until.Minutes())+1)
			}
		}
	}
	return ""
}

func githubAPIRateLimited(status int) bool {
	return status == http.StatusForbidden || status == http.StatusTooManyRequests
}

func releaseDownloadURL(tag, name string) string {
	return fmt.Sprintf("%s/%s/%s", githubReleasesDownload, tag, name)
}

// checkLatestRedirect follows github.com/…/releases/latest and parses the tag
// from the redirect target (/releases/tag/vX.Y.Z).
func checkLatestRedirect(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubReleasesLatest, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "interseptor/"+String()+" (https://github.com/"+Repo+")")
	client := githubWebHTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
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
