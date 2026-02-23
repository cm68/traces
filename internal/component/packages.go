package component

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

// PartInfo holds package and pin count information for a known part number.
type PartInfo struct {
	Package     string `json:"package"`
	PinCount    int    `json:"pins"`
	Description string `json:"description"`
}

var (
	partDatabase     map[string]PartInfo
	partDatabaseOnce sync.Once
)

// loadPartDatabase loads the parts database from the JSON file.
func loadPartDatabase() {
	partDatabase = make(map[string]PartInfo)

	// Find parts.json relative to the executable or source
	paths := []string{}

	// Next to the executable
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "..", "data", "parts.json"))
		paths = append(paths, filepath.Join(filepath.Dir(exe), "data", "parts.json"))
	}

	// Relative to source file (for development)
	_, thisFile, _, _ := runtime.Caller(0)
	srcRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	paths = append(paths, filepath.Join(srcRoot, "data", "parts.json"))

	// Current working directory
	if wd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(wd, "data", "parts.json"))
	}

	var data []byte
	var loadPath string
	for _, p := range paths {
		var err error
		data, err = os.ReadFile(p)
		if err == nil {
			loadPath = p
			break
		}
	}

	if data == nil {
		fmt.Println("[Parts DB] Warning: could not find data/parts.json")
		return
	}

	if err := json.Unmarshal(data, &partDatabase); err != nil {
		fmt.Printf("[Parts DB] Error parsing %s: %v\n", loadPath, err)
		return
	}

	// Normalize keys to uppercase
	normalized := make(map[string]PartInfo, len(partDatabase))
	for k, v := range partDatabase {
		normalized[strings.ToUpper(k)] = v
	}
	partDatabase = normalized

	fmt.Printf("[Parts DB] Loaded %d parts from %s\n", len(partDatabase), loadPath)
}

// getPartDatabase returns the loaded database, initializing on first call.
func getPartDatabase() map[string]PartInfo {
	partDatabaseOnce.Do(loadPartDatabase)
	return partDatabase
}

// LookupPartPackage returns the package info for a given part number.
// Handles 74/54-series logic with family prefixes stripped, and direct part matches.
// Returns nil if no match is found.
func LookupPartPackage(partNumber string) *PartInfo {
	pn := strings.ToUpper(strings.TrimSpace(partNumber))
	// Strip trailing punctuation (OCR artifacts)
	pn = strings.TrimRight(pn, "-_.,;:!/ ")
	if pn == "" {
		return nil
	}

	db := getPartDatabase()

	// Direct lookup first
	if info, ok := db[pn]; ok {
		return &info
	}

	// Try OCR-corrected form
	if corrected, changed := CorrectOCRPartNumber(pn); changed {
		if info, ok := db[corrected]; ok {
			return &info
		}
		pn = corrected
	}

	// Strip manufacturer prefix for 74/54 series (SN, DM, MC, HD, etc.)
	logic := ExtractLogicPart(pn)
	if logic != "" {
		if info, ok := db[logic]; ok {
			return &info
		}
		// Strip family code: 74LS244 -> 74244
		stripped := stripFamilyCode(logic)
		if stripped != logic {
			if info, ok := db[stripped]; ok {
				return &info
			}
		}
		// Try with 74 swapped for 54 and vice versa
		if strings.HasPrefix(stripped, "74") {
			alt := "54" + stripped[2:]
			if info, ok := db[alt]; ok {
				return &info
			}
		} else if strings.HasPrefix(stripped, "54") {
			alt := "74" + stripped[2:]
			if info, ok := db[alt]; ok {
				return &info
			}
		}
	}

	return nil
}

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

// stripFamilyCode removes the logic family from a 74/54-series part.
// E.g., "74LS244" -> "74244", "74HCT245" -> "74245"
var familyPattern = regexp.MustCompile(`^(74|54)(LS|HC|HCT|AC|ACT|ALS|AS|F|S|LV|LVC|LVT|ABT|BCT|FCT|GTL|GTLP)?(\d+)$`)

func stripFamilyCode(pn string) string {
	m := familyPattern.FindStringSubmatch(pn)
	if m != nil {
		return m[1] + m[3]
	}
	return pn
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

// PrintDatabase prints the part database for debugging.
func PrintDatabase() {
	db := getPartDatabase()
	fmt.Printf("Part database: %d entries\n", len(db))
	for pn, info := range db {
		fmt.Printf("  %s -> %s (%d pins) %s\n", pn, info.Package, info.PinCount, info.Description)
	}
}
