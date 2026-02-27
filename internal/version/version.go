package version

import "runtime"

var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

// GoVersion returns the Go runtime version string.
func GoVersion() string { return runtime.Version() }
