package alignment

import (
	"fmt"
	"image"
	"sort"

	"pcb-tracer/internal/board"
	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// detectContactsOnEdge detects contacts on a specific edge of the board.
// Returns the detected contacts, search bounds, and line parameters for rescue.
func detectContactsOnEdge(img gocv.Mat, goldMask gocv.Mat, boardBounds geometry.RectInt, edge string, spec board.Spec, params DetectionParams) ([]Contact, geometry.RectInt, *ContactLineParams) {
	bx, by, bw, bh := boardBounds.X, boardBounds.Y, boardBounds.Width, boardBounds.Height
	imgH := img.Rows()
	imgW := img.Cols()

	// Use larger margins - contacts may be outside detected board bounds
	// 10% of board dimension or 300 pixels, whichever is larger
	marginY := max(int(float64(bh)*0.10), 300)
	marginX := max(int(float64(bw)*0.10), 300)

	var searchX1, searchY1, searchX2, searchY2 int
	var isHorizontal bool

	switch edge {
	case "top":
		searchY1 = max(0, by-marginY)
		searchY2 = by + int(float64(bh)*0.20) // Search 20% into board
		searchX1 = bx
		searchX2 = bx + bw
		isHorizontal = true
	case "bottom":
		searchY1 = by + bh - int(float64(bh)*0.20)
		searchY2 = min(imgH, by+bh+marginY)
		searchX1 = bx
		searchX2 = bx + bw
		isHorizontal = true
	case "left":
		searchX1 = max(0, bx-marginX)
		searchX2 = bx + int(float64(bw)*0.20)
		searchY1 = by
		searchY2 = by + bh
		isHorizontal = false
	case "right":
		searchX1 = bx + bw - int(float64(bw)*0.20)
		searchX2 = min(imgW, bx+bw+marginX)
		searchY1 = by
		searchY2 = by + bh
		isHorizontal = false
	}

	// Clamp search region to image bounds
	maskH := goldMask.Rows()
	maskW := goldMask.Cols()
	if searchX1 < 0 {
		searchX1 = 0
	}
	if searchY1 < 0 {
		searchY1 = 0
	}
	if searchX2 > maskW {
		searchX2 = maskW
	}
	if searchY2 > maskH {
		searchY2 = maskH
	}

	searchBounds := geometry.RectInt{
		X: searchX1, Y: searchY1,
		Width: searchX2 - searchX1, Height: searchY2 - searchY1,
	}

	if searchX2 <= searchX1 || searchY2 <= searchY1 {
		return nil, searchBounds, nil
	}

	// Log search area
	searchW := searchX2 - searchX1
	searchH := searchY2 - searchY1
	if params.DPI > 0 {
		fmt.Printf("  Search %s: X=%d-%d Y=%d-%d (%dx%d px = %.2fx%.2f in)\n",
			edge, searchX1, searchX2, searchY1, searchY2,
			searchW, searchH,
			float64(searchW)/params.DPI, float64(searchH)/params.DPI)
	} else {
		fmt.Printf("  Search %s: X=%d-%d Y=%d-%d (%dx%d px)\n",
			edge, searchX1, searchX2, searchY1, searchY2, searchW, searchH)
	}

	// Extract region from gold mask
	region := goldMask.Region(image.Rect(searchX1, searchY1, searchX2, searchY2))
	defer region.Close()

	// Find contours in region
	contours := gocv.FindContours(region, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	// First pass: collect ALL gold regions that could be contacts (loose filtering)
	var candidates []Contact
	for i := 0; i < contours.Size(); i++ {
		rect := gocv.BoundingRect(contours.At(i))
		w, h := rect.Dx(), rect.Dy()
		area := w * h

		if w > 0 && h > 0 && area >= params.MinArea/10 {
			var aspect float64
			if isHorizontal {
				aspect = float64(h) / float64(w)
			} else {
				aspect = float64(w) / float64(h)
			}

			// Loose aspect ratio check (at least 2:1)
			if aspect >= 2.0 && aspect <= 15.0 {
				candidates = append(candidates, Contact{
					Bounds: geometry.RectInt{
						X:      searchX1 + rect.Min.X,
						Y:      searchY1 + rect.Min.Y,
						Width:  w,
						Height: h,
					},
				})
			}
		}
	}

	if len(candidates) == 0 {
		return nil, searchBounds, nil
	}

	// Sort by position along edge
	if isHorizontal {
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Bounds.X < candidates[j].Bounds.X
		})
	} else {
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Bounds.Y < candidates[j].Bounds.Y
		})
	}

	// Calculate centers with sub-pixel precision using image moments
	for i := range candidates {
		// First set bounding box center as fallback
		candidates[i].Center = geometry.Point2D{
			X: float64(candidates[i].Bounds.X) + float64(candidates[i].Bounds.Width)/2,
			Y: float64(candidates[i].Bounds.Y) + float64(candidates[i].Bounds.Height)/2,
		}

		// Refine using image moments on the gold mask region
		b := candidates[i].Bounds
		if b.X >= searchX1 && b.Y >= searchY1 && b.X+b.Width <= searchX2 && b.Y+b.Height <= searchY2 {
			// Extract the contact region from gold mask (relative to search region)
			localX := b.X - searchX1
			localY := b.Y - searchY1
			if localX >= 0 && localY >= 0 && localX+b.Width <= region.Cols() && localY+b.Height <= region.Rows() {
				contactRegion := region.Region(image.Rect(localX, localY, localX+b.Width, localY+b.Height))
				moments := gocv.Moments(contactRegion, true)
				contactRegion.Close()

				// Calculate centroid from moments: cx = m10/m00, cy = m01/m00
				m00 := moments["m00"]
				if m00 > 0 {
					cx := moments["m10"] / m00
					cy := moments["m01"] / m00
					// Convert back to image coordinates
					candidates[i].Center = geometry.Point2D{
						X: float64(b.X) + cx,
						Y: float64(b.Y) + cy,
					}
				}
			}
		}
	}

	// Filter to contacts that fit expected grid pattern with stricter validation
	filtered, lineParams := filterToGridStrictWithParams(candidates, spec, params, isHorizontal)

	return filtered, searchBounds, lineParams
}
