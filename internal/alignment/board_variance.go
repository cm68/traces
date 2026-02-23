package alignment

import (
	"fmt"
	"image"
	"math"

	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// varianceGrid holds per-cell on-board classification from S+V variance analysis.
type varianceGrid struct {
	onBoard  []byte // 1=board, 0=background (row-major)
	gridCols int
	gridRows int
	cellSize int
	onCount  int // number of on-board cells
}

// VarianceBoardResult contains the result of variance-based board detection.
type VarianceBoardResult struct {
	Bounds   geometry.RectInt // Axis-aligned crop bounds (in rotated-image space)
	Angle    float64          // Rotation angle in degrees to orthogonalize the board
	Detected bool
}

// DetectBoardByVariance detects board bounds and rotation using HSV variance analysis.
// Board cells have high S+V variance (traces, components, text) while scanner background
// is uniform gray with near-zero variance. Uses detect-rotate-redetect for precise
// axis-aligned bounds.
func DetectBoardByVariance(img image.Image) VarianceBoardResult {
	mat, err := imageToMat(img)
	if err != nil {
		return VarianceBoardResult{Detected: false}
	}
	defer mat.Close()
	return detectBoardByVarianceMat(mat)
}

// detectBoardByVarianceMat implements the full variance-based board detection pipeline.
func detectBoardByVarianceMat(mat gocv.Mat) VarianceBoardResult {
	imgW := mat.Cols()
	imgH := mat.Rows()

	// Cell size: ~1% of long edge, clamped to [10, 100] px
	cellSize := imgW
	if imgH > cellSize {
		cellSize = imgH
	}
	cellSize = cellSize / 100
	if cellSize < 10 {
		cellSize = 10
	}
	if cellSize > 100 {
		cellSize = 100
	}

	fmt.Printf("[BoardVariance] Image %dx%d, cell %dpx\n", imgW, imgH, cellSize)

	// Pass 1: Classify cells on the original image
	grid := computeVarianceGrid(mat, cellSize)
	if grid == nil || grid.onCount == 0 {
		fmt.Println("[BoardVariance] No high-variance cells found")
		return VarianceBoardResult{Detected: false}
	}

	fmt.Printf("[BoardVariance] Pass 1: %d/%d cells on-board (%.1f%%)\n",
		grid.onCount, grid.gridRows*grid.gridCols,
		float64(grid.onCount)/float64(grid.gridRows*grid.gridCols)*100)

	// Fit MinAreaRect to on-board cell centroids to get rotation angle
	angle := boardAngleFromCells(grid)
	fmt.Printf("[BoardVariance] Detected rotation: %.2f°\n", angle)

	// If rotation is small enough, just compute bounds from the original grid
	if math.Abs(angle) < 0.1 {
		minX, minY, maxX, maxY := boundsFromGrid(grid, imgW, imgH)
		fmt.Printf("[BoardVariance] No rotation needed, bounds (%d,%d) %dx%d\n",
			minX, minY, maxX-minX, maxY-minY)
		return VarianceBoardResult{
			Bounds:   geometry.RectInt{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY},
			Angle:    0,
			Detected: true,
		}
	}

	// Rotate the image and re-detect for precise axis-aligned bounds
	rotated := rotateMatByAngle(mat, angle)
	defer rotated.Close()

	rotW := rotated.Cols()
	rotH := rotated.Rows()

	grid2 := computeVarianceGrid(rotated, cellSize)
	if grid2 == nil || grid2.onCount == 0 {
		// Fallback to unrotated bounds
		minX, minY, maxX, maxY := boundsFromGrid(grid, imgW, imgH)
		return VarianceBoardResult{
			Bounds:   geometry.RectInt{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY},
			Angle:    0,
			Detected: true,
		}
	}

	fmt.Printf("[BoardVariance] Pass 2 (rotated): %d/%d cells on-board\n",
		grid2.onCount, grid2.gridRows*grid2.gridCols)

	minX, minY, maxX, maxY := boundsFromGrid(grid2, rotW, rotH)
	fmt.Printf("[BoardVariance] Final bounds (%d,%d) %dx%d, rotation %.2f°\n",
		minX, minY, maxX-minX, maxY-minY, angle)

	return VarianceBoardResult{
		Bounds:   geometry.RectInt{X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY},
		Angle:    angle,
		Detected: true,
	}
}

// computeVarianceGrid classifies image cells by S+V variance.
// Board cells have high combined saturation+value variance; background cells have near-zero.
// Uses histogram gap thresholding to separate the two populations.
func computeVarianceGrid(mat gocv.Mat, cellSize int) *varianceGrid {
	imgW := mat.Cols()
	imgH := mat.Rows()

	if cellSize < 4 {
		cellSize = 4
	}

	gridCols := (imgW + cellSize - 1) / cellSize
	gridRows := (imgH + cellSize - 1) / cellSize
	totalCells := gridRows * gridCols

	// Convert to HSV, extract raw bytes
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(mat, &hsv, gocv.ColorBGRToHSV)
	hsvBytes := hsv.ToBytes()
	ch := 3

	// Compute per-cell S+V variance (skip H to avoid hue-wrap artifacts at 180)
	scores := make([]float64, totalCells)
	for gy := 0; gy < gridRows; gy++ {
		for gx := 0; gx < gridCols; gx++ {
			x1 := gx * cellSize
			y1 := gy * cellSize
			x2 := x1 + cellSize
			y2 := y1 + cellSize
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

	// Quantize scores to 0-255
	maxScore := 0.0
	for _, s := range scores {
		if s > maxScore {
			maxScore = s
		}
	}

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

	// Find biggest gap (run of empty bins) with best-balanced split
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

	// Threshold at midpoint of the best gap
	threshold := 0.0
	if bestGap.length > 0 {
		midBin := float64(bestGap.start) + float64(bestGap.length)/2
		threshold = midBin / 255 * maxScore
	}

	// Classify cells
	onBoard := make([]byte, totalCells)
	onCount := 0
	for i := 0; i < totalCells; i++ {
		if scores[i] > threshold {
			onBoard[i] = 1
			onCount++
		}
	}

	if onCount == 0 {
		return nil
	}

	return &varianceGrid{
		onBoard:  onBoard,
		gridCols: gridCols,
		gridRows: gridRows,
		cellSize: cellSize,
		onCount:  onCount,
	}
}

// boardAngleFromCells fits a MinAreaRect to on-board cell centroids to determine
// the board's rotation angle (degrees, normalized to [-45, 45]).
func boardAngleFromCells(grid *varianceGrid) float64 {
	// Collect centroids of on-board cells
	points := make([]image.Point, 0, grid.onCount)
	for gy := 0; gy < grid.gridRows; gy++ {
		for gx := 0; gx < grid.gridCols; gx++ {
			if grid.onBoard[gy*grid.gridCols+gx] == 1 {
				cx := gx*grid.cellSize + grid.cellSize/2
				cy := gy*grid.cellSize + grid.cellSize/2
				points = append(points, image.Point{X: cx, Y: cy})
			}
		}
	}

	if len(points) < 3 {
		return 0
	}

	pv := gocv.NewPointVectorFromPoints(points)
	defer pv.Close()

	rotRect := gocv.MinAreaRect(pv)

	// Extract and normalize angle (same logic as DetectBoardRotation)
	rawAngle := rotRect.Angle
	width := float64(rotRect.Width)
	height := float64(rotRect.Height)

	var angle float64
	if width >= height {
		angle = rawAngle
	} else {
		angle = rawAngle + 90
	}

	// Normalize to [-45, 45]
	for angle > 45 {
		angle -= 90
	}
	for angle < -45 {
		angle += 90
	}

	return angle
}

// boundsFromGrid computes axis-aligned bounds from a variance grid using
// row/column density filtering. A row/column is "board" if >= 50% of its cells
// are on-board. Returns (minX, minY, maxX, maxY) in pixels.
func boundsFromGrid(grid *varianceGrid, imgW, imgH int) (int, int, int, int) {
	const densityThreshold = 0.50

	colCounts := make([]int, grid.gridCols)
	rowCounts := make([]int, grid.gridRows)
	for gy := 0; gy < grid.gridRows; gy++ {
		for gx := 0; gx < grid.gridCols; gx++ {
			if grid.onBoard[gy*grid.gridCols+gx] == 1 {
				colCounts[gx]++
				rowCounts[gy]++
			}
		}
	}

	// Find range of dense columns
	minGX, maxGX := -1, -1
	for gx := 0; gx < grid.gridCols; gx++ {
		density := float64(colCounts[gx]) / float64(grid.gridRows)
		if density >= densityThreshold {
			if minGX < 0 {
				minGX = gx
			}
			maxGX = gx
		}
	}

	// Find range of dense rows
	minGY, maxGY := -1, -1
	for gy := 0; gy < grid.gridRows; gy++ {
		density := float64(rowCounts[gy]) / float64(grid.gridCols)
		if density >= densityThreshold {
			if minGY < 0 {
				minGY = gy
			}
			maxGY = gy
		}
	}

	// Fallback: bounding box of all on-board cells
	if minGX < 0 || minGY < 0 {
		for gy := 0; gy < grid.gridRows; gy++ {
			for gx := 0; gx < grid.gridCols; gx++ {
				if grid.onBoard[gy*grid.gridCols+gx] == 1 {
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

	minX := minGX * grid.cellSize
	minY := minGY * grid.cellSize
	maxX := (maxGX + 1) * grid.cellSize
	maxY := (maxGY + 1) * grid.cellSize
	if maxX > imgW {
		maxX = imgW
	}
	if maxY > imgH {
		maxY = imgH
	}

	return minX, minY, maxX, maxY
}
