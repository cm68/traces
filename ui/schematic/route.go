package schematic

import (
	"fmt"
	"math"
	"sort"

	"pcb-tracer/pkg/geometry"
)

// ManhattanRoute computes an orthogonal path between two pin positions.
// Returns waypoints for a horizontal-first or vertical-first L-shaped path,
// choosing the variant that best matches signal flow (left→right).
func ManhattanRoute(from, to pinPos) []geometry.Point2D {
	p1 := geometry.Point2D{X: from.X, Y: from.Y}
	p2 := geometry.Point2D{X: to.X, Y: to.Y}

	// Same point
	if math.Abs(p1.X-p2.X) < 1 && math.Abs(p1.Y-p2.Y) < 1 {
		return []geometry.Point2D{p1, p2}
	}

	// Same horizontal line
	if math.Abs(p1.Y-p2.Y) < 1 {
		return []geometry.Point2D{p1, p2}
	}

	// Same vertical line
	if math.Abs(p1.X-p2.X) < 1 {
		return []geometry.Point2D{p1, p2}
	}

	// Determine routing based on pin directions:
	// Output (right side) → horizontal first
	// Input (left side) → the other end probably goes horizontal first
	if from.Dir == "output" || to.Dir == "input" {
		// Horizontal first: go right from output, then vertical, then right to input
		midX := (p1.X + p2.X) / 2
		return []geometry.Point2D{
			p1,
			{X: midX, Y: p1.Y},
			{X: midX, Y: p2.Y},
			p2,
		}
	}

	// Default: L-shaped, horizontal first
	return []geometry.Point2D{
		p1,
		{X: p2.X, Y: p1.Y},
		p2,
	}
}

// RouteAllWires creates wire paths for all nets, routing per-sheet.
func RouteAllWires(doc *SchematicDoc) {
	if doc == nil {
		return
	}
	doc.Wires = nil

	// Route each sheet independently
	sheets := doc.Sheets
	if len(sheets) == 0 {
		sheets = []Sheet{{Number: 1}}
	}
	wireID := 0
	for _, sheet := range sheets {
		wireID = routeSheetWires(doc, sheet.Number, wireID)
	}
}

// routeSheetWires routes wires for symbols on a specific sheet.
// Returns the updated wireID counter.
func routeSheetWires(doc *SchematicDoc, sheetNum int, wireID int) int {
	// Build pin-to-net index for this sheet only
	netPins := make(map[string][]pinPos)
	for _, sym := range doc.Symbols {
		if effectiveSheet(sym.Sheet) != sheetNum {
			continue
		}
		for _, pin := range sym.Pins {
			if pin.NetID != "" {
				netPins[pin.NetID] = append(netPins[pin.NetID], pinPos{
					X: pin.X, Y: pin.Y, Dir: pin.Direction,
				})
			}
		}
	}
	// Skip power nets — they use local power port symbols, not wires
	for netID := range doc.PowerNetIDs {
		delete(netPins, netID)
	}

	// Include off-sheet connector positions as wire endpoints
	for _, osc := range doc.OffSheetConnectors {
		if osc.Sheet != sheetNum {
			continue
		}
		netPins[osc.NetID] = append(netPins[osc.NetID], pinPos{
			X: osc.X, Y: osc.Y, Dir: osc.Direction,
		})
	}

	for netID, pins := range netPins {
		if len(pins) < 2 {
			continue
		}

		// For 2-pin nets: simple Manhattan route
		if len(pins) == 2 {
			wireID++
			doc.Wires = append(doc.Wires, &Wire{
				ID:     fmt.Sprintf("wire-%d", wireID),
				NetID:  netID,
				Points: ManhattanRoute(pins[0], pins[1]),
				Sheet:  sheetNum,
			})
			continue
		}

		// For multi-pin nets: build minimum spanning tree, route each edge
		edges := mstEdges(pins)
		for _, edge := range edges {
			wireID++
			doc.Wires = append(doc.Wires, &Wire{
				ID:     fmt.Sprintf("wire-%d", wireID),
				NetID:  netID,
				Points: ManhattanRoute(pins[edge[0]], pins[edge[1]]),
				Sheet:  sheetNum,
			})
		}
	}
	return wireID
}

// mstEdges returns edges of a minimum spanning tree using Kruskal's algorithm.
// Returns pairs of indices into the pins slice.
func mstEdges(pins []pinPos) [][2]int {
	type edge struct {
		i, j int
		dist float64
	}
	n := len(pins)

	// Build all edges with Manhattan distances
	var edges []edge
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			d := math.Abs(pins[i].X-pins[j].X) + math.Abs(pins[i].Y-pins[j].Y)
			edges = append(edges, edge{i, j, d})
		}
	}
	sort.Slice(edges, func(a, b int) bool {
		return edges[a].dist < edges[b].dist
	})

	// Union-Find
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) bool {
		ra, rb := find(a), find(b)
		if ra == rb {
			return false
		}
		parent[ra] = rb
		return true
	}

	var result [][2]int
	for _, e := range edges {
		if union(e.i, e.j) {
			result = append(result, [2]int{e.i, e.j})
		}
		if len(result) == n-1 {
			break
		}
	}
	return result
}
