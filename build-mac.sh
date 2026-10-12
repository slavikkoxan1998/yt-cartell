#!/usr/bin/env bash
# Builds the macOS release of YT Cartell Studio: "YT Cartell Studio.app"
# for Apple Silicon (arm64) and Intel (x64) — plus a universal one when both
# build — zipped with ditto, their SHA-256 lines in SHA256SUMS.txt, and
# optionally uploaded to the GitHub release v<version> (the app's updater
# reads the zips and SHA256SUMS.txt from there). Run it on a Mac; Gio needs
# cgo there, so it cannot be cross-compiled from Windows. docs/BUILD-MAC.md
# has the step-by-step guide (Ukrainian).
#
#   bash build-mac.sh                       build + zip into dist/mac
#   bash build-mac.sh --upload              … and upload to the release v<version>
#   bash build-mac.sh --version 2.0.1       another version (default: internal/version/version.go)
#   bash build-mac.sh --arch arm64          one architecture only (arm64 | amd64 | both)
#   bash build-mac.sh --repo me/studio      another repository (default gitkalenyuk/yt-cartell-studio)
#   bash build-mac.sh --out /tmp/mac        another output folder
#
# Needs: Go 1.27+, Xcode Command Line Tools (xcode-select --install: clang,
# lipo, codesign, iconutil), and for --upload the GitHub CLI signed in
# (gh auth login). The release must exist already: publish the Windows
# build first with release.ps1 (SHA256SUMS.txt is merged, not replaced).
set -euo pipefail
cd "$(dirname "$0")"

APP_NAME="YT Cartell Studio"
BUNDLE_ID="com.ytcartell.studio"
MIN_MACOS="13.0" # Go 1.27 runs on macOS 13 Ventura and later (its linker targets 13.0)
REPO="gitkalenyuk/yt-cartell-studio"
VERSION=""
ARCHES="both"
UPLOAD=0
OUT="dist/mac" # git-ignored (/dist/)

die() {
	echo "build-mac: $*" >&2
	exit 1
}
warn() { echo "build-mac: $*" >&2; }

while [ $# -gt 0 ]; do
	case "$1" in
	--version) VERSION="${2:-}"; shift 2 ;;
	--version=*) VERSION="${1#*=}"; shift ;;
	--arch) ARCHES="${2:-}"; shift 2 ;;
	--arch=*) ARCHES="${1#*=}"; shift ;;
	--repo) REPO="${2:-}"; shift 2 ;;
	--repo=*) REPO="${1#*=}"; shift ;;
	--out) OUT="${2:-}"; shift 2 ;;
	--out=*) OUT="${1#*=}"; shift ;;
	--upload) UPLOAD=1; shift ;;
	-h | --help) sed -n '2,24p' "$0"; exit 0 ;;
	*) die "unknown option $1 (see --help)" ;;
	esac
done

# ---- checks ------------------------------------------------------------------------------------

[ "$(uname -s)" = "Darwin" ] || die "run this on a Mac (Gio needs cgo there)."
command -v go >/dev/null 2>&1 || die "Go not found: brew install go (or https://go.dev/dl/)."
gover=$(go env GOVERSION | sed 's/^go//')
gomajor=${gover%%.*}
gominor=$(echo "$gover" | cut -d. -f2)
if [ "$gomajor" -lt 1 ] || { [ "$gomajor" -eq 1 ] && [ "${gominor%%[!0-9]*}" -lt 27 ]; }; then
	die "Go $gover is too old: Go 1.27+ is needed (brew upgrade go)."
fi
xcode-select -p >/dev/null 2>&1 || die "Xcode Command Line Tools are missing: xcode-select --install"
for tool in clang sips iconutil ditto shasum; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool not found (Xcode Command Line Tools: xcode-select --install)."
done
if [ "$UPLOAD" = 1 ]; then
	command -v gh >/dev/null 2>&1 || die "GitHub CLI not found: brew install gh && gh auth login"
	gh auth status --hostname github.com >/dev/null 2>&1 || die "gh is not signed in: gh auth login"
fi

if [ -z "$VERSION" ]; then
	VERSION=$(sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/version/version.go | head -n 1)
fi
VERSION=${VERSION#v}
echo "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || die "version '$VERSION' is not X.Y.Z"
TAG="v$VERSION"

case "$ARCHES" in
both) ARCH_LIST="arm64 amd64" ;;
arm64 | amd64) ARCH_LIST="$ARCHES" ;;
*) die "--arch must be arm64, amd64 or both" ;;
esac

mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/ytc-mac.XXXXXX")
trap 'rm -rf "$WORK"' EXIT

COMMIT=$(git rev-parse --short HEAD 2>/dev/null || true)
LDFLAGS="-s -w -X ytcartell/internal/version.Version=$VERSION -X ytcartell/internal/version.BuildDate=$(date -u +%Y-%m-%d)"
[ -n "$COMMIT" ] && LDFLAGS="$LDFLAGS -X ytcartell/internal/version.Commit=$COMMIT"
# The built-in updater reads the releases of the repository it is published to (forks: --repo me/studio).
LDFLAGS="$LDFLAGS -X ytcartell/internal/update.Owner=${REPO%%/*} -X ytcartell/internal/update.Repo=${REPO#*/}"

echo "YT Cartell Studio $VERSION for macOS ($ARCH_LIST) -> $OUT"

# ---- binaries ----------------------------------------------------------------------------------

export MACOSX_DEPLOYMENT_TARGET="$MIN_MACOS"
BUILT=""
for arch in $ARCH_LIST; do
	carch=$arch
	[ "$arch" = "amd64" ] && carch=x86_64
	bin="$WORK/bin-$arch/$APP_NAME"
	mkdir -p "$WORK/bin-$arch"
	echo "Building $arch..."
	if CGO_ENABLED=1 GOOS=darwin GOARCH=$arch CC="clang -arch $carch" \
		CGO_CFLAGS="-mmacosx-version-min=$MIN_MACOS" CGO_LDFLAGS="-mmacosx-version-min=$MIN_MACOS" \
		go build -trimpath -ldflags "$LDFLAGS" -o "$bin" .; then
		BUILT="$BUILT $arch"
	else
		warn "the $arch build failed — skipped."
	fi
done
BUILT=${BUILT# }
[ -n "$BUILT" ] || die "nothing built."
if [ "$BUILT" = "arm64 amd64" ] && command -v lipo >/dev/null 2>&1; then
	mkdir -p "$WORK/bin-universal"
	if lipo -create -output "$WORK/bin-universal/$APP_NAME" "$WORK/bin-arm64/$APP_NAME" "$WORK/bin-amd64/$APP_NAME"; then
		BUILT="$BUILT universal"
	else
		warn "lipo failed — no universal build."
	fi
fi

# ---- icon: internal/assets/icon256.png (256×264) → square → AppIcon.icns ----------------------

cat >"$WORK/square.go" <<'GOEOF'
// square pads a PNG to a transparent square (macOS icons must be square).
package main

import (
	"image"
	"image/draw"
	"image/png"
	"os"
)

func main() {
	f, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	src, err := png.Decode(f)
	if err != nil {
		panic(err)
	}
	b := src.Bounds()
	n := max(b.Dx(), b.Dy())
	dst := image.NewNRGBA(image.Rect(0, 0, n, n))
	off := image.Pt((n-b.Dx())/2, (n-b.Dy())/2)
	draw.Draw(dst, b.Sub(b.Min).Add(off), src, b.Min, draw.Src)
	out, err := os.Create(os.Args[2])
	if err != nil {
		panic(err)
	}
	if err := png.Encode(out, dst); err != nil {
		panic(err)
	}
	if err := out.Close(); err != nil {
		panic(err)
	}
}
GOEOF
(cd "$WORK" && GOOS= GOARCH= CGO_ENABLED=0 GOFLAGS= go run square.go "$OLDPWD/internal/assets/icon256.png" "$WORK/icon-square.png")
ICONSET="$WORK/AppIcon.iconset"
mkdir -p "$ICONSET"
for spec in 16:icon_16x16 32:icon_16x16@2x 32:icon_32x32 64:icon_32x32@2x 128:icon_128x128 \
	256:icon_128x128@2x 256:icon_256x256 512:icon_256x256@2x 512:icon_512x512; do
	px=${spec%%:*}
	name=${spec#*:}
	sips -z "$px" "$px" "$WORK/icon-square.png" --out "$ICONSET/$name.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$WORK/AppIcon.icns"

# ---- bundles and zips --------------------------------------------------------------------------

make_app() { # make_app <binary> <folder>: <folder>/YT Cartell Studio.app
	local app="$2/$APP_NAME.app"
	rm -rf "$app"
	mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
	cp "$1" "$app/Contents/MacOS/$APP_NAME"
	chmod 755 "$app/Contents/MacOS/$APP_NAME"
	cp "$WORK/AppIcon.icns" "$app/Contents/Resources/AppIcon.icns"
	printf 'APPL????' >"$app/Contents/PkgInfo"
	cat >"$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key><string>uk</string>
	<key>CFBundleDisplayName</key><string>$APP_NAME</string>
	<key>CFBundleExecutable</key><string>$APP_NAME</string>
	<key>CFBundleIconFile</key><string>AppIcon</string>
	<key>CFBundleIdentifier</key><string>$BUNDLE_ID</string>
	<key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
	<key>CFBundleName</key><string>$APP_NAME</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>$VERSION</string>
	<key>CFBundleVersion</key><string>$VERSION</string>
	<key>LSApplicationCategoryType</key><string>public.app-category.video</string>
	<key>LSMinimumSystemVersion</key><string>$MIN_MACOS</string>
	<key>NSHighResolutionCapable</key><true/>
	<key>NSHumanReadableCopyright</key><string>YT CARTELL</string>
	<key>NSMicrophoneUsageDescription</key><string>Студія слухає мікрофон лише тоді, коли ти натискаєш кнопку диктування в «Новій історії», — щоб перетворити твою мову на текст.</string>
</dict>
</plist>
PLIST
	plutil -lint "$app/Contents/Info.plist" >/dev/null 2>&1 || warn "Info.plist did not pass plutil -lint"
	# Ad-hoc signature: not notarized (first launch: right-click → Open),
	# but a consistent bundle signature that Apple Silicon requires anyway.
	# Dictation records through a child ffmpeg; macOS asks for the microphone
	# on behalf of this bundle (NSMicrophoneUsageDescription above). Signing
	# with the hardened runtime (--options runtime) would also need the
	# com.apple.security.device.audio-input entitlement.
	if command -v codesign >/dev/null 2>&1; then
		codesign --force --deep --sign - "$app" >/dev/null 2>&1 || warn "codesign (ad-hoc) failed for $app"
	fi
}

ZIPS=""
for variant in $BUILT; do
	case "$variant" in
	arm64) suffix=arm64 ;;
	amd64) suffix=x64 ;;
	universal) suffix=universal ;;
	esac
	dir="$WORK/app-$variant"
	mkdir -p "$dir"
	make_app "$WORK/bin-$variant/$APP_NAME" "$dir"
	zip="YT-Cartell-Studio-$VERSION-macos-$suffix.zip"
	rm -f "$OUT/$zip"
	(cd "$dir" && ditto -c -k --keepParent "$APP_NAME.app" "$OUT/$zip")
	ZIPS="$ZIPS $zip"
	echo "  $zip  $(du -h "$OUT/$zip" | cut -f1)"
done

# ---- SHA256SUMS.txt (merged with the release's when uploading) ----------------------------------

SUMS="$OUT/SHA256SUMS.txt"
if [ "$UPLOAD" = 1 ]; then
	gh release view "$TAG" -R "$REPO" >/dev/null 2>&1 ||
		die "the release $TAG does not exist in $REPO yet — publish the Windows build first: .\\release.ps1 -Notes <file>"
	rm -f "$SUMS"
	gh release download "$TAG" -R "$REPO" -p SHA256SUMS.txt -D "$OUT" --clobber >/dev/null 2>&1 || true
fi
touch "$SUMS"
for zip in $ZIPS; do
	awk -v n="$zip" '$2 != n && $2 != "*" n' "$SUMS" >"$SUMS.tmp" && mv "$SUMS.tmp" "$SUMS"
	(cd "$OUT" && shasum -a 256 "$zip") >>"$SUMS"
done
sort -k2 "$SUMS" -o "$SUMS"
echo "SHA256SUMS.txt:"
sed 's/^/  /' "$SUMS"

if [ "$UPLOAD" = 1 ]; then
	echo "Uploading to $REPO $TAG..."
	files=()
	for zip in $ZIPS; do files+=("$OUT/$zip"); done
	gh release upload "$TAG" -R "$REPO" --clobber "${files[@]}" "$SUMS"
	echo "Done: https://github.com/$REPO/releases/tag/$TAG"
else
	echo "Built into $OUT (not uploaded; add --upload to publish)."
fi
