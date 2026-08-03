#!/usr/bin/env bash
#
# Build Interseptor.app — a macOS bundle wrapping the existing static Go binary
# and its embedded web UI. See packaging/macos/README.md for the full workflow.
#
# Signing and notarization are opt-in: without credentials this produces a
# working unsigned bundle for local use. Set the env vars below to produce a
# distributable one.
#
#   CODESIGN_IDENTITY   "Developer ID Application: Name (TEAMID)" — enables signing
#   NOTARY_PROFILE      notarytool keychain profile name        — enables notarization
#   BUNDLE_ID           override the bundle identifier (default below)
#   VERSION             override the version string (default: git describe)
#   MAKE_DMG=1          also produce a DMG next to the bundle
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST_DIR="${DIST_DIR:-$REPO_ROOT/dist/macos}"
APP_NAME="Interseptor"
APP_DIR="$DIST_DIR/$APP_NAME.app"
BUNDLE_ID="${BUNDLE_ID:-com.veyal.interseptor}"

if [[ "$(uname -s)" != "Darwin" ]]; then
	echo "error: this script must run on macOS (needs lipo, codesign, iconutil)" >&2
	exit 1
fi

# Temp dirs are registered here so a failure mid-build (a failed go build, a
# failed hdiutil) cannot leak them: set -e would otherwise skip the rm -rf.
TMPDIRS=()
cleanup() {
	local d
	for d in "${TMPDIRS[@]:-}"; do
		if [[ -n "$d" ]]; then rm -rf "$d"; fi
	done
}
trap cleanup EXIT

VERSION="${VERSION:-$(git -C "$REPO_ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
# CFBundleShortVersionString/CFBundleVersion must be numeric-dotted; strip any
# -g<sha>/-dirty suffix. A shallow CI checkout has no tags, so `git describe`
# yields a bare commit SHA — fall back to the compiled-in version rather than
# stamping something Apple will reject.
SHORT_VERSION="$(printf '%s' "$VERSION" | sed -E 's/[-+].*$//')"
if [[ ! "$SHORT_VERSION" =~ ^[0-9]+(\.[0-9]+)*$ ]]; then
	SHORT_VERSION="$(sed -n 's/^const Version = "\(.*\)"$/\1/p' "$REPO_ROOT/internal/version/version.go" | head -1)"
	echo "==> git describe gave no usable version ('$VERSION'); using internal/version: ${SHORT_VERSION:-0.0.0}"
fi
[[ "$SHORT_VERSION" =~ ^[0-9]+(\.[0-9]+)*$ ]] || SHORT_VERSION="0.0.0"

echo "==> Building $APP_NAME $VERSION"
rm -rf "$APP_DIR"
mkdir -p "$APP_DIR/Contents/MacOS" "$APP_DIR/Contents/Resources"

# --- universal binaries -------------------------------------------------------
build_universal() {
	local pkg="$1" out="$2" tmp
	tmp="$(mktemp -d)"
	TMPDIRS+=("$tmp")
	for arch in amd64 arm64; do
		echo "    $out ($arch)"
		( cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" \
			go build -trimpath -ldflags "-s -w" -o "$tmp/$arch" "$pkg" )
	done
	lipo -create -output "$out" "$tmp/amd64" "$tmp/arm64"
	rm -rf "$tmp"
}

echo "==> Compiling universal binaries"
build_universal ./cmd/interseptor "$APP_DIR/Contents/MacOS/interseptor"
build_universal ./cmd/interseptor-macapp "$APP_DIR/Contents/MacOS/interseptor-macapp"
chmod +x "$APP_DIR/Contents/MacOS/"*

# --- bundle metadata ----------------------------------------------------------
# '|' as the delimiter: BUNDLE_ID is user-overridable and a '/' in it would
# otherwise be read as the end of sed's replacement and abort the build.
sed -e "s|__VERSION__|$SHORT_VERSION|g" -e "s|__BUNDLE_ID__|$BUNDLE_ID|g" \
	"$REPO_ROOT/packaging/macos/Info.plist" > "$APP_DIR/Contents/Info.plist"
printf 'APPL????' > "$APP_DIR/Contents/PkgInfo"

# Icon sources, in precedence order: a hand-built .iconset, or a single square
# PNG (1024x1024 recommended) that we expand into one.
ICONSET="$REPO_ROOT/packaging/macos/AppIcon.iconset"
ICON_PNG="$REPO_ROOT/packaging/macos/AppIcon.png"
GENERATED_ICONSET=""
if [[ ! -d "$ICONSET" && -f "$ICON_PNG" ]]; then
	echo "==> Generating iconset from AppIcon.png"
	GENERATED_ICONSET="$(mktemp -d)/AppIcon.iconset"
	TMPDIRS+=("$(dirname "$GENERATED_ICONSET")")
	mkdir -p "$GENERATED_ICONSET"
	for size in 16 32 128 256 512; do
		sips -z $size $size "$ICON_PNG" --out "$GENERATED_ICONSET/icon_${size}x${size}.png" >/dev/null
		sips -z $((size * 2)) $((size * 2)) "$ICON_PNG" --out "$GENERATED_ICONSET/icon_${size}x${size}@2x.png" >/dev/null
	done
	ICONSET="$GENERATED_ICONSET"
fi
if [[ -d "$ICONSET" ]]; then
	echo "==> Building icon"
	iconutil -c icns "$ICONSET" -o "$APP_DIR/Contents/Resources/AppIcon.icns"
	if [[ -n "$GENERATED_ICONSET" ]]; then
		rm -rf "$(dirname "$GENERATED_ICONSET")"
	fi
else
	echo "==> No AppIcon.iconset or AppIcon.png — bundle will use the generic app icon"
fi

# --- signing ------------------------------------------------------------------
if [[ -n "${CODESIGN_IDENTITY:-}" ]]; then
	echo "==> Signing with: $CODESIGN_IDENTITY"
	# Inner Mach-O files first, bundle last — the bundle seal covers them.
	for bin in "$APP_DIR/Contents/MacOS/"*; do
		codesign --force --timestamp --options runtime --sign "$CODESIGN_IDENTITY" "$bin"
	done
	codesign --force --timestamp --options runtime --sign "$CODESIGN_IDENTITY" "$APP_DIR"
	codesign --verify --deep --strict --verbose=2 "$APP_DIR"
else
	echo "==> CODESIGN_IDENTITY unset — leaving the bundle unsigned"
	echo "    Gatekeeper will quarantine this build if it is downloaded."
fi

# --- notarization -------------------------------------------------------------
if [[ -n "${NOTARY_PROFILE:-}" ]]; then
	if [[ -z "${CODESIGN_IDENTITY:-}" ]]; then
		echo "error: NOTARY_PROFILE set but CODESIGN_IDENTITY is not — notarization requires a signed bundle" >&2
		exit 1
	fi
	echo "==> Notarizing"
	ZIP="$DIST_DIR/$APP_NAME-notarize.zip"
	ditto -c -k --keepParent "$APP_DIR" "$ZIP"
	xcrun notarytool submit "$ZIP" --keychain-profile "$NOTARY_PROFILE" --wait
	xcrun stapler staple "$APP_DIR"
	rm -f "$ZIP"
else
	echo "==> NOTARY_PROFILE unset — skipping notarization"
fi

# --- optional DMG -------------------------------------------------------------
if [[ "${MAKE_DMG:-}" == "1" ]]; then
	echo "==> Building DMG"
	DMG="$DIST_DIR/${APP_NAME}_${VERSION}.dmg"
	rm -f "$DMG"
	STAGE="$(mktemp -d)"
	TMPDIRS+=("$STAGE")
	cp -R "$APP_DIR" "$STAGE/"
	ln -s /Applications "$STAGE/Applications"
	hdiutil create -volname "$APP_NAME" -srcfolder "$STAGE" -ov -format UDZO "$DMG" >/dev/null
	rm -rf "$STAGE"
	if [[ -n "${CODESIGN_IDENTITY:-}" ]]; then
		codesign --force --timestamp --sign "$CODESIGN_IDENTITY" "$DMG"
	fi
	echo "    $DMG"
fi

echo "==> Done: $APP_DIR"
