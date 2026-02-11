// Package version holds build-time version information for stereosd.
//
// Variables are set via -ldflags at build time:
//
// go build -ldflags "-X github.com/papercomputeco/stereosd/pkg/version.Version=v1.0.0 ..."
package version

import (
	"fmt"
	"runtime"
)

// Build-time variables, set via ldflags.
var (
	// Version is the semantic version (e.g. "v0.1.0" or "dev").
	Version = "dev"

	// GitCommit is the short SHA of the build commit.
	GitCommit = "unknown"

	// BuildDate is the ISO-8601 date of the build.
	BuildDate = "unknown"
)

// Info returns a human-readable version string.
func Info() string {
	return fmt.Sprintf("stereosd %s (commit: %s, built: %s, %s/%s)",
		Version, GitCommit, BuildDate, runtime.GOOS, runtime.GOARCH)
}
