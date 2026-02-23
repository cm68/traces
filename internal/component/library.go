package component

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"pcb-tracer/internal/connector"
)

// PartPin defines a single pin in a part definition.
type PartPin struct {
	Number    int                      `json:"number"`
	Name      string                   `json:"name"`
	Direction connector.SignalDirection `json:"direction"`
}

// PartDefinition defines a component part template (e.g., 74LS244 / DIP-20).
type PartDefinition struct {
	PartNumber string    `json:"part_number"`
	Package    string    `json:"package"`
	PinCount   int       `json:"pin_count"`
	Pins       []PartPin `json:"pins"`
	Aliases    []string  `json:"aliases,omitempty"` // Alternate part numbers that map to this part
}

// Key returns the display/lookup key for this part definition.
func (pd *PartDefinition) Key() string {
	return pd.PartNumber + " / " + pd.Package
}

// ComponentLibrary stores a collection of part definitions.
type ComponentLibrary struct {
	Parts []*PartDefinition `json:"parts"`
}

// NewComponentLibrary creates a new empty component library.
func NewComponentLibrary() *ComponentLibrary {
	return &ComponentLibrary{
		Parts: make([]*PartDefinition, 0),
	}
}

// Add adds or replaces a part definition in the library.
func (lib *ComponentLibrary) Add(part *PartDefinition) {
	key := part.Key()
	for i, p := range lib.Parts {
		if p.Key() == key {
			lib.Parts[i] = part
			lib.Sort()
			return
		}
	}
	lib.Parts = append(lib.Parts, part)
	lib.Sort()
}

// Remove removes a part definition by part number and package.
func (lib *ComponentLibrary) Remove(partNumber, pkg string) {
	key := partNumber + " / " + pkg
	for i, p := range lib.Parts {
		if p.Key() == key {
			lib.Parts = append(lib.Parts[:i], lib.Parts[i+1:]...)
			return
		}
	}
}

// Get returns a part definition by part number and package, or nil if not found.
func (lib *ComponentLibrary) Get(partNumber, pkg string) *PartDefinition {
	key := partNumber + " / " + pkg
	for _, p := range lib.Parts {
		if p.Key() == key {
			return p
		}
	}
	return nil
}

// GetByAlias returns a part definition matching by exact name, alias, or normalized form.
// Falls back through: exact match → alias match → normalized match (strips suffixes,
// family codes, and swaps 54↔74). If the part number matches but the package differs,
// returns the part anyway and logs a warning (package mismatch).
func (lib *ComponentLibrary) GetByAlias(partNumber, pkg string) *PartDefinition {
	// 1. Exact match
	if p := lib.Get(partNumber, pkg); p != nil {
		return p
	}

	pn := strings.ToUpper(strings.TrimSpace(partNumber))

	// 2. Check explicit aliases (case-insensitive, matching package)
	for _, p := range lib.Parts {
		if !strings.EqualFold(p.Package, pkg) {
			continue
		}
		for _, alias := range p.Aliases {
			if strings.EqualFold(alias, pn) {
				return p
			}
		}
	}

	// 3. Normalized matching — strip suffixes and compare canonical forms
	canon := normalizePartNumber(pn)
	for _, p := range lib.Parts {
		if !strings.EqualFold(p.Package, pkg) {
			continue
		}
		if normalizePartNumber(strings.ToUpper(p.PartNumber)) == canon {
			return p
		}
	}

	// 4. Package-mismatch fallback: part number matches but package differs.
	// Try alias match ignoring package.
	for _, p := range lib.Parts {
		for _, alias := range p.Aliases {
			if strings.EqualFold(alias, pn) {
				fmt.Printf("WARNING: part %q matched alias in library as %q but package differs: component=%q library=%q\n",
					partNumber, p.PartNumber, pkg, p.Package)
				return p
			}
		}
	}
	// Try normalized match ignoring package.
	for _, p := range lib.Parts {
		if normalizePartNumber(strings.ToUpper(p.PartNumber)) == canon {
			fmt.Printf("WARNING: part %q matched library entry %q but package differs: component=%q library=%q\n",
				partNumber, p.PartNumber, pkg, p.Package)
			return p
		}
	}

	return nil
}

// FindByPartNumber looks up a part definition by part number alone (ignoring package).
// Tries exact match, then alias match, then normalized match.
func (lib *ComponentLibrary) FindByPartNumber(partNumber string) *PartDefinition {
	if lib == nil {
		return nil
	}
	pn := strings.ToUpper(strings.TrimSpace(partNumber))
	if pn == "" {
		return nil
	}

	// Exact part number match
	for _, p := range lib.Parts {
		if strings.EqualFold(p.PartNumber, pn) {
			return p
		}
	}

	// Alias match
	for _, p := range lib.Parts {
		for _, alias := range p.Aliases {
			if strings.EqualFold(alias, pn) {
				return p
			}
		}
	}

	// Normalized match
	canon := normalizePartNumber(pn)
	for _, p := range lib.Parts {
		if normalizePartNumber(strings.ToUpper(p.PartNumber)) == canon {
			return p
		}
	}

	return nil
}

// trailingSuffixRe strips trailing package/speed suffixes like N, P, D, -02, B-02.
var trailingSuffixRe = regexp.MustCompile(`[A-Z]?(?:-\d+)?$`)

// aliasFamilyRe matches 74/54-series parts with optional family codes.
var aliasFamilyRe = regexp.MustCompile(`^(74|54)(LS|HC|HCT|AC|ACT|ALS|AS|F|S|LV|LVC|LVT|ABT|BCT|FCT|GTL|GTLP)?(\d+)$`)

// normalizePartNumber produces a canonical form for fuzzy matching.
// E.g., "74LS373N" → "74373", "SN74LS244N" → "74244", "54LS123" → "54123".
func normalizePartNumber(pn string) string {
	// Strip trailing package suffix letters and speed grades
	pn = strings.TrimRight(pn, " ")
	// Remove trailing -NN speed grade (e.g., -02)
	if idx := strings.LastIndex(pn, "-"); idx > 0 {
		suffix := pn[idx+1:]
		allDigits := true
		for _, c := range suffix {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits && len(suffix) > 0 {
			pn = pn[:idx]
		}
	}
	// Strip single trailing letter (package code like N, P, D)
	if len(pn) > 1 {
		last := pn[len(pn)-1]
		if last >= 'A' && last <= 'Z' {
			// Only strip if the char before it is a digit (to avoid stripping part of the name)
			prev := pn[len(pn)-2]
			if prev >= '0' && prev <= '9' {
				pn = pn[:len(pn)-1]
			}
		}
	}

	// Try to extract the logic part (handles manufacturer prefixes)
	logic := ExtractLogicPart(pn)
	if logic != "" {
		pn = logic
	}

	// Strip family code: 74LS244 → 74244
	m := aliasFamilyRe.FindStringSubmatch(pn)
	if m != nil {
		return m[1] + m[3]
	}

	return pn
}

// Sort sorts parts by part number (case-insensitive).
func (lib *ComponentLibrary) Sort() {
	sort.Slice(lib.Parts, func(i, j int) bool {
		return strings.ToLower(lib.Parts[i].Key()) < strings.ToLower(lib.Parts[j].Key())
	})
}

// getLibDirPath returns the path to component_library.json in the lib/ directory
// next to the executable, or empty string if it can't be determined.
func getLibDirPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "..", "lib", "component_library.json")
}

// GetPreferencesPath returns the path to the component library file.
// Prefers lib/component_library.json next to the executable; falls back to
// ~/.config/pcb-tracer/component_library.json.
func GetPreferencesPath() (string, error) {
	// Prefer lib/ directory next to the build output
	if libPath := getLibDirPath(); libPath != "" {
		// If the file already exists there, use it
		if _, err := os.Stat(libPath); err == nil {
			return libPath, nil
		}
		// If the lib/ directory exists, use it (for new saves)
		if dir := filepath.Dir(libPath); dir != "" {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				return libPath, nil
			}
		}
	}

	// Fall back to user config directory
	configDir, err := os.UserConfigDir()
	if err != nil {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine config directory: %w", err)
		}
		configDir = filepath.Join(home, ".config")
	}

	appDir := filepath.Join(configDir, "pcb-tracer")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return "", fmt.Errorf("cannot create config directory: %w", err)
	}

	return filepath.Join(appDir, "component_library.json"), nil
}

// SaveToPreferences saves the component library to the preferences file.
func (lib *ComponentLibrary) SaveToPreferences() error {
	path, err := GetPreferencesPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(lib, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot serialize component library: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("cannot write component library: %w", err)
	}

	fmt.Printf("Saved %d parts to %s\n", len(lib.Parts), path)
	return nil
}

// LoadComponentLibrary loads the component library.
// Checks lib/ directory next to the executable first, then falls back to
// the user config directory. Returns an empty library if no file exists.
func LoadComponentLibrary() (*ComponentLibrary, error) {
	// Try lib/ directory first
	if libPath := getLibDirPath(); libPath != "" {
		if data, err := os.ReadFile(libPath); err == nil {
			var lib ComponentLibrary
			if err := json.Unmarshal(data, &lib); err == nil {
				lib.Sort()
				fmt.Printf("Loaded %d parts from %s\n", len(lib.Parts), libPath)
				return &lib, nil
			}
		}
	}

	// Fall back to preferences path
	path, err := GetPreferencesPath()
	if err != nil {
		return NewComponentLibrary(), err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewComponentLibrary(), nil
		}
		return NewComponentLibrary(), fmt.Errorf("cannot read component library: %w", err)
	}

	var lib ComponentLibrary
	if err := json.Unmarshal(data, &lib); err != nil {
		return NewComponentLibrary(), fmt.Errorf("cannot parse component library: %w", err)
	}

	lib.Sort()

	fmt.Printf("Loaded %d parts from %s\n", len(lib.Parts), path)
	return &lib, nil
}
