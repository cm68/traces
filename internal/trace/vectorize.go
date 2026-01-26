package trace

import (
	"fmt"
	"image"
	"math"
	"sort"
	"time"

	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// TraceSource indicates how a trace was created.
type TraceSource int

const (
	// SourceDetected indicates the trace was automatically detected.
	SourceDetected TraceSource = iota
	// SourceManual indicates the trace was manually drawn by the user.
	SourceManual
	// SourceInferred indicates the trace was inferred from electrical testing.
	// Used for traces under components that can't be seen in scans.
	SourceInferred
)

func (s TraceSource) String() string {
	switch s {
	case SourceDetected:
		return "Detected"
	case SourceManual:
		return "Manual"
	case SourceInferred:
		return "Inferred"
	default:
		return "Unknown"
	}
}

// ExtendedTrace adds source tracking and bounds to the basic Trace.
type ExtendedTrace struct {
	Trace
	Source TraceSource    // How the trace was created
	Bounds geometry.RectInt // Bounding box for hit testing
}

// VectorizeOptions configures trace vectorization.
type VectorizeOptions struct {
	MinPathLength    int     // Minimum path length in pixels to keep
	SimplifyEpsilon  float64 // Douglas-Peucker simplification tolerance
	MaxWidth         float64 // Maximum trace width to consider valid
	MergeDistance    float64 // Distance to merge nearby path endpoints
}

// DefaultVectorizeOptions returns sensible defaults for vectorization.
func DefaultVectorizeOptions() VectorizeOptions {
	return VectorizeOptions{
		MinPathLength:   20,   // Ignore very short traces (noise)
		SimplifyEpsilon: 2.0,  // Reduce path vertex count
		MaxWidth:        100,  // Maximum trace width in pixels
		MergeDistance:   5.0,  // Merge endpoints within 5 pixels
	}
}

// VectorizeTraces converts a copper mask into vectorized trace paths.
func VectorizeTraces(copperMask gocv.Mat, layer TraceLayer, opts VectorizeOptions) []ExtendedTrace {
	if copperMask.Empty() {
		return nil
	}

	// Step 1: Skeletonize the mask to get centerlines
	skeleton := skeletonize(copperMask)
	defer skeleton.Close()

	// Step 2: Extract paths from skeleton
	paths := extractPaths(skeleton, opts.MinPathLength)

	// Step 3: Simplify paths to reduce vertex count
	for i := range paths {
		paths[i] = simplifyPath(paths[i], opts.SimplifyEpsilon)
	}

	// Step 4: Estimate widths by measuring distance to mask edge
	widths := estimateWidths(paths, copperMask, opts.MaxWidth)

	// Step 5: Create trace objects
	traces := make([]ExtendedTrace, len(paths))
	for i, path := range paths {
		traces[i] = ExtendedTrace{
			Trace: Trace{
				ID:     fmt.Sprintf("trace-%s-%03d", layerPrefix(layer), i+1),
				Layer:  layer,
				Points: path,
				Width:  widths[i],
			},
			Source: SourceDetected,
			Bounds: pathBounds(path),
		}
	}

	return traces
}

// skeletonize reduces a binary mask to single-pixel-wide lines.
// Uses morphological thinning via iterative erosion.
func skeletonize(mask gocv.Mat) gocv.Mat {
	// Clone the mask since we'll modify it
	skeleton := gocv.NewMatWithSize(mask.Rows(), mask.Cols(), gocv.MatTypeCV8U)
	temp := mask.Clone()
	defer temp.Close()

	eroded := gocv.NewMat()
	defer eroded.Close()

	element := gocv.GetStructuringElement(gocv.MorphCross, image.Point{3, 3})
	defer element.Close()

	for {
		// Erode
		gocv.Erode(temp, &eroded, element)

		// Dilate eroded image
		dilated := gocv.NewMat()
		gocv.Dilate(eroded, &dilated, element)

		// Subtract dilated from temp to get skeleton pixels
		diff := gocv.NewMat()
		gocv.Subtract(temp, dilated, &diff)
		dilated.Close()

		// Add to skeleton
		gocv.BitwiseOr(skeleton, diff, &skeleton)
		diff.Close()

		// Copy eroded to temp for next iteration
		eroded.CopyTo(&temp)

		// Check if we're done (eroded image is empty)
		if gocv.CountNonZero(eroded) == 0 {
			break
		}
	}

	return skeleton
}

// extractPaths traces connected pixels in the skeleton to form paths.
func extractPaths(skeleton gocv.Mat, minLength int) [][]geometry.Point2D {
	// Find contours on skeleton
	contours := gocv.FindContours(skeleton, gocv.RetrievalList, gocv.ChainApproxNone)
	defer contours.Close()

	var paths [][]geometry.Point2D

	for i := 0; i < contours.Size(); i++ {
		contour := contours.At(i)

		// Convert contour points to our Point2D type
		var path []geometry.Point2D
		for j := 0; j < contour.Size(); j++ {
			pt := contour.At(j)
			path = append(path, geometry.Point2D{X: float64(pt.X), Y: float64(pt.Y)})
		}

		// Filter by minimum length
		if len(path) >= minLength {
			paths = append(paths, path)
		}
	}

	return paths
}

// simplifyPath reduces the number of vertices using Douglas-Peucker algorithm.
func simplifyPath(path []geometry.Point2D, epsilon float64) []geometry.Point2D {
	if len(path) <= 2 {
		return path
	}

	// Find point with maximum distance from line between first and last points
	dmax := 0.0
	index := 0
	end := len(path) - 1

	for i := 1; i < end; i++ {
		d := perpendicularDistance(path[i], path[0], path[end])
		if d > dmax {
			dmax = d
			index = i
		}
	}

	// If max distance is greater than epsilon, recursively simplify
	if dmax > epsilon {
		// Recursive call
		left := simplifyPath(path[:index+1], epsilon)
		right := simplifyPath(path[index:], epsilon)

		// Build result (avoid duplicating middle point)
		result := make([]geometry.Point2D, 0, len(left)+len(right)-1)
		result = append(result, left[:len(left)-1]...)
		result = append(result, right...)
		return result
	}

	// All points between first and last are within epsilon, return just endpoints
	return []geometry.Point2D{path[0], path[end]}
}

// perpendicularDistance calculates the perpendicular distance from point p to line a-b.
func perpendicularDistance(p, a, b geometry.Point2D) float64 {
	dx := b.X - a.X
	dy := b.Y - a.Y

	if dx == 0 && dy == 0 {
		// a and b are the same point
		return math.Sqrt((p.X-a.X)*(p.X-a.X) + (p.Y-a.Y)*(p.Y-a.Y))
	}

	// Calculate perpendicular distance
	num := math.Abs(dy*p.X - dx*p.Y + b.X*a.Y - b.Y*a.X)
	den := math.Sqrt(dx*dx + dy*dy)
	return num / den
}

// estimateWidths estimates trace width for each path by sampling mask coverage.
func estimateWidths(paths [][]geometry.Point2D, mask gocv.Mat, maxWidth float64) []float64 {
	widths := make([]float64, len(paths))

	for i, path := range paths {
		if len(path) == 0 {
			widths[i] = 2.0 // Default width
			continue
		}

		// Sample width at multiple points along path by counting perpendicular mask pixels
		var totalWidth float64
		sampleCount := 0

		step := max(1, len(path)/10) // Sample ~10 points
		for j := step; j < len(path)-step; j += step {
			pt := path[j]
			x, y := int(pt.X), int(pt.Y)

			if x >= 0 && x < mask.Cols() && y >= 0 && y < mask.Rows() {
				// Estimate local direction from neighbors
				prev := path[max(0, j-step)]
				next := path[min(len(path)-1, j+step)]
				dx := next.X - prev.X
				dy := next.Y - prev.Y
				length := math.Sqrt(dx*dx + dy*dy)
				if length == 0 {
					continue
				}

				// Perpendicular direction
				px, py := -dy/length, dx/length

				// Count pixels perpendicular to trace
				width := 0.0
				for d := -int(maxWidth / 2); d <= int(maxWidth/2); d++ {
					sx := x + int(float64(d)*px)
					sy := y + int(float64(d)*py)
					if sx >= 0 && sx < mask.Cols() && sy >= 0 && sy < mask.Rows() {
						if mask.GetUCharAt(sy, sx) > 0 {
							width++
						}
					}
				}

				if width > 0 && width < maxWidth {
					totalWidth += width
					sampleCount++
				}
			}
		}

		if sampleCount > 0 {
			widths[i] = totalWidth / float64(sampleCount)
		} else {
			widths[i] = 2.0 // Default width
		}
	}

	return widths
}

// pathBounds calculates the bounding box for a path.
func pathBounds(path []geometry.Point2D) geometry.RectInt {
	if len(path) == 0 {
		return geometry.RectInt{}
	}

	minX, minY := path[0].X, path[0].Y
	maxX, maxY := path[0].X, path[0].Y

	for _, pt := range path[1:] {
		if pt.X < minX {
			minX = pt.X
		}
		if pt.X > maxX {
			maxX = pt.X
		}
		if pt.Y < minY {
			minY = pt.Y
		}
		if pt.Y > maxY {
			maxY = pt.Y
		}
	}

	return geometry.RectInt{
		X:      int(minX),
		Y:      int(minY),
		Width:  int(maxX-minX) + 1,
		Height: int(maxY-minY) + 1,
	}
}

// layerPrefix returns a short prefix for trace IDs.
func layerPrefix(layer TraceLayer) string {
	switch layer {
	case LayerFront:
		return "F"
	case LayerBack:
		return "B"
	default:
		return "U"
	}
}

// HitTestTrace checks if a point is near the trace path.
func HitTestTrace(trace ExtendedTrace, x, y float64, tolerance float64) bool {
	// Quick bounds check first
	b := trace.Bounds
	if x < float64(b.X)-tolerance || x > float64(b.X+b.Width)+tolerance ||
		y < float64(b.Y)-tolerance || y > float64(b.Y+b.Height)+tolerance {
		return false
	}

	// Check distance to each line segment
	for i := 0; i < len(trace.Points)-1; i++ {
		if pointToSegmentDistance(x, y, trace.Points[i], trace.Points[i+1]) <= tolerance+trace.Width/2 {
			return true
		}
	}

	return false
}

// pointToSegmentDistance calculates minimum distance from point to line segment.
func pointToSegmentDistance(px, py float64, a, b geometry.Point2D) float64 {
	dx := b.X - a.X
	dy := b.Y - a.Y

	if dx == 0 && dy == 0 {
		// Segment is a point
		return math.Sqrt((px-a.X)*(px-a.X) + (py-a.Y)*(py-a.Y))
	}

	// Parameter t of closest point on infinite line
	t := ((px-a.X)*dx + (py-a.Y)*dy) / (dx*dx + dy*dy)

	// Clamp to segment
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}

	// Closest point on segment
	closestX := a.X + t*dx
	closestY := a.Y + t*dy

	return math.Sqrt((px-closestX)*(px-closestX) + (py-closestY)*(py-closestY))
}

// CreateManualTrace creates a user-drawn trace.
func CreateManualTrace(points []geometry.Point2D, width float64, layer TraceLayer) ExtendedTrace {
	return ExtendedTrace{
		Trace: Trace{
			ID:     fmt.Sprintf("trace-manual-%d", time.Now().UnixNano()),
			Layer:  layer,
			Points: points,
			Width:  width,
		},
		Source: SourceManual,
		Bounds: pathBounds(points),
	}
}

// CreateInferredTrace creates a trace inferred from electrical testing.
// Used for connections under components that can't be detected visually.
func CreateInferredTrace(startViaID, endViaID string, startPt, endPt geometry.Point2D, layer TraceLayer) ExtendedTrace {
	points := []geometry.Point2D{startPt, endPt}

	return ExtendedTrace{
		Trace: Trace{
			ID:     fmt.Sprintf("trace-inferred-%d", time.Now().UnixNano()),
			Layer:  layer,
			Points: points,
			Width:  2.0, // Default width for inferred traces
			Net:    fmt.Sprintf("%s-%s", startViaID, endViaID),
		},
		Source: SourceInferred,
		Bounds: pathBounds(points),
	}
}

// MergeResults combines detection results from multiple sources.
func MergeResults(results ...*DetectionResult) *DetectionResult {
	if len(results) == 0 {
		return nil
	}

	merged := &DetectionResult{
		Layer: results[0].Layer,
	}

	for _, r := range results {
		if r == nil {
			continue
		}
		merged.Traces = append(merged.Traces, r.Traces...)
	}

	return merged
}

// SortTracesByLength sorts traces by path length (longest first).
func SortTracesByLength(traces []ExtendedTrace) {
	sort.Slice(traces, func(i, j int) bool {
		return pathLength(traces[i].Points) > pathLength(traces[j].Points)
	})
}

// pathLength calculates the total length of a path.
func pathLength(points []geometry.Point2D) float64 {
	if len(points) < 2 {
		return 0
	}

	var total float64
	for i := 1; i < len(points); i++ {
		dx := points[i].X - points[i-1].X
		dy := points[i].Y - points[i-1].Y
		total += math.Sqrt(dx*dx + dy*dy)
	}
	return total
}
