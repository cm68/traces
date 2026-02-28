package trace

import (
	"image"
	"math"

	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// Terminal represents a destination point (via or connector) that a skeleton walk can reach.
type Terminal struct {
	Center geometry.Point2D
	Radius float64
	ID     string
}

// WalkResult contains the outcome of a walk from the source via.
type WalkResult struct {
	Path       []geometry.Point2D
	TerminalID string // ID of the terminal reached
	Reason     string // "terminal", "dead_end", "boundary", "max_steps"
}

// FloodResult holds all results of a copper flood fill.
type FloodResult struct {
	Walks    []WalkResult  // paths to terminals reached
	Explored []image.Point // grid points that passed the copper probe (for visualization)
}

// FloodFillCopper explores outward from center on a grayscale image by probing
// circles of probeRadius pixels. A position is "copper" if at least minFraction
// of the probe circle pixels exceed threshold. Steps by stepSize pixels in 8
// directions (giving 50% overlap between probes). Returns paths to all reachable
// terminators via BFS parent-pointer reconstruction.
func FloodFillCopper(gray gocv.Mat, center geometry.Point2D, excludeRadius float64,
	terminators []Terminal, probeRadius, stepSize int, threshold uint8, minFraction float64) FloodResult {

	rows, cols := gray.Rows(), gray.Cols()

	// Precompute probe circle offsets
	var probeOffsets []image.Point
	for dy := -probeRadius; dy <= probeRadius; dy++ {
		for dx := -probeRadius; dx <= probeRadius; dx++ {
			if dx*dx+dy*dy <= probeRadius*probeRadius {
				probeOffsets = append(probeOffsets, image.Point{X: dx, Y: dy})
			}
		}
	}

	// Check if a position is "copper" — minFraction of probe circle pixels > threshold
	isCopper := func(x, y int) bool {
		total := 0
		bright := 0
		for _, off := range probeOffsets {
			px, py := x+off.X, y+off.Y
			if px < 0 || px >= cols || py < 0 || py >= rows {
				continue
			}
			total++
			if gray.GetUCharAt(py, px) > threshold {
				bright++
			}
		}
		if total == 0 {
			return false
		}
		return float64(bright)/float64(total) >= minFraction
	}

	// BFS on a grid with spacing = stepSize
	start := image.Point{X: int(math.Round(center.X)), Y: int(math.Round(center.Y))}
	excludeR2 := excludeRadius * excludeRadius

	visited := make(map[image.Point]bool)
	parent := make(map[image.Point]image.Point)
	visited[start] = true

	queue := []image.Point{start}
	var explored []image.Point

	// 8-connected steps
	dirs := [8]image.Point{
		{0, -stepSize}, {stepSize, -stepSize}, {stepSize, 0}, {stepSize, stepSize},
		{0, stepSize}, {-stepSize, stepSize}, {-stepSize, 0}, {-stepSize, -stepSize},
	}

	// Track which terminators we've reached (first arrival = shortest path)
	foundTerminals := make(map[string]image.Point)

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		// Check terminators at current position
		for _, t := range terminators {
			dx := float64(cur.X) - t.Center.X
			dy := float64(cur.Y) - t.Center.Y
			if dx*dx+dy*dy <= t.Radius*t.Radius {
				if _, already := foundTerminals[t.ID]; !already {
					foundTerminals[t.ID] = cur
				}
			}
		}

		// Explore 8 neighbors
		for _, d := range dirs {
			next := image.Point{X: cur.X + d.X, Y: cur.Y + d.Y}
			if visited[next] {
				continue
			}
			if next.X < probeRadius || next.X >= cols-probeRadius ||
				next.Y < probeRadius || next.Y >= rows-probeRadius {
				continue
			}

			// Inside source via exclusion zone: always passable
			edx := float64(next.X) - center.X
			edy := float64(next.Y) - center.Y
			inExclude := edx*edx+edy*edy < excludeR2

			// Inside any terminal: always passable
			inTerminal := false
			if !inExclude {
				for _, t := range terminators {
					tdx := float64(next.X) - t.Center.X
					tdy := float64(next.Y) - t.Center.Y
					if tdx*tdx+tdy*tdy < t.Radius*t.Radius {
						inTerminal = true
						break
					}
				}
			}

			if !inExclude && !inTerminal && !isCopper(next.X, next.Y) {
				continue
			}

			visited[next] = true
			parent[next] = cur
			queue = append(queue, next)
			if !inExclude {
				explored = append(explored, next)
			}
		}
	}

	// Reconstruct paths for found terminals
	var walks []WalkResult
	for termID, termPt := range foundTerminals {
		var path []geometry.Point2D
		cur := termPt
		for {
			path = append(path, geometry.Point2D{X: float64(cur.X), Y: float64(cur.Y)})
			p, ok := parent[cur]
			if !ok {
				break
			}
			cur = p
		}
		// Reverse to get start→terminal order
		for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
			path[i], path[j] = path[j], path[i]
		}
		walks = append(walks, WalkResult{
			Path:       path,
			TerminalID: termID,
			Reason:     "terminal",
		})
	}

	return FloodResult{Walks: walks, Explored: explored}
}
