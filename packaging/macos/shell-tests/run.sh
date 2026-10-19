#!/bin/bash
# Compiles and runs the Swift shell unit tests with plain swiftc (no Xcode).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT
xcrun swiftc -swift-version 5 -O -module-name ShellTests \
	"$here/main.swift" "$here/../shell/Supervisor.swift" -o "$out/shell-tests"
"$out/shell-tests"
