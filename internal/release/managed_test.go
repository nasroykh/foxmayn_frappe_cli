package release

import "testing"

func TestManagedBy(t *testing.T) {
	t.Setenv("SCOOP", `D:\tools\sc`)
	t.Setenv("SCOOP_GLOBAL", "")
	cases := []struct {
		path string
		want string // manager name, "" for none
	}{
		{"/opt/homebrew/Caskroom/ffc/1.16.0/ffc", "Homebrew"},
		{"/usr/local/Caskroom/ffc/1.16.0/ffc", "Homebrew"},
		{"/home/linuxbrew/.linuxbrew/Cellar/ffc/1.16.0/bin/ffc", "Homebrew"},
		{`C:\Users\me\scoop\apps\ffc\1.16.0\ffc.exe`, "Scoop"},
		{`C:\ProgramData\scoop\apps\ffc\current\ffc.exe`, "Scoop"},
		{`D:\tools\sc\apps\ffc\1.16.0\ffc.exe`, "Scoop"},
		{`C:\Users\me\scoop\shims\ffc.exe`, "Scoop"},
		{`D:\tools\sc\shims\ffc.exe`, "Scoop"},
		{`C:\Users\me\AppData\Local\Microsoft\WinGet\Packages\Foxmayn.ffc_Microsoft.Winget.Source_8wekyb3d8bbwe\ffc.exe`, "winget"},
		{`C:\Users\me\AppData\Local\Microsoft\WinGet\Links\ffc.exe`, "winget"},
		{`C:\Program Files\WinGet\Packages\Foxmayn.ffc\ffc.exe`, "winget"},
		// The install scripts' and go install's locations.
		{"/usr/local/bin/ffc", ""},
		{"/home/me/.local/bin/ffc", ""},
		{"/home/me/go/bin/ffc", ""},
		{`C:\Users\me\AppData\Local\Programs\ffc\ffc.exe`, ""},
		// Another app in the same managers is not ffc.
		{"/opt/homebrew/Cellar/ffcx/1.0/bin/ffc", ""},
		{`C:\Users\me\scoop\apps\other\1.0\ffc.exe`, ""},
		{`D:\tools\scx\apps\ffc\1.0\ffc.exe`, ""},
	}
	for _, c := range cases {
		got := ""
		if m := ManagedBy(c.path); m != nil {
			got = m.Name
			if m.Command == "" {
				t.Errorf("ManagedBy(%q): no command", c.path)
			}
		}
		if got != c.want {
			t.Errorf("ManagedBy(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
