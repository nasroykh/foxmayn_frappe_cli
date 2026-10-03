// Package text holds string helpers shared by the client and output layers.
package text

import "strings"

// Sanitize removes C0/C1 control characters (except newline and tab) so text
// that came from a server cannot inject terminal escape sequences (window
// title, clipboard writes, hidden text) when it is printed.
func Sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}
