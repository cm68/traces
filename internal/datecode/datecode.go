// Package datecode decodes IC chip date codes from various manufacturers.
package datecode

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// DecodedDate represents a decoded date code.
type DecodedDate struct {
	Year        int    // Full year (e.g., 1989)
	YearAmbig   bool   // True if year is ambiguous (single digit)
	Month       int    // 1-12
	Week        int    // Week number (1-52 or 1-5 depending on format)
	WeekOfMonth bool   // True if week is week-of-month, false if week-of-year
	Format      string // Format name (e.g., "Hitachi YMW", "YYWW")
	Raw         string // Original date code
}

// String returns a human-readable representation.
func (d DecodedDate) String() string {
	monthNames := []string{"", "Jan", "Feb", "Mar", "Apr", "May", "Jun",
		"Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

	var sb strings.Builder
	if d.YearAmbig {
		// Show possible decades
		sb.WriteString(fmt.Sprintf("%s %d (or %d, %d)",
			monthNames[d.Month], d.Year, d.Year-10, d.Year+10))
	} else {
		sb.WriteString(fmt.Sprintf("%s %d", monthNames[d.Month], d.Year))
	}

	if d.Week > 0 {
		if d.WeekOfMonth {
			sb.WriteString(fmt.Sprintf(", week %d of month", d.Week))
		} else {
			sb.WriteString(fmt.Sprintf(", week %d", d.Week))
		}
	}

	return sb.String()
}

// Decode attempts to decode a date code string.
// It tries multiple formats and returns the best match.
// contextYear hints at the expected decade (e.g., 1990 for 1990s chips).
func Decode(code string, contextYear int) *DecodedDate {
	code = strings.TrimSpace(strings.ToUpper(code))
	if len(code) < 2 {
		return nil
	}

	// Try each format in order of specificity
	if d := decodeYYWW(code, contextYear); d != nil {
		return d
	}
	if d := decodeYMW(code, contextYear); d != nil {
		return d
	}
	if d := decodeYWW(code, contextYear); d != nil {
		return d
	}

	return nil
}

// decodeYMW decodes Hitachi-style YMW (Year-Month-Week) format.
// Y = single digit year
// M = month letter (A-H = Jan-Aug, J-M = Sep-Dec, skipping I)
// W = week of month (1-5)
// Examples: 9L5 = Dec 1989/1999 week 5, 3C2 = Mar 1983/1993 week 2
func decodeYMW(code string, contextYear int) *DecodedDate {
	if len(code) != 3 {
		return nil
	}

	// First char must be digit (year)
	if code[0] < '0' || code[0] > '9' {
		return nil
	}
	yearDigit := int(code[0] - '0')

	// Second char must be month letter A-M (excluding I)
	month := monthFromLetter(code[1])
	if month == 0 {
		return nil
	}

	// Third char must be digit (week)
	if code[2] < '1' || code[2] > '5' {
		return nil
	}
	week := int(code[2] - '0')

	// Determine full year based on context
	year := resolveYear(yearDigit, contextYear)

	return &DecodedDate{
		Year:        year,
		YearAmbig:   true,
		Month:       month,
		Week:        week,
		WeekOfMonth: true,
		Format:      "Hitachi YMW",
		Raw:         code,
	}
}

// decodeYYWW decodes standard YYWW format (2-digit year, 2-digit week).
// Examples: 8923 = 1989 week 23, 0145 = 2001 week 45
func decodeYYWW(code string, contextYear int) *DecodedDate {
	if len(code) != 4 {
		return nil
	}

	// All chars must be digits
	for _, c := range code {
		if c < '0' || c > '9' {
			return nil
		}
	}

	yy, _ := strconv.Atoi(code[0:2])
	ww, _ := strconv.Atoi(code[2:4])

	// Week must be valid (1-53)
	if ww < 1 || ww > 53 {
		return nil
	}

	// Determine century
	year := yy
	if contextYear > 0 {
		century := (contextYear / 100) * 100
		year = century + yy
		// If that puts us too far in future, use previous century
		if year > contextYear+20 {
			year -= 100
		}
	} else {
		// Default: 00-30 = 2000s, 31-99 = 1900s
		if yy <= 30 {
			year = 2000 + yy
		} else {
			year = 1900 + yy
		}
	}

	// Convert week to approximate month
	month := weekToMonth(ww)

	return &DecodedDate{
		Year:        year,
		YearAmbig:   false,
		Month:       month,
		Week:        ww,
		WeekOfMonth: false,
		Format:      "YYWW",
		Raw:         code,
	}
}

// decodeYWW decodes YWW format (1-digit year, 2-digit week).
// Examples: 923 = 1989/1999 week 23
func decodeYWW(code string, contextYear int) *DecodedDate {
	if len(code) != 3 {
		return nil
	}

	// All chars must be digits
	for _, c := range code {
		if c < '0' || c > '9' {
			return nil
		}
	}

	y := int(code[0] - '0')
	ww, _ := strconv.Atoi(code[1:3])

	// Week must be valid (1-53)
	if ww < 1 || ww > 53 {
		return nil
	}

	year := resolveYear(y, contextYear)
	month := weekToMonth(ww)

	return &DecodedDate{
		Year:        year,
		YearAmbig:   true,
		Month:       month,
		Week:        ww,
		WeekOfMonth: false,
		Format:      "YWW",
		Raw:         code,
	}
}

// monthFromLetter converts a month letter to month number (1-12).
// Uses the convention: A-H = Jan-Aug, J-M = Sep-Dec (I skipped).
// Returns 0 if invalid.
func monthFromLetter(c byte) int {
	switch c {
	case 'A':
		return 1 // January
	case 'B':
		return 2 // February
	case 'C':
		return 3 // March
	case 'D':
		return 4 // April
	case 'E':
		return 5 // May
	case 'F':
		return 6 // June
	case 'G':
		return 7 // July
	case 'H':
		return 8 // August
	case 'J':
		return 9 // September (I skipped)
	case 'K':
		return 10 // October
	case 'L':
		return 11 // November
	case 'M':
		return 12 // December
	default:
		return 0
	}
}

// resolveYear converts a single digit year to a full year.
func resolveYear(digit int, contextYear int) int {
	if contextYear > 0 {
		// Use context to pick the right decade
		decade := (contextYear / 10) * 10
		year := decade + digit
		// If that's too far in future, use previous decade
		if year > contextYear+5 {
			year -= 10
		}
		return year
	}
	// Default to 1990s for ambiguous dates
	return 1990 + digit
}

// weekToMonth converts a week number (1-53) to approximate month (1-12).
func weekToMonth(week int) int {
	// Approximate: each month has ~4.33 weeks
	month := (week-1)/4 + 1
	if month > 12 {
		month = 12
	}
	if month < 1 {
		month = 1
	}
	return month
}

// ExtractDateCode attempts to find and extract a date code from OCR text.
// Returns the extracted code and decoded date, or empty/nil if not found.
func ExtractDateCode(text string, contextYear int) (string, *DecodedDate) {
	text = strings.ToUpper(text)

	// Pattern for YMW (Hitachi style): digit, letter A-M (not I), digit
	ymwPattern := regexp.MustCompile(`\b([0-9][A-HJ-M][1-5])\b`)
	if m := ymwPattern.FindStringSubmatch(text); m != nil {
		code := m[1]
		if d := decodeYMW(code, contextYear); d != nil {
			return code, d
		}
	}

	// Pattern for YYWW: 4 digits where last 2 are 01-53
	yywwPattern := regexp.MustCompile(`\b([0-9]{2})(0[1-9]|[1-4][0-9]|5[0-3])\b`)
	if m := yywwPattern.FindStringSubmatch(text); m != nil {
		code := m[1] + m[2]
		if d := decodeYYWW(code, contextYear); d != nil {
			return code, d
		}
	}

	// Pattern for YWW: 3 digits where last 2 are 01-53
	ywwPattern := regexp.MustCompile(`\b([0-9])(0[1-9]|[1-4][0-9]|5[0-3])\b`)
	if m := ywwPattern.FindStringSubmatch(text); m != nil {
		code := m[1] + m[2]
		if d := decodeYWW(code, contextYear); d != nil {
			return code, d
		}
	}

	return "", nil
}

// FormatInfo provides detailed format information for display.
type FormatInfo struct {
	Name        string
	Description string
	Example     string
	Decoded     string
}

// GetFormatInfo returns information about known date code formats.
func GetFormatInfo() []FormatInfo {
	return []FormatInfo{
		{
			Name:        "Hitachi YMW",
			Description: "Year (0-9) + Month (A-M, skip I) + Week of month (1-5)",
			Example:     "9L5",
			Decoded:     "1989/1999, November, week 5",
		},
		{
			Name:        "YYWW",
			Description: "2-digit year + 2-digit week (01-53)",
			Example:     "8923",
			Decoded:     "1989, week 23 (early June)",
		},
		{
			Name:        "YWW",
			Description: "1-digit year + 2-digit week (01-53)",
			Example:     "923",
			Decoded:     "1989/1999, week 23 (early June)",
		},
	}
}
