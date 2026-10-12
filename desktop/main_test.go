package main

import "testing"

func TestAfterUpdatePID(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		pid  int
		ok   bool
	}{
		{"valid", []string{"--after-update", "1234"}, 1234, true},
		{"valid after another argument", []string{"--foo", "--after-update", "7"}, 7, true},
		{"no arguments", nil, 0, false},
		{"flag without a pid", []string{"--after-update"}, 0, false},
		{"flag last, pid before it", []string{"5", "--after-update"}, 0, false},
		{"not an integer", []string{"--after-update", "abc"}, 0, false},
		{"decimal", []string{"--after-update", "1.5"}, 0, false},
		{"negative", []string{"--after-update", "-5"}, 0, false},
		{"zero", []string{"--after-update", "0"}, 0, false},
		{"plus sign", []string{"--after-update", "+5"}, 0, false},
		{"leading space", []string{"--after-update", " 5"}, 0, false},
		{"trailing space", []string{"--after-update", "5 "}, 0, false},
		{"leading zero", []string{"--after-update", "05"}, 0, false},
		{"empty", []string{"--after-update", ""}, 0, false},
		{"too large", []string{"--after-update", "99999999999999999999"}, 0, false},
		{"extra arguments after", []string{"--after-update", "42", "--other", "x"}, 42, true},
		{"flag as the pid slot", []string{"--after-update", "--after-update", "9"}, 0, false},
		{"similar flag", []string{"--after-updates", "5"}, 0, false},
		{"flag with equals", []string{"--after-update=5"}, 0, false},
		{"flag elsewhere only as a value", []string{"--name", "--after-update"}, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			pid, ok := afterUpdatePID(c.args)
			if pid != c.pid || ok != c.ok {
				t.Errorf("afterUpdatePID(%q) = %d, %v; want %d, %v", c.args, pid, ok, c.pid, c.ok)
			}
		})
	}
}
