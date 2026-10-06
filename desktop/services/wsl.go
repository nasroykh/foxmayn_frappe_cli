package services

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf16"
)

// WSLInfo reports ffc configs inside WSL distributions (Windows only). The
// app manages the Windows config; a config in WSL belongs to the ffc
// installed there and is not synced.
type WSLInfo struct {
	Detected bool     `json:"detected"`
	Distros  []string `json:"distros"`
	// Paths are the config files found, as Windows paths
	// (\\wsl.localhost\<distro>\home\<user>\.config\ffc\config.yaml).
	Paths []string `json:"paths"`
}

// wslDetector looks for ~/.config/ffc/config.yaml in the running WSL
// distributions. Only running ones: reading \\wsl.localhost\<distro> would
// boot a stopped distribution, which is slow and not ours to do.
type wslDetector struct {
	goos    string
	run     func(ctx context.Context, name string, args ...string) ([]byte, error)
	readDir func(string) ([]os.DirEntry, error)
	isFile  func(string) bool
}

func newWSLDetector(goos string) *wslDetector {
	return &wslDetector{
		goos: goos,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			path, err := exec.LookPath(name)
			if err != nil {
				return nil, err
			}
			cmd := exec.CommandContext(ctx, path, args...)
			hideWindow(cmd)
			return cmd.Output()
		},
		readDir: os.ReadDir,
		isFile:  isRegularFile,
	}
}

func (d *wslDetector) detect() WSLInfo {
	info := WSLInfo{Distros: []string{}, Paths: []string{}}
	if d.goos != "windows" {
		return info
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := d.run(ctx, "wsl.exe", "--list", "--running", "--quiet")
	if err != nil {
		return info
	}
	for _, distro := range parseWSLList(out) {
		for _, root := range []string{`\\wsl.localhost\` + distro, `\\wsl$\` + distro} {
			paths := d.configsIn(root)
			if len(paths) == 0 {
				continue
			}
			info.Distros = append(info.Distros, distro)
			info.Paths = append(info.Paths, paths...)
			break
		}
	}
	info.Detected = len(info.Paths) > 0
	return info
}

// configsIn returns the ffc configs of every user of the distribution whose
// file system is at root.
func (d *wslDetector) configsIn(root string) []string {
	var homes []string
	if entries, err := d.readDir(root + `\home`); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				homes = append(homes, root+`\home\`+e.Name())
			}
		}
	}
	homes = append(homes, root+`\root`)
	var out []string
	for _, h := range homes {
		p := h + `\.config\ffc\config.yaml`
		if d.isFile(p) {
			out = append(out, p)
		}
	}
	return out
}

// parseWSLList decodes the output of "wsl.exe --list --quiet": UTF-16LE
// (with or without a byte order mark), one distribution per line. Plain
// UTF-8 output (WSL_UTF8=1) is accepted too.
func parseWSLList(out []byte) []string {
	s := decodeUTF16LE(out)
	var names []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(strings.Trim(l, "\r\x00\ufeff"))
		if l != "" {
			names = append(names, l)
		}
	}
	return names
}

func decodeUTF16LE(b []byte) string {
	// UTF-16LE text of ASCII names has a zero in every odd byte.
	looks16 := len(b) >= 2 && (b[0] == 0xff && b[1] == 0xfe || b[1] == 0)
	if !looks16 {
		return string(b)
	}
	if len(b)%2 == 1 {
		b = b[:len(b)-1]
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(u))
}
