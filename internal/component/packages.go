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

	// Strip manufacturer prefix for 74/54 series (SN, DM, MC, HD, etc.)
	logic := extractLogicPart(pn)
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

// extractLogicPart strips manufacturer prefix from 74/54-series part numbers.
// E.g., "SN74LS244N" -> "74LS244", "DM7438" -> "7438"
var logicPartPattern = regexp.MustCompile(`(?i)^[A-Z]{0,3}((?:74|54)[A-Z]{0,4}\d{1,4})[A-Z]{0,3}$`)

func extractLogicPart(pn string) string {
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

// PrintDatabase prints the part database for debugging.
func PrintDatabase() {
	db := getPartDatabase()
	fmt.Printf("Part database: %d entries\n", len(db))
	for pn, info := range db {
		fmt.Printf("  %s -> %s (%d pins) %s\n", pn, info.Package, info.PinCount, info.Description)
	}
}
