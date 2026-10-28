#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

VERSION=${1:-"2.1.0"}
ARCH="amd64"
PKG_NAME="yt-cartell-studio"
DEB_FILE="${PKG_NAME}_${VERSION}_${ARCH}.deb"

echo "=== Збірка .deb пакунка для $PKG_NAME v$VERSION ($ARCH) з повним набором іконок ==="

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
mkdir -p "$BUILD_DIR/usr/share/pixmaps"

# 3. Копіювання бінарника
install -m 755 YT-Cartell-Studio "$BUILD_DIR/usr/bin/yt-cartell-studio"

# 4. Встановлення іконок у всі системні директорії та розміри
if [ -f "internal/assets/icon256.png" ]; then
    echo "Генерація та розміщення іконок..."
    # Стандартний pixmaps (для Synaptic та головного меню Linux Mint)
    install -m 644 internal/assets/icon256.png "$BUILD_DIR/usr/share/pixmaps/yt-cartell.png"
    install -m 644 internal/assets/icon256.png "$BUILD_DIR/usr/share/pixmaps/yt-cartell-studio.png"

    # hicolor іконки різних розмірів
    for sz in 16 32 48 64 128 256; do
        mkdir -p "$BUILD_DIR/usr/share/icons/hicolor/${sz}x${sz}/apps"
        ffmpeg -y -v error -i internal/assets/icon256.png -vf "scale=${sz}:${sz}" -update 1 "$BUILD_DIR/usr/share/icons/hicolor/${sz}x${sz}/apps/yt-cartell.png"
        cp "$BUILD_DIR/usr/share/icons/hicolor/${sz}x${sz}/apps/yt-cartell.png" "$BUILD_DIR/usr/share/icons/hicolor/${sz}x${sz}/apps/yt-cartell-studio.png"
    done
fi

# 5. Створення файлу запуску .desktop для системи
cat << 'EOF' > "$BUILD_DIR/usr/share/applications/yt-cartell-studio.desktop"
[Desktop Entry]
Name=YT Cartell Studio
GenericName=AI Video Studio
Comment=Автономна десктопна AI-студія створення відео
Exec=/usr/bin/yt-cartell-studio %U
Icon=yt-cartell-studio
Terminal=false
Type=Application
Categories=AudioVideo;Video;AudioVideoEditing;
StartupNotify=true
StartupWMClass=yt-cartell-studio
Keywords=video;ai;youtube;shorts;montage;
EOF

# Копія для сумісності з коротким іменем
cp "$BUILD_DIR/usr/share/applications/yt-cartell-studio.desktop" "$BUILD_DIR/usr/share/applications/yt-cartell.desktop"

# 5.1 Створення AppStream / Metainfo для Менеджера програм Linux Mint / Ubuntu
mkdir -p "$BUILD_DIR/usr/share/metainfo"
cat << 'EOF' > "$BUILD_DIR/usr/share/metainfo/yt-cartell-studio.metainfo.xml"
<?xml version="1.0" encoding="UTF-8"?>
<component type="desktop-application">
  <id>yt-cartell-studio.desktop</id>
  <metadata_license>CC0-1.0</metadata_license>
  <project_license>MIT</project_license>
  <name>YT Cartell Studio</name>
  <summary>Автономна десктопна AI-студія створення відео</summary>
  <description>
    <p>Відкрита автономна десктопна студія, де історія стає відео: історія -> план -> кадри -> озвучка -> монтаж -> готовий .mp4.</p>
  </description>
  <launchable type="desktop-id">yt-cartell-studio.desktop</launchable>
  <icon type="stock">yt-cartell-studio</icon>
  <provides>
    <binary>yt-cartell-studio</binary>
  </provides>
  <categories>
    <category>AudioVideo</category>
    <category>Video</category>
    <category>AudioVideoEditing</category>
  </categories>
</component>
EOF

# 6. Створення postinst / postrm для оновлення системного кешу іконок
cat << 'EOF' > "$BUILD_DIR/DEBIAN/postinst"
#!/bin/sh
set -e
if [ -x /usr/bin/gtk-update-icon-cache ]; then
    gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor 2>/dev/null || true
fi
if [ -x /usr/bin/update-desktop-database ]; then
    update-desktop-database -q /usr/share/applications 2>/dev/null || true
fi
exit 0
EOF
chmod 755 "$BUILD_DIR/DEBIAN/postinst"

cat << 'EOF' > "$BUILD_DIR/DEBIAN/postrm"
#!/bin/sh
set -e
if [ -x /usr/bin/gtk-update-icon-cache ]; then
    gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor 2>/dev/null || true
fi
if [ -x /usr/bin/update-desktop-database ]; then
    update-desktop-database -q /usr/share/applications 2>/dev/null || true
fi
exit 0
EOF
chmod 755 "$BUILD_DIR/DEBIAN/postrm"

# 7. Створення керуючого файлу DEBIAN/control
INSTALLED_SIZE=$(du -sk "$BUILD_DIR/usr" | cut -f1)

cat << EOF > "$BUILD_DIR/DEBIAN/control"
Package: $PKG_NAME
Version: $VERSION
Architecture: $ARCH
Maintainer: slavikkoxan1998 <slavikkoxan1998@gmail.com>
Installed-Size: $INSTALLED_SIZE
Depends: ffmpeg, zenity, xclip, libwayland-client0, libx11-6, libxkbcommon0
Section: video
Priority: optional
Homepage: https://github.com/slavikkoxan1998/yt-cartell
Description: YT Cartell Studio - Автономна десктопна AI-студія створення відео
 Відкрита автономна десктопна студія, де історія стає відео:
 історія -> план -> кадри -> озвучка -> монтаж -> готовий .mp4.
EOF

# 8. Збирання пакунка
echo "Збирання пакунка dpkg-deb..."
dpkg-deb --build --root-owner-group "$BUILD_DIR" "$DEB_FILE"

echo "=== Готово! Створено пакунок: $DEB_FILE ==="
