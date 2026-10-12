package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ToolsDir is where the studio installs helper programs itself (FFmpeg,
// yt-dlp …), one folder per program: <ToolsDir>/ffmpeg/bin/ffmpeg(.exe).
// Windows: %LOCALAPPDATA%\YT Cartell Studio (programs never roam with the
// profile); macOS and Linux: the settings folder (Dir) —
// ~/Library/Application Support/YT Cartell Studio on a Mac. The folder is
// not created here.
func ToolsDir() string {
	if runtime.GOOS == "windows" {
		if v := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); v != "" {
			return filepath.Join(v, "YT Cartell Studio")
		}
	}
	return Dir()
}
