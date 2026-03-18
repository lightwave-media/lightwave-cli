package version

// Set via ldflags at build time. GoReleaser injects these automatically from git tags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
