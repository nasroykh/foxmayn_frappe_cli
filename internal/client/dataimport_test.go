package client

import "testing"

func TestHTMLTextKeepsLineBreaks(t *testing.T) {
	for in, want := range map[string]string{
		"Row 2 is invalid<br>Allowed: Open, Closed":         "Row 2 is invalid; Allowed: Open, Closed",
		"<p>One</p><p>Two</p>":                              "One; Two",
		"a <br/><br /> b":                                   "a; b",
		"<strong>Bogus</strong> is not one of Open, Closed": "Bogus is not one of Open, Closed",
	} {
		if got := htmlText(in); got != want {
			t.Errorf("htmlText(%q) = %q, want %q", in, got, want)
		}
	}
}
