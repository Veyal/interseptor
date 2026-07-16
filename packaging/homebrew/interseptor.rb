# Homebrew formula template for Interseptor.
# When the `brews:` block in .goreleaser.yaml is enabled, goreleaser regenerates
# this with the real version + SHA256 per release into the configured tap repo.
# Until then this is a reference shape; install via the Releases page or
# `interseptor update`.

class Interseptor < Formula
  desc "Intercepting HTTP/HTTPS proxy + security toolkit (single static Go binary)"
  homepage "https://github.com/Veyal/interseptor"
  url "https://github.com/Veyal/interseptor/releases/download/v0.0.0-placeholder/interseptor_0.0.0-placeholder_darwin_arm64.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
  license "MIT"
  version "0.0.0-placeholder"

  # Single static binary, no cgo, no runtime deps.
  def install
    bin.install "interseptor"
  end

  test do
    assert_match "interseptor v", shell_output("#{bin}/interseptor version")
  end
end
