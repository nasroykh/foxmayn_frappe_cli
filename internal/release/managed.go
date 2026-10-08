package release

import (
	"os"
	"strings"
)

// Manager is a package manager that installed ffc. A binary one of these owns
// must not be replaced by ffc itself: the manager would go on reporting the old
// version, and its own upgrade would later overwrite or trip over the change.
type Manager struct {
	Name    string // "Homebrew", "Scoop", "winget"
	Command string // the command that updates ffc through it
}

var (
	homebrew = Manager{Name: "Homebrew", Command: "brew upgrade ffc"}
	scoop    = Manager{Name: "Scoop", Command: "scoop update ffc"}
	winget   = Manager{Name: "winget", Command: "winget upgrade --id Foxmayn.ffc"}
)

// managedSegments are path parts, lower case with forward slashes, that only a
// package manager's install of ffc contains:
//   - Homebrew keeps formulae in <prefix>/Cellar and casks in <prefix>/Caskroom
//     (/opt/homebrew, /usr/local or /home/linuxbrew/.linuxbrew), linked from
//     <prefix>/bin;
//   - Scoop installs to <root>/apps/ffc/<version>, by default ~/scoop and
//     C:\ProgramData\scoop (see also $SCOOP and $SCOOP_GLOBAL), and puts a shim
//     <root>/shims/ffc.exe on PATH (what the desktop app finds there);
//   - winget unpacks a portable package under ...\WinGet\Packages and links it
//     from ...\WinGet\Links.
var managedSegments = []struct {
	segment string
	manager Manager
}{
	{"/cellar/ffc/", homebrew},
	{"/caskroom/ffc/", homebrew},
	{"/scoop/apps/ffc/", scoop},
	{"/scoop/shims/ffc.", scoop}, // ffc.exe, ffc.shim, ffc.cmd, ffc.ps1
	{"/winget/packages/", winget},
	{"/winget/links/", winget},
}

// ManagedBy returns the package manager that installed the ffc binary at path,
// or nil. path should be absolute with symlinks resolved, as `ffc update`
// replaces it (Homebrew's <prefix>/bin/ffc is a link into the Cellar or
// Caskroom). The match ignores case and the path separator.
func ManagedBy(path string) *Manager {
	p := slashLower(path)
	for _, s := range managedSegments {
		if strings.Contains(p, s.segment) {
			m := s.manager
			return &m
		}
	}
	// A Scoop root moved elsewhere need not be named "scoop".
	for _, env := range []string{"SCOOP", "SCOOP_GLOBAL"} {
		if root := os.Getenv(env); root != "" {
			root = strings.TrimSuffix(slashLower(root), "/")
			if strings.HasPrefix(p, root+"/apps/ffc/") || strings.HasPrefix(p, root+"/shims/ffc.") {
				m := scoop
				return &m
			}
		}
	}
	return nil
}

func slashLower(p string) string {
	return strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
}
