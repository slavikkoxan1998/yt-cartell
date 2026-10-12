// Package codeanim renders code-driven animation clips: a JSON spec
// ({grammar, duration, fps, width, height, data, cues}) → an exact-length
// video, using the MIT-licensed huashu-art-motion engine
// (github.com/alchaincyf/huashu-art-motion, scripts/engine; vendored and
// embedded in engine/, see engine/README.md for attribution, licences and
// the changes).
//
// How a render works (a Go port of the upstream render.py --spec):
//
//   - An in-process HTTP server on 127.0.0.1 serves the embedded engine
//     under a random per-job token, plus only the image files the spec names
//     (cues[].image, data.image; relative paths resolve against
//     Options.AssetsDir; http(s) images are refused).
//   - A headless Chromium-family browser (Edge on Windows, Chrome/Edge/
//     Chromium elsewhere, or the pinned chrome-headless-shell the user can
//     install with InstallBrowser) is driven over the DevTools Protocol by a
//     small built-in client (cdp.go; no third-party module). One browser
//     process is shared by the app; every page gets its own browser context
//     so pages render in parallel; Concurrency pages at most.
//   - clip.html boots with window.CLIP_SPEC injected; frame i is drawn by
//     window.renderFrame(i/fps) — never the wall clock — and its canvas
//     pixels (getImageData, unpremultiplied RGBA) are POSTed back to the
//     server and piped raw into ffmpeg. Frames = round(duration×fps) with
//     Python's half-even rounding, exactly like upstream.
//   - Output: H.264 yuv420p BT.709 (a keyframe every second) in .mp4, or
//     with Options.Alpha ProRes 4444 (yuva444p10le) in a QuickTime .mov.
//   - Any uncaught exception or console.error in the page fails the render
//     with the exact JS message (*PageError); warnings are logged.
//
// Grammars lists the 8 upstream grammars with Ukrainian metadata, a JSON
// Schema and an example spec each; RegisterGrammar adds more (their clip
// file is served as clips/<id>.js and has the whole engine library).
//
// The engine was changed for Ukrainian text: Cyrillic overlay faces under
// the grammar font families (lib/fonts.js) and word-aware wrapping of
// Cyrillic in TY.wrap (lib/typo.js).
package codeanim
