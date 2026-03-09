package version

// Build-time variables injected via ldflags.
//
//	go build -ldflags "-X foxmayn_frappe_cli/internal/version.Version=v1.0.0 ..."
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
