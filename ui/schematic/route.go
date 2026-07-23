package schematic

import (
	"fmt"
	"math"
	"sort"

	"pcb-tracer/pkg/geometry"
)

// Collision-aware Manhattan routing.
//
// KiCad's connectivity model joins wires that touch at endpoints, wire
// endpoints that land anywhere on another wire segment, and pins whose
// connection point lies on a wire. A router that ignores other nets therefore
// produces electrical shorts in the exported schematic. To avoid that, nets
// are routed sequentially against an obstacle index of everything already
// placed, and each connection picks the best candidate path that creates no
// foreign contact.

const routeEps = 0.75 // coordinate comparison tolerance (all routing is on the 5-unit grid)

// obSegment is an axis-aligned wire segment owned by a net.
type obSegment struct {
	X1, Y1, X2, Y2 float64
	NetID          string
}

// obPoint is a connection point owned by a net (wire endpoint or pin tip).
// An empty NetID marks an unconnected pin, which no wire may touch.
type obPoint struct {
	X, Y  float64
	NetID string
}

// obstacleIndex holds everything a new wire must not electrically touch.
type obstacleIndex struct {
	segs   []obSegment
	points []obPoint // wire endpoints and corners
	tips   []obPoint // pin and power-port connection points
}

// buildObstacles indexes the sheet's pins, power-port stubs, and existing
// wires, except wires of nets in skipNets (the nets about to be rerouted).
func buildObstacles(doc *SchematicDoc, sheetNum int, skipNets map[string]bool) *obstacleIndex {
	oi := &obstacleIndex{}

	symByID := make(map[string]*PlacedSymbol)
	for _, sym := range doc.Symbols {
		symByID[sym.ID] = sym
		if effectiveSheet(sym.Sheet) != sheetNum {
			continue
		}
		for _, pin := range sym.Pins {
			oi.tips = append(oi.tips, obPoint{X: pin.X, Y: pin.Y, NetID: pin.NetID})
		}
	}

	for _, osc := range doc.OffSheetConnectors {
		if effectiveSheet(osc.Sheet) != sheetNum {
			continue
		}
		oi.tips = append(oi.tips, obPoint{X: osc.X, Y: osc.Y, NetID: osc.NetID})
	}

	// Power-port stubs become short wires in the KiCad export, and the port
	// position itself is a connection point (power pin + PWR_FLAG).
	for _, pp := range doc.PowerPorts {
		if effectiveSheet(pp.Sheet) != sheetNum {
			continue
		}
		netID := ""
		if sym := symByID[pp.OwnerSymbolID]; sym != nil {
			for _, pin := range sym.Pins {
				if pin.PinNumber == pp.OwnerPinNum {
					netID = pin.NetID
					break
				}
			}
		}
		oi.tips = append(oi.tips, obPoint{X: pp.X, Y: pp.Y, NetID: netID})
		oi.segs = append(oi.segs, obSegment{X1: pp.X, Y1: pp.Y, X2: pp.PinX, Y2: pp.PinY, NetID: netID})
	}

	for _, w := range doc.Wires {
		if effectiveSheet(w.Sheet) != sheetNum || skipNets[w.NetID] {
			continue
		}
		oi.addPath(w.NetID, w.Points)
	}

	return oi
}

// addPath registers a routed path's segments and endpoints as obstacles.
func (oi *obstacleIndex) addPath(netID string, pts []geometry.Point2D) {
	for i, p := range pts {
		oi.points = append(oi.points, obPoint{X: p.X, Y: p.Y, NetID: netID})
		if i > 0 {
			q := pts[i-1]
			if math.Abs(p.X-q.X) < routeEps && math.Abs(p.Y-q.Y) < routeEps {
				continue // zero-length
			}
			oi.segs = append(oi.segs, obSegment{X1: q.X, Y1: q.Y, X2: p.X, Y2: p.Y, NetID: netID})
		}
	}
}

// onSegment reports whether point (px,py) lies on the axis-aligned segment.
func onSegment(px, py float64, s obSegment) bool {
	if math.Abs(s.X1-s.X2) < routeEps { // vertical
		lo, hi := math.Min(s.Y1, s.Y2), math.Max(s.Y1, s.Y2)
		return math.Abs(px-s.X1) < routeEps && py > lo-routeEps && py < hi+routeEps
	}
	// horizontal
	lo, hi := math.Min(s.X1, s.X2), math.Max(s.X1, s.X2)
	return math.Abs(py-s.Y1) < routeEps && px > lo-routeEps && px < hi+routeEps
}

// collinearOverlap reports whether two segments lie on the same line and
// share more than a single point.
func collinearOverlap(a, b obSegment) bool {
	aV := math.Abs(a.X1-a.X2) < routeEps
	bV := math.Abs(b.X1-b.X2) < routeEps
	if aV != bV {
		return false
	}
	if aV {
		if math.Abs(a.X1-b.X1) >= routeEps {
			return false
		}
		lo1, hi1 := math.Min(a.Y1, a.Y2), math.Max(a.Y1, a.Y2)
		lo2, hi2 := math.Min(b.Y1, b.Y2), math.Max(b.Y1, b.Y2)
		return math.Min(hi1, hi2)-math.Max(lo1, lo2) > routeEps
	}
	if math.Abs(a.Y1-b.Y1) >= routeEps {
		return false
	}
	lo1, hi1 := math.Min(a.X1, a.X2), math.Max(a.X1, a.X2)
	lo2, hi2 := math.Min(b.X1, b.X2), math.Max(b.X1, b.X2)
	return math.Min(hi1, hi2)-math.Max(lo1, lo2) > routeEps
}

// pathValid reports whether the path creates no electrical contact with a
// foreign net: no foreign pin tip or wire endpoint on any of our segments,
// and none of our waypoints on a foreign segment or pin tip.
//
// Contacts exactly at the path terminals are ignored: a foreign pin
// coincident with our pin is a placement-level short the router cannot avoid,
// and rejecting every path would only add a long fallback wire through the
// middle of the sheet on top of it.
func (oi *obstacleIndex) pathValid(pts []geometry.Point2D, netID string) bool {
	atTerminal := func(x, y float64) bool {
		for _, t := range []geometry.Point2D{pts[0], pts[len(pts)-1]} {
			if math.Abs(x-t.X) < routeEps && math.Abs(y-t.Y) < routeEps {
				return true
			}
		}
		return false
	}
	for i := 1; i < len(pts); i++ {
		seg := obSegment{X1: pts[i-1].X, Y1: pts[i-1].Y, X2: pts[i].X, Y2: pts[i].Y}
		for _, t := range oi.tips {
			if t.NetID != netID && onSegment(t.X, t.Y, seg) && !atTerminal(t.X, t.Y) {
				return false
			}
		}
		for _, p := range oi.points {
			if p.NetID != netID && onSegment(p.X, p.Y, seg) && !atTerminal(p.X, p.Y) {
				return false
			}
		}
	}
	for _, p := range pts {
		for _, s := range oi.segs {
			if s.NetID != netID && onSegment(p.X, p.Y, s) && !atTerminal(p.X, p.Y) {
				return false
			}
		}
		for _, t := range oi.tips {
			if t.NetID != netID && math.Abs(p.X-t.X) < routeEps && math.Abs(p.Y-t.Y) < routeEps &&
				!atTerminal(p.X, p.Y) {
				return false
			}
		}
	}
	return true
}

// pathScore rates a valid path: length plus a bend penalty plus a penalty for
// running on top of same-net wires (harmless but noisy).
func (oi *obstacleIndex) pathScore(pts []geometry.Point2D, netID string) float64 {
	length := 0.0
	for i := 1; i < len(pts); i++ {
		length += math.Abs(pts[i].X-pts[i-1].X) + math.Abs(pts[i].Y-pts[i-1].Y)
	}
	score := length + 30*float64(len(pts)-2)
	for i := 1; i < len(pts); i++ {
		seg := obSegment{X1: pts[i-1].X, Y1: pts[i-1].Y, X2: pts[i].X, Y2: pts[i].Y}
		for _, s := range oi.segs {
			if s.NetID == netID && collinearOverlap(seg, s) {
				score += 40
				break
			}
		}
	}
	return score
}

// dedupPath removes consecutive duplicate waypoints.
func dedupPath(pts []geometry.Point2D) []geometry.Point2D {
	out := pts[:0:0]
	for _, p := range pts {
		if n := len(out); n > 0 &&
			math.Abs(out[n-1].X-p.X) < routeEps && math.Abs(out[n-1].Y-p.Y) < routeEps {
			continue
		}
		out = append(out, p)
	}
	return out
}

// RouteFallbackCount counts edges where no collision-free path was found and
// the plain Manhattan route was used. Reset by RouteAllWires; useful for
// diagnosing residual shorts in exports.
var RouteFallbackCount int

// RouteFallbackEdges records the first few fallback edges for diagnostics.
var RouteFallbackEdges []string

// midCandidates returns grid-aligned channel positions between a and b,
// nearest the midpoint first, plus detour channels just outside the span for
// when the pins align or the span is congested.
func midCandidates(a, b, insideMax, step, outsideMax float64) []float64 {
	lo, hi := math.Min(a, b), math.Max(a, b)
	center := snap5((a + b) / 2)
	seen := make(map[float64]bool)
	var out []float64
	push := func(v float64) {
		v = snap5(v)
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for off := 0.0; off <= insideMax; off += step {
		if v := center - off; v > lo+routeEps && v < hi-routeEps {
			push(v)
		}
		if off == 0 {
			continue
		}
		if v := center + off; v > lo+routeEps && v < hi-routeEps {
			push(v)
		}
	}
	for off := step; off <= outsideMax; off += step {
		push(lo - off)
		push(hi + off)
	}
	return out
}

// candidatePaths generates direct, L-shaped, and Z/U-shaped candidates
// between two pin positions. wide=true searches a much larger channel range.
func candidatePaths(from, to pinPos, wide bool) [][]geometry.Point2D {
	insideMax, step, outsideMax := 120.0, 10.0, 60.0
	if wide {
		insideMax, step, outsideMax = 600, 5, 300
	}
	p1 := geometry.Point2D{X: from.X, Y: from.Y}
	p2 := geometry.Point2D{X: to.X, Y: to.Y}
	var out [][]geometry.Point2D
	add := func(pts ...geometry.Point2D) {
		if d := dedupPath(pts); len(d) >= 2 {
			out = append(out, d)
		}
	}

	if math.Abs(p1.X-p2.X) < routeEps || math.Abs(p1.Y-p2.Y) < routeEps {
		add(p1, p2)
	}
	add(p1, geometry.Point2D{X: p2.X, Y: p1.Y}, p2)
	add(p1, geometry.Point2D{X: p1.X, Y: p2.Y}, p2)
	for _, m := range midCandidates(p1.X, p2.X, insideMax, step, outsideMax) {
		add(p1, geometry.Point2D{X: m, Y: p1.Y}, geometry.Point2D{X: m, Y: p2.Y}, p2)
	}
	for _, m := range midCandidates(p1.Y, p2.Y, insideMax, step, outsideMax) {
		add(p1, geometry.Point2D{X: p1.X, Y: m}, geometry.Point2D{X: p2.X, Y: m}, p2)
	}
	return out
}

// escapeOffsets returns small horizontal offsets for leaving a pin, natural
// exit direction (outputs exit right, inputs left) first.
func escapeOffsets(dir string) []float64 {
	base := []float64{10, 15, 20, 25, 30, 40}
	var out []float64
	sign := 1.0
	if dir != "output" {
		sign = -1
	}
	for _, d := range base {
		out = append(out, sign*d)
	}
	for _, d := range base {
		out = append(out, -sign*d)
	}
	return out
}

// escapePaths generates 5-segment paths for congested cases where both
// endpoints sit on crowded pin rows/columns: a short horizontal escape off
// each pin, joined by a free horizontal trunk row.
//
//	p1 → (a,y1) → (a,m) → (b,m) → (b,y2) → p2
//
// Returns the first valid path found, trying near offsets and central trunk
// rows first.
func escapePaths(from, to pinPos, netID string, oi *obstacleIndex) []geometry.Point2D {
	p1 := geometry.Point2D{X: from.X, Y: from.Y}
	p2 := geometry.Point2D{X: to.X, Y: to.Y}
	mids := midCandidates(p1.Y, p2.Y, 600, 10, 300)
	for _, m := range mids {
		for _, da := range escapeOffsets(from.Dir) {
			a := snap5(p1.X + da)
			for _, db := range escapeOffsets(to.Dir) {
				b := snap5(p2.X + db)
				c := dedupPath([]geometry.Point2D{
					p1,
					{X: a, Y: p1.Y},
					{X: a, Y: m},
					{X: b, Y: m},
					{X: b, Y: p2.Y},
					p2,
				})
				if oi.pathValid(c, netID) {
					return c
				}
			}
		}
	}
	return nil
}

// routeEdge picks the best collision-free candidate path between two pins,
// widening the search if the normal candidate set is fully blocked. If every
// candidate collides, it falls back to the plain Manhattan route.
func routeEdge(from, to pinPos, netID string, oi *obstacleIndex) []geometry.Point2D {
	for _, wide := range []bool{false, true} {
		var best []geometry.Point2D
		bestScore := math.Inf(1)
		for _, c := range candidatePaths(from, to, wide) {
			if !oi.pathValid(c, netID) {
				continue
			}
			if s := oi.pathScore(c, netID); s < bestScore {
				best, bestScore = c, s
			}
		}
		if best != nil {
			return best
		}
	}
	if c := escapePaths(from, to, netID, oi); c != nil {
		return c
	}
	RouteFallbackCount++
	if len(RouteFallbackEdges) < 20 {
		// Diagnose: what blocks a simple jog 15 units right of the source?
		p1 := geometry.Point2D{X: from.X, Y: from.Y}
		p2 := geometry.Point2D{X: to.X, Y: to.Y}
		m := snap5(math.Max(from.X, to.X) + 15)
		probe := dedupPath([]geometry.Point2D{p1, {X: m, Y: p1.Y}, {X: m, Y: p2.Y}, p2})
		RouteFallbackEdges = append(RouteFallbackEdges,
			fmt.Sprintf("net=%s (%.0f,%.0f)->(%.0f,%.0f) probe@%v: %s",
				netID, from.X, from.Y, to.X, to.Y, m, oi.explainBlock(probe, netID)))
	}
	return ManhattanRoute(from, to)
}

// explainBlock describes the first obstacle that invalidates a path.
func (oi *obstacleIndex) explainBlock(pts []geometry.Point2D, netID string) string {
	for i := 1; i < len(pts); i++ {
		seg := obSegment{X1: pts[i-1].X, Y1: pts[i-1].Y, X2: pts[i].X, Y2: pts[i].Y}
		for _, t := range oi.tips {
			if t.NetID != netID && onSegment(t.X, t.Y, seg) {
				return fmt.Sprintf("tip net=%q at (%.1f,%.1f) on seg %d", t.NetID, t.X, t.Y, i)
			}
		}
		for _, p := range oi.points {
			if p.NetID != netID && onSegment(p.X, p.Y, seg) {
				return fmt.Sprintf("wirept net=%q at (%.1f,%.1f) on seg %d", p.NetID, p.X, p.Y, i)
			}
		}
	}
	for _, p := range pts {
		for _, s := range oi.segs {
			if s.NetID != netID && onSegment(p.X, p.Y, s) {
				return fmt.Sprintf("waypoint (%.1f,%.1f) on seg net=%q (%.1f,%.1f)-(%.1f,%.1f)",
					p.X, p.Y, s.NetID, s.X1, s.Y1, s.X2, s.Y2)
			}
		}
		for _, t := range oi.tips {
			if t.NetID != netID && math.Abs(p.X-t.X) < routeEps && math.Abs(p.Y-t.Y) < routeEps {
				return fmt.Sprintf("waypoint (%.1f,%.1f) on tip net=%q", p.X, p.Y, t.NetID)
			}
		}
	}
	return "valid?!"
}

// ManhattanRoute computes an orthogonal path between two pin positions.
// Returns waypoints for a horizontal-first or vertical-first L-shaped path,
// choosing the variant that best matches signal flow (left→right).
// Used as the fallback when no collision-free candidate exists.
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
		midX := snap5((p1.X + p2.X) / 2)
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

// routeNetsOnSheet routes the given nets on one sheet, avoiding electrical
// contact with pins, power stubs, and wires of other nets. Wires for the nets
// being routed must already have been removed from doc.Wires for this sheet.
// Returns the updated wireID counter.
func routeNetsOnSheet(doc *SchematicDoc, sheetNum int, netPins map[string][]pinPos, wireID int) int {
	skip := make(map[string]bool, len(netPins))
	for netID := range netPins {
		skip[netID] = true
	}
	oi := buildObstacles(doc, sheetNum, skip)

	// Deterministic order: bus-like nets (most pins) claim channels first.
	netIDs := make([]string, 0, len(netPins))
	for netID := range netPins {
		netIDs = append(netIDs, netID)
	}
	sort.Slice(netIDs, func(i, j int) bool {
		ni, nj := len(netPins[netIDs[i]]), len(netPins[netIDs[j]])
		if ni != nj {
			return ni > nj
		}
		return netIDs[i] < netIDs[j]
	})

	for _, netID := range netIDs {
		pins := netPins[netID]
		if len(pins) < 2 {
			continue
		}
		for _, edge := range mstEdges(pins) {
			path := routeEdge(pins[edge[0]], pins[edge[1]], netID, oi)
			oi.addPath(netID, path)
			wireID++
			doc.Wires = append(doc.Wires, &Wire{
				ID:     fmt.Sprintf("wire-%d", wireID),
				NetID:  netID,
				Points: path,
				Sheet:  sheetNum,
			})
		}
	}
	return wireID
}

// RouteAllWires creates wire paths for all nets, routing per-sheet.
func RouteAllWires(doc *SchematicDoc) {
	if doc == nil {
		return
	}
	doc.Wires = nil
	RouteFallbackCount = 0
	RouteFallbackEdges = nil

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
	// For power nets: keep only non-"power"-direction pins for routing.
	// Supply pins (direction="power") are represented by PowerPort symbols.
	// Signal-function pins (input/enable/clock/output) tied to GND/VCC are
	// logically significant and need wires to show the connection.
	for netID := range doc.PowerNetIDs {
		pins := netPins[netID]
		var signalPins []pinPos
		for _, p := range pins {
			if p.Dir != "power" {
				signalPins = append(signalPins, p)
			}
		}
		if len(signalPins) < 2 {
			delete(netPins, netID) // nothing to route
		} else {
			netPins[netID] = signalPins
		}
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

	return routeNetsOnSheet(doc, sheetNum, netPins, wireID)
}

// regenerateNetLabels rebuilds doc.NetLabels from the current wire set.
// Labels are placed at the midpoint of each wire's first segment and carry the
// wire's sheet, so they are always in sync with routed wires regardless of
// how symbols have been moved between sheets.
func regenerateNetLabels(doc *SchematicDoc) {
	doc.NetLabels = nil

	// Build a netID→name fallback from symbol pins and off-sheet connectors.
	netNames := make(map[string]string)
	for _, sym := range doc.Symbols {
		for _, pin := range sym.Pins {
			if pin.NetID != "" && pin.NetName != "" {
				netNames[pin.NetID] = pin.NetName
			}
		}
	}
	for _, osc := range doc.OffSheetConnectors {
		if osc.NetID != "" && osc.NetName != "" {
			netNames[osc.NetID] = osc.NetName
		}
	}

	// Index all wire segments per sheet so label anchors can avoid points
	// where a foreign net's wire crosses: a KiCad label attaches to every
	// wire under its anchor, so a label on a crossing merges the nets.
	type labSeg struct {
		x1, y1, x2, y2 float64
		netID          string
		sheet          int
	}
	var segs []labSeg
	for _, w := range doc.Wires {
		for i := 1; i < len(w.Points); i++ {
			segs = append(segs, labSeg{
				x1: w.Points[i-1].X, y1: w.Points[i-1].Y,
				x2: w.Points[i].X, y2: w.Points[i].Y,
				netID: w.NetID, sheet: effectiveSheet(w.Sheet),
			})
		}
	}
	anchorClear := func(x, y float64, netID string, sheet int) bool {
		for _, s := range segs {
			if s.sheet != sheet || s.netID == netID {
				continue
			}
			if onSegment(x, y, obSegment{X1: s.x1, Y1: s.y1, X2: s.x2, Y2: s.y2}) {
				return false
			}
		}
		return true
	}

	for _, wire := range doc.Wires {
		netName := wire.NetName
		if netName == "" {
			netName = netNames[wire.NetID]
		}
		if netName == "" || len(wire.Points) < 2 {
			continue
		}
		sheet := effectiveSheet(wire.Sheet)

		// Walk the wire's segments for a grid point clear of foreign wires,
		// starting from each segment's midpoint and stepping outward.
		ax := snap5((wire.Points[0].X + wire.Points[1].X) / 2)
		ay := snap5((wire.Points[0].Y + wire.Points[1].Y) / 2)
	search:
		for i := 1; i < len(wire.Points); i++ {
			p, q := wire.Points[i-1], wire.Points[i]
			mx, my := snap5((p.X+q.X)/2), snap5((p.Y+q.Y)/2)
			for off := 0.0; off <= 200; off += 10 {
				for _, sgn := range []float64{1, -1} {
					x, y := mx, my
					if math.Abs(p.Y-q.Y) < routeEps { // horizontal
						x = mx + sgn*off
						if x < math.Min(p.X, q.X)+2 || x > math.Max(p.X, q.X)-2 {
							continue
						}
					} else {
						y = my + sgn*off
						if y < math.Min(p.Y, q.Y)+2 || y > math.Max(p.Y, q.Y)-2 {
							continue
						}
					}
					if anchorClear(x, y, wire.NetID, sheet) {
						ax, ay = x, y
						break search
					}
					if off == 0 {
						break
					}
				}
			}
		}

		doc.NetLabels = append(doc.NetLabels, &NetLabel{
			NetID:   wire.NetID,
			NetName: netName,
			X:       ax,
			Y:       ay - 8,
			Sheet:   sheet,
		})
	}
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
