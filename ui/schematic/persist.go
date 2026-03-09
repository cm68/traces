package schematic

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SymbolLayout stores the user-adjusted position, flip, rotation, and sheet for one symbol.
type SymbolLayout struct {
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	FlipH    bool    `json:"flip_h,omitempty"`
	FlipV    bool    `json:"flip_v,omitempty"`
	Rotation int     `json:"rotation,omitempty"`
	Sheet    int     `json:"sheet,omitempty"`
}

// OffSheetConnectorLayout stores the user-adjusted position and transform for an off-sheet connector.
type OffSheetConnectorLayout struct {
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	FlipH    bool    `json:"flip_h,omitempty"`
	FlipV    bool    `json:"flip_v,omitempty"`
	Rotation int     `json:"rotation,omitempty"`
}

// SchematicLayout stores all user layout overrides, keyed by symbol ID.
type SchematicLayout struct {
	Symbols            map[string]SymbolLayout            `json:"symbols"`
	OffSheetConnectors map[string]OffSheetConnectorLayout `json:"off_sheet_connectors,omitempty"`
	Sheets             []Sheet                            `json:"sheets,omitempty"`
}

// layoutPath returns the schematic layout file path derived from the project path.
// e.g., "/path/to/project.json" → "/path/to/project_schematic.json"
func layoutPath(projectPath string) string {
	if projectPath == "" {
		return ""
	}
	ext := filepath.Ext(projectPath)
	base := strings.TrimSuffix(projectPath, ext)
	return base + "_schematic.json"
}

// SaveLayout writes the current symbol positions and flip states to disk.
func SaveLayout(doc *SchematicDoc, projectPath string) error {
	path := layoutPath(projectPath)
	if path == "" {
		return nil
	}

	layout := &SchematicLayout{
		Symbols:            make(map[string]SymbolLayout),
		OffSheetConnectors: make(map[string]OffSheetConnectorLayout),
		Sheets:             doc.Sheets,
	}
	for _, sym := range doc.Symbols {
		layout.Symbols[sym.ID] = SymbolLayout{
			X:        sym.X,
			Y:        sym.Y,
			FlipH:    sym.FlipH,
			FlipV:    sym.FlipV,
			Rotation: sym.Rotation,
			Sheet:    sym.Sheet,
		}
	}
	for _, osc := range doc.OffSheetConnectors {
		key := fmt.Sprintf("%d:%s", osc.Sheet, osc.NetID)
		layout.OffSheetConnectors[key] = OffSheetConnectorLayout{
			X:        osc.X,
			Y:        osc.Y,
			FlipH:    osc.FlipH,
			FlipV:    osc.FlipV,
			Rotation: osc.Rotation,
		}
	}
	// Net labels are auto-generated from wires; no need to persist their positions.

	data, err := json.MarshalIndent(layout, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// HasSavedLayout returns true if a schematic layout file exists for the project.
func HasSavedLayout(projectPath string) bool {
	path := layoutPath(projectPath)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// LoadLayout reads saved symbol positions and flip states from disk.
// Returns nil if the file doesn't exist.
func LoadLayout(projectPath string) *SchematicLayout {
	path := layoutPath(projectPath)
	if path == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var layout SchematicLayout
	if err := json.Unmarshal(data, &layout); err != nil {
		return nil
	}
	return &layout
}

// ApplyLayout restores saved positions and flip states to matching symbols.
// Symbols not found in the layout keep their auto-layout positions.
// Returns the number of symbols that were restored.
func ApplyLayout(doc *SchematicDoc, layout *SchematicLayout) int {
	if layout == nil || len(layout.Symbols) == 0 {
		return 0
	}

	// Restore sheet definitions if saved
	if len(layout.Sheets) > 0 {
		doc.Sheets = layout.Sheets
	}

	restored := 0
	for _, sym := range doc.Symbols {
		if sl, ok := layout.Symbols[sym.ID]; ok {
			sym.X = sl.X
			sym.Y = sl.Y
			sym.FlipH = sl.FlipH
			sym.FlipV = sl.FlipV
			sym.Rotation = sl.Rotation
			sym.Sheet = sl.Sheet
			restored++
		}
	}
	// Recompute pin positions after applying layout
	for _, sym := range doc.Symbols {
		def := GetSymbolDef(sym.GateType,
			countPinsByDir(sym, "input"),
			countPinsByDir(sym, "output"),
			countPinsByDir(sym, "enable"),
			countPinsByDir(sym, "clock"))
		ComputePinPositions(sym, def)
	}

	// Reposition power ports and regenerate off-sheet connectors before routing
	positionPowerPorts(doc)
	generateOffSheetConnectors(doc)

	// Restore off-sheet connector transforms if saved
	if layout.OffSheetConnectors != nil {
		for _, osc := range doc.OffSheetConnectors {
			key := fmt.Sprintf("%d:%s", osc.Sheet, osc.NetID)
			if ocl, ok := layout.OffSheetConnectors[key]; ok {
				osc.X = ocl.X
				osc.Y = ocl.Y
				osc.FlipH = ocl.FlipH
				osc.FlipV = ocl.FlipV
				osc.Rotation = ocl.Rotation
			}
		}
	}

	RouteAllWires(doc)
	regenerateNetLabels(doc)

	return restored
}
