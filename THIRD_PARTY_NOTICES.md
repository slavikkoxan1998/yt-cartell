# Third-party notices

YT Cartell Studio is MIT-licensed (see [`LICENSE`](LICENSE)). It builds on the work below; each part
keeps its own license. Full license texts ship next to the bundled files (paths given) or in the
Go module cache (`go env GOMODCACHE`) for the Go dependencies.

## Go modules (compiled into the program)

| Module | License |
|---|---|
| [Gio](https://gioui.org) — `gioui.org`, `gioui.org/x`, `gioui.org/shader` | Unlicense OR MIT (dual) |
| [oto](https://github.com/ebitengine/oto) — `github.com/ebitengine/oto/v3` (audio playback) | Apache License 2.0 |
| [purego](https://github.com/ebitengine/purego) — `github.com/ebitengine/purego` (oto's dependency) | Apache License 2.0 |
| `golang.org/x/image`, `golang.org/x/net`, `golang.org/x/sys`, `golang.org/x/text`, `golang.org/x/exp` | BSD 3-Clause (The Go Authors) |
| [go-text/typesetting](https://github.com/go-text/typesetting) (Gio's dependency) | Unlicense OR BSD 3-Clause |
| [go-ole](https://github.com/go-ole/go-ole) (Gio's dependency, Windows) | MIT |
| [godbus/dbus](https://github.com/godbus/dbus) (Gio's dependency, Linux) | BSD 2-Clause |
| [jfreymuth/pulse](https://github.com/jfreymuth/pulse) (oto's dependency, Linux) | MIT |
| `git.wow.st/gmp/jni` (Gio's dependency, Android only — not built for desktop) | BSD 3-Clause |

The exact versions are in [`go.mod`](go.mod) / [`go.sum`](go.sum).

## Bundled assets

| What | Where | License |
|---|---|---|
| **huashu-art-motion** animation engine by alchaincyf (花叔 · 花生), [github.com/alchaincyf/huashu-art-motion](https://github.com/alchaincyf/huashu-art-motion) — vendored, with the changes listed in its README | `internal/codeanim/engine/` | MIT — [`internal/codeanim/engine/LICENSE`](internal/codeanim/engine/LICENSE) |
| Fonts of the animation engine (Inter, EB Garamond, Noto Sans/Serif SC, LXGW WenKai, Press Start 2P, Roboto Condensed, Computer Modern, Fira Sans, Montserrat, Caveat …) | `internal/codeanim/engine/lib/fonts/` | SIL Open Font License 1.1 — [`LICENSES.md`](internal/codeanim/engine/lib/fonts/LICENSES.md), [`OFL.txt`](internal/codeanim/engine/lib/fonts/OFL.txt), `cyr/OFL-*.txt` |
| **Fira Sans** (the UI font) | `internal/gui/fonts/` | SIL Open Font License 1.1 — `OFL-FiraSans.txt` |
| Cover fonts: Alegreya, Dela Gothic One, Montserrat, Oswald, Pacifico, Rubik, Russo One, Unbounded | `internal/cover/fonts/` | SIL Open Font License 1.1 — `OFL-*.txt` next to each font |
| **Lucide** icons | `internal/gui/icons_data.go` | ISC — [`internal/gui/icons/LICENSE`](internal/gui/icons/LICENSE) |
| **Natural Earth** 1:50m cultural and physical vectors (countries, admin-1, lakes, rivers of the map animation), [naturalearthdata.com](https://www.naturalearthdata.com) | `internal/codeanim/grammars/map/world.js` | Public domain |

The app icon, logo and the 72 style preview pictures (`internal/assets/`) are part of this project (MIT).

## Downloaded at run time, not bundled

These programs are not part of the source or the release builds. The studio downloads them only when
the user presses the install button (pinned versions, SHA-256 checked) or uses a copy already on the
computer; each is used under its own license:

| Program | Source | License |
|---|---|---|
| **FFmpeg** / ffprobe | Windows: gyan.dev builds; macOS: Homebrew or ffmpeg.martin-riedl.de | LGPL / GPL (depending on the build) |
| **whisper.cpp** + a Whisper model (local speech recognition) | ggml-org/whisper.cpp releases, Hugging Face; macOS: Homebrew | MIT (whisper.cpp), MIT (OpenAI Whisper models) |
| **chrome-headless-shell** (renders code animation when no system Chrome / Edge is found) | Chrome for Testing (Google) | BSD-style (Chromium) and its bundled notices |

External AI services (text, image, video, voice, speech recognition, Telegram) are called with the
user's own keys and accounts under those services' terms.
