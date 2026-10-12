package desktopinstall

import "testing"

func TestVersionCompare(t *testing.T) {
	order := []string{
		"0.0.1", "0.1.0-alpha", "0.1.0-alpha.1", "0.1.0-alpha.beta", "0.1.0-beta", "0.1.0-beta.2",
		"0.1.0-beta.11", "0.1.0-rc.1", "0.1.0", "0.1.1", "0.2.0", "0.10.0", "1.0.0-rc.1", "1.0.0", "2.0.0",
	}
	for i := range order {
		for j := range order {
			a, ok1 := ParseVersion(order[i])
			b, ok2 := ParseVersion(order[j])
			if !ok1 || !ok2 {
				t.Fatalf("parse %q %q", order[i], order[j])
			}
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := a.Compare(b); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	a, _ := ParseVersion("v1.2.3+one")
	b, _ := ParseVersion("1.2.3+two")
	if a.Compare(b) != 0 || a.String() != "1.2.3" {
		t.Errorf("build metadata must not matter: %v %v", a, b)
	}
	for _, bad := range []string{"", "1", "1.2", "1.2.3.4", "1.2.x", "-1.2.3", "1.2.3-", "1.2.3-a..b", "01.2.3", "99999999999999999999.0.0", "v v1.2.3"} {
		if _, ok := ParseVersion(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestTagVersionAndDev(t *testing.T) {
	if v, ok := TagVersion("desktop-v0.10.0"); !ok || v.String() != "0.10.0" {
		t.Errorf("desktop tag: %v %v", v, ok)
	}
	for _, bad := range []string{"v1.2.3", "desktop-1.2.3", "desktop-v", "desktop-vx", "foo-desktop-v1.0.0", ""} {
		if _, ok := TagVersion(bad); ok {
			t.Errorf("%q parsed as a desktop tag", bad)
		}
	}
	for s, want := range map[string]bool{"0.0.0-dev": true, "0.0.0-dev.3": true, "0.0.0": false, "0.1.0-dev": false, "1.0.0": false} {
		v, _ := ParseVersion(s)
		if v.IsDevBuild() != want {
			t.Errorf("IsDevBuild(%s) = %v", s, !want)
		}
	}
}
