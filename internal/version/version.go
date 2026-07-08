package version

// Build-time variables injected via ldflags. The module path prefix is
// required (the Makefile and GoReleaser use the fully-qualified path):
//
//	go build -ldflags "-X github.com/nasroykh/foxmayn_frappe_cli/internal/version.Version=v1.0.0 ..."
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
