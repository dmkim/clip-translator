// Package version exposes the application version injected at build time.
package version

import "strings"

// Version is overridden by the build script through
//
//	-ldflags "-X github.com/genius/clip-translator/internal/version.Version=vX.Y.Z"
//
// It stays "dev" for local builds so update checks are skipped.
var Version = "dev"

// Current returns the version without a leading "v", trimmed of whitespace.
func Current() string {
	return strings.TrimPrefix(strings.TrimSpace(Version), "v")
}

// IsDev reports whether this build has no release version injected.
func IsDev() bool {
	switch Current() {
	case "", "dev":
		return true
	default:
		return false
	}
}
