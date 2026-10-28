// Package version names this build of YT Cartell Studio. The values are
// plain strings so the build scripts can set them at link time:
//
//	go build -ldflags "-X ytcartell/internal/version.Version=2.1.0 \
//	    -X ytcartell/internal/version.Commit=abc1234 \
//	    -X ytcartell/internal/version.BuildDate=2026-10-01" .
//
// Version is the semantic version the updater compares with the GitHub
// releases (tags "v<Version>"); keep the default in step with the release
// being prepared (release.ps1 and build-mac.sh read it from this file).
package version

import (
	"strings"
	"time"
)

var (
	// Version is the semantic version without the "v" ("2.0.0").
	Version = "2.5.1"
	// Commit is the source revision ("" when unknown).
	Commit = ""
	// BuildDate is the build day, "2006-01-02" or RFC 3339 ("" when unknown).
	BuildDate = ""
)

// String is Version without a leading "v" or spaces ("0.0.0" when empty).
func String() string {
	v := strings.TrimPrefix(strings.TrimSpace(Version), "v")
	if v == "" {
		return "0.0.0"
	}
	return v
}

// Built parses BuildDate (zero when unknown or malformed).
func Built() time.Time {
	s := strings.TrimSpace(BuildDate)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
