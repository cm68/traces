// Package component provides component detection for PCB images.
package component

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"pcb-tracer/pkg/geometry"
	"pcb-tracer/ui/canvas"

	"gocv.io/x/gocv"
)

// ColorProfile defines a color range for a component type (e.g. black IC, grey IC).
type ColorProfile struct {
	ValueMin float64 // Minimum V value for this profile
	ValueMax float64 // Maximum V value for this profile
	SatMax   float64 // Maximum saturation for this profile
}

// DetectionParams controls the component detection behavior.
type DetectionParams struct {
	// Color thresholds for black plastic (HSV) - used as fallback if no profiles
	ValueMax float64 // Maximum V value for black (0-255)
	SatMax   float64 // Maximum S value (black is low saturation)

	// Multiple color profiles from training samples (e.g. black ICs, grey ICs)
	// Cell matches if it fits ANY profile
	ColorProfiles []ColorProfile

	// Size constraints (in mm, converted to pixels using DPI)
	MinWidth  float64 // Minimum component width in mm
	MaxWidth  float64 // Maximum component width in mm
	MinHeight float64 // Minimum component height in mm
	MaxHeight float64 // Maximum component height in mm

	// Aspect ratio constraints
	MinAspectRatio float64 // Minimum height/width ratio
	MaxAspectRatio float64 // Maximum height/width ratio

	// Quality filters
	MinSolidity    float64 // Minimum area/convex_hull_area ratio (rectangularity)
	MinWhitePixels float64 // Minimum percentage of white pixels inside (markings)

	// Grid cell size derived from training data (mm). 0 = use default (2mm).
	CellSizeMM float64

	// Expected component sizes from training data
	SizeTemplates []SizeTemplate
}

// DefaultParams returns default detection parameters for DIP packages.
func DefaultParams() DetectionParams {
	return DetectionParams{
		// Black plastic detection (low value, low saturation)
		ValueMax: 100, // Allow somewhat dark (black ICs can be 40-100)
		SatMax:   120, // Allow moderate saturation

		// DIP package size range (mm)
		MinWidth:  4.0,  // Narrower than DIP-8
		MaxWidth:  25.0, // Wider than DIP-40
		MinHeight: 6.0,  // Shorter than DIP-8
		MaxHeight: 70.0, // Longer than DIP-40

		// Aspect ratio (height/width for vertically oriented DIPs)
		MinAspectRatio: 1.0, // Allow square-ish
		MaxAspectRatio: 10.0, // Very elongated allowed

		// Quality
		MinSolidity:    0.6,  // Fairly rectangular
		MinWhitePixels: 0.0,  // Don't require white markings (some ICs don't have them)
	}
}

// DetectionResult contains the results of component detection.
type DetectionResult struct {
	Components []*Component
	DebugImage gocv.Mat // Optional debug visualization

	// Intermediate grid data for visualization
	Grid          []byte            // Cell classification grid (0/1), row-major, post-morph
	GridCols      int               // Grid columns
	GridRows      int               // Grid rows
	CellSizePx    int              // Cell size in pixels
	ScanX         int               // Grid origin X in image coordinates
	ScanY         int               // Grid origin Y in image coordinates
	Regions       []image.Rectangle // Connected regions in grid coordinates
	RefinedBounds []image.Rectangle // Refined pixel bounds after dense-core + green trim
	HSVBytes      []byte            // Raw HSV pixel data for visualization
	HSVWidth      int               // Image width for HSV byte indexing
	HSVChannels   int               // Number of channels (3)
}

// DetectComponents finds DIP components in an image.
func DetectComponents(img image.Image, dpi float64, params DetectionParams) (*DetectionResult, error) {
	// Convert Go image to OpenCV Mat
	mat, err := imageToMat(img)
	if err != nil {
		return nil, fmt.Errorf("convert image: %w", err)
	}
	defer mat.Close()

	return DetectComponentsMat(mat, dpi, params)
}

// DetectComponentsWithBounds finds DIP components in an image, constrained to board bounds.
func DetectComponentsWithBounds(img image.Image, dpi float64, params DetectionParams, boardBounds *geometry.Rect) (*DetectionResult, error) {
	mat, err := imageToMat(img)
	if err != nil {
		return nil, fmt.Errorf("convert image: %w", err)
	}
	defer mat.Close()
	return DetectComponentsMatWithBounds(mat, dpi, params, boardBounds)
}

// DetectComponentsMat finds DIP components using grid-based scanning.
// Divides image into 2mm cells, classifies each cell, then merges adjacent component cells.
// Uses boardBounds to constrain detection to the actual board area (nil = use full image).
func DetectComponentsMat(img gocv.Mat, dpi float64, params DetectionParams) (*DetectionResult, error) {
	return DetectComponentsMatWithBounds(img, dpi, params, nil)
}

// DetectComponentsMatWithBounds finds DIP components using grid-based scanning with explicit board bounds.
// Uses training-derived cell size and parallel scoring for performance.
func DetectComponentsMatWithBounds(img gocv.Mat, dpi float64, params DetectionParams, boardBounds *geometry.Rect) (*DetectionResult, error) {
	if img.Empty() {
		return nil, fmt.Errorf("empty image")
	}

	// Determine scan region
	scanX, scanY := 0, 0
	scanW, scanH := img.Cols(), img.Rows()
	if boardBounds != nil && boardBounds.Width > 0 && boardBounds.Height > 0 {
		scanX = int(boardBounds.X)
		scanY = int(boardBounds.Y)
		scanW = int(boardBounds.Width)
		scanH = int(boardBounds.Height)
		if scanX < 0 {
			scanX = 0
		}
		if scanY < 0 {
			scanY = 0
		}
		if scanX+scanW > img.Cols() {
			scanW = img.Cols() - scanX
		}
		if scanY+scanH > img.Rows() {
			scanH = img.Rows() - scanY
		}
		fmt.Printf("Board bounds: (%d,%d) %dx%d px\n", scanX, scanY, scanW, scanH)
	}

	// Cell size: from training data or default 2mm
	mmToPixels := dpi / 25.4
	cellMM := params.CellSizeMM
	if cellMM <= 0 {
		cellMM = 2.0
	}
	cellSizePx := int(cellMM * mmToPixels)
	if cellSizePx < 4 {
		cellSizePx = 4
	}

	gridRows := (scanH + cellSizePx - 1) / cellSizePx
	gridCols := (scanW + cellSizePx - 1) / cellSizePx

	fmt.Printf("Grid detection: DPI=%.0f, cell=%.2fmm (%dpx), grid=%dx%d (%d cells)\n",
		dpi, cellMM, cellSizePx, gridCols, gridRows, gridCols*gridRows)

	// Convert to HSV once, then extract raw bytes for parallel access
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(img, &hsv, gocv.ColorBGRToHSV)

	hsvBytes := hsv.ToBytes()
	imgW := hsv.Cols()
	imgH := hsv.Rows()
	channels := hsv.Channels() // 3

	// Parallel cell scoring
	grid := make([]byte, gridRows*gridCols) // 0 or 1
	var hitCount int64

	numWorkers := runtime.NumCPU()
	if numWorkers > gridRows {
		numWorkers = gridRows
	}
	if numWorkers < 1 {
		numWorkers = 1
	}

	var wg sync.WaitGroup
	rowsPerWorker := (gridRows + numWorkers - 1) / numWorkers

	for w := 0; w < numWorkers; w++ {
		startRow := w * rowsPerWorker
		endRow := startRow + rowsPerWorker
		if endRow > gridRows {
			endRow = gridRows
		}
		if startRow >= endRow {
			continue
		}

		wg.Add(1)
		go func(startRow, endRow int) {
			defer wg.Done()
			localHits := 0
			for gy := startRow; gy < endRow; gy++ {
				for gx := 0; gx < gridCols; gx++ {
					x1 := scanX + gx*cellSizePx
					y1 := scanY + gy*cellSizePx
					x2 := x1 + cellSizePx
					y2 := y1 + cellSizePx
					if x2 > scanX+scanW {
						x2 = scanX + scanW
					}
					if y2 > scanY+scanH {
						y2 = scanY + scanH
					}
					if x2 > imgW {
						x2 = imgW
					}
					if y2 > imgH {
						y2 = imgH
					}

					if classifyCellBytes(hsvBytes, imgW, channels, x1, y1, x2, y2, params) {
						grid[gy*gridCols+gx] = 1
						localHits++
					}
				}
			}
			atomic.AddInt64(&hitCount, int64(localHits))
		}(startRow, endRow)
	}
	wg.Wait()

	totalCells := gridRows * gridCols
	fmt.Printf("Grid scan: %d of %d cells classified as component (%.1f%%) [%d workers]\n",
		hitCount, totalCells, float64(hitCount)/float64(totalCells)*100, numWorkers)

	// Morphological opening on the grid to remove thin features (e.g. pin rows).
	// Erode: a cell survives only if it has >= 3 of 4 cardinal neighbors also set.
	// Dilate: any cell adjacent (cardinal) to a surviving cell gets set.
	grid = morphOpenGrid(grid, gridCols, gridRows)

	// Flood fill to find connected regions
	visited := make([]byte, gridRows*gridCols)
	var regions []image.Rectangle

	for gy := 0; gy < gridRows; gy++ {
		for gx := 0; gx < gridCols; gx++ {
			idx := gy*gridCols + gx
			if grid[idx] == 1 && visited[idx] == 0 {
				region := floodFillFlat(grid, visited, gx, gy, gridCols, gridRows)
				regions = append(regions, region)
			}
		}
	}

	fmt.Printf("Found %d connected regions\n", len(regions))

	// Debug stage 1: return grid + raw regions for visualization
	if false {
	return &DetectionResult{
		Grid:       grid,
		GridCols:   gridCols,
		GridRows:   gridRows,
		CellSizePx: cellSizePx,
		ScanX:      scanX,
		ScanY:      scanY,
		Regions:    regions,
	}, nil
	} // end debug stage 1

	// Debug stage 2a: size prefilter only (no refine)
	var survivingRegions []image.Rectangle

	for i, region := range regions {
		x1 := scanX + region.Min.X*cellSizePx
		y1 := scanY + region.Min.Y*cellSizePx
		x2 := scanX + region.Max.X*cellSizePx
		y2 := scanY + region.Max.Y*cellSizePx
		if x2 > scanX+scanW {
			x2 = scanX + scanW
		}
		if y2 > scanY+scanH {
			y2 = scanY + scanH
		}

		rawW := float64(x2 - x1)
		rawH := float64(y2 - y1)
		rawWMM := rawW / mmToPixels
		rawHMM := rawH / mmToPixels
		regionCells := (region.Max.X - region.Min.X) * (region.Max.Y - region.Min.Y)

		fmt.Printf("  Region %d: grid %dx%d (%d cells) at (%d,%d) raw %.1fx%.1f mm\n",
			i+1, region.Max.X-region.Min.X, region.Max.Y-region.Min.Y,
			regionCells, x1, y1, rawWMM, rawHMM)

		// Quick size pre-filter: skip regions obviously too small or too large
		minDim := rawWMM
		if rawHMM < minDim {
			minDim = rawHMM
		}
		maxDim := rawWMM
		if rawHMM > maxDim {
			maxDim = rawHMM
		}
		if minDim < params.MinWidth {
			fmt.Printf("    REJECT: too narrow (min dim %.1f mm < %.1f mm)\n",
				minDim, params.MinWidth)
			continue
		}
		if maxDim < params.MinHeight {
			fmt.Printf("    REJECT: too short (max dim %.1f mm < %.1f mm)\n",
				maxDim, params.MinHeight)
			continue
		}
		if maxDim > params.MaxHeight {
			fmt.Printf("    REJECT: too large (max dim %.1f mm > threshold %.1f mm)\n",
				maxDim, params.MaxHeight)
			continue
		}
		aspect := maxDim / minDim
		if aspect < params.MinAspectRatio {
			fmt.Printf("    REJECT: aspect ratio %.1f < min %.1f\n",
				aspect, params.MinAspectRatio)
			continue
		}
		if aspect > params.MaxAspectRatio {
			fmt.Printf("    REJECT: aspect ratio %.1f > max %.1f\n",
				aspect, params.MaxAspectRatio)
			continue
		}

		fmt.Printf("    SURVIVE prefilter (%.1fx%.1f mm, aspect %.1f)\n", rawWMM, rawHMM, aspect)
		survivingRegions = append(survivingRegions, region)
	}

	fmt.Printf("Prefilter: %d of %d regions survived\n", len(survivingRegions), len(regions))

	// Debug stage 2a: return after prefilter only
	if false {
	return &DetectionResult{
		Grid:       grid,
		GridCols:   gridCols,
		GridRows:   gridRows,
		CellSizePx: cellSizePx,
		ScanX:      scanX,
		ScanY:      scanY,
		Regions:    survivingRegions,
	}, nil
	} // end debug stage 2a

	// --- Stage 2b: refine bounds (green trim + dense-core) ---
	var refinedBounds []image.Rectangle

	for i, region := range survivingRegions {
		x1 := scanX + region.Min.X*cellSizePx
		y1 := scanY + region.Min.Y*cellSizePx
		x2 := scanX + region.Max.X*cellSizePx
		y2 := scanY + region.Max.Y*cellSizePx
		if x2 > scanX+scanW {
			x2 = scanX + scanW
		}
		if y2 > scanY+scanH {
			y2 = scanY + scanH
		}

		// Refine bounds by scanning actual HSV pixels at the edges
		rx1, ry1, rx2, ry2 := refineClusterBounds(hsvBytes, imgW, imgH, channels,
			x1, y1, x2, y2, params)

		width := float64(rx2 - rx1)
		height := float64(ry2 - ry1)
		widthMM := width / mmToPixels
		heightMM := height / mmToPixels

		fmt.Printf("  Region %d: Refined (%d,%d)-(%d,%d) = %.1fx%.1f mm\n",
			i+1, rx1, ry1, rx2, ry2, widthMM, heightMM)

		refinedBounds = append(refinedBounds, image.Rect(rx1, ry1, rx2, ry2))
	}

	fmt.Printf("Refined: %d regions\n", len(refinedBounds))

	return &DetectionResult{
		Grid:          grid,
		GridCols:      gridCols,
		GridRows:      gridRows,
		CellSizePx:    cellSizePx,
		ScanX:         scanX,
		ScanY:         scanY,
		Regions:       survivingRegions,
		RefinedBounds: refinedBounds,
		HSVBytes:      hsvBytes,
		HSVWidth:      imgW,
		HSVChannels:   channels,
	}, nil
}

// classifyCellBytes classifies a cell using raw HSV byte data (no gocv.Mat allocation).
// Returns true if >= 40% of pixels match any color profile.
func classifyCellBytes(hsvBytes []byte, imgW, channels, x1, y1, x2, y2 int, params DetectionParams) bool {
	const matchThreshold = 0.40

	matchCount := 0
	totalPixels := 0

	if len(params.ColorProfiles) > 0 {
		for y := y1; y < y2; y++ {
			rowStart := (y*imgW + x1) * channels
			for x := x1; x < x2; x++ {
				offset := rowStart + (x-x1)*channels
				s := float64(hsvBytes[offset+1])
				v := float64(hsvBytes[offset+2])
				totalPixels++

				for _, p := range params.ColorProfiles {
					if v >= p.ValueMin && v <= p.ValueMax && s <= p.SatMax {
						matchCount++
						break
					}
				}
			}
		}
	} else {
		for y := y1; y < y2; y++ {
			rowStart := (y*imgW + x1) * channels
			for x := x1; x < x2; x++ {
				offset := rowStart + (x-x1)*channels
				s := float64(hsvBytes[offset+1])
				v := float64(hsvBytes[offset+2])
				totalPixels++

				if v <= params.ValueMax && s <= params.SatMax {
					matchCount++
				}
			}
		}
	}

	if totalPixels == 0 {
		return false
	}
	return float64(matchCount)/float64(totalPixels) > matchThreshold
}

// morphOpenGrid applies morphological opening (erode then dilate) to remove thin
// features like pin rows while preserving compact body regions.
// Erode: a cell survives only if >= 3 of its 4 cardinal neighbors are also set.
// Dilate: a cleared cell becomes set if any cardinal neighbor is set (post-erosion).
func morphOpenGrid(grid []byte, cols, rows int) []byte {
	total := rows * cols

	// Erode: require >= 3 cardinal neighbors
	eroded := make([]byte, total)
	for gy := 0; gy < rows; gy++ {
		for gx := 0; gx < cols; gx++ {
			idx := gy*cols + gx
			if grid[idx] == 0 {
				continue
			}
			neighbors := 0
			if gx > 0 && grid[idx-1] == 1 {
				neighbors++
			}
			if gx < cols-1 && grid[idx+1] == 1 {
				neighbors++
			}
			if gy > 0 && grid[idx-cols] == 1 {
				neighbors++
			}
			if gy < rows-1 && grid[idx+cols] == 1 {
				neighbors++
			}
			if neighbors >= 3 {
				eroded[idx] = 1
			}
		}
	}

	// Dilate: set if any cardinal neighbor is set in eroded grid
	result := make([]byte, total)
	copy(result, eroded)
	for gy := 0; gy < rows; gy++ {
		for gx := 0; gx < cols; gx++ {
			idx := gy*cols + gx
			if eroded[idx] == 1 {
				continue // already set
			}
			if (gx > 0 && eroded[idx-1] == 1) ||
				(gx < cols-1 && eroded[idx+1] == 1) ||
				(gy > 0 && eroded[idx-cols] == 1) ||
				(gy < rows-1 && eroded[idx+cols] == 1) {
				result[idx] = 1
			}
		}
	}

	// Count for diagnostics
	erodedCount, resultCount := 0, 0
	for i := 0; i < total; i++ {
		if eroded[i] == 1 {
			erodedCount++
		}
		if result[i] == 1 {
			resultCount++
		}
	}
	fmt.Printf("Morph open: eroded to %d, dilated to %d cells\n", erodedCount, resultCount)

	return result
}

// floodFillFlat performs flood fill on a flat grid (row-major byte array).
func floodFillFlat(grid []byte, visited []byte, startX, startY, gridCols, gridRows int) image.Rectangle {
	minX, minY := startX, startY
	maxX, maxY := startX+1, startY+1

	stack := []image.Point{{X: startX, Y: startY}}

	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if p.X < 0 || p.X >= gridCols || p.Y < 0 || p.Y >= gridRows {
			continue
		}
		idx := p.Y*gridCols + p.X
		if visited[idx] != 0 || grid[idx] == 0 {
			continue
		}

		visited[idx] = 1

		if p.X < minX {
			minX = p.X
		}
		if p.X+1 > maxX {
			maxX = p.X + 1
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.Y+1 > maxY {
			maxY = p.Y + 1
		}

		stack = append(stack,
			image.Point{X: p.X - 1, Y: p.Y},
			image.Point{X: p.X + 1, Y: p.Y},
			image.Point{X: p.X, Y: p.Y - 1},
			image.Point{X: p.X, Y: p.Y + 1},
		)
	}

	return image.Rect(minX, minY, maxX, maxY)
}

// refineClusterBounds tightens the bounding box in two passes:
// 1. Board-trim: scan inward from each edge, trimming rows/columns that contain significant
//    green (board surface) pixels. IC body should contain no visible board surface.
// 2. Dense-core: find the contiguous column/row range with high color-profile match density.
func refineClusterBounds(hsvBytes []byte, imgW, imgH, channels, x1, y1, x2, y2 int, params DetectionParams) (int, int, int, int) {
	// Clamp
	if x1 < 0 {
		x1 = 0
	}
	if y1 < 0 {
		y1 = 0
	}
	if x2 > imgW {
		x2 = imgW
	}
	if y2 > imgH {
		y2 = imgH
	}

	matchPixel := func(offset int) bool {
		s := float64(hsvBytes[offset+1])
		v := float64(hsvBytes[offset+2])
		for _, p := range params.ColorProfiles {
			if v >= p.ValueMin && v <= p.ValueMax && s <= p.SatMax {
				return true
			}
		}
		return false
	}

	// Board surface detector: green PCB in HSV.
	// H=35-85 covers yellow-green through blue-green, S>30 ensures color (not grey),
	// V>50 ensures visible (not shadow).
	isBoardPixel := func(offset int) bool {
		h := hsvBytes[offset]
		s := hsvBytes[offset+1]
		v := hsvBytes[offset+2]
		return h >= 35 && h <= 85 && s >= 30 && v >= 50
	}

	regionW := x2 - x1
	regionH := y2 - y1
	if regionW <= 0 || regionH <= 0 {
		return x1, y1, x2, y2
	}

	// --- Pass 1: Board-surface trim (top edge only) ---
	// Board is dark green (has color/saturation). IC body is black or dark gray
	// (achromatic — low saturation). Scan rows from top, trim until we hit a row
	// with enough achromatic pixels.

	// A pixel is IC body if it's dark (low V) and not green board.
	// IC body = black/dark gray plastic, V typically 20-100.
	// Green board also has low V but has green hue (H=35-85).
	isChipPixel := func(offset int) bool {
		h := hsvBytes[offset]
		v := hsvBytes[offset+2]
		return v < 120 && (h < 35 || h > 85)
	}

	// Trim top and bottom: trim while row is green board, stop when it's not.
	// Stops at both black IC body AND white silkscreen (neither is green).
	_ = isChipPixel // used by left/right trim
	const boardRowThreshold = 0.50

	// Scan from top
	for y := y1; y < y2; y++ {
		boardPx := 0
		total := x2 - x1
		for x := x1; x < x2; x++ {
			if isBoardPixel((y*imgW + x) * channels) {
				boardPx++
			}
		}
		if total > 0 && float64(boardPx)/float64(total) >= boardRowThreshold {
			y1 = y + 1
		} else {
			break
		}
	}

	// Scan from bottom
	for y := y2 - 1; y > y1; y-- {
		boardPx := 0
		total := x2 - x1
		for x := x1; x < x2; x++ {
			if isBoardPixel((y*imgW + x) * channels) {
				boardPx++
			}
		}
		if total > 0 && float64(boardPx)/float64(total) >= boardRowThreshold {
			y2 = y
		} else {
			break
		}
	}

	// Pass 1: compute chip% for every column to find the peak (IC body level)
	numCols := x2 - x1
	colChipPct := make([]float64, numCols)
	colHeight := y2 - y1
	if colHeight > 0 {
		for cx := 0; cx < numCols; cx++ {
			x := x1 + cx
			bodyPx := 0
			for y := y1; y < y2; y++ {
				if isChipPixel((y*imgW + x) * channels) {
					bodyPx++
				}
			}
			colChipPct[cx] = float64(bodyPx) / float64(colHeight)
		}
	}

	// Find peak chip% (the IC body level)
	peakChip := 0.0
	for _, pct := range colChipPct {
		if pct > peakChip {
			peakChip = pct
		}
	}

	// Dynamic threshold: 80% of peak
	bodyColThreshold := peakChip * 0.80
	if bodyColThreshold < 0.20 {
		bodyColThreshold = 0.20 // floor to avoid trimming everything
	}
	fmt.Printf("    chip peak=%.0f%% threshold=%.0f%%\n", peakChip*100, bodyColThreshold*100)

	// Scan from left: trim until column hits the dynamic threshold
	for cx := 0; cx < numCols; cx++ {
		if colChipPct[cx] >= bodyColThreshold {
			break
		}
		x1++
	}

	// Scan from right
	for cx := numCols - 1; cx >= 0; cx-- {
		if colChipPct[cx] >= bodyColThreshold {
			break
		}
		x2--
	}

	// Trim from bottom — old green-based (disabled)
	if false {
	const boardThreshold = 0.50
	for y := y2 - 1; y > y1; y-- {
		boardPx := 0
		total := x2 - x1
		for x := x1; x < x2; x++ {
			if isBoardPixel((y*imgW + x) * channels) {
				boardPx++
			}
		}
		if total > 0 && float64(boardPx)/float64(total) > boardThreshold {
			y2 = y
		} else {
			break
		}
	}

	// Trim from left
	for x := x1; x < x2; x++ {
		boardPx := 0
		total := y2 - y1
		for y := y1; y < y2; y++ {
			if isBoardPixel((y*imgW + x) * channels) {
				boardPx++
			}
		}
		if total > 0 && float64(boardPx)/float64(total) > boardThreshold {
			x1 = x + 1
		} else {
			break
		}
	}

	// Trim from right
	for x := x2 - 1; x > x1; x-- {
		boardPx := 0
		total := y2 - y1
		for y := y1; y < y2; y++ {
			if isBoardPixel((y*imgW + x) * channels) {
				boardPx++
			}
		}
		if total > 0 && float64(boardPx)/float64(total) > boardThreshold {
			x2 = x
		} else {
			break
		}
	}
	} // end disabled bottom/left/right trim

	// --- Pass 2: Dense-core trim (disabled for debugging) ---
	if false {
	regionW = x2 - x1
	regionH = y2 - y1
	if regionW > 0 && regionH > 0 {

	// Compute per-column density (fraction of pixels matching color profiles)
	colDensity := make([]float64, regionW)
	for cx := 0; cx < regionW; cx++ {
		x := x1 + cx
		matches := 0
		for y := y1; y < y2; y++ {
			if matchPixel((y*imgW + x) * channels) {
				matches++
			}
		}
		colDensity[cx] = float64(matches) / float64(regionH)
	}

	// Compute per-row density
	rowDensity := make([]float64, regionH)
	for ry := 0; ry < regionH; ry++ {
		y := y1 + ry
		matches := 0
		for x := x1; x < x2; x++ {
			if matchPixel((y*imgW + x) * channels) {
				matches++
			}
		}
		rowDensity[ry] = float64(matches) / float64(regionW)
	}

	const coreThreshold = 0.50

	coreX1, coreX2 := findDenseRange(colDensity, coreThreshold)
	coreY1, coreY2 := findDenseRange(rowDensity, coreThreshold)

	if coreX2-coreX1 >= regionW/4 {
		x1orig := x1
		x1 = x1orig + coreX1
		x2 = x1orig + coreX2
	}
	if coreY2-coreY1 >= regionH/4 {
		y1orig := y1
		y1 = y1orig + coreY1
		y2 = y1orig + coreY2
	}

	} // end regionW/regionH check
	} // end dense-core disabled

	return x1, y1, x2, y2
}

// findDenseRange finds the longest contiguous range where density >= threshold.
// Returns (start, end) indices. Falls back to the full range if nothing qualifies.
func findDenseRange(density []float64, threshold float64) (int, int) {
	n := len(density)
	bestStart, bestEnd := 0, n
	bestLen := 0

	start := -1
	for i := 0; i < n; i++ {
		if density[i] >= threshold {
			if start < 0 {
				start = i
			}
		} else {
			if start >= 0 {
				if i-start > bestLen {
					bestLen = i - start
					bestStart = start
					bestEnd = i
				}
				start = -1
			}
		}
	}
	// Handle range extending to the end
	if start >= 0 && n-start > bestLen {
		bestStart = start
		bestEnd = n
	}

	return bestStart, bestEnd
}

// matchSizeTemplate tries to match a region's dimensions against training size templates.
// Checks both orientations (w x h and h x w). Falls back to DIP validation if no templates.
func matchSizeTemplate(widthMM, heightMM, mmToPixels float64, params DetectionParams) (bool, string) {
	if len(params.SizeTemplates) > 0 {
		// Try normal orientation
		for _, t := range params.SizeTemplates {
			if widthMM >= t.MinWidthMM && widthMM <= t.MaxWidthMM &&
				heightMM >= t.MinHeightMM && heightMM <= t.MaxHeightMM {
				return true, classifyPackage(widthMM*mmToPixels, heightMM*mmToPixels, mmToPixels)
			}
		}
		// Try rotated 90 degrees
		for _, t := range params.SizeTemplates {
			if heightMM >= t.MinWidthMM && heightMM <= t.MaxWidthMM &&
				widthMM >= t.MinHeightMM && widthMM <= t.MaxHeightMM {
				return true, classifyPackage(widthMM*mmToPixels, heightMM*mmToPixels, mmToPixels)
			}
		}
		return false, ""
	}

	// Fallback: original DIP validation
	if !IsValidDIPWidth(widthMM) && !IsValidDIPWidth(heightMM) {
		return false, ""
	}
	dipLength := heightMM
	if IsValidDIPWidth(heightMM) && !IsValidDIPWidth(widthMM) {
		dipLength = widthMM
	}
	if !isValidDIPLength(dipLength) {
		return false, ""
	}
	return true, classifyPackage(widthMM*mmToPixels, heightMM*mmToPixels, mmToPixels)
}

// matchSizeTemplateWithReason is like matchSizeTemplate but returns a human-readable
// rejection reason when no template matches.
func matchSizeTemplateWithReason(widthMM, heightMM, mmToPixels float64, params DetectionParams) (bool, string, string) {
	if len(params.SizeTemplates) > 0 {
		// Try normal orientation
		for _, t := range params.SizeTemplates {
			if widthMM >= t.MinWidthMM && widthMM <= t.MaxWidthMM &&
				heightMM >= t.MinHeightMM && heightMM <= t.MaxHeightMM {
				return true, classifyPackage(widthMM*mmToPixels, heightMM*mmToPixels, mmToPixels), ""
			}
		}
		// Try rotated 90 degrees
		for _, t := range params.SizeTemplates {
			if heightMM >= t.MinWidthMM && heightMM <= t.MaxWidthMM &&
				widthMM >= t.MinHeightMM && widthMM <= t.MaxHeightMM {
				return true, classifyPackage(widthMM*mmToPixels, heightMM*mmToPixels, mmToPixels), ""
			}
		}
		// Build rejection reason showing what templates exist
		reason := fmt.Sprintf("no template match for %.1fx%.1f mm — templates:", widthMM, heightMM)
		for i, t := range params.SizeTemplates {
			reason += fmt.Sprintf(" [%d] W=%.1f-%.1f H=%.1f-%.1f",
				i+1, t.MinWidthMM, t.MaxWidthMM, t.MinHeightMM, t.MaxHeightMM)
		}
		return false, "", reason
	}

	// Fallback: original DIP validation
	if !IsValidDIPWidth(widthMM) && !IsValidDIPWidth(heightMM) {
		return false, "", fmt.Sprintf("no valid DIP width (%.1f mm or %.1f mm)", widthMM, heightMM)
	}
	dipLength := heightMM
	if IsValidDIPWidth(heightMM) && !IsValidDIPWidth(widthMM) {
		dipLength = widthMM
	}
	if !isValidDIPLength(dipLength) {
		return false, "", fmt.Sprintf("invalid DIP length %.1f mm", dipLength)
	}
	return true, classifyPackage(widthMM*mmToPixels, heightMM*mmToPixels, mmToPixels), ""
}

// BoardBoundsResult holds the detected board region and per-cell variance data
// so the grid scorer can skip off-board cells.
type BoardBoundsResult struct {
	Bounds    geometry.Rect  // Board bounding box in pixels
	CellSize  int            // Patch/cell size in pixels
	GridCols  int
	GridRows  int
	OnBoard   []byte         // 1 = on-board, 0 = background (flat row-major)
	Threshold float64        // Variance threshold used
}

// DetectBoardBounds finds the PCB board region using local variance.
// The board has high color variance (components, traces, text, solder).
// The background (scanner lid etc) is uniform with very low variance.
// cellSizePx is the grid cell size to use (matches the component scoring grid).
// Returns nil if detection fails.
func DetectBoardBounds(img image.Image, cellSizePx int) *BoardBoundsResult {
	mat, err := imageToMat(img)
	if err != nil {
		return nil
	}
	defer mat.Close()

	return DetectBoardBoundsMat(mat, cellSizePx)
}

// DetectBoardBoundsMat finds board bounds from a Mat using per-cell HSV variance.
// Board cells have high color diversity (S and V variance) while background cells
// (uniform scanner gray) have very low variance. Uses biggest-gap thresholding on
// the combined S+V variance score to separate board from background.
func DetectBoardBoundsMat(mat gocv.Mat, cellSizePx int) *BoardBoundsResult {
	imgW := mat.Cols()
	imgH := mat.Rows()

	if cellSizePx < 4 {
		cellSizePx = 4
	}

	gridCols := (imgW + cellSizePx - 1) / cellSizePx
	gridRows := (imgH + cellSizePx - 1) / cellSizePx
	totalCells := gridRows * gridCols

	fmt.Printf("DetectBoardBounds: %dx%d cells (%dpx), image %dx%d\n", gridCols, gridRows, cellSizePx, imgW, imgH)

	// Step 2: Convert to HSV, extract bytes
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(mat, &hsv, gocv.ColorBGRToHSV)
	hsvBytes := hsv.ToBytes()
	ch := 3

	// Step 3: Compute per-cell variance of S and V channels.
	// Combined score = varS + varV.
	// We skip H to avoid hue-wrap artifacts at 180.
	scores := make([]float64, totalCells)
	for gy := 0; gy < gridRows; gy++ {
		for gx := 0; gx < gridCols; gx++ {
			x1 := gx * cellSizePx
			y1 := gy * cellSizePx
			x2 := x1 + cellSizePx
			y2 := y1 + cellSizePx
			if x2 > imgW {
				x2 = imgW
			}
			if y2 > imgH {
				y2 = imgH
			}

			var sumS, sumS2, sumV, sumV2 float64
			n := 0
			for y := y1; y < y2; y++ {
				rowOff := y * imgW * ch
				for x := x1; x < x2; x++ {
					off := rowOff + x*ch
					s := float64(hsvBytes[off+1])
					v := float64(hsvBytes[off+2])
					sumS += s
					sumS2 += s * s
					sumV += v
					sumV2 += v * v
					n++
				}
			}
			if n > 0 {
				nf := float64(n)
				varS := sumS2/nf - (sumS/nf)*(sumS/nf)
				varV := sumV2/nf - (sumV/nf)*(sumV/nf)
				if varS < 0 {
					varS = 0
				}
				if varV < 0 {
					varV = 0
				}
				scores[gy*gridCols+gx] = varS + varV
			}
		}
	}

	// Step 4: Quantize scores to 8-bit (0-255), build histogram, find biggest gap.
	// Quantizing compresses the dynamic range so outliers don't dominate.
	maxScore := 0.0
	for _, s := range scores {
		if s > maxScore {
			maxScore = s
		}
	}

	// Quantize to 0-255
	quant := make([]int, totalCells)
	if maxScore > 0 {
		for i, s := range scores {
			q := int(s / maxScore * 255)
			if q > 255 {
				q = 255
			}
			quant[i] = q
		}
	}

	// Build 256-bin histogram
	var hist [256]int
	for _, q := range quant {
		hist[q]++
	}

	// Find biggest gap (run of empty bins) in the histogram.
	// Track which gap has the best-balanced split (largest minority side).
	type gapInfo struct {
		start   int
		length  int
		loCount int
		hiCount int
	}
	var bestGap gapInfo
	bestMinSide := 0
	gapStart := -1
	for b := 0; b < 256; b++ {
		if hist[b] == 0 {
			if gapStart < 0 {
				gapStart = b
			}
		} else {
			if gapStart >= 0 {
				lo, hi := 0, 0
				for bb := 0; bb < gapStart; bb++ {
					lo += hist[bb]
				}
				for bb := b; bb < 256; bb++ {
					hi += hist[bb]
				}
				minSide := lo
				if hi < minSide {
					minSide = hi
				}
				if lo > 0 && hi > 0 && minSide > bestMinSide {
					bestMinSide = minSide
					bestGap = gapInfo{gapStart, b - gapStart, lo, hi}
				}
				gapStart = -1
			}
		}
	}

	// Threshold at midpoint of the best gap, mapped back to original score scale
	threshold := 0.0
	if bestGap.length > 0 {
		midBin := float64(bestGap.start) + float64(bestGap.length)/2
		threshold = midBin / 255 * maxScore
	}

	fmt.Printf("  Best gap: %d empty bins at [%d-%d], lo=%d hi=%d, threshold=%.1f\n",
		bestGap.length, bestGap.start, bestGap.start+bestGap.length-1,
		bestGap.loCount, bestGap.hiCount, threshold)

	// Step 5: Classify cells
	onBoard := make([]byte, totalCells)
	onBoardCount := 0
	for i := 0; i < totalCells; i++ {
		if scores[i] > threshold {
			onBoard[i] = 1
			onBoardCount++
		}
	}

	if onBoardCount == 0 {
		fmt.Println("DetectBoardBounds: no high-variance cells found")
		return nil
	}

	fmt.Printf("  On-board cells: %d/%d (%.1f%%)\n",
		onBoardCount, totalCells, float64(onBoardCount)/float64(totalCells)*100)

	// Step 6: Board bounds via row/column density.
	// A row/column is "board" if >= 50% of its cells are on-board.
	const densityThreshold = 0.50

	colCounts := make([]int, gridCols)
	rowCounts := make([]int, gridRows)
	for gy := 0; gy < gridRows; gy++ {
		for gx := 0; gx < gridCols; gx++ {
			if onBoard[gy*gridCols+gx] == 1 {
				colCounts[gx]++
				rowCounts[gy]++
			}
		}
	}

	// Find range of dense columns
	minGX, maxGX := -1, -1
	for gx := 0; gx < gridCols; gx++ {
		density := float64(colCounts[gx]) / float64(gridRows)
		if density >= densityThreshold {
			if minGX < 0 {
				minGX = gx
			}
			maxGX = gx
		}
	}

	// Find range of dense rows
	minGY, maxGY := -1, -1
	for gy := 0; gy < gridRows; gy++ {
		density := float64(rowCounts[gy]) / float64(gridCols)
		if density >= densityThreshold {
			if minGY < 0 {
				minGY = gy
			}
			maxGY = gy
		}
	}

	if minGX < 0 || minGY < 0 {
		// Fallback: bounding box of all on-board cells
		fmt.Println("DetectBoardBounds: density filter found nothing, using simple bounds")
		for gy := 0; gy < gridRows; gy++ {
			for gx := 0; gx < gridCols; gx++ {
				if onBoard[gy*gridCols+gx] == 1 {
					if minGX < 0 || gx < minGX {
						minGX = gx
					}
					if gx > maxGX {
						maxGX = gx
					}
					if minGY < 0 || gy < minGY {
						minGY = gy
					}
					if gy > maxGY {
						maxGY = gy
					}
				}
			}
		}
	}

	// Step 7: Diagnostics — row/column density bars
	fmt.Printf("  Row density: ")
	for gy := 0; gy < gridRows; gy++ {
		d := float64(rowCounts[gy]) / float64(gridCols)
		if gy == minGY {
			fmt.Print("[")
		}
		if d >= densityThreshold {
			fmt.Print("#")
		} else {
			fmt.Print(".")
		}
		if gy == maxGY {
			fmt.Print("]")
		}
	}
	fmt.Println()

	fmt.Printf("  Col density: ")
	for gx := 0; gx < gridCols; gx++ {
		d := float64(colCounts[gx]) / float64(gridRows)
		if gx == minGX {
			fmt.Print("[")
		}
		if d >= densityThreshold {
			fmt.Print("#")
		} else {
			fmt.Print(".")
		}
		if gx == maxGX {
			fmt.Print("]")
		}
	}
	fmt.Println()

	minX := minGX * cellSizePx
	minY := minGY * cellSizePx
	maxX := (maxGX + 1) * cellSizePx
	maxY := (maxGY + 1) * cellSizePx
	if maxX > imgW {
		maxX = imgW
	}
	if maxY > imgH {
		maxY = imgH
	}

	fmt.Printf("DetectBoardBounds: board at (%d,%d) %dx%d px, %d/%d cells on-board\n",
		minX, minY, maxX-minX, maxY-minY, onBoardCount, totalCells)

	return &BoardBoundsResult{
		Bounds: geometry.Rect{
			X:      float64(minX),
			Y:      float64(minY),
			Width:  float64(maxX - minX),
			Height: float64(maxY - minY),
		},
		CellSize:  cellSizePx,
		GridCols:  gridCols,
		GridRows:  gridRows,
		OnBoard:   onBoard,
		Threshold: threshold,
	}
}

// GridScoringResult holds the output of the grid scoring phase.
type GridScoringResult struct {
	Overlay    *canvas.Overlay // Overlay with hit cells painted
	HitCount   int
	TotalCells int
	CellSizePx int
	GridCols   int
	GridRows   int
}

// ScoreComponentGrid runs the grid scoring phase using the board bounds mask to skip
// off-board cells. Returns an overlay of hit cells for visualization.
func ScoreComponentGrid(img image.Image, dpi float64, params DetectionParams, board *BoardBoundsResult) (*GridScoringResult, error) {
	mat, err := imageToMat(img)
	if err != nil {
		return nil, fmt.Errorf("convert image: %w", err)
	}
	defer mat.Close()

	return ScoreComponentGridMat(mat, dpi, params, board)
}

// ScoreComponentGridMat runs grid scoring on a Mat, using board variance mask.
func ScoreComponentGridMat(img gocv.Mat, dpi float64, params DetectionParams, board *BoardBoundsResult) (*GridScoringResult, error) {
	if img.Empty() {
		return nil, fmt.Errorf("empty image")
	}

	mmToPixels := dpi / 25.4
	cellMM := params.CellSizeMM
	if cellMM <= 0 {
		cellMM = 2.0
	}
	cellSizePx := int(cellMM * mmToPixels)
	if cellSizePx < 4 {
		cellSizePx = 4
	}

	imgW := img.Cols()
	imgH := img.Rows()
	gridRows := (imgH + cellSizePx - 1) / cellSizePx
	gridCols := (imgW + cellSizePx - 1) / cellSizePx

	fmt.Printf("Grid scoring: DPI=%.0f, cell=%.2fmm (%dpx), grid=%dx%d (%d cells)\n",
		dpi, cellMM, cellSizePx, gridCols, gridRows, gridCols*gridRows)

	// Convert to HSV and extract bytes
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(img, &hsv, gocv.ColorBGRToHSV)

	hsvBytes := hsv.ToBytes()
	channels := hsv.Channels()

	// If board bounds uses the same cell size, we can use the onBoard mask directly.
	// Otherwise we need to map between grids.
	var boardMask []byte
	var boardGridCols int
	var boardCellSize int
	if board != nil {
		boardMask = board.OnBoard
		boardGridCols = board.GridCols
		boardCellSize = board.CellSize
	}

	// Parallel scoring — only score cells that are on-board
	grid := make([]byte, gridRows*gridCols)
	var hitCount int64

	numWorkers := runtime.NumCPU()
	if numWorkers > gridRows {
		numWorkers = gridRows
	}
	if numWorkers < 1 {
		numWorkers = 1
	}

	var wg sync.WaitGroup
	rowsPerWorker := (gridRows + numWorkers - 1) / numWorkers

	for w := 0; w < numWorkers; w++ {
		startRow := w * rowsPerWorker
		endRow := startRow + rowsPerWorker
		if endRow > gridRows {
			endRow = gridRows
		}
		if startRow >= endRow {
			continue
		}

		wg.Add(1)
		go func(startRow, endRow int) {
			defer wg.Done()
			localHits := 0
			for gy := startRow; gy < endRow; gy++ {
				for gx := 0; gx < gridCols; gx++ {
					// Check board mask — map this cell's center to board grid
					if boardMask != nil {
						cx := gx*cellSizePx + cellSizePx/2
						cy := gy*cellSizePx + cellSizePx/2
						bgx := cx / boardCellSize
						bgy := cy / boardCellSize
						if bgx >= 0 && bgy >= 0 && bgx < boardGridCols && bgy < len(boardMask)/boardGridCols {
							if boardMask[bgy*boardGridCols+bgx] == 0 {
								continue // off-board, skip
							}
						}
					}

					x1 := gx * cellSizePx
					y1 := gy * cellSizePx
					x2 := x1 + cellSizePx
					y2 := y1 + cellSizePx
					if x2 > imgW { x2 = imgW }
					if y2 > imgH { y2 = imgH }

					if classifyCellBytes(hsvBytes, imgW, channels, x1, y1, x2, y2, params) {
						grid[gy*gridCols+gx] = 1
						localHits++
					}
				}
			}
			atomic.AddInt64(&hitCount, int64(localHits))
		}(startRow, endRow)
	}
	wg.Wait()

	totalCells := gridRows * gridCols
	fmt.Printf("Grid scoring: %d of %d cells are hits (%.1f%%) [%d workers]\n",
		hitCount, totalCells, float64(hitCount)/float64(totalCells)*100, numWorkers)

	// Build overlay
	hitColor := color.RGBA{R: 255, G: 0, B: 0, A: 128}
	overlay := &canvas.Overlay{
		Color: hitColor,
	}

	for gy := 0; gy < gridRows; gy++ {
		for gx := 0; gx < gridCols; gx++ {
			if grid[gy*gridCols+gx] == 1 {
				overlay.Rectangles = append(overlay.Rectangles, canvas.OverlayRect{
					X:      gx * cellSizePx,
					Y:      gy * cellSizePx,
					Width:  cellSizePx,
					Height: cellSizePx,
					Fill:   canvas.FillSolid,
				})
			}
		}
	}

	return &GridScoringResult{
		Overlay:    overlay,
		HitCount:   int(hitCount),
		TotalCells: totalCells,
		CellSizePx: cellSizePx,
		GridCols:   gridCols,
		GridRows:   gridRows,
	}, nil
}

// classifyCell determines if a cell looks like part of a component.
// Uses training-derived color profiles - cell matches if it fits ANY profile.
// Uses 40% threshold to allow for white text/markings on component body.
func classifyCell(cell gocv.Mat, params DetectionParams) bool {
	if cell.Empty() {
		return false
	}

	rows := cell.Rows()
	cols := cell.Cols()
	totalPixels := rows * cols
	if totalPixels == 0 {
		return false
	}

	// Match threshold: 40% allows for text/markings on component body
	const matchThreshold = 0.40

	// If we have color profiles from training, use them
	if len(params.ColorProfiles) > 0 {
		// Count pixels matching ANY color profile
		matchCount := 0
		for y := 0; y < rows; y++ {
			for x := 0; x < cols; x++ {
				pixel := cell.GetVecbAt(y, x)
				s := float64(pixel[1])
				v := float64(pixel[2])

				// Check against each profile
				for _, profile := range params.ColorProfiles {
					if v >= profile.ValueMin && v <= profile.ValueMax && s <= profile.SatMax {
						matchCount++
						break // Only count pixel once even if matches multiple profiles
					}
				}
			}
		}
		matchRatio := float64(matchCount) / float64(totalPixels)
		return matchRatio > matchThreshold
	}

	// Fallback: use single threshold (no training samples)
	matchCount := 0
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			pixel := cell.GetVecbAt(y, x)
			s := float64(pixel[1])
			v := float64(pixel[2])

			if v <= params.ValueMax && s <= params.SatMax {
				matchCount++
			}
		}
	}

	matchRatio := float64(matchCount) / float64(totalPixels)
	return matchRatio > matchThreshold
}

// floodFillRegion finds a connected region of component cells starting from (startX, startY).
// Returns the bounding rectangle in grid coordinates.
func floodFillRegion(grid [][]bool, visited [][]bool, startX, startY, gridCols, gridRows int) image.Rectangle {
	minX, minY := startX, startY
	maxX, maxY := startX+1, startY+1

	// Stack-based flood fill
	stack := []image.Point{{X: startX, Y: startY}}

	for len(stack) > 0 {
		// Pop
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if p.X < 0 || p.X >= gridCols || p.Y < 0 || p.Y >= gridRows {
			continue
		}
		if visited[p.Y][p.X] || !grid[p.Y][p.X] {
			continue
		}

		visited[p.Y][p.X] = true

		// Update bounds
		if p.X < minX {
			minX = p.X
		}
		if p.X+1 > maxX {
			maxX = p.X + 1
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.Y+1 > maxY {
			maxY = p.Y + 1
		}

		// Push neighbors (4-connectivity)
		stack = append(stack, image.Point{X: p.X - 1, Y: p.Y})
		stack = append(stack, image.Point{X: p.X + 1, Y: p.Y})
		stack = append(stack, image.Point{X: p.X, Y: p.Y - 1})
		stack = append(stack, image.Point{X: p.X, Y: p.Y + 1})
	}

	return image.Rect(minX, minY, maxX, maxY)
}

// ValidateTrainingSamples runs the detector on each training sample and prints diagnostics.
// This helps debug why training samples may not be detected.
// Writes detailed analysis to /tmp/component_validation.txt
func ValidateTrainingSamples(img image.Image, samples []TrainingSample, params DetectionParams) {
	if len(samples) == 0 {
		fmt.Println("ValidateTrainingSamples: no samples to validate")
		return
	}

	// Open output file
	f, err := os.Create("/tmp/component_validation.txt")
	if err != nil {
		fmt.Printf("ValidateTrainingSamples: cannot create output file: %v\n", err)
		return
	}
	defer f.Close()

	write := func(format string, args ...interface{}) {
		msg := fmt.Sprintf(format, args...)
		fmt.Print(msg)
		f.WriteString(msg)
	}

	// Convert image to Mat and HSV
	mat, err := imageToMat(img)
	if err != nil {
		write("ValidateTrainingSamples: image conversion error: %v\n", err)
		return
	}
	defer mat.Close()

	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(mat, &hsv, gocv.ColorBGRToHSV)

	write("\n=== Validating %d training samples against %d color profiles ===\n",
		len(samples), len(params.ColorProfiles))
	write("Output file: /tmp/component_validation.txt\n\n")

	// Print the profiles
	write("Color Profiles:\n")
	for i, p := range params.ColorProfiles {
		write("  Profile %d: V=%.0f-%.0f, SatMax=%.0f\n", i+1, p.ValueMin, p.ValueMax, p.SatMax)
	}
	write("\nSize constraints: %.1f-%.1f x %.1f-%.1f mm\n\n",
		params.MinWidth, params.MaxWidth, params.MinHeight, params.MaxHeight)

	for i, sample := range samples {
		bounds := sample.Bounds
		rect := image.Rect(
			int(bounds.X),
			int(bounds.Y),
			int(bounds.X+bounds.Width),
			int(bounds.Y+bounds.Height),
		)

		write("======== Sample %d ========\n", i+1)
		write("  Bounds: (%.0f,%.0f) %.0fx%.0f px\n", bounds.X, bounds.Y, bounds.Width, bounds.Height)
		write("  Size: %.1f x %.1f mm\n", sample.WidthMM, sample.HeightMM)
		write("  Training values: V=%.0f S=%.0f\n", sample.BackgroundVal, sample.MeanSat)

		// Clamp to image bounds
		if rect.Min.X < 0 {
			rect.Min.X = 0
		}
		if rect.Min.Y < 0 {
			rect.Min.Y = 0
		}
		if rect.Max.X > hsv.Cols() {
			rect.Max.X = hsv.Cols()
		}
		if rect.Max.Y > hsv.Rows() {
			rect.Max.Y = hsv.Rows()
		}

		if rect.Dx() <= 0 || rect.Dy() <= 0 {
			write("  INVALID BOUNDS (empty region after clamping)\n\n")
			continue
		}

		// Extract the sample region
		region := hsv.Region(rect)

		// Analyze pixel-by-pixel and build V histogram
		rows := region.Rows()
		cols := region.Cols()
		totalPixels := rows * cols

		// Count matches per profile
		profileMatches := make([]int, len(params.ColorProfiles))
		noMatchCount := 0

		// Track why pixels don't match
		tooHighV := 0
		tooLowV := 0
		tooHighS := 0

		// Build actual V and S histograms for this sample
		vHist := make([]int, 256)
		sHist := make([]int, 256)

		// Sample some non-matching pixels for detail
		type nonMatchPixel struct {
			v, s float64
		}
		var nonMatchSamples []nonMatchPixel

		for y := 0; y < rows; y++ {
			for x := 0; x < cols; x++ {
				pixel := region.GetVecbAt(y, x)
				s := float64(pixel[1])
				v := float64(pixel[2])

				vHist[int(v)]++
				sHist[int(s)]++

				matched := false
				for pi, profile := range params.ColorProfiles {
					if v >= profile.ValueMin && v <= profile.ValueMax && s <= profile.SatMax {
						profileMatches[pi]++
						matched = true
						break
					}
				}

				if !matched {
					noMatchCount++
					// Determine why it didn't match
					matchedAnyV := false
					for _, profile := range params.ColorProfiles {
						if v >= profile.ValueMin && v <= profile.ValueMax {
							matchedAnyV = true
							if s > profile.SatMax {
								tooHighS++
							}
							break
						}
					}
					if !matchedAnyV {
						// V is out of range for all profiles
						allTooLow := true
						allTooHigh := true
						for _, profile := range params.ColorProfiles {
							if v >= profile.ValueMin {
								allTooLow = false
							}
							if v <= profile.ValueMax {
								allTooHigh = false
							}
						}
						if allTooHigh {
							tooHighV++
						} else if allTooLow {
							tooLowV++
						}
					}

					// Collect some samples
					if len(nonMatchSamples) < 10 {
						nonMatchSamples = append(nonMatchSamples, nonMatchPixel{v: v, s: s})
					}
				}
			}
		}
		region.Close()

		// Calculate match ratio
		totalMatches := 0
		for _, m := range profileMatches {
			totalMatches += m
		}
		matchRatio := float64(totalMatches) / float64(totalPixels)
		wouldDetect := matchRatio > 0.40 // Same threshold as classifyCell

		// Print results
		status := "MISS"
		if wouldDetect {
			status = "HIT"
		}
		write("\n  Result: [%s] %.1f%% match (%d/%d pixels)\n", status, matchRatio*100, totalMatches, totalPixels)

		// Per-profile breakdown
		write("  Per-profile matches:\n")
		for pi, m := range profileMatches {
			pct := float64(m) / float64(totalPixels) * 100
			write("    Profile %d (V=%.0f-%.0f, S<=%.0f): %d pixels (%.1f%%)\n",
				pi+1, params.ColorProfiles[pi].ValueMin, params.ColorProfiles[pi].ValueMax,
				params.ColorProfiles[pi].SatMax, m, pct)
		}

		// Non-match breakdown
		if noMatchCount > 0 {
			write("  Non-matching: %d pixels (%.1f%%)\n", noMatchCount, float64(noMatchCount)/float64(totalPixels)*100)
			if tooHighV > 0 {
				write("    V too high (brighter than all profiles): %d (%.1f%%)\n",
					tooHighV, float64(tooHighV)/float64(totalPixels)*100)
			}
			if tooLowV > 0 {
				write("    V too low (darker than all profiles): %d (%.1f%%)\n",
					tooLowV, float64(tooLowV)/float64(totalPixels)*100)
			}
			if tooHighS > 0 {
				write("    S too high (too saturated): %d (%.1f%%)\n",
					tooHighS, float64(tooHighS)/float64(totalPixels)*100)
			}
		}

		// Print actual V histogram (grouped into 16 buckets)
		write("\n  Actual V histogram (16 buckets of 16):\n")
		for bucket := 0; bucket < 16; bucket++ {
			count := 0
			for v := bucket * 16; v < (bucket+1)*16 && v < 256; v++ {
				count += vHist[v]
			}
			pct := float64(count) / float64(totalPixels) * 100
			if pct >= 0.5 {
				bar := ""
				for j := 0; j < int(pct/2) && j < 40; j++ {
					bar += "#"
				}
				write("    V[%3d-%3d]: %5.1f%% %s\n", bucket*16, bucket*16+15, pct, bar)
			}
		}

		// Print actual S histogram (grouped into 16 buckets)
		write("  Actual S histogram (16 buckets of 16):\n")
		for bucket := 0; bucket < 16; bucket++ {
			count := 0
			for s := bucket * 16; s < (bucket+1)*16 && s < 256; s++ {
				count += sHist[s]
			}
			pct := float64(count) / float64(totalPixels) * 100
			if pct >= 0.5 {
				bar := ""
				for j := 0; j < int(pct/2) && j < 40; j++ {
					bar += "#"
				}
				write("    S[%3d-%3d]: %5.1f%% %s\n", bucket*16, bucket*16+15, pct, bar)
			}
		}

		// Sample non-matching pixels
		if len(nonMatchSamples) > 0 {
			write("  Sample non-matching pixels:\n")
			for j, p := range nonMatchSamples {
				// Show which profile it's closest to
				closest := -1
				minDist := 1e9
				for pi, profile := range params.ColorProfiles {
					dist := 0.0
					if p.v < profile.ValueMin {
						dist += profile.ValueMin - p.v
					} else if p.v > profile.ValueMax {
						dist += p.v - profile.ValueMax
					}
					if p.s > profile.SatMax {
						dist += p.s - profile.SatMax
					}
					if dist < minDist {
						minDist = dist
						closest = pi
					}
				}
				write("    [%d] V=%.0f S=%.0f (closest to profile %d, dist=%.0f)\n",
					j+1, p.v, p.s, closest+1, minDist)
			}
		}

		write("\n")
	}
	write("=== Validation complete ===\n")
	write("Full output saved to: /tmp/component_validation.txt\n")
}

// checkWhiteMarkings checks for white text/markings inside a region.
// Returns the percentage of bright pixels.
func checkWhiteMarkings(img gocv.Mat, rect image.Rectangle) float64 {
	// Clamp to image bounds
	if rect.Min.X < 0 {
		rect.Min.X = 0
	}
	if rect.Min.Y < 0 {
		rect.Min.Y = 0
	}
	if rect.Max.X > img.Cols() {
		rect.Max.X = img.Cols()
	}
	if rect.Max.Y > img.Rows() {
		rect.Max.Y = img.Rows()
	}
	if rect.Dx() <= 0 || rect.Dy() <= 0 {
		return 0
	}

	// Extract region
	region := img.Region(rect)
	defer region.Close()

	// Convert to grayscale
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(region, &gray, gocv.ColorBGRToGray)

	// Count bright pixels (white markings)
	// White text on black background should be > 200
	whiteMask := gocv.NewMat()
	defer whiteMask.Close()
	gocv.Threshold(gray, &whiteMask, 180, 255, gocv.ThresholdBinary)

	whitePixels := gocv.CountNonZero(whiteMask)
	totalPixels := rect.Dx() * rect.Dy()

	if totalPixels == 0 {
		return 0
	}

	return float64(whitePixels) / float64(totalPixels) * 100
}

// ExtractSampleFeatures extracts HSV and size features from an image region.
// Used to create training samples from user-selected rectangles.
// Analyzes histogram to find background (dark) and marking (light) peaks.
func ExtractSampleFeatures(img image.Image, bounds geometry.Rect, dpi float64) TrainingSample {
	sample := TrainingSample{
		Bounds: bounds,
	}

	// Convert to mm
	mmToPixels := dpi / 25.4
	sample.WidthMM = bounds.Width / mmToPixels
	sample.HeightMM = bounds.Height / mmToPixels

	// Convert Go image to Mat
	mat, err := imageToMat(img)
	if err != nil {
		fmt.Printf("ExtractSampleFeatures: image conversion error: %v\n", err)
		return sample
	}
	defer mat.Close()

	// Clamp bounds to image
	rect := image.Rect(
		int(bounds.X),
		int(bounds.Y),
		int(bounds.X+bounds.Width),
		int(bounds.Y+bounds.Height),
	)
	if rect.Min.X < 0 {
		rect.Min.X = 0
	}
	if rect.Min.Y < 0 {
		rect.Min.Y = 0
	}
	if rect.Max.X > mat.Cols() {
		rect.Max.X = mat.Cols()
	}
	if rect.Max.Y > mat.Rows() {
		rect.Max.Y = mat.Rows()
	}
	if rect.Dx() <= 0 || rect.Dy() <= 0 {
		return sample
	}

	// Extract region
	region := mat.Region(rect)
	defer region.Close()

	// Convert to HSV
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(region, &hsv, gocv.ColorBGRToHSV)

	// Calculate mean HSV values
	mean := gocv.NewMat()
	defer mean.Close()
	stddev := gocv.NewMat()
	defer stddev.Close()
	gocv.MeanStdDev(hsv, &mean, &stddev)

	// Extract mean values (mean is a 1x1 Mat with 3 channels)
	if !mean.Empty() && mean.Cols() >= 1 {
		sample.MeanHue = mean.GetDoubleAt(0, 0)
		sample.MeanSat = mean.GetDoubleAt(1, 0)
		sample.MeanVal = mean.GetDoubleAt(2, 0)
	}

	// Build quantized histogram - 16 buckets for V channel
	// Background is dominant bucket, marking is brightest significant bucket above it
	sample.BackgroundVal, sample.MarkingVal, sample.BackgroundPct, sample.MarkingPct = analyzeValueHistogram(hsv)

	// Get full HSV bucket distributions for all three channels
	hsvHist := AnalyzeHSVBuckets(hsv)

	// Get white ratio
	sample.WhiteRatio = checkWhiteMarkings(mat, rect)

	fmt.Printf("=== Training Sample Added ===\n")
	fmt.Printf("  Size: %.1f x %.1f mm\n", sample.WidthMM, sample.HeightMM)
	fmt.Printf("  Mean HSV: H=%.1f S=%.1f V=%.1f\n", sample.MeanHue, sample.MeanSat, sample.MeanVal)
	printHSVHistogram("H (Hue)", hsvHist.H, 180)
	printHSVHistogram("S (Saturation)", hsvHist.S, 256)
	printHSVHistogram("V (Value)", hsvHist.V, 256)
	fmt.Printf("  Background: V=%3.0f (bucket %d, %.1f%%)\n",
		sample.BackgroundVal, int(sample.BackgroundVal)/ColorBucketWidth, sample.BackgroundPct)
	fmt.Printf("  Markings:   V=%3.0f (bucket %d, %.1f%%)\n",
		sample.MarkingVal, int(sample.MarkingVal)/ColorBucketWidth, sample.MarkingPct)
	fmt.Printf("  White ratio: %.1f%%\n", sample.WhiteRatio)
	fmt.Printf("=============================\n")

	return sample
}

// NumColorBuckets is the number of quantized brightness buckets for histogram analysis.
const NumColorBuckets = 16

// ColorBucketWidth is the V-channel range covered by each bucket.
const ColorBucketWidth = 256 / NumColorBuckets // 16

// analyzeValueHistogram builds a quantized histogram of the V channel.
// Uses 16 buckets (0-15, 16-31, ..., 240-255).
// Returns (backgroundVal, markingVal, backgroundPct, markingPct).
// Background is the dominant bucket; marking is the brightest bucket with >1% of pixels above background.
func analyzeValueHistogram(hsv gocv.Mat) (float64, float64, float64, float64) {
	// Build quantized histogram with 16 buckets
	buckets := make([]int, NumColorBuckets)
	totalPixels := 0

	rows := hsv.Rows()
	cols := hsv.Cols()

	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			// V is the 3rd channel (index 2) in HSV
			v := hsv.GetVecbAt(y, x)[2]
			bucket := int(v) / ColorBucketWidth
			if bucket >= NumColorBuckets {
				bucket = NumColorBuckets - 1
			}
			buckets[bucket]++
			totalPixels++
		}
	}

	if totalPixels == 0 {
		return 0, 0, 0, 0
	}

	// Find the dominant bucket (background - always the largest)
	bgBucket := 0
	bgCount := buckets[0]
	for i := 1; i < NumColorBuckets; i++ {
		if buckets[i] > bgCount {
			bgCount = buckets[i]
			bgBucket = i
		}
	}

	// Find the brightest bucket with significant pixels (>1%) above the background bucket
	// This represents the markings (lighter text/logos on dark IC body)
	markBucket := bgBucket
	markCount := 0
	minSignificant := totalPixels / 100 // 1% threshold
	for i := bgBucket + 1; i < NumColorBuckets; i++ {
		if buckets[i] > minSignificant {
			markBucket = i
			markCount = buckets[i]
		}
	}

	// If no marking bucket found above background, marking = background
	if markBucket == bgBucket {
		markCount = bgCount
	}

	// Convert bucket indices to V values (center of bucket)
	bgVal := float64(bgBucket*ColorBucketWidth + ColorBucketWidth/2)
	markVal := float64(markBucket*ColorBucketWidth + ColorBucketWidth/2)
	bgPct := float64(bgCount) / float64(totalPixels) * 100
	markPct := float64(markCount) / float64(totalPixels) * 100

	return bgVal, markVal, bgPct, markPct
}

// AnalyzeColorBuckets returns the full bucket distribution for detailed analysis.
// Returns an array of 16 percentages (one per bucket).
func AnalyzeColorBuckets(hsv gocv.Mat) [NumColorBuckets]float64 {
	buckets := make([]int, NumColorBuckets)
	totalPixels := 0

	rows := hsv.Rows()
	cols := hsv.Cols()

	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			v := hsv.GetVecbAt(y, x)[2]
			bucket := int(v) / ColorBucketWidth
			if bucket >= NumColorBuckets {
				bucket = NumColorBuckets - 1
			}
			buckets[bucket]++
			totalPixels++
		}
	}

	var result [NumColorBuckets]float64
	if totalPixels > 0 {
		for i := 0; i < NumColorBuckets; i++ {
			result[i] = float64(buckets[i]) / float64(totalPixels) * 100
		}
	}
	return result
}

// HSVHistograms holds histogram data for all three HSV channels.
type HSVHistograms struct {
	H [NumColorBuckets]float64 // Hue buckets (0-180 mapped to 16 buckets)
	S [NumColorBuckets]float64 // Saturation buckets (0-255)
	V [NumColorBuckets]float64 // Value buckets (0-255)
}

// AnalyzeHSVBuckets returns histogram distributions for all three HSV channels.
func AnalyzeHSVBuckets(hsv gocv.Mat) HSVHistograms {
	hBuckets := make([]int, NumColorBuckets)
	sBuckets := make([]int, NumColorBuckets)
	vBuckets := make([]int, NumColorBuckets)
	totalPixels := 0

	rows := hsv.Rows()
	cols := hsv.Cols()

	// H channel is 0-180 in OpenCV, S and V are 0-255
	hBucketWidth := 180 / NumColorBuckets // ~11 per bucket

	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			pixel := hsv.GetVecbAt(y, x)
			h, s, v := int(pixel[0]), int(pixel[1]), int(pixel[2])

			hBucket := h / hBucketWidth
			if hBucket >= NumColorBuckets {
				hBucket = NumColorBuckets - 1
			}
			sBucket := s / ColorBucketWidth
			if sBucket >= NumColorBuckets {
				sBucket = NumColorBuckets - 1
			}
			vBucket := v / ColorBucketWidth
			if vBucket >= NumColorBuckets {
				vBucket = NumColorBuckets - 1
			}

			hBuckets[hBucket]++
			sBuckets[sBucket]++
			vBuckets[vBucket]++
			totalPixels++
		}
	}

	var result HSVHistograms
	if totalPixels > 0 {
		for i := 0; i < NumColorBuckets; i++ {
			result.H[i] = float64(hBuckets[i]) / float64(totalPixels) * 100
			result.S[i] = float64(sBuckets[i]) / float64(totalPixels) * 100
			result.V[i] = float64(vBuckets[i]) / float64(totalPixels) * 100
		}
	}
	return result
}

// printHSVHistogram prints a single channel histogram with a label.
func printHSVHistogram(label string, buckets [NumColorBuckets]float64, maxVal int) {
	fmt.Printf("  %s channel (0-%d in %d buckets):\n", label, maxVal, NumColorBuckets)
	bucketWidth := maxVal / NumColorBuckets
	for i := 0; i < NumColorBuckets; i++ {
		vMin := i * bucketWidth
		vMax := vMin + bucketWidth - 1
		if buckets[i] >= 0.5 { // Show buckets with >= 0.5%
			bar := ""
			barLen := int(buckets[i] / 2) // 2% per char
			for j := 0; j < barLen && j < 40; j++ {
				bar += "#"
			}
			fmt.Printf("    [%3d-%3d]: %5.1f%% %s\n", vMin, vMax, buckets[i], bar)
		}
	}
}

// ExtractLogoFeatures extracts features from a logo region.
// Logos are typically high-contrast markings on component bodies.
func ExtractLogoFeatures(img image.Image, bounds geometry.Rect, dpi float64) LogoSample {
	sample := LogoSample{
		Bounds: bounds,
	}

	// Convert to mm
	mmToPixels := dpi / 25.4
	sample.WidthMM = bounds.Width / mmToPixels
	sample.HeightMM = bounds.Height / mmToPixels

	// Convert Go image to Mat
	mat, err := imageToMat(img)
	if err != nil {
		fmt.Printf("ExtractLogoFeatures: image conversion error: %v\n", err)
		return sample
	}
	defer mat.Close()

	// Clamp bounds to image
	rect := image.Rect(
		int(bounds.X),
		int(bounds.Y),
		int(bounds.X+bounds.Width),
		int(bounds.Y+bounds.Height),
	)
	if rect.Min.X < 0 {
		rect.Min.X = 0
	}
	if rect.Min.Y < 0 {
		rect.Min.Y = 0
	}
	if rect.Max.X > mat.Cols() {
		rect.Max.X = mat.Cols()
	}
	if rect.Max.Y > mat.Rows() {
		rect.Max.Y = mat.Rows()
	}
	if rect.Dx() <= 0 || rect.Dy() <= 0 {
		return sample
	}

	// Extract region
	region := mat.Region(rect)
	defer region.Close()

	// Convert to HSV
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(region, &hsv, gocv.ColorBGRToHSV)

	// Analyze histogram - for logos, background is typically dark, foreground is light
	sample.BackgroundVal, sample.ForegroundVal, sample.BackgroundPct, sample.ForegroundPct = analyzeValueHistogram(hsv)

	// Get full HSV bucket distributions for all three channels
	hsvHist := AnalyzeHSVBuckets(hsv)

	// Calculate contrast ratio
	if sample.BackgroundVal > 0 {
		sample.ContrastRatio = sample.ForegroundVal / sample.BackgroundVal
	} else {
		sample.ContrastRatio = sample.ForegroundVal
	}

	fmt.Printf("=== Logo Sample Added ===\n")
	fmt.Printf("  Size: %.1f x %.1f mm\n", sample.WidthMM, sample.HeightMM)
	printHSVHistogram("H (Hue)", hsvHist.H, 180)
	printHSVHistogram("S (Saturation)", hsvHist.S, 256)
	printHSVHistogram("V (Value)", hsvHist.V, 256)
	fmt.Printf("  Background: V=%3.0f (bucket %d, %.1f%%)\n",
		sample.BackgroundVal, int(sample.BackgroundVal)/ColorBucketWidth, sample.BackgroundPct)
	fmt.Printf("  Foreground: V=%3.0f (bucket %d, %.1f%%)\n",
		sample.ForegroundVal, int(sample.ForegroundVal)/ColorBucketWidth, sample.ForegroundPct)
	fmt.Printf("  Contrast ratio: %.2f\n", sample.ContrastRatio)
	fmt.Printf("=========================\n")

	return sample
}

// Standard DIP package widths in mm
const (
	NarrowDIPWidthMM = 7.62  // 0.3 inch
	WideDIPWidthMM   = 15.24 // 0.6 inch
	PinPitchMM       = 2.54  // 0.1 inch
	WidthToleranceMM = 1.5   // Allow +/- 1.5mm tolerance on width
	LengthToleranceMM = 1.5  // Allow +/- 1.5mm tolerance on length quantization
)

// IsValidDIPWidth checks if a width matches either standard DIP width (0.3" or 0.6").
func IsValidDIPWidth(widthMM float64) bool {
	// Check narrow width (0.3" = 7.62mm)
	if math.Abs(widthMM-NarrowDIPWidthMM) <= WidthToleranceMM {
		return true
	}
	// Check wide width (0.6" = 15.24mm)
	if math.Abs(widthMM-WideDIPWidthMM) <= WidthToleranceMM {
		return true
	}
	return false
}

// isValidDIPLength checks if a length is approximately a multiple of 0.1 inch (2.54mm).
func isValidDIPLength(lengthMM float64) bool {
	// Calculate how many 0.1" units fit
	units := lengthMM / PinPitchMM
	// Check if close to an integer
	remainder := math.Abs(units - math.Round(units))
	// Allow tolerance (in terms of 0.1" units)
	return remainder <= (LengthToleranceMM / PinPitchMM)
}

// classifyPackage determines the package type based on dimensions.
// DIP packages come in two standard widths:
//   - Narrow (0.3" = 7.62mm): DIP-8, DIP-14, DIP-16, DIP-18, DIP-20, DIP-24, DIP-28
//   - Wide (0.6" = 15.24mm): DIP-40 (and some DIP-24, DIP-28)
//
// Pin count = 2 * (number of pins per side), and pins are at 0.1" (2.54mm) pitch.
func classifyPackage(widthPx, heightPx, mmToPixels float64) string {
	// Convert to mm
	widthMM := widthPx / mmToPixels
	heightMM := heightPx / mmToPixels

	// Determine width type: narrow (0.3" = 7.62mm) or wide (0.6" = 15.24mm)
	// Threshold at 11mm (halfway between 7.62 and 15.24)
	isWide := widthMM > 11

	// Height determines pin count
	// Pin pitch is 2.54mm (0.1"), pins on both sides
	// Height = (pins_per_side - 1) * pitch + some body overhang
	// Approximate: pins_per_side ≈ height / pitch
	pitchMM := 2.54
	pinsPerSide := int(math.Round(heightMM / pitchMM))

	// Total pins = 2 * pins per side, must be even
	totalPins := pinsPerSide * 2

	// Standard narrow DIP pin counts
	narrowPins := []int{8, 14, 16, 18, 20, 24, 28}
	// Wide DIP is typically 40-pin
	widePins := []int{24, 28, 40}

	var standardPins []int
	if isWide {
		standardPins = widePins
	} else {
		standardPins = narrowPins
	}

	// Find closest standard pin count
	closestPins := standardPins[0]
	minDiff := totalPins - closestPins
	if minDiff < 0 {
		minDiff = -minDiff
	}
	for _, pins := range standardPins {
		diff := totalPins - pins
		if diff < 0 {
			diff = -diff
		}
		if diff < minDiff {
			minDiff = diff
			closestPins = pins
		}
	}

	return fmt.Sprintf("DIP-%d", closestPins)
}

// imageToMat converts a Go image.Image to an OpenCV Mat.
func imageToMat(img image.Image) (gocv.Mat, error) {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	// Create RGBA image
	rgba := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			rgba.Set(x, y, img.At(x, y))
		}
	}

	// Create Mat from RGBA data
	mat, err := gocv.NewMatFromBytes(h, w, gocv.MatTypeCV8UC4, rgba.Pix)
	if err != nil {
		return gocv.Mat{}, err
	}

	// Convert RGBA to BGR (OpenCV format)
	bgr := gocv.NewMat()
	gocv.CvtColor(mat, &bgr, gocv.ColorRGBAToBGR)
	mat.Close()

	return bgr, nil
}

// CreateOverlay creates a canvas overlay with white hollow rectangles for detected components.
func CreateOverlay(components []*Component) *canvas.Overlay {
	overlay := &canvas.Overlay{
		Color: color.RGBA{R: 255, G: 255, B: 255, A: 255}, // White
	}

	for _, comp := range components {
		rect := canvas.OverlayRect{
			X:      int(comp.Bounds.X),
			Y:      int(comp.Bounds.Y),
			Width:  int(comp.Bounds.Width),
			Height: int(comp.Bounds.Height),
			Label:  comp.ID + " " + comp.Package,
			Fill:   canvas.FillNone, // Hollow rectangle
		}
		overlay.Rectangles = append(overlay.Rectangles, rect)
	}

	return overlay
}

// CreateDebugImage creates a visualization of detected components.
func CreateDebugImage(img gocv.Mat, components []*Component) gocv.Mat {
	debug := img.Clone()

	green := color.RGBA{R: 0, G: 255, B: 0, A: 255}
	red := color.RGBA{R: 255, G: 0, B: 0, A: 255}

	for _, comp := range components {
		rect := image.Rect(
			int(comp.Bounds.X),
			int(comp.Bounds.Y),
			int(comp.Bounds.X+comp.Bounds.Width),
			int(comp.Bounds.Y+comp.Bounds.Height),
		)

		col := green
		if !comp.Confirmed {
			col = red
		}

		gocv.Rectangle(&debug, rect, col, 2)

		// Draw label
		labelPos := image.Point{X: rect.Min.X, Y: rect.Min.Y - 5}
		if labelPos.Y < 15 {
			labelPos.Y = rect.Max.Y + 15
		}
		gocv.PutText(&debug, comp.ID+" "+comp.Package, labelPos,
			gocv.FontHersheyPlain, 1.0, col, 1)
	}

	return debug
}

// FloodFillResult contains the result of a flood fill operation.
type FloodFillResult struct {
	Bounds     geometry.RectInt // Bounding box of filled region
	PixelCount int              // Number of pixels filled
	SeedColor  color.RGBA       // Color at the seed point
}

// FloodFillDetect performs a flood fill from a click point to find a component region.
// Returns the bounding box of connected similar-color pixels.
// colorTolerance is the maximum difference in each RGB channel to consider a match (0-255).
func FloodFillDetect(img image.Image, clickX, clickY int, colorTolerance int) (*FloodFillResult, error) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	// Validate click position
	if clickX < bounds.Min.X || clickX >= bounds.Max.X ||
		clickY < bounds.Min.Y || clickY >= bounds.Max.Y {
		return nil, fmt.Errorf("click position (%d,%d) outside image bounds", clickX, clickY)
	}

	// Get seed color
	seedColor := img.At(clickX, clickY)
	sr, sg, sb, _ := seedColor.RGBA()
	seedR := uint8(sr >> 8)
	seedG := uint8(sg >> 8)
	seedB := uint8(sb >> 8)

	if false {
		fmt.Printf("FloodFill: seed at (%d,%d) color RGB(%d,%d,%d) tolerance=%d\n",
			clickX, clickY, seedR, seedG, seedB, colorTolerance)
	}

	// Visited map
	visited := make([]bool, w*h)

	// Track bounds
	minX, minY := clickX, clickY
	maxX, maxY := clickX, clickY
	pixelCount := 0

	// Stack for flood fill (use slice as stack)
	stack := []image.Point{{X: clickX, Y: clickY}}

	// Color matching function
	colorMatch := func(x, y int) bool {
		c := img.At(x, y)
		r, g, b, _ := c.RGBA()
		pr := uint8(r >> 8)
		pg := uint8(g >> 8)
		pb := uint8(b >> 8)

		dr := absDiff(int(pr), int(seedR))
		dg := absDiff(int(pg), int(seedG))
		db := absDiff(int(pb), int(seedB))

		return dr <= colorTolerance && dg <= colorTolerance && db <= colorTolerance
	}

	// Flood fill
	for len(stack) > 0 {
		// Pop from stack
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		x, y := p.X, p.Y

		// Check bounds
		if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
			continue
		}

		// Check if already visited
		idx := (y-bounds.Min.Y)*w + (x - bounds.Min.X)
		if visited[idx] {
			continue
		}

		// Check color match
		if !colorMatch(x, y) {
			continue
		}

		// Mark visited
		visited[idx] = true
		pixelCount++

		// Update bounds
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}

		// Add neighbors (4-connected)
		stack = append(stack,
			image.Point{X: x + 1, Y: y},
			image.Point{X: x - 1, Y: y},
			image.Point{X: x, Y: y + 1},
			image.Point{X: x, Y: y - 1},
		)
	}

	if pixelCount == 0 {
		return nil, fmt.Errorf("no pixels matched at seed point")
	}

	result := &FloodFillResult{
		Bounds: geometry.RectInt{
			X:      minX,
			Y:      minY,
			Width:  maxX - minX + 1,
			Height: maxY - minY + 1,
		},
		PixelCount: pixelCount,
		SeedColor:  color.RGBA{R: seedR, G: seedG, B: seedB, A: 255},
	}

	if false {
		fmt.Printf("FloodFill: found region (%d,%d) %dx%d with %d pixels\n",
			result.Bounds.X, result.Bounds.Y, result.Bounds.Width, result.Bounds.Height, pixelCount)
	}

	return result, nil
}

// absDiff returns absolute difference between two ints.
func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// GridPoint represents a single sample point in the scoring grid.
type GridPoint struct {
	X, Y    int     // Position in image coordinates
	Matches bool    // Whether this point matches the seed color
	Score   float64 // Row/column score at this point (for coloring)
}

// GridScoreResult contains the scoring grid data for debug visualization.
type GridScoreResult struct {
	Points       []GridPoint        // All grid sample points
	RowScores    []float64          // Score for each row
	ColScores    []float64          // Score for each column
	OrigBounds   geometry.RectInt   // Original bounds
	TrimBounds   geometry.RectInt   // Trimmed bounds
	GridStep     int                // Grid spacing used
	MinScore     float64            // Minimum score threshold
}

// GetGridScores computes and returns the scoring grid data for debug visualization.
func GetGridScores(img image.Image, bounds geometry.RectInt, seedColor color.RGBA, colorTolerance int, gridStep int, minScore float64) *GridScoreResult {
	if gridStep < 1 {
		gridStep = 2
	}
	if minScore <= 0 {
		minScore = 0.5
	}

	seedR, seedG, seedB := int(seedColor.R), int(seedColor.G), int(seedColor.B)

	// Color matching function
	colorMatch := func(x, y int) bool {
		c := img.At(x, y)
		r, g, b, _ := c.RGBA()
		pr := int(uint8(r >> 8))
		pg := int(uint8(g >> 8))
		pb := int(uint8(b >> 8))

		dr := absDiff(pr, seedR)
		dg := absDiff(pg, seedG)
		db := absDiff(pb, seedB)

		return dr <= colorTolerance && dg <= colorTolerance && db <= colorTolerance
	}

	// Calculate grid dimensions
	cols := (bounds.Width + gridStep - 1) / gridStep
	rows := (bounds.Height + gridStep - 1) / gridStep

	result := &GridScoreResult{
		Points:     make([]GridPoint, 0, rows*cols),
		RowScores:  make([]float64, rows),
		ColScores:  make([]float64, cols),
		OrigBounds: bounds,
		GridStep:   gridStep,
		MinScore:   minScore,
	}

	if cols < 3 || rows < 3 {
		result.TrimBounds = bounds
		return result
	}

	// First pass: collect all points and calculate matches
	grid := make([][]bool, rows)
	for row := 0; row < rows; row++ {
		grid[row] = make([]bool, cols)
		y := bounds.Y + row*gridStep
		if y >= bounds.Y+bounds.Height {
			y = bounds.Y + bounds.Height - 1
		}

		for col := 0; col < cols; col++ {
			x := bounds.X + col*gridStep
			if x >= bounds.X+bounds.Width {
				x = bounds.X + bounds.Width - 1
			}
			grid[row][col] = colorMatch(x, y)
		}
	}

	// Score each row
	for row := 0; row < rows; row++ {
		matches := 0
		for col := 0; col < cols; col++ {
			if grid[row][col] {
				matches++
			}
		}
		result.RowScores[row] = float64(matches) / float64(cols)
	}

	// Score each column
	for col := 0; col < cols; col++ {
		matches := 0
		for row := 0; row < rows; row++ {
			if grid[row][col] {
				matches++
			}
		}
		result.ColScores[col] = float64(matches) / float64(rows)
	}

	// Build points list with match info
	for row := 0; row < rows; row++ {
		y := bounds.Y + row*gridStep
		if y >= bounds.Y+bounds.Height {
			y = bounds.Y + bounds.Height - 1
		}

		for col := 0; col < cols; col++ {
			x := bounds.X + col*gridStep
			if x >= bounds.X+bounds.Width {
				x = bounds.X + bounds.Width - 1
			}
			// Use the minimum of row/col scores to determine if this point is in a "good" area
			score := result.RowScores[row]
			if result.ColScores[col] < score {
				score = result.ColScores[col]
			}
			result.Points = append(result.Points, GridPoint{
				X:       x,
				Y:       y,
				Matches: grid[row][col],
				Score:   score,
			})
		}
	}

	// Find trim bounds: start from center, expand outward until >75% miss
	centerRow := rows / 2
	centerCol := cols / 2

	// Expand upward from center until we hit a bad row
	topRow := centerRow
	for topRow > 0 && result.RowScores[topRow-1] >= minScore {
		topRow--
	}

	// Expand downward from center until we hit a bad row
	bottomRow := centerRow
	for bottomRow < rows-1 && result.RowScores[bottomRow+1] >= minScore {
		bottomRow++
	}

	// Expand left from center until we hit a bad column
	leftCol := centerCol
	for leftCol > 0 && result.ColScores[leftCol-1] >= minScore {
		leftCol--
	}

	// Expand right from center until we hit a bad column
	rightCol := centerCol
	for rightCol < cols-1 && result.ColScores[rightCol+1] >= minScore {
		rightCol++
	}

	newX := bounds.X + leftCol*gridStep
	newY := bounds.Y + topRow*gridStep
	newWidth := (rightCol - leftCol + 1) * gridStep
	newHeight := (bottomRow - topRow + 1) * gridStep

	if newX < bounds.X {
		newX = bounds.X
	}
	if newY < bounds.Y {
		newY = bounds.Y
	}
	if newX+newWidth > bounds.X+bounds.Width {
		newWidth = bounds.X + bounds.Width - newX
	}
	if newY+newHeight > bounds.Y+bounds.Height {
		newHeight = bounds.Y + bounds.Height - newY
	}
	if newWidth < gridStep*2 {
		newWidth = bounds.Width
		newX = bounds.X
	}
	if newHeight < gridStep*2 {
		newHeight = bounds.Height
		newY = bounds.Y
	}

	result.TrimBounds = geometry.RectInt{
		X:      newX,
		Y:      newY,
		Width:  newWidth,
		Height: newHeight,
	}

	return result
}

// TrimFloodFillBounds refines flood fill bounds by scoring a grid and trimming low-scoring edges.
// This removes green PCB areas and metallic pins that got included in the initial flood fill.
// gridStep is the pixel spacing for the scoring grid (e.g., 2-4 pixels).
// minScore is the minimum percentage (0-1) of matching pixels required to keep a row/column.
func TrimFloodFillBounds(img image.Image, bounds geometry.RectInt, seedColor color.RGBA, colorTolerance int, gridStep int, minScore float64) geometry.RectInt {
	if gridStep < 1 {
		gridStep = 2
	}
	if minScore <= 0 {
		minScore = 0.5 // Default: require 50% matching pixels
	}

	seedR, seedG, seedB := int(seedColor.R), int(seedColor.G), int(seedColor.B)

	// Color matching function
	colorMatch := func(x, y int) bool {
		c := img.At(x, y)
		r, g, b, _ := c.RGBA()
		pr := int(uint8(r >> 8))
		pg := int(uint8(g >> 8))
		pb := int(uint8(b >> 8))

		dr := absDiff(pr, seedR)
		dg := absDiff(pg, seedG)
		db := absDiff(pb, seedB)

		return dr <= colorTolerance && dg <= colorTolerance && db <= colorTolerance
	}

	// Calculate grid dimensions
	cols := (bounds.Width + gridStep - 1) / gridStep
	rows := (bounds.Height + gridStep - 1) / gridStep

	if cols < 3 || rows < 3 {
		// Too small to trim meaningfully
		return bounds
	}

	// Score each row (horizontal stripe)
	rowScores := make([]float64, rows)
	for row := 0; row < rows; row++ {
		y := bounds.Y + row*gridStep
		if y >= bounds.Y+bounds.Height {
			y = bounds.Y + bounds.Height - 1
		}

		matches := 0
		samples := 0
		for col := 0; col < cols; col++ {
			x := bounds.X + col*gridStep
			if x >= bounds.X+bounds.Width {
				x = bounds.X + bounds.Width - 1
			}
			samples++
			if colorMatch(x, y) {
				matches++
			}
		}
		if samples > 0 {
			rowScores[row] = float64(matches) / float64(samples)
		}
	}

	// Score each column (vertical stripe)
	colScores := make([]float64, cols)
	for col := 0; col < cols; col++ {
		x := bounds.X + col*gridStep
		if x >= bounds.X+bounds.Width {
			x = bounds.X + bounds.Width - 1
		}

		matches := 0
		samples := 0
		for row := 0; row < rows; row++ {
			y := bounds.Y + row*gridStep
			if y >= bounds.Y+bounds.Height {
				y = bounds.Y + bounds.Height - 1
			}
			samples++
			if colorMatch(x, y) {
				matches++
			}
		}
		if samples > 0 {
			colScores[col] = float64(matches) / float64(samples)
		}
	}

	// Find trim bounds: start from center, expand outward until >75% miss
	centerRow := rows / 2
	centerCol := cols / 2

	// Expand upward from center until we hit a bad row
	topRow := centerRow
	for topRow > 0 && rowScores[topRow-1] >= minScore {
		topRow--
	}

	// Expand downward from center until we hit a bad row
	bottomRow := centerRow
	for bottomRow < rows-1 && rowScores[bottomRow+1] >= minScore {
		bottomRow++
	}

	// Expand left from center until we hit a bad column
	leftCol := centerCol
	for leftCol > 0 && colScores[leftCol-1] >= minScore {
		leftCol--
	}

	// Expand right from center until we hit a bad column
	rightCol := centerCol
	for rightCol < cols-1 && colScores[rightCol+1] >= minScore {
		rightCol++
	}

	// Convert grid indices back to pixel coordinates
	newX := bounds.X + leftCol*gridStep
	newY := bounds.Y + topRow*gridStep
	newWidth := (rightCol - leftCol + 1) * gridStep
	newHeight := (bottomRow - topRow + 1) * gridStep

	// Clamp to original bounds
	if newX < bounds.X {
		newX = bounds.X
	}
	if newY < bounds.Y {
		newY = bounds.Y
	}
	if newX+newWidth > bounds.X+bounds.Width {
		newWidth = bounds.X + bounds.Width - newX
	}
	if newY+newHeight > bounds.Y+bounds.Height {
		newHeight = bounds.Y + bounds.Height - newY
	}

	// Ensure minimum size
	if newWidth < gridStep*2 {
		newWidth = bounds.Width
		newX = bounds.X
	}
	if newHeight < gridStep*2 {
		newHeight = bounds.Height
		newY = bounds.Y
	}

	trimmed := geometry.RectInt{
		X:      newX,
		Y:      newY,
		Width:  newWidth,
		Height: newHeight,
	}

	if false {
		fmt.Printf("TrimFloodFill: %dx%d -> %dx%d (trimmed top=%d bot=%d left=%d right=%d rows)\n",
			bounds.Width, bounds.Height, trimmed.Width, trimmed.Height,
			topRow, rows-1-bottomRow, leftCol, cols-1-rightCol)
	}

	return trimmed
}
