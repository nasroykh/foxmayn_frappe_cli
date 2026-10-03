package config

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// ─── Number format ────────────────────────────────────────────────────────────

// NumberFormat defines the display style for numeric values.
type NumberFormat string

const (
	// FormatFrench  1 000 000,00  (non-breaking space thousands, comma decimal) — DEFAULT
	FormatFrench NumberFormat = "french"
	// FormatUS      1,000,000.00  (comma thousands, period decimal)
	FormatUS NumberFormat = "us"
	// FormatGerman  1.000.000,00  (period thousands, comma decimal)
	FormatGerman NumberFormat = "german"
	// FormatPlain   1000000.00    (no grouping, period decimal)
	FormatPlain NumberFormat = "plain"
)

// AllFormats lists every supported number format.
var AllFormats = []struct {
	Key     NumberFormat
	Label   string
	Example string
}{
	{FormatFrench, "French / European", "1 000 000,00"},
	{FormatUS, "US / English", "1,000,000.00"},
	{FormatGerman, "German / Spanish", "1.000.000,00"},
	{FormatPlain, "Plain (no grouping)", "1000000.00"},
}

// ─── Date format ──────────────────────────────────────────────────────────────

// DateFormat defines the display style for dates.
type DateFormat string

const (
	// FormatISODate YYYY-MM-DD (Frappe default)
	FormatISODate DateFormat = "yyyy-mm-dd"
	// FormatEuroDate DD-MM-YYYY
	FormatEuroDate DateFormat = "dd-mm-yyyy"
	// FormatEuroSlashDate DD/MM/YYYY
	FormatEuroSlashDate DateFormat = "dd/mm/yyyy"
	// FormatUSDate   MM/DD/YYYY
	FormatUSDate DateFormat = "mm/dd/yyyy"
)

// AllDateFormats lists every supported date format.
var AllDateFormats = []struct {
	Key     DateFormat
	Label   string
	Example string
}{
	{FormatISODate, "ISO (YYYY-MM-DD)", "2025-12-31"},
	{FormatEuroDate, "European (DD-MM-YYYY)", "31-12-2025"},
	{FormatEuroSlashDate, "European (DD/MM/YYYY)", "31/12/2025"},
	{FormatUSDate, "US (MM/DD/YYYY)", "12/31/2025"},
}

// ActiveFormat and ActiveDateFormat are the formats used by the output layer.
var ActiveFormat NumberFormat = FormatFrench
var ActiveDateFormat DateFormat = FormatISODate

// FormatNumber formats f according to the active number format. Integral
// values render without a decimal part; fractional values keep at least 2 and
// at most 9 decimals (Frappe's maximum float precision), so an exchange rate of
// 0.0045 is shown as such instead of being rounded away to 0.
func FormatNumber(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	intStr, frac, _ := strings.Cut(strconv.FormatFloat(math.Abs(f), 'f', 9, 64), ".")
	frac = strings.TrimRight(frac, "0")
	if frac != "" && len(frac) < 2 {
		frac += "0"
	}

	result := groupDigits(intStr, thousandsSep(ActiveFormat))
	if frac != "" {
		result += decimalSep(ActiveFormat) + frac
	}
	if f < 0 && (strings.Trim(intStr, "0") != "" || frac != "") { // avoid "-0"
		result = "-" + result
	}
	return result
}

// dateOutLayout returns the output layout for the active date format, appending
// a time component when hasTime is set.
func dateOutLayout(hasTime bool) string {
	var l string
	switch ActiveDateFormat {
	case FormatEuroDate:
		l = "02-01-2006"
	case FormatEuroSlashDate:
		l = "02/01/2006"
	case FormatUSDate:
		l = "01/02/2006"
	default:
		l = "2006-01-02"
	}
	if hasTime {
		l += " 15:04:05"
	}
	return l
}

// FormatDate converts a Frappe date/datetime string into the active format.
// Frappe always stores dates in ISO form, so only exact ISO matches are
// reformatted — the function never guesses DD/MM vs MM/DD and never truncates a
// longer string down to a date prefix (M17). Anything else is returned as-is.
func FormatDate(s string) string {
	for _, layout := range []string{
		"2006-01-02 15:04:05.000000",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format(dateOutLayout(true))
		}
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Format(dateOutLayout(false))
	}
	return s
}

func thousandsSep(nf NumberFormat) string {
	switch nf {
	case FormatFrench:
		return "\u00a0" // non-breaking space
	case FormatUS:
		return ","
	case FormatGerman:
		return "."
	default:
		return ""
	}
}

func decimalSep(nf NumberFormat) string {
	switch nf {
	case FormatFrench, FormatGerman:
		return ","
	default:
		return "."
	}
}

// groupDigits inserts sep every 3 digits from the right of the digit string s.
func groupDigits(s, sep string) string {
	if sep == "" || len(s) <= 3 {
		return s
	}
	var b strings.Builder
	start := len(s) % 3
	if start > 0 {
		b.WriteString(s[:start])
	}
	for i := start; i < len(s); i += 3 {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// Valid reports whether nf is one of AllFormats.
func (nf NumberFormat) Valid() bool {
	for _, f := range AllFormats {
		if f.Key == nf {
			return true
		}
	}
	return false
}

// Valid reports whether df is one of AllDateFormats.
func (df DateFormat) Valid() bool {
	for _, f := range AllDateFormats {
		if f.Key == df {
			return true
		}
	}
	return false
}
