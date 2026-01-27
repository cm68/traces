package via

import (
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"

	"pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"
)

// candidateMatch represents a potential via match with its distance.
type candidateMatch struct {
	frontIdx int
	backIdx  int
	distance float64
}

// MatchResult holds the results of cross-side via matching.
type MatchResult struct {
	Matched       int             // Number of vias matched on both sides
	Unmatched     int             // Number of vias only detected on one side
	AvgError      float64         // Average center distance for matched pairs (pixels)
	MaxError      float64         // Maximum center distance for matched pairs (pixels)
	ConfirmedVias []*ConfirmedVia // Created confirmed vias for matched pairs
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
	result := MatchResult{
		ConfirmedVias: make([]*ConfirmedVia, 0),
	}

	if len(frontVias) == 0 || len(backVias) == 0 {
		result.Unmatched = len(frontVias) + len(backVias)
		return result
	}

	// Phase 1: Parallel distance computation
	// Find all candidate matches within tolerance
	candidates := findCandidateMatchesParallel(frontVias, backVias, tolerancePixels)

	// Phase 2: Sort candidates by distance (greedy best-first matching)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].distance < candidates[j].distance
	})

	// Phase 3: Greedy matching - assign closest pairs first
	frontMatched := make([]bool, len(frontVias))
	backMatched := make([]bool, len(backVias))
	confirmedNum := 1

	for _, cand := range candidates {
		if frontMatched[cand.frontIdx] || backMatched[cand.backIdx] {
			continue // Already matched
		}

		// Assign this match
		front := &frontVias[cand.frontIdx]
		back := &backVias[cand.backIdx]

		front.MatchedViaID = back.ID
		front.BothSidesConfirmed = true
		back.MatchedViaID = front.ID
		back.BothSidesConfirmed = true

		frontMatched[cand.frontIdx] = true
		backMatched[cand.backIdx] = true

		// Create confirmed via
		cvID := fmt.Sprintf("cvia-%03d", confirmedNum)
		cv := NewConfirmedVia(cvID, front, back)
		result.ConfirmedVias = append(result.ConfirmedVias, cv)
		confirmedNum++

		result.Matched++
		result.AvgError += cand.distance
		if cand.distance > result.MaxError {
			result.MaxError = cand.distance
		}
	}

	// Count unmatched vias
	for _, matched := range frontMatched {
		if !matched {
			result.Unmatched++
		}
	}
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

// findCandidateMatchesParallel finds all via pairs within tolerance using parallel workers.
func findCandidateMatchesParallel(frontVias, backVias []Via, tolerancePixels float64) []candidateMatch {
	numWorkers := runtime.NumCPU()
	if numWorkers > len(frontVias) {
		numWorkers = len(frontVias)
	}
	if numWorkers < 1 {
		numWorkers = 1
	}

	// Channel for collecting results
	resultChan := make(chan []candidateMatch, numWorkers)

	// Divide front vias among workers
	chunkSize := (len(frontVias) + numWorkers - 1) / numWorkers

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		start := w * chunkSize
		end := start + chunkSize
		if end > len(frontVias) {
			end = len(frontVias)
		}
		if start >= end {
			continue
		}

		wg.Add(1)
		go func(startIdx, endIdx int) {
			defer wg.Done()
			var localMatches []candidateMatch

			for i := startIdx; i < endIdx; i++ {
				front := &frontVias[i]
				for j := range backVias {
					back := &backVias[j]
					dist := front.Center.Distance(back.Center)
					if dist <= tolerancePixels {
						localMatches = append(localMatches, candidateMatch{
							frontIdx: i,
							backIdx:  j,
							distance: dist,
						})
					}
				}
			}

			resultChan <- localMatches
		}(start, end)
	}

	// Close channel when all workers done
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect all results
	var allCandidates []candidateMatch
	for matches := range resultChan {
		allCandidates = append(allCandidates, matches...)
	}

	return allCandidates
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
// Vias should match within about 0.015" (15 mil) to account for alignment errors
// and differences in via pad shapes between front and back.
func SuggestMatchTolerance(dpi float64) float64 {
	if dpi <= 0 {
		return 15.0 // Default fallback
	}
	// 15 mil tolerance - allows for small alignment errors and pad shape differences
	return 0.015 * dpi
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
