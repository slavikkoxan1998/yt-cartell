#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

VERSION=${1:-"2.1.0"}
ARCH="amd64"
PKG_NAME="yt-cartell-studio"
DEB_FILE="${PKG_NAME}_${VERSION}_${ARCH}.deb"

echo "=== Збірка .deb пакунка для $PKG_NAME v$VERSION ($ARCH) ==="

# 1. Збірка бінарника, якщо його немає
if [ ! -f "YT-Cartell-Studio" ]; then
    echo "Компіляція бінарника..."
    ./build.sh "$VERSION"
fi

# 2. Підготовка тимчасової структури пакунка у /tmp (за правилом чистоти)
BUILD_DIR=$(mktemp -d /tmp/deb-build-XXXXXX)
trap 'rm -rf "$BUILD_DIR"' EXIT

echo "Підготовка структури пакунка..."
mkdir -p "$BUILD_DIR/DEBIAN"
mkdir -p "$BUILD_DIR/usr/bin"
mkdir -p "$BUILD_DIR/usr/share/applications"
mkdir -p "$BUILD_DIR/usr/share/icons/hicolor/256x256/apps"

# 3. Копіювання бінарника та іконки
install -m 755 YT-Cartell-Studio "$BUILD_DIR/usr/bin/yt-cartell-studio"

if [ -f "internal/assets/icon256.png" ]; then
    install -m 644 internal/assets/icon256.png "$BUILD_DIR/usr/share/icons/hicolor/256x256/apps/yt-cartell.png"
fi

# 4. Створення файлу запуску .desktop для системи
cat << 'EOF' > "$BUILD_DIR/usr/share/applications/yt-cartell.desktop"
[Desktop Entry]
Name=YT Cartell Studio
GenericName=AI Video Studio
Comment=Автономна десктопна AI-студія створення відео
Exec=/usr/bin/yt-cartell-studio
Icon=yt-cartell
Terminal=false
Type=Application
Categories=AudioVideo;Video;AudioVideoEditing;
StartupNotify=true
Keywords=video;ai;youtube;shorts;montage;
EOF

# 5. Створення керуючого файлу DEBIAN/control
INSTALLED_SIZE=$(du -sk "$BUILD_DIR/usr" | cut -f1)

cat << EOF > "$BUILD_DIR/DEBIAN/control"
Package: $PKG_NAME
Version: $VERSION
Architecture: $ARCH
Maintainer: slavikkoxan1998 <slavikkoxan1998@gmail.com>
Installed-Size: $INSTALLED_SIZE
Depends: ffmpeg, zenity, libwayland-client0, libx11-6, libxkbcommon0
Section: video
Priority: optional
Homepage: https://github.com/slavikkoxan1998/yt-cartell
Description: YT Cartell Studio - Автономна десктопна AI-студія створення відео
 Відкрита автономна десктопна студія, де історія стає відео:
 історія -> план -> кадри -> озвучка -> монтаж -> готовий .mp4.
EOF

# 6. Збирання пакунка
echo "Збирання пакунка dpkg-deb..."
dpkg-deb --build --root-owner-group "$BUILD_DIR" "$DEB_FILE"

echo "=== Готово! Створено пакунок: $DEB_FILE ==="
