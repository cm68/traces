package schematic

import (
	"math"
	"regexp"
	"sort"
	"strconv"
)

const (
	colSpacing = 600.0 // Horizontal spacing between columns
	rowSpacing = 200.0 // Vertical spacing between rows
	startX     = 200.0 // Left margin
	startY     = 200.0 // Top margin
)

// symInfo is per-symbol layout state.
type symInfo struct {
	sym      *PlacedSymbol
	column   int
	row      int
	isInput  bool // connector providing input
	isOutput bool // connector receiving output

	busFamily string // indexed net family this symbol belongs to ("DO", "A", ...)
	busBit    int
	hasBus    bool
}

var busIndexRe = regexp.MustCompile(`^(.+?)(\d+)$`)

// detectBusTags finds indexed net-name families (DO0..DO7, A0..A15) with at
// least four members and returns netID → (family, bit).
func detectBusTags(doc *SchematicDoc) map[string]symInfo {
	type nameBit struct {
		family string
		bit    int
	}
	netName := make(map[string]string)
	for _, sym := range doc.Symbols {
		for _, pin := range sym.Pins {
			if pin.NetID != "" && pin.NetName != "" {
				netName[pin.NetID] = pin.NetName
			}
		}
	}

	parsed := make(map[string]nameBit) // netID → candidate
	familyCount := make(map[string]int)
	for netID, name := range netName {
		m := busIndexRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		bit, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		parsed[netID] = nameBit{family: m[1], bit: bit}
		familyCount[m[1]]++
	}

	tags := make(map[string]symInfo)
	for netID, nb := range parsed {
		if familyCount[nb.family] >= 4 {
			tags[netID] = symInfo{busFamily: nb.family, busBit: nb.bit, hasBus: true}
		}
	}
	return tags
}

// AutoLayout arranges symbols in a left-to-right signal flow: input
// connectors on the left, logic by topological depth, output connectors on
// the right. Rows are ordered bus-aware (bit i of every stage on the same
// row) and refined with barycenter sweeps; Y coordinates align each symbol
// with its upstream partners so wires run straight.
func AutoLayout(doc *SchematicDoc) {
	if doc == nil || len(doc.Symbols) == 0 {
		return
	}

	// ── Graph construction ─────────────────────────────────────────────
	syms := make([]*symInfo, len(doc.Symbols))
	symByID := make(map[string]*symInfo)
	for i, s := range doc.Symbols {
		si := &symInfo{sym: s, column: -1}
		syms[i] = si
		symByID[s.ID] = si
	}

	// For each net: which symbols drive it, which consume it
	type netRole struct {
		outputs []string
		inputs  []string
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

	downstream := make(map[string][]string)
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

	// Undirected connectivity for row ordering and Y alignment — bus nets
	// connect stages regardless of modeled pin direction.
	neighbors := make(map[string][]string)
	for _, nr := range netRoles {
		var members []string
		members = append(members, nr.outputs...)
		members = append(members, nr.inputs...)
		for _, a := range members {
			for _, b := range members {
				if a != b {
					neighbors[a] = append(neighbors[a], b)
				}
			}
		}
	}

	// ── Bus tagging ────────────────────────────────────────────────────
	busTags := detectBusTags(doc)
	for _, si := range syms {
		// A symbol's bus identity: the tagged net most of its pins agree on;
		// ties broken toward the lowest bit (stable).
		famVotes := make(map[string]int)
		best := symInfo{}
		for _, pin := range si.sym.Pins {
			if t, ok := busTags[pin.NetID]; ok {
				famVotes[t.busFamily]++
			}
		}
		bestFam, bestVotes := "", 0
		for f, v := range famVotes {
			if v > bestVotes || (v == bestVotes && f < bestFam) {
				bestFam, bestVotes = f, v
			}
		}
		if bestFam != "" {
			best.busFamily = bestFam
			best.busBit = 1 << 20
			for _, pin := range si.sym.Pins {
				if t, ok := busTags[pin.NetID]; ok && t.busFamily == bestFam && t.busBit < best.busBit {
					best.busBit = t.busBit
				}
			}
			best.hasBus = true
		}
		si.busFamily, si.busBit, si.hasBus = best.busFamily, best.busBit, best.hasBus
	}

	// ── Connector classification ───────────────────────────────────────
	for _, si := range syms {
		if si.sym.GateType != "CONNECTOR" {
			continue
		}
		hasOutputPin, hasInputPin := false, false
		for _, pin := range si.sym.Pins {
			if pin.Direction == "output" {
				hasOutputPin = true
			}
			if pin.Direction == "input" {
				hasInputPin = true
			}
		}
		switch {
		case hasInputPin && !hasOutputPin:
			si.isInput = true
		case hasOutputPin && !hasInputPin:
			si.isOutput = true
		default:
			if len(downstream[si.sym.ID]) > 0 {
				si.isInput = true
			} else {
				si.isOutput = true
			}
		}
	}

	// ── Column assignment (Kahn) ───────────────────────────────────────
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
			if si.column+1 > dsi.column {
				dsi.column = si.column + 1
			}
			inDegree[downID]--
			if inDegree[downID] <= 0 {
				queue = append(queue, downID)
			}
		}
	}
	for _, si := range syms {
		if si.column < 0 {
			si.column = 1
		}
	}
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
	maxCol = 0
	for _, si := range syms {
		if si.column > maxCol {
			maxCol = si.column
		}
	}

	columns := make([][]*symInfo, maxCol+1)
	for _, si := range syms {
		columns[si.column] = append(columns[si.column], si)
	}

	// ── Initial row order: bus families in bit order, then everything else ──
	for _, col := range columns {
		sort.SliceStable(col, func(i, j int) bool {
			a, b := col[i], col[j]
			if a.hasBus != b.hasBus {
				return a.hasBus // bus-connected symbols first, as blocks
			}
			if a.hasBus {
				if a.busFamily != b.busFamily {
					return a.busFamily < b.busFamily
				}
				if a.busBit != b.busBit {
					return a.busBit < b.busBit
				}
			}
			return a.sym.ID < b.sym.ID
		})
		for r, si := range col {
			si.row = r
		}
	}

	// ── Barycenter sweeps over neighbor rows (both directions) ─────────
	reorder := func(col []*symInfo) {
		if len(col) < 2 {
			return
		}
		bary := make(map[*symInfo]float64)
		for _, si := range col {
			sum, count := 0.0, 0
			for _, nID := range neighbors[si.sym.ID] {
				n := symByID[nID]
				if n != nil && n.column != si.column {
					sum += float64(n.row)
					count++
				}
			}
			if count > 0 {
				bary[si] = sum / float64(count)
			} else {
				bary[si] = float64(si.row)
			}
		}
		sort.SliceStable(col, func(i, j int) bool {
			if bary[col[i]] != bary[col[j]] {
				return bary[col[i]] < bary[col[j]]
			}
			a, b := col[i], col[j]
			if a.hasBus && b.hasBus && a.busFamily == b.busFamily {
				return a.busBit < b.busBit
			}
			return a.sym.ID < b.sym.ID
		})
		for r, si := range col {
			si.row = r
		}
	}
	for sweep := 0; sweep < 4; sweep++ {
		for c := 1; c <= maxCol; c++ {
			reorder(columns[c])
		}
		for c := maxCol - 1; c >= 0; c-- {
			reorder(columns[c])
		}
	}

	// ── Coordinate assignment ──────────────────────────────────────────
	// X by column, compacted to content width; Y aligns each symbol with the
	// mean Y of its already-placed partners (left columns first), pushed
	// apart to avoid overlap.
	const colGap = 40.0
	halfWidth := func(si *symInfo) float64 {
		def := GetSymbolDef(si.sym.GateType,
			countPinsByDir(si.sym, "input"),
			countPinsByDir(si.sym, "output"),
			countPinsByDir(si.sym, "enable"),
			countPinsByDir(si.sym, "clock"))
		if def == nil {
			return 100.0
		}
		return def.BodyWidth/2 + stubLength
	}
	colX := make([]float64, maxCol+1)
	{
		const colGapX = 250.0
		x := startX
		var prevHalf float64
		for c := 0; c <= maxCol; c++ {
			half := 100.0
			for _, si := range columns[c] {
				if hw := halfWidth(si); hw > half {
					half = hw
				}
			}
			if c > 0 {
				x += prevHalf + colGapX + half
			}
			colX[c] = x
			prevHalf = half
		}
	}
	halfHeight := func(si *symInfo) float64 {
		def := GetSymbolDef(si.sym.GateType,
			countPinsByDir(si.sym, "input"),
			countPinsByDir(si.sym, "output"),
			countPinsByDir(si.sym, "enable"),
			countPinsByDir(si.sym, "clock"))
		if def == nil {
			return 50.0
		}
		return def.BodyHeight/2 + stubLength
	}
	placed := make(map[string]bool)

	for c := 0; c <= maxCol; c++ {
		col := columns[c]
		type slot struct {
			si      *symInfo
			desired float64
			half    float64
		}
		slots := make([]slot, len(col))
		for i, si := range col {
			desired := startY + float64(si.row)*rowSpacing
			sum, count := 0.0, 0
			for _, nID := range neighbors[si.sym.ID] {
				n := symByID[nID]
				if n != nil && placed[nID] && n.column < c {
					sum += n.sym.Y
					count++
				}
			}
			if count > 0 {
				desired = sum / float64(count)
			}
			slots[i] = slot{si: si, desired: desired, half: halfHeight(si)}
		}
		// Keep the swept row order; resolve overlaps downward from the top.
		sort.SliceStable(slots, func(i, j int) bool {
			return slots[i].si.row < slots[j].si.row
		})
		minY := startY
		for i := range slots {
			y := math.Max(slots[i].desired, minY+slots[i].half)
			slots[i].si.sym.Y = snap5(y)
			slots[i].si.sym.X = snap5(colX[c])
			slots[i].si.sym.Column = c
			slots[i].si.sym.Row = slots[i].si.row
			minY = y + slots[i].half + colGap
			placed[slots[i].si.sym.ID] = true
		}
	}
}
