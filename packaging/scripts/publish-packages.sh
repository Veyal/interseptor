#!/usr/bin/env bash
# Publish Homebrew/Scoop manifests for a release tag.
# Usage: TAG=v1.5.2 HOMEBREW_TAP_TOKEN=… SCOOP_BUCKET_TOKEN=… ./packaging/scripts/publish-packages.sh
set -euo pipefail
TAG="${TAG:?set TAG=vX.Y.Z}"
VER="${TAG#v}"
SUMS=$(curl -fsSL "https://github.com/Veyal/interseptor/releases/download/${TAG}/checksums.txt")
sha() { echo "$SUMS" | awk -v a="$1" '$2==a{print $1;exit}'; }

if [ -n "${HOMEBREW_TAP_TOKEN:-}" ]; then
  sha_arm=$(sha "interseptor_${VER}_darwin_arm64.tar.gz")
  sha_amd=$(sha "interseptor_${VER}_darwin_amd64.tar.gz")
  tmp=$(mktemp -d)
  git clone --depth 1 "https://x-access-token:${HOMEBREW_TAP_TOKEN}@github.com/Veyal/homebrew-tap.git" "$tmp/tap"
  mkdir -p "$tmp/tap/Formula"
  cat > "$tmp/tap/Formula/interseptor.rb" <<RB
class Interseptor < Formula
  desc "Intercepting HTTP/HTTPS proxy + security toolkit (single static Go binary)"
  homepage "https://github.com/Veyal/interseptor"
  license "MIT"
  version "${VER}"

  on_arm do
    url "https://github.com/Veyal/interseptor/releases/download/${TAG}/interseptor_${VER}_darwin_arm64.tar.gz"
    sha256 "${sha_arm}"
  end
  on_intel do
    url "https://github.com/Veyal/interseptor/releases/download/${TAG}/interseptor_${VER}_darwin_amd64.tar.gz"
    sha256 "${sha_amd}"
  end

  def install
    bin.install "interseptor"
  end

  test do
    assert_match "interseptor v", shell_output("#{bin}/interseptor version")
  end
end
RB
  cd "$tmp/tap"
  git config user.name "interseptor-bot"
  git config user.email "noreply@github.com"
  git add Formula/interseptor.rb
  git diff --cached --quiet || git commit -m "interseptor ${VER}"
  git push origin HEAD:main
  echo "Homebrew tap updated"
else
  echo "HOMEBREW_TAP_TOKEN unset — skip Homebrew"
fi

if [ -n "${SCOOP_BUCKET_TOKEN:-}" ]; then
  sha64=$(sha "interseptor_${VER}_windows_amd64.zip")
  shaarm=$(sha "interseptor_${VER}_windows_arm64.zip")
  tmp=$(mktemp -d)
  git clone --depth 1 "https://x-access-token:${SCOOP_BUCKET_TOKEN}@github.com/Veyal/scoop-bucket.git" "$tmp/bucket"
  python3 - <<PY
import json
from pathlib import Path
ver, tag = "${VER}", "${TAG}"
Path("$tmp/bucket/interseptor.json").write_text(json.dumps({
  "version": ver,
  "description": "Intercepting HTTP/HTTPS proxy + security toolkit (single static Go binary)",
  "homepage": "https://github.com/Veyal/interseptor",
  "license": "MIT",
  "architecture": {
    "64bit": {"url": f"https://github.com/Veyal/interseptor/releases/download/{tag}/interseptor_{ver}_windows_amd64.zip", "hash": "${sha64}"},
    "arm64": {"url": f"https://github.com/Veyal/interseptor/releases/download/{tag}/interseptor_{ver}_windows_arm64.zip", "hash": "${shaarm}"},
  },
  "bin": "interseptor.exe",
  "checkver": {"github": "https://github.com/Veyal/interseptor"},
  "autoupdate": {"architecture": {
    "64bit": {"url": "https://github.com/Veyal/interseptor/releases/download/v\$version/interseptor_\$version_windows_amd64.zip"},
    "arm64": {"url": "https://github.com/Veyal/interseptor/releases/download/v\$version/interseptor_\$version_windows_arm64.zip"},
  }},
}, indent=2) + "\n")
PY
  cd "$tmp/bucket"
  git config user.name "interseptor-bot"
  git config user.email "noreply@github.com"
  git add interseptor.json
  git diff --cached --quiet || git commit -m "interseptor ${VER}"
  git push origin HEAD:main
  echo "Scoop bucket updated"
else
  echo "SCOOP_BUCKET_TOKEN unset — skip Scoop"
fi
