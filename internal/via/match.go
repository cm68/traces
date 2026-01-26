package via

import (
	"math"

	"pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"
)

// MatchResult holds the results of cross-side via matching.
type MatchResult struct {
	Matched    int     // Number of vias matched on both sides
	Unmatched  int     // Number of vias only detected on one side
	AvgError   float64 // Average center distance for matched pairs (pixels)
	MaxError   float64 // Maximum center distance for matched pairs (pixels)
}

// MatchViasAcrossSides matches vias detected on front and back images.
// After alignment, vias should appear at the same coordinates on both sides.
// Matched vias are updated in-place with MatchedViaID and BothSidesConfirmed set.
//
// Parameters:
//   - frontVias: vias detected on front side
//   - backVias: vias detected on back side
//   - tolerancePixels: maximum distance between centers to consider a match
//
// Returns match statistics.
func MatchViasAcrossSides(frontVias, backVias []Via, tolerancePixels float64) MatchResult {
	result := MatchResult{}

	// Track which back vias have been matched
	backMatched := make([]bool, len(backVias))

	for i := range frontVias {
		front := &frontVias[i]
		bestMatch := -1
		bestDist := tolerancePixels + 1

		// Find closest unmatched back via within tolerance
		for j := range backVias {
			if backMatched[j] {
				continue
			}
			back := &backVias[j]
			dist := front.Center.Distance(back.Center)
			if dist <= tolerancePixels && dist < bestDist {
				bestMatch = j
				bestDist = dist
			}
		}

		if bestMatch >= 0 {
			// Found a match
			back := &backVias[bestMatch]
			front.MatchedViaID = back.ID
			front.BothSidesConfirmed = true
			back.MatchedViaID = front.ID
			back.BothSidesConfirmed = true
			backMatched[bestMatch] = true

			result.Matched++
			result.AvgError += bestDist
			if bestDist > result.MaxError {
				result.MaxError = bestDist
			}
		} else {
			result.Unmatched++
		}
	}

	// Count unmatched back vias
	for _, matched := range backMatched {
		if !matched {
			result.Unmatched++
		}
	}

	if result.Matched > 0 {
		result.AvgError /= float64(result.Matched)
	}

	return result
}

// MatchViasInResults matches vias between front and back detection results.
// Convenience wrapper that handles Side filtering.
func MatchViasInResults(frontResult, backResult *ViaDetectionResult, tolerancePixels float64) MatchResult {
	if frontResult == nil || backResult == nil {
		return MatchResult{}
	}
	return MatchViasAcrossSides(frontResult.Vias, backResult.Vias, tolerancePixels)
}

// BoostMatchedConfidence increases confidence for vias detected on both sides.
// The boost factor is applied multiplicatively: new_conf = conf + (1-conf) * boost
// This means a via with 0.5 confidence and 0.5 boost becomes 0.75.
func BoostMatchedConfidence(vias []Via, boostFactor float64) {
	for i := range vias {
		if vias[i].BothSidesConfirmed {
			// Asymptotic boost toward 1.0
			vias[i].Confidence = vias[i].Confidence + (1.0-vias[i].Confidence)*boostFactor
		}
	}
}

// FilterUnmatchedVias removes vias that weren't detected on both sides.
// This is an aggressive filter - only use when high precision is needed.
func FilterUnmatchedVias(vias []Via) []Via {
	var filtered []Via
	for _, v := range vias {
		if v.BothSidesConfirmed {
			filtered = append(filtered, v)
		}
	}
	return filtered
}

// SuggestMatchTolerance calculates a reasonable matching tolerance based on DPI.
// Vias should match within about 0.005" (5 mil) for well-aligned boards.
func SuggestMatchTolerance(dpi float64) float64 {
	if dpi <= 0 {
		return 10.0 // Default fallback
	}
	// 5 mil tolerance for well-aligned boards
	return 0.005 * dpi
}

// FindUnmatchedVias returns vias that exist on only one side.
// These may be false positives, or may be surface-mount pads (not through-hole).
func FindUnmatchedVias(vias []Via) []Via {
	var unmatched []Via
	for _, v := range vias {
		if !v.BothSidesConfirmed {
			unmatched = append(unmatched, v)
		}
	}
	return unmatched
}

// ComputeAverageMatchedCenter returns the centroid of all matched vias.
// Useful for verifying alignment - should be near image center for symmetric boards.
func ComputeAverageMatchedCenter(frontVias, backVias []Via) (geometry.Point2D, int) {
	var sumX, sumY float64
	count := 0

	for _, v := range frontVias {
		if v.BothSidesConfirmed {
			sumX += v.Center.X
			sumY += v.Center.Y
			count++
		}
	}
	for _, v := range backVias {
		if v.BothSidesConfirmed {
			sumX += v.Center.X
			sumY += v.Center.Y
			count++
		}
	}

	if count == 0 {
		return geometry.Point2D{}, 0
	}
	return geometry.Point2D{X: sumX / float64(count), Y: sumY / float64(count)}, count
}

// ValidateAlignmentWithVias uses matched via positions to estimate alignment quality.
// Returns the RMS error between matched via centers on front and back.
func ValidateAlignmentWithVias(frontVias, backVias []Via) float64 {
	var sumSqErr float64
	count := 0

	// Build map of back vias by ID for quick lookup
	backByID := make(map[string]*Via)
	for i := range backVias {
		backByID[backVias[i].ID] = &backVias[i]
	}

	for _, front := range frontVias {
		if front.MatchedViaID == "" {
			continue
		}
		back, ok := backByID[front.MatchedViaID]
		if !ok {
			continue
		}
		dx := front.Center.X - back.Center.X
		dy := front.Center.Y - back.Center.Y
		sumSqErr += dx*dx + dy*dy
		count++
	}

	if count == 0 {
		return 0
	}
	return math.Sqrt(sumSqErr / float64(count))
}

// SeparateBySide splits a via slice into front and back vias.
func SeparateBySide(vias []Via) (front, back []Via) {
	for _, v := range vias {
		if v.Side == image.SideFront {
			front = append(front, v)
		} else {
			back = append(back, v)
		}
	}
	return
}
