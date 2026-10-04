// Package text holds string helpers shared by the client and output layers.
package text

import "strings"

// Sanitize removes C0/C1 control characters (except newline and tab) so text
// that came from a server cannot inject terminal escape sequences (window
// title, clipboard writes, hidden text) when it is printed. It also removes
// the Unicode bidi embedding, override and isolate controls and invisible
// zero-width characters, which can make a table cell display text in a
// different order than it holds ("Trojan Source"). Marks that real scripts
// need (LRM, RLM, ALM, ZWJ, ZWNJ) are kept.
func Sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			return -1
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
			return -1
		case r == 0x200b, r == 0x2060, r == 0xfeff:
			return -1
		}
		return r
	}, s)
}
