#!/usr/bin/env bash
# Fail when a Go file changed on this branch is not gofmt-clean.
#
# Only files changed relative to BASE (default: merge-base with origin/main, else
# HEAD~1) plus uncommitted edits are checked, so legacy files that were never
# formatted do not block unrelated work. Use `GOFMT_ALL=1` to check everything.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

if [[ "${GOFMT_ALL:-}" == "1" ]]; then
  files=$(git ls-files '*.go')
else
  base="${BASE:-}"
  if [[ -z "$base" ]]; then
    base=$(git merge-base HEAD origin/main 2>/dev/null || true)
    # On main itself the merge-base is HEAD; compare with the previous commit.
    if [[ -z "$base" || "$base" == "$(git rev-parse HEAD)" ]]; then
      base=$(git rev-parse HEAD~1 2>/dev/null || git hash-object -t tree /dev/null)
    fi
  fi
  files=$( { git diff --name-only --diff-filter=ACMR "$base" -- '*.go'; git ls-files --others --exclude-standard -- '*.go'; } | sort -u)
fi

[[ -n "$files" ]] || { echo "gofmt: no Go files to check"; exit 0; }
# shellcheck disable=SC2086
bad=$(echo "$files" | xargs gofmt -l)
if [[ -n "$bad" ]]; then
  echo "gofmt: these files need formatting (run: gofmt -w <file>):" >&2
  echo "$bad" >&2
  exit 1
fi
echo "gofmt: ok ($(echo "$files" | wc -l | tr -d ' ') files checked)"
