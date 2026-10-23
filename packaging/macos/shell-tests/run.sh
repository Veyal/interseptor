#!/bin/bash
# Compiles and runs the Swift shell unit tests with plain swiftc (no Xcode).
# Only Foundation-only shell files are compiled in; AppKit/WebKit glue is
# covered by compiling the full shell (packaging/macos/build-app.sh).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
shell="$here/../shell"
out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT
xcrun swiftc -swift-version 5 -O -module-name ShellTests \
	"$here/main.swift" \
	"$shell/Supervisor.swift" \
	"$shell/ExternalLinks.swift" \
	"$shell/DownloadStaging.swift" \
	"$shell/ReloadBudget.swift" \
	"$shell/Versioning.swift" \
	"$shell/UpdateVerifier.swift" \
	"$shell/UpdateHelperScript.swift" \
	-o "$out/shell-tests"
"$out/shell-tests"
