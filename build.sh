#!/usr/bin/env sh
# Builds YT Cartell Studio for the current OS.
#
#   ./build.sh           the version written in internal/version/version.go
#   ./build.sh 2.0.1     another version (shown in Settings, compared with the
#                        GitHub releases by the updater)
#
#   Optional environment: UPDATE_OWNER / UPDATE_REPO point the updater at
#   another GitHub repository (default gitkalenyuk/yt-cartell-studio).
#
#   Windows (Git Bash/MSYS): no cgo, GUI subsystem → YT-Cartell-Studio.exe
#     (same as build.bat; icon from rsrc_windows_<arch>.syso via go-winres).
#
#   macOS / Linux: Gio needs cgo there, so build natively on that OS
#   (no cross-compiling from Windows).
#     macOS: Xcode Command Line Tools (xcode-select --install). For the
#       release (.app bundles, zips, SHA256SUMS.txt, upload) use build-mac.sh.
#     Linux (Debian/Ubuntu names; list taken from Gio's #cgo pkg-config lines):
#       sudo apt install gcc pkg-config libwayland-dev libx11-dev \
#         libx11-xcb-dev libxkbcommon-dev libxkbcommon-x11-dev \
#         libgles2-mesa-dev libegl1-mesa-dev libffi-dev libxcursor-dev \
#         libxfixes-dev libvulkan-dev
#     Audio (oto) needs no -dev package: it talks to PulseAudio/PipeWire in
#     pure Go and falls back to libasound.so.2 loaded at runtime.
#     File dialogs at runtime: zenity or kdialog.
set -eu
cd "$(dirname "$0")"

command -v go >/dev/null 2>&1 || {
	echo "Go not found. Install Go 1.27+ from https://go.dev/dl/" >&2
	exit 1
}

version=${1:-$(sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' internal/version/version.go | head -n 1)}
version=${version:-0.0.0}
ldflags="-X ytcartell/internal/version.Version=$version -X ytcartell/internal/version.BuildDate=$(date -u +%Y-%m-%d)"
if commit=$(git rev-parse --short HEAD 2>/dev/null); then
	ldflags="$ldflags -X ytcartell/internal/version.Commit=$commit"
fi
[ -n "${UPDATE_OWNER:-}" ] && ldflags="$ldflags -X ytcartell/internal/update.Owner=$UPDATE_OWNER"
[ -n "${UPDATE_REPO:-}" ] && ldflags="$ldflags -X ytcartell/internal/update.Repo=$UPDATE_REPO"

echo "Building YT Cartell Studio $version..."
case "$(uname -s)" in
MINGW* | MSYS* | CYGWIN*)
	arch=$(go env GOARCH)
	if [ ! -f "rsrc_windows_$arch.syso" ]; then
		echo "Generating the exe icon..."
		go run github.com/tc-hib/go-winres@v0.3.3 simply \
			--icon internal/assets/icon256.png --arch "$arch" --manifest none \
			--product-name "YT Cartell Studio" --file-description "YT Cartell Studio" \
			--original-filename YT-Cartell-Studio.exe \
			--product-version "$version" --file-version "$version" ||
			echo "Icon skipped: go-winres unavailable. The exe will use the default icon."
	fi
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui $ldflags" -o YT-Cartell-Studio.exe .
	echo "Built: $(pwd)/YT-Cartell-Studio.exe (version $version)"
	;;
*)
	command -v cc >/dev/null 2>&1 || {
		echo "No C compiler: Gio needs cgo on $(uname -s). See the notes at the top of build.sh." >&2
		exit 1
	}
	CGO_ENABLED=1 go build -trimpath -ldflags "-s -w $ldflags" -o YT-Cartell-Studio .
	echo "Built: $(pwd)/YT-Cartell-Studio (version $version)"
	;;
esac
