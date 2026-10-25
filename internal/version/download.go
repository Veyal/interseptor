package version

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// DownloadTo streams url into w with client (callers choose the timeouts; a
// large download should have no whole-request timeout). report, when non-nil,
// is called at most every 200ms and once at the end with (done, total); total
// is 0 when the server sent no length. limit > 0 fails the download once the
// body exceeds limit bytes. It returns the bytes written and the advertised
// length.
func DownloadTo(ctx context.Context, client *http.Client, url string, w io.Writer, limit int64, report func(done, total int64)) (int64, int64, error) {
	req, err := newGitHubRequest(ctx, http.MethodGet, url)
	if err != nil {
		return 0, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	total := resp.ContentLength
	var body io.Reader = resp.Body
	var last int64 = -1
	if report != nil {
		body = &progressReader{r: resp.Body, total: total, report: func(done, size int64) {
			last = done
			report(done, size)
		}}
	}
	if limit > 0 {
		body = io.LimitReader(body, limit+1)
	}
	n, err := io.Copy(w, body)
	if err != nil {
		return n, total, err
	}
	if limit > 0 && n > limit {
		return n, total, fmt.Errorf("download %s: exceeds %d bytes", url, limit)
	}
	if report != nil && last != n {
		report(n, total)
	}
	return n, total, nil
}

// ParseChecksums returns the SHA-256 listed for exactly assetName in
// sha256sum-format text ("<hex>  <name>" or "<hex> *<name>").
func ParseChecksums(data, assetName string) (string, bool) { return parseChecksums(data, assetName) }

// CompareVersions compares two dotted versions numerically (a leading v and
// any prerelease/build suffix are ignored). ok is false when either side is
// not a version.
func CompareVersions(a, b string) (cmp int, ok bool) {
	x, y := parseSemver(a), parseSemver(b)
	if x == nil || y == nil {
		return 0, false
	}
	return cmpSemver(x, y), true
}

// ReleaseAssetURL is the direct download URL of a file attached to the
// release tagged tag (for example "v2.19.0").
func ReleaseAssetURL(tag, name string) string { return releaseDownloadURL(tag, name) }
