// Package desktopinstall finds, downloads, verifies and installs Foxmayn
// Frappe Desktop releases. It is shared by the app's "Install update" button
// and `ffc desktop`, so it never prompts, never touches stdout and imports
// neither Wails nor cobra. Only Windows (per-user NSIS setup) and macOS
// (.dmg holding the .app) are supported.
package desktopinstall

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// TagPrefix starts every desktop release tag ("desktop-v0.10.0").
const TagPrefix = "desktop-v"

// Version is a parsed semantic version (build metadata is dropped). It is
// strict: anything that is not a version does not parse, so it is never
// compared.
type Version struct {
	Major, Minor, Patch uint64
	Pre                 string
}

var semverRE = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// ParseVersion parses "1.2.3", "v1.2.3", "1.2.3-rc.1" and "1.2.3+build". It
// accepts nothing else.
func ParseVersion(s string) (Version, bool) {
	m := semverRE.FindStringSubmatch(s)
	if m == nil {
		return Version{}, false
	}
	var n [3]uint64
	for i := range n {
		v, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return Version{}, false
		}
		n[i] = v
	}
	return Version{n[0], n[1], n[2], m[4]}, true
}

// TagVersion returns the version of a desktop release tag; a tag that does
// not start with TagPrefix (the CLI's "v1.2.3") is not one.
func TagVersion(tag string) (Version, bool) {
	rest, ok := strings.CutPrefix(tag, TagPrefix)
	if !ok {
		return Version{}, false
	}
	return ParseVersion(rest)
}

// String is "major.minor.patch" plus "-pre" when there is one.
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// IsDevBuild is the "0.0.0-dev" placeholder of a build without a release
// version.
func (v Version) IsDevBuild() bool {
	return v.Major == 0 && v.Minor == 0 && v.Patch == 0 && strings.HasPrefix(v.Pre, "dev")
}

// Compare orders versions by semver precedence: -1, 0 or 1.
func (v Version) Compare(o Version) int {
	for _, p := range [][2]uint64{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
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
		if c := comparePreIdent(a[i], b[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// comparePreIdent compares two dot-separated prerelease identifiers:
// numbers by value and below text, text in ASCII order.
func comparePreIdent(a, b string) int {
	an, aerr := strconv.ParseUint(a, 10, 64)
	bn, berr := strconv.ParseUint(b, 10, 64)
	switch {
	case aerr == nil && berr == nil:
		if an != bn {
			if an < bn {
				return -1
			}
			return 1
		}
		return 0
	case aerr == nil:
		return -1
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}
