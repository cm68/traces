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

	// Calculate expected contact dimensions from DPI if available
	var expectW, expectH float64
	if params.DPI > 0 && spec != nil && spec.ContactSpec() != nil {
		cs := spec.ContactSpec()
		expectW = cs.WidthInches * params.DPI
		expectH = cs.HeightInches * params.DPI
	}

	// First pass: collect ALL gold regions that could be contacts (loose filtering)
	var candidates []Contact
	totalContours := contours.Size()
	rejectedTooSmall := 0
	rejectedAspect := 0
	rejectedSize := 0
	var rejectedAspectSamples []string // first few rejected by aspect
	var rejectedSizeSamples []string   // first few rejected by size
	for i := 0; i < totalContours; i++ {
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
			if aspect < 2.0 || aspect > 15.0 {
				rejectedAspect++
				if len(rejectedAspectSamples) < 10 {
					rejectedAspectSamples = append(rejectedAspectSamples,
						fmt.Sprintf("%dx%d@(%d,%d) asp=%.2f", w, h,
							searchX1+rect.Min.X, searchY1+rect.Min.Y, aspect))
				}
				continue
			}

			// When DPI is known, reject contours far from expected size
			// Allow 3x range: 0.33x to 3x expected dimensions
			if expectW > 0 && expectH > 0 {
				cw, ch := float64(w), float64(h)
				if isHorizontal {
					if cw > expectW*3 || cw < expectW*0.33 || ch > expectH*3 || ch < expectH*0.33 {
						rejectedSize++
						if len(rejectedSizeSamples) < 10 {
							rejectedSizeSamples = append(rejectedSizeSamples,
								fmt.Sprintf("%dx%d@(%d,%d)", w, h,
									searchX1+rect.Min.X, searchY1+rect.Min.Y))
						}
						continue
					}
				} else {
					if ch > expectW*3 || ch < expectW*0.33 || cw > expectH*3 || cw < expectH*0.33 {
						rejectedSize++
						if len(rejectedSizeSamples) < 10 {
							rejectedSizeSamples = append(rejectedSizeSamples,
								fmt.Sprintf("%dx%d@(%d,%d)", w, h,
									searchX1+rect.Min.X, searchY1+rect.Min.Y))
						}
						continue
					}
				}
			}

			candidates = append(candidates, Contact{
				Bounds: geometry.RectInt{
					X:      searchX1 + rect.Min.X,
					Y:      searchY1 + rect.Min.Y,
					Width:  w,
					Height: h,
				},
			})
		} else if w > 0 && h > 0 {
			rejectedTooSmall++
		}
	}
	fmt.Printf("  Contours: %d total, %d passed, %d rejected(aspect), %d rejected(size), %d rejected(area<%d)\n",
		totalContours, len(candidates), rejectedAspect, rejectedSize, rejectedTooSmall, params.MinArea/10)
	if len(rejectedAspectSamples) > 0 {
		fmt.Printf("  Rejected by aspect (first %d): %v\n", len(rejectedAspectSamples), rejectedAspectSamples)
	}
	if len(rejectedSizeSamples) > 0 {
		fmt.Printf("  Rejected by size (first %d): %v\n", len(rejectedSizeSamples), rejectedSizeSamples)
	}

	// Also log gold mask coverage in search region
	goldPixels := gocv.CountNonZero(region)
	totalPixels := region.Rows() * region.Cols()
	fmt.Printf("  Gold mask: %d/%d pixels (%.1f%%)\n", goldPixels, totalPixels,
		100*float64(goldPixels)/float64(totalPixels))

	if len(candidates) > 0 {
		// Log size distribution of accepted candidates
		var minW, maxW, minH, maxH int
		minW, minH = candidates[0].Bounds.Width, candidates[0].Bounds.Height
		maxW, maxH = minW, minH
		for _, c := range candidates {
			if c.Bounds.Width < minW {
				minW = c.Bounds.Width
			}
			if c.Bounds.Width > maxW {
				maxW = c.Bounds.Width
			}
			if c.Bounds.Height < minH {
				minH = c.Bounds.Height
			}
			if c.Bounds.Height > maxH {
				maxH = c.Bounds.Height
			}
		}
		fmt.Printf("  Accepted sizes: W=%d-%d H=%d-%d\n", minW, maxW, minH, maxH)
	}

	// Filter by line-position clustering: contacts should be at roughly the same
	// Y (for horizontal edge) or X (for vertical edge). Reject scattered outliers.
	if len(candidates) >= 5 {
		// Get line positions (Y for horizontal, X for vertical)
		linePositions := make([]float64, len(candidates))
		for i, c := range candidates {
			if isHorizontal {
				linePositions[i] = float64(c.Bounds.Y) + float64(c.Bounds.Height)/2
			} else {
				linePositions[i] = float64(c.Bounds.X) + float64(c.Bounds.Width)/2
			}
		}

		// Find the densest cluster using a sliding window
		// Window size = expected contact height (contacts span this much vertically)
		windowSize := float64(180) // default
		if expectH > 0 {
			windowSize = expectH
		}

		sorted := make([]float64, len(linePositions))
		copy(sorted, linePositions)
		sort.Float64s(sorted)

		bestCount := 0
		bestCenter := 0.0
		for i := range sorted {
			// Count how many positions fall within window centered on sorted[i]
			lo := sorted[i] - windowSize/2
			hi := sorted[i] + windowSize/2
			count := 0
			for _, p := range sorted {
				if p >= lo && p <= hi {
					count++
				}
			}
			if count > bestCount {
				bestCount = count
				bestCenter = (lo + hi) / 2
			}
		}

		// Keep only candidates within the densest cluster (±windowSize from center)
		var clustered []Contact
		for _, c := range candidates {
			var lp float64
			if isHorizontal {
				lp = float64(c.Bounds.Y) + float64(c.Bounds.Height)/2
			} else {
				lp = float64(c.Bounds.X) + float64(c.Bounds.Width)/2
			}
			if lp >= bestCenter-windowSize && lp <= bestCenter+windowSize {
				clustered = append(clustered, c)
			}
		}

		if len(clustered) < len(candidates) {
			fmt.Printf("  Line clustering: %d -> %d candidates (center=%.0f ±%.0f)\n",
				len(candidates), len(clustered), bestCenter, windowSize)
			candidates = clustered
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
