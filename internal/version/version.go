// Package version provides build-time version information.
package version

// These variables are set at build time using -ldflags
var (
	// Version is the semantic version
	Version = "0.1.0"

	// BuildTime is the UTC time when the binary was built
	BuildTime = "unknown"

	// GitCommit is the git commit hash
	GitCommit = "unknown"
)
