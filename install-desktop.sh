#!/usr/bin/env bash
# Встановлює іконку та пункт запуску .desktop у систему для локальної збірки YT Cartell Studio.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_PATH="$SCRIPT_DIR/YT-Cartell-Studio"

if [ ! -f "$BIN_PATH" ]; then
    echo "Бінарник $BIN_PATH не знайдено. Спочатку запустіть ./build.sh"
    exit 1
fi

echo "=== Встановлення іконок та ярлика YT Cartell Studio ==="

# Визначаємо цільові каталоги (локальні для користувача)
TARGET_APPS="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
TARGET_ICONS="${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor"
TARGET_PIXMAPS="${XDG_DATA_HOME:-$HOME/.local/share}/pixmaps"

mkdir -p "$TARGET_APPS"
mkdir -p "$TARGET_PIXMAPS"
mkdir -p "$TARGET_ICONS/256x256/apps"

# Копіювання головної іконки
cp "$SCRIPT_DIR/internal/assets/icon256.png" "$TARGET_ICONS/256x256/apps/yt-cartell-studio.png"
cp "$SCRIPT_DIR/internal/assets/icon256.png" "$TARGET_ICONS/256x256/apps/yt-cartell.png"
cp "$SCRIPT_DIR/internal/assets/icon256.png" "$TARGET_PIXMAPS/yt-cartell-studio.png"
cp "$SCRIPT_DIR/internal/assets/icon256.png" "$TARGET_PIXMAPS/yt-cartell.png"

# Додаткові розміри, якщо є ffmpeg
if command -v ffmpeg >/dev/null 2>&1; then
    for sz in 16 32 48 64 128; do
        mkdir -p "$TARGET_ICONS/${sz}x${sz}/apps"
        ffmpeg -y -v error -i "$SCRIPT_DIR/internal/assets/icon256.png" -vf "scale=${sz}:${sz}" -update 1 "$TARGET_ICONS/${sz}x${sz}/apps/yt-cartell-studio.png" || true
        cp "$TARGET_ICONS/${sz}x${sz}/apps/yt-cartell-studio.png" "$TARGET_ICONS/${sz}x${sz}/apps/yt-cartell.png" || true
    done
fi

# Створення ярлика запуску
cat << EOF > "$TARGET_APPS/yt-cartell-studio.desktop"
[Desktop Entry]
Name=YT Cartell Studio
GenericName=AI Video Studio
Comment=Автономна десктопна AI-студія створення відео
Exec=$BIN_PATH %U
Icon=yt-cartell-studio
Terminal=false
Type=Application
Categories=AudioVideo;Video;AudioVideoEditing;
StartupNotify=true
StartupWMClass=yt-cartell-studio
Keywords=video;ai;youtube;shorts;montage;
EOF

chmod +x "$TARGET_APPS/yt-cartell-studio.desktop"
cp "$TARGET_APPS/yt-cartell-studio.desktop" "$TARGET_APPS/yt-cartell.desktop"

# Оновлення системного кешу
if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database "$TARGET_APPS" 2>/dev/null || true
fi
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
    gtk-update-icon-cache -q -t -f "$TARGET_ICONS" 2>/dev/null || true
fi

echo "=== Готово! Іконку та ярлик встановлено в меню Linux Mint. ==="
