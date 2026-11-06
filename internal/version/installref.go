package version

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// installModulePath is the module `go install` must reference. It is exactly
// what go.mod declares — github.com/Veyal/interseptor, with no /v2 suffix —
// while releases are tagged v2.x. That mismatch is why a tag ref cannot be
// installed (see installRefFor) and why the source fallback resolves the
// release tag to its commit instead.
const installModulePath = "github.com/" + Repo

// installPackagePath is the main package inside the module.
const installPackagePath = installModulePath + "/cmd/interseptor"

// installKind says which ref the source fallback hands to `go install`.
type installKind string

const (
	// installByCommit is module@<sha>. The toolchain derives the version
	// itself, and it derives a pseudo-version whose major comes from the
	// highest ancestor tag allowed by the path's major (§"semantic import
	// versioning"), so the module-path/major check always passes: for this
	// unsuffixed path the pull is v1.8.2-0.<ts>-<sha> (or v0.0.0-<ts>-<sha>
	// when the history has no v0/v1 tag at all).
	installByCommit installKind = "commit"
	// installByTag is module@vX.Y.Z, usable only when the tag's major matches
	// the path's major suffix.
	installByTag installKind = "tag"
	// installNone means no ref can work; the caller must print guidance
	// instead of running `go install` at all.
	installNone installKind = "none"
)

// installDecision is the outcome of the "this release has no prebuilt binary
// for the running platform" fallback.
type installDecision struct {
	Ref    string      // argument for `go install`; empty when Kind == installNone
	Kind   installKind // commit, tag or none
	Commit string      // resolved commit, when Kind == installByCommit
	Why    string      // one line: why no ref can be used (Kind == installNone)
}

// installRefFor decides how `go install` may reproduce release tag on
// goos/goarch when the release carries no prebuilt asset for that platform.
//
// Rules, in order:
//
//  1. An unknown platform or module path is refused: there is nothing to install.
//  2. A resolvable commit wins, because it is the only form that survives this
//     module's layout: `go install …@<sha>` is always accepted for a path whose
//     major suffix agrees with the derived pseudo-version, which it does by
//     construction (v0/v1 for an unsuffixed path such as this one).
//  3. A tag is accepted only when its major matches the path's major suffix
//     (none → v0/v1, "/v2" → v2). For github.com/Veyal/interseptor every v2.x
//     tag is refused here, before `go install` can emit its raw exit status.
//
// The rules are pure: no network, no toolchain, no filesystem.
func installRefFor(goos, goarch, tag, modulePath, commit string) installDecision {
	goos, goarch = strings.TrimSpace(goos), strings.TrimSpace(goarch)
	modulePath = strings.TrimSuffix(strings.TrimSpace(modulePath), "/")
	commit = strings.TrimSpace(commit)

	if goos == "" || goarch == "" {
		return installDecision{Kind: installNone, Why: "the target platform (GOOS/GOARCH) is unknown"}
	}
	if modulePath == "" {
		return installDecision{Kind: installNone, Why: "the module path is unknown"}
	}
	if isCommitSHA(commit) {
		return installDecision{
			Kind:   installByCommit,
			Ref:    modulePath + "/cmd/interseptor@" + commit,
			Commit: commit,
		}
	}
	ver, major, ok := splitTag(tag)
	if !ok {
		return installDecision{Kind: installNone, Why: fmt.Sprintf("release tag %q is not a semantic version", strings.TrimSpace(tag))}
	}
	if !majorAllowed(modulePath, major) {
		return installDecision{Kind: installNone, Why: majorMismatchReason(modulePath, ver, major)}
	}
	return installDecision{Kind: installByTag, Ref: modulePath + "/cmd/interseptor@v" + ver}
}

// majorMismatchReason explains, in the user's terms, why the Go toolchain
// refuses this tag for this module path.
func majorMismatchReason(modulePath, ver string, major int) string {
	if suffix := pathMajorSuffix(modulePath); suffix == "" {
		return fmt.Sprintf("module %q has no %q suffix, so the Go toolchain rejects v%s (module path must match major version; it would have to be %q)",
			modulePath, "/v"+strconv.Itoa(major), ver, modulePath+"/v"+strconv.Itoa(major))
	}
	return fmt.Sprintf("module %q is major %s, so the Go toolchain rejects the v%s tag (module path must match major version)",
		modulePath, pathMajorSuffix(modulePath), ver)
}

// guidance is the text the user sees when the source fallback is unavailable or
// failed. It names the platform, the release, the reason, and the exact
// commands that produce a working binary.
func (d installDecision) guidance(tag, goos, goarch string) string {
	ver := strings.TrimPrefix(strings.TrimSpace(tag), "v")
	var lead string
	switch d.Kind {
	case installByCommit:
		lead = fmt.Sprintf("no prebuilt binary for %s/%s in release v%s, and the source install at commit %s did not complete.", goos, goarch, ver, d.Commit)
	case installByTag:
		lead = fmt.Sprintf("no prebuilt binary for %s/%s in release v%s, and `go install %s` did not complete.", goos, goarch, ver, d.Ref)
	default:
		lead = fmt.Sprintf("no prebuilt binary for %s/%s in release v%s, and `go install` cannot fill the gap: %s.", goos, goarch, ver, d.Why)
	}
	return lead + "\n\n" + sourceBuildCommands(ver, goos)
}

// sourceBuildCommands is the "build it yourself" remedy, shared by every refusal
// and every failed source install.
func sourceBuildCommands(ver, goos string) string {
	bin := "interseptor"
	if goos == "windows" {
		bin = "interseptor.exe"
	}
	dir := repoName()
	return fmt.Sprintf(`Build it from source instead:
  git clone --depth 1 --branch v%s https://github.com/%s.git
  cd %s
  CGO_ENABLED=0 go build -o %s ./cmd/interseptor

Then move that %s over the binary you are updating; keep a copy of the old one to
roll back. It is a source build, so it reports the version baked into that tag and
carries no release stamp: the CLI `+"`interseptor update`"+` works from it, while the UI's
one-click updater leaves source builds alone (it will not overwrite a developer's
binary with a release).

Release page: https://github.com/%s/releases/tag/v%s`, ver, Repo, dir, bin, bin, Repo, ver)
}

// repoName is the repository's own name, i.e. the directory `git clone` makes.
func repoName() string {
	if i := strings.LastIndex(Repo, "/"); i >= 0 && i+1 < len(Repo) {
		return Repo[i+1:]
	}
	return Repo
}

// splitTag returns the tag without its leading v, its major version, and whether
// it is a semantic version at all.
func splitTag(tag string) (ver string, major int, ok bool) {
	ver = strings.TrimPrefix(strings.TrimSpace(tag), "v")
	if ver == "" {
		return "", 0, false
	}
	parts := parseSemver(ver)
	if parts == nil {
		return "", 0, false
	}
	return ver, parts[0], true
}

// pathMajorSuffix returns the major-version suffix the toolchain reads off a
// module path ("/v2" → "v2"), or "" when the path carries none. A path without
// a suffix may only be tagged v0.x/v1.x.
func pathMajorSuffix(modulePath string) string {
	p := strings.TrimSuffix(strings.TrimSpace(modulePath), "/")
	last := p[strings.LastIndex(p, "/")+1:]
	if len(last) < 2 || last[0] != 'v' {
		return ""
	}
	if n, err := strconv.Atoi(last[1:]); err == nil && n >= 2 {
		return last
	}
	return ""
}

// majorAllowed reports whether a tag of this major can be installed for
// modulePath under Go's semantic-import-versioning rule.
func majorAllowed(modulePath string, major int) bool {
	suffix := pathMajorSuffix(modulePath)
	if suffix == "" {
		return major == 0 || major == 1
	}
	return suffix == "v"+strconv.Itoa(major)
}

// isCommitSHA reports whether s is an abbreviated or full git object id
// (7-40 lowercase hex characters), the form `go install` resolves itself.
func isCommitSHA(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// shortSHA abbreviates a commit for progress lines.
func shortSHA(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// releaseCommitSHA resolves tag to the commit it points at, through the GitHub
// API. A release tag is usually annotated, so the tag object's own id is not the
// commit; /commits/<tag> peels it. Best-effort: on any failure the caller prints
// the guidance text instead of guessing a ref.
func releaseCommitSHA(ctx context.Context, tag string) (string, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", fmt.Errorf("empty release tag")
	}
	req, err := newGitHubRequest(ctx, http.MethodGet, githubAPIRoot+"/commits/"+url.PathEscape(tag))
	if err != nil {
		return "", err
	}
	resp, err := githubHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", githubAPIError(resp, "github release commit")
	}
	var raw struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return "", fmt.Errorf("github release commit %s: %w", tag, err)
	}
	if !isCommitSHA(raw.SHA) {
		return "", fmt.Errorf("github release commit %s: unexpected sha %q", tag, raw.SHA)
	}
	return strings.TrimSpace(raw.SHA), nil
}
