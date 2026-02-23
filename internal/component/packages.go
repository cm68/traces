package component

import (
	"fmt"
	"regexp"
	"strings"
)

// ExtractLogicPart strips manufacturer prefix and package suffix from 74/54-series part numbers.
// E.g., "SN74LS244N" -> "74LS244", "DM7438" -> "7438"
var logicPartPattern = regexp.MustCompile(`(?i)^[A-Z]{0,3}((?:74|54)[A-Z]{0,4}\d{1,4})[A-Z]{0,3}$`)

func ExtractLogicPart(pn string) string {
	m := logicPartPattern.FindStringSubmatch(pn)
	if m != nil {
		return strings.ToUpper(m[1])
	}
	return ""
}

// CorrectOCRPartNumber attempts to fix common OCR misreads in IC part numbers.
// For 74/54-series parts, the family code portion (letters) and the numeric portion
// have different valid characters, so we can correct context-dependent misreads.
// E.g., "74LSO4" -> "74LS04" (O->0 in numeric portion), "74L504" -> "74LS04" (5->S in family).
// Returns the corrected string and true if a correction was made.
func CorrectOCRPartNumber(pn string) (string, bool) {
	upper := strings.ToUpper(strings.TrimSpace(pn))
	if upper == "" {
		return pn, false
	}

	// Match 74/54-series: optional prefix + 74/54 + alphanumeric body
	looseLogic := regexp.MustCompile(`^([A-Z]{0,3})(7[4A]|54)([A-Z0-9]{2,8})$`)
	m := looseLogic.FindStringSubmatch(upper)
	if m == nil {
		return pn, false
	}

	prefix := m[1] // manufacturer prefix (SN, DM, etc.)
	series := m[2] // 74 or 54
	body := m[3]   // family + number + possible package suffix

	// Fix series: 7A -> 74
	if series == "7A" {
		series = "74"
	}

	// OCR letter correction: digits -> letters
	correctToLetter := func(s string) string {
		return strings.Map(func(r rune) rune {
			switch r {
			case '0':
				return 'O'
			case '5':
				return 'S'
			case '1':
				return 'L'
			case '8':
				return 'B'
			default:
				return r
			}
		}, s)
	}

	// OCR digit correction: letters -> digits
	correctToDigit := func(s string) string {
		return strings.Map(func(r rune) rune {
			switch r {
			case 'O':
				return '0'
			case 'S':
				return '5'
			case 'I', 'L':
				return '1'
			case 'B':
				return '8'
			case 'Z':
				return '2'
			case 'G':
				return '6'
			default:
				return r
			}
		}, s)
	}

	// Known logic family codes, longest first for greedy matching
	families := []string{
		"GTLP", "HCT", "ACT", "ALS", "LVC", "LVT", "ABT", "BCT", "FCT", "GTL",
		"HC", "AC", "AS", "LV", "LS", "F", "S",
	}

	// Try to match a known family code at the start of the body (with OCR correction)
	matchedFamily := ""
	remainder := body
	for _, fam := range families {
		if len(body) > len(fam) { // must have chars left for number
			candidate := body[:len(fam)]
			if correctToLetter(candidate) == fam {
				matchedFamily = fam
				remainder = body[len(fam):]
				break
			}
		}
	}

	// Correct remainder as digits
	correctedRemainder := correctToDigit(remainder)

	// Split into number (leading digits) and package suffix (trailing non-digits)
	numberEnd := len(correctedRemainder)
	for numberEnd > 0 && (correctedRemainder[numberEnd-1] < '0' || correctedRemainder[numberEnd-1] > '9') {
		numberEnd--
	}
	if numberEnd == 0 {
		return pn, false // no digits found
	}

	number := correctedRemainder[:numberEnd]
	suffix := remainder[numberEnd:] // use original chars for suffix

	corrected := prefix + series + matchedFamily + number + suffix
	if corrected != upper {
		fmt.Printf("[OCR Correct] %s -> %s\n", upper, corrected)
		return corrected, true
	}
	return pn, false
}
