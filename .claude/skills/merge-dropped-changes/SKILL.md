---
name: merge-dropped-changes
description: A merge can silently drop code from one parent while the CHANGELOG still advertises it. Use after resolving a conflicted merge, when a documented behaviour is missing from the code, or when auditing a merge commit.
---

# Changes lost in a merge

Merge `2e1a562e` (#121) resolved conflicts in `internal/proxy/proxy.go` by hand and dropped work
from its second parent: the MITM write coalescing (`f2052430`), most of the connection-setup
timeouts (`457abea1`) and the proxy listener's `IdleTimeout`. Their test files were deleted by the
merge, so nothing went red, and the CHANGELOG kept describing all three.

## Find them

```bash
M=<merge>
# Test files a parent had and the merge removed, that are still absent today.
for p in 1 2; do
  git diff --diff-filter=D --name-only $M^$p $M | grep -E '_test\.go$|\.test\.mjs$' |
    while read f; do [ -e "$f" ] || echo "parent $p dropped $f"; done
done
# Files that differ from BOTH parents were resolved by hand; read those diffs.
comm -12 <(git diff --name-only $M^1 $M | sort) <(git diff --name-only $M^2 $M | sort)
```

`git log --diff-filter=D -- <file>` prints nothing for these files: a deletion made inside a merge
is not attributed to any commit, which is why they are easy to miss.

## Decide: lost or superseded

Restore the test from the parent (`git show $M^2:path > path`) and run it.

- It fails on behaviour the commit message promised: the change was lost. Restore the code with the
  test, and re-measure if it was a performance change.
- It does not compile because an API moved, and newer tests cover the same behaviour under another
  name: it was superseded. Delete the restored copy and say so.

Both outcomes occurred here: `mitm_write_test.go` exposed a real loss (53 writes per small
response instead of 3); `getflow_test.go` and `fullproject_ai_test.go` had been replaced by
`untrusted_test.go` and `fullproject_paths_test.go`.

## After a conflicted merge

Run each parent's new tests against the result before pushing, and check every CHANGELOG bullet
the merge carries against the code it names.
