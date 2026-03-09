package schematic

import (
	"sort"
)

const (
	colSpacing = 600.0  // Horizontal spacing between columns
	rowSpacing = 200.0  // Vertical spacing between rows
	startX     = 200.0  // Left margin
	startY     = 200.0  // Top margin
)

// AutoLayout arranges symbols in a left-to-right signal flow.
// Input connectors on the left, logic gates in the middle by depth,
// output connectors on the right.
func AutoLayout(doc *SchematicDoc) {
	if doc == nil || len(doc.Symbols) == 0 {
		return
	}

	// Step 1: Build a directed graph of signal flow
	// edge: output pin's net → input pin's net (connecting symbols)
	type symInfo struct {
		sym     *PlacedSymbol
		idx     int
		column  int
		row     int
		isInput bool // connector providing input
		isOutput bool // connector receiving output
	}

	syms := make([]*symInfo, len(doc.Symbols))
	symByID := make(map[string]*symInfo)
	for i, s := range doc.Symbols {
		si := &symInfo{sym: s, idx: i, column: -1}
		syms[i] = si
		symByID[s.ID] = si
	}

	// Build net-to-symbols mapping
	// For each net: which symbols have output pins on it, which have input pins
	type netRole struct {
		outputs []string // symbol IDs with output pins on this net
		inputs  []string // symbol IDs with input pins on this net
	}
	netRoles := make(map[string]*netRole)

	for _, si := range syms {
		for _, pin := range si.sym.Pins {
			if pin.NetID == "" {
				continue
			}
			nr := netRoles[pin.NetID]
			if nr == nil {
				nr = &netRole{}
				netRoles[pin.NetID] = nr
			}
			if pin.Direction == "output" {
				nr.outputs = append(nr.outputs, si.sym.ID)
			} else if pin.Direction == "input" || pin.Direction == "clock" {
				nr.inputs = append(nr.inputs, si.sym.ID)
			}
		}
	}

	// Build adjacency: sym → downstream symbols
	downstream := make(map[string][]string) // symID → list of downstream symIDs
	upstream := make(map[string][]string)
	inDegree := make(map[string]int)

	for _, si := range syms {
		inDegree[si.sym.ID] = 0
	}

	for _, nr := range netRoles {
		for _, outSym := range nr.outputs {
			for _, inSym := range nr.inputs {
				if outSym != inSym {
					downstream[outSym] = append(downstream[outSym], inSym)
					upstream[inSym] = append(upstream[inSym], outSym)
					inDegree[inSym]++
				}
			}
		}
	}

	// Step 2: Classify connectors
	for _, si := range syms {
		if si.sym.GateType == "CONNECTOR" {
			hasOutputPin := false
			hasInputPin := false
			for _, pin := range si.sym.Pins {
				if pin.Direction == "output" {
					hasOutputPin = true
				}
				if pin.Direction == "input" {
					hasInputPin = true
				}
			}
			// A connector with an "input" pin (signal comes FROM connector) is a schematic input
			// A connector with an "output" pin (signal goes TO connector) is a schematic output
			if hasInputPin && !hasOutputPin {
				// This connector is an input source (e.g., address bus from CPU)
				si.isInput = true
			} else if hasOutputPin && !hasInputPin {
				si.isOutput = true
			} else {
				// Default: if connector has downstream connections, it's an input
				if len(downstream[si.sym.ID]) > 0 {
					si.isInput = true
				} else {
					si.isOutput = true
				}
			}
		}
	}

	// Step 3: Topological sort (Kahn's algorithm) for column assignment
	queue := make([]string, 0)
	for _, si := range syms {
		if inDegree[si.sym.ID] == 0 || si.isInput {
			si.column = 0
			queue = append(queue, si.sym.ID)
		}
	}

	visited := make(map[string]bool)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			continue
		}
		visited[id] = true

		si := symByID[id]
		if si == nil {
			continue
		}
		for _, downID := range downstream[id] {
			dsi := symByID[downID]
			if dsi == nil {
				continue
			}
			newCol := si.column + 1
			if newCol > dsi.column {
				dsi.column = newCol
			}
			inDegree[downID]--
			if inDegree[downID] <= 0 {
				queue = append(queue, downID)
			}
		}
	}

	// Assign unvisited symbols (cycles) to column 1
	for _, si := range syms {
		if si.column < 0 {
			si.column = 1
		}
	}

	// Force output connectors to the last column
	maxCol := 0
	for _, si := range syms {
		if si.column > maxCol {
			maxCol = si.column
		}
	}
	for _, si := range syms {
		if si.isOutput {
			si.column = maxCol + 1
		}
	}
	// Recompute maxCol
	maxCol = 0
	for _, si := range syms {
		if si.column > maxCol {
			maxCol = si.column
		}
	}

	// Step 4: Group by column and assign rows
	columns := make(map[int][]*symInfo)
	for _, si := range syms {
		columns[si.column] = append(columns[si.column], si)
	}

	// Sort within each column by barycenter of upstream connections
	for col := 0; col <= maxCol; col++ {
		colSyms := columns[col]
		if col > 0 {
			// Compute barycenter: average row of upstream symbols
			for _, si := range colSyms {
				sum := 0.0
				count := 0
				for _, upID := range upstream[si.sym.ID] {
					upSI := symByID[upID]
					if upSI != nil && upSI.row >= 0 {
						sum += float64(upSI.row)
						count++
					}
				}
				if count > 0 {
					si.row = int(sum / float64(count))
				}
			}
		}
		sort.Slice(colSyms, func(i, j int) bool {
			if colSyms[i].row != colSyms[j].row {
				return colSyms[i].row < colSyms[j].row
			}
			return colSyms[i].sym.ID < colSyms[j].sym.ID
		})
		for i, si := range colSyms {
			si.row = i
		}
	}

	// Step 5: Assign absolute coordinates.
	// Columns use fixed X spacing. Within each column we stack symbols with
	// a gap derived from their actual body heights to prevent overlap.
	const colGap = 40.0 // minimum vertical gap between symbol bodies

	for col := 0; col <= maxCol; col++ {
		colSyms := columns[col]
		// Sort by the row index assigned above
		sort.Slice(colSyms, func(a, b int) bool {
			return colSyms[a].row < colSyms[b].row
		})
		nextY := startY
		for _, si := range colSyms {
			def := GetSymbolDef(si.sym.GateType,
				countPinsByDir(si.sym, "input"),
				countPinsByDir(si.sym, "output"),
				countPinsByDir(si.sym, "enable"),
				countPinsByDir(si.sym, "clock"))
			halfH := 50.0 // fallback half-height
			if def != nil {
				halfH = def.BodyHeight/2 + stubLength
			}
			// Place symbol centre so its top clears nextY
			si.sym.Y = nextY + halfH
			si.sym.X = startX + float64(col)*colSpacing
			si.sym.Column = col
			// Advance nextY past this symbol's bottom
			nextY = si.sym.Y + halfH + colGap
		}
	}

}
