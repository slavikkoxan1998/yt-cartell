// Package update keeps YT Cartell Studio current from its public GitHub
// repository (Owner/Repo): it asks for the latest release, downloads the
// build for this computer, verifies it against the release's SHA256SUMS.txt
// and swaps it in place of the running app — only ever after the user
// clicked (the UI decides; nothing here installs on its own).
//
//	c := update.New(config.Dir())               // ETag cache next to config.json
//	rel, err := c.Latest(ctx)                   // GET /repos/{owner}/{repo}/releases/latest
//	if rel.Newer(version.String()) {
//	    path, err := c.Download(ctx, rel, progress) // temp folder, resumable, SHA-256 checked
//	    restart, err := c.Apply(path)               // swap files; the old build stays as *.old
//	    // close the window, flush the engine, then:
//	    restart(version.String())                   // starts the new build with --updated-from=<old>
//	}
//	go update.Cleanup("", version.String())      // at start-up: removes *.old, stale downloads
//
// Release channel: tags "vX.Y.Z" (drafts and pre-releases are ignored) with
// the assets YT-Cartell-Studio-<ver>-windows-x64.exe,
// YT-Cartell-Studio-<ver>-macos-arm64.zip / -macos-x64.zip (zipped .app
// bundles; -macos-universal.zip is accepted as a fallback) and
// SHA256SUMS.txt ("<hex>  <file>" lines). A build without a checksum line
// is never installed.
//
// Install: Windows renames the running exe to <exe>.old (allowed while it
// runs), moves the new exe into its place and starts it; macOS unpacks the
// zip next to the running .app, swaps the bundles (the old one becomes
// "<name>.old.app"), clears the quarantine flag and relaunches with
// `open -n`. Elsewhere (a bare binary, Linux) Apply returns KindManual and
// the UI opens the release page instead.
//
// Tests never reach GitHub: the Client takes its API root and HTTP client
// as fields (httptest), and the app's headless renders use Fake.
package update

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Owner and Repo name the public GitHub repository with the release builds.
// A fork that publishes its own releases overrides them at link time (the
// build scripts take UPDATE_OWNER / UPDATE_REPO from the environment):
//
//	-ldflags "-X ytcartell/internal/update.Owner=me -X ytcartell/internal/update.Repo=studio"
var (
	Owner = "gitkalenyuk"
	Repo  = "yt-cartell-studio"
)

// AssetPrefix starts the name of every build asset.
const AssetPrefix = "YT-Cartell-Studio-"

// SumsName is the checksum file of a release.
const SumsName = "SHA256SUMS.txt"

// UpdatedFlag is passed to the new build after an update: "--updated-from=2.0.0".
const UpdatedFlag = "--updated-from"

// ReleasesURL is the repository's releases page.
func ReleasesURL() string { return "https://github.com/" + Owner + "/" + Repo + "/releases" }

// Release is one published version.
type Release struct {
	Tag        string // "v2.1.0"
	Version    string // "2.1.0": the tag without its "v"
	Title      string // "YT Cartell Studio v2.1.0 — …"
	Notes      string // the release body (Markdown; see ParseNotes)
	URL        string // the release page
	Published  time.Time
	Draft      bool
	Prerelease bool
	Assets     []Asset
}

// Asset is one downloadable file of a release.
type Asset struct {
	Name string
	URL  string // browser_download_url
	Size int64  // bytes (0 = unknown)
}

// Newer reports whether r is a regular release newer than the version
// current ("2.0.0"). Drafts, pre-releases (also a "-beta" version) and tags
// that are not versions never are.
func (r Release) Newer(current string) bool {
	if r.Draft || r.Prerelease {
		return false
	}
	v, ok := ParseVersion(r.Version)
	if !ok || v.Pre != "" {
		return false
	}
	cur, ok := ParseVersion(current)
	if !ok {
		cur = Version{}
	}
	return v.Compare(cur) > 0
}

// PageURL is the release page (the releases list when unknown).
func (r Release) PageURL() string {
	if strings.HasPrefix(r.URL, "https://") {
		return r.URL
	}
	return ReleasesURL()
}

// platformSuffixes lists the asset name endings for a platform, best first.
func platformSuffixes(goos, goarch string) []string {
	switch goos {
	case "windows":
		switch goarch {
		case "amd64":
			return []string{"-windows-x64.exe", "-windows-amd64.exe"}
		case "arm64":
			// Windows on Arm runs the x64 build emulated.
			return []string{"-windows-arm64.exe", "-windows-x64.exe", "-windows-amd64.exe"}
		}
	case "darwin":
		switch goarch {
		case "arm64":
			return []string{"-macos-arm64.zip", "-macos-universal.zip"}
		case "amd64":
			return []string{"-macos-x64.zip", "-macos-amd64.zip", "-macos-universal.zip"}
		}
	}
	return nil
}

// AssetFor picks the build of r for goos/goarch (runtime.GOOS, runtime.GOARCH).
func (r Release) AssetFor(goos, goarch string) (Asset, bool) {
	for _, suf := range platformSuffixes(goos, goarch) {
		for _, a := range r.Assets {
			n := strings.ToLower(a.Name)
			if strings.HasPrefix(n, strings.ToLower(AssetPrefix)) && strings.HasSuffix(n, suf) {
				return a, true
			}
		}
	}
	return Asset{}, false
}

// Checksums is the release's SHA256SUMS.txt.
func (r Release) Checksums() (Asset, bool) {
	for _, a := range r.Assets {
		if strings.EqualFold(a.Name, SumsName) {
			return a, true
		}
	}
	return Asset{}, false
}

// Updater is what the app needs from the release channel: *Client is the
// real one; tests and headless renders use Fake.
type Updater interface {
	// Latest returns the newest published release.
	Latest(ctx context.Context) (Release, error)
	// Download fetches and verifies the build of rel for this computer and
	// returns its path; progress (optional) gets bytes done and the total.
	Download(ctx context.Context, rel Release, progress func(done, total int64)) (string, error)
	// Apply installs a verified download in place of the running app and
	// returns what starts it once the app has closed.
	Apply(staged string) (Restart, error)
}

// Restart starts the freshly installed build with --updated-from=from. Call
// it after the window closed and the engine flushed, then exit.
type Restart func(from string) error

// UpdatedFrom finds --updated-from=<version> (or "--updated-from <version>")
// among the program's arguments.
func UpdatedFrom(args []string) (string, bool) {
	for i, a := range args {
		a = strings.TrimSpace(a)
		for _, f := range []string{UpdatedFlag, UpdatedFlag[1:]} { // --flag or -flag
			if v, ok := strings.CutPrefix(a, f+"="); ok {
				return strings.TrimSpace(v), v != ""
			}
			if a == f && i+1 < len(args) {
				return strings.TrimSpace(args[i+1]), true
			}
		}
	}
	return "", false
}

// ---- versions ------------------------------------------------------------------------------

// Version is a semantic version (https://semver.org); build metadata is dropped.
type Version struct {
	Major, Minor, Patch int
	Pre                 string // "beta.1" ("" = a release)
}

// ParseVersion reads "v2.1.0", "2.1", "2.1.0-beta.2+build5".
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if core == "" || len(parts) > 3 {
		return Version{}, false
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || len(p) > 9 {
			return Version{}, false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return Version{}, false
			}
		}
		n[i], _ = strconv.Atoi(p)
	}
	return Version{Major: n[0], Minor: n[1], Patch: n[2], Pre: pre}, true
}

// String is "2.1.0" or "2.1.0-beta.1".
func (v Version) String() string {
	s := strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare returns -1, 0 or +1 (semver precedence: a pre-release sorts before its release).
func (v Version) Compare(o Version) int {
	for _, d := range [...][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if d[0] != d[1] {
			return cmpInt(d[0], d[1])
		}
	}
	switch {
	case v.Pre == o.Pre:
		return 0
	case v.Pre == "":
		return 1
	case o.Pre == "":
		return -1
	}
	a, b := strings.Split(v.Pre, "."), strings.Split(o.Pre, ".")
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] == b[i] {
			continue
		}
		na, ea := strconv.Atoi(a[i])
		nb, eb := strconv.Atoi(b[i])
		switch {
		case ea == nil && eb == nil:
			return cmpInt(na, nb)
		case ea == nil: // numeric identifiers sort first
			return -1
		case eb == nil:
			return 1
		case a[i] < b[i]:
			return -1
		default:
			return 1
		}
	}
	return cmpInt(len(a), len(b))
}

// Newer reports whether version latest is newer than current (an
// unreadable current counts as 0.0.0; an unreadable latest is never newer).
func Newer(latest, current string) bool {
	l, ok := ParseVersion(latest)
	if !ok {
		return false
	}
	c, ok := ParseVersion(current)
	if !ok {
		c = Version{}
	}
	return l.Compare(c) > 0
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
