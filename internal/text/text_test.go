package text

import "testing"

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"plain":                      "plain",
		"a\tb\nc":                    "a\tb\nc",
		"\x1b]0;pwned\x07title":      "]0;pwnedtitle",
		"bell\x07 and \u009b31m csi": "bell and 31m csi",
		"unicode é ✓ stays":          "unicode é ✓ stays",
		"\x1b[31mred\x1b[0m":         "[31mred[0m",
		"pay \u202eevil\u202c ok":    "pay evil ok",
		"iso\u2066late\u2069":        "isolate",
		"zero\u200bwidth\ufeff":      "zerowidth",
		"مرحبا\u200fعالم":            "مرحبا\u200fعالم",
		"👨\u200d👩\u200d👧":            "👨\u200d👩\u200d👧",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
