package alignment

import (
	"fmt"
	"image"

	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/internal/via"
	"pcb-tracer/pkg/geometry"
)

// ViaAlignmentResult holds the result of via-based alignment between front and back images.
type ViaAlignmentResult struct {
	Transform      geometry.AffineTransform
	MatchedVias    int
	TotalFrontVias int
	TotalBackVias  int
	AvgError       float64
	FrontVias      []via.Via
	BackVias       []via.Via
}

// AlignWithVias performs via-based alignment between front and back images.
// It detects vias on both sides, matches them, and computes an affine transform
// that maps back image coordinates to front image coordinates.
//
// Returns an error if not enough vias are matched (need >= 3 for affine, prefer >= 6).
func AlignWithVias(frontImg, backImg image.Image, dpi float64) (*ViaAlignmentResult, error) {
	if frontImg == nil || backImg == nil {
		return nil, fmt.Errorf("nil image")
	}

	params := via.DefaultParams().WithDPI(dpi)

	// Detect vias on front
	fmt.Printf("AlignWithVias: detecting front vias (DPI=%.0f, minR=%d, maxR=%d)\n",
		dpi, params.MinRadiusPixels, params.MaxRadiusPixels)
	frontResult, err := via.DetectViasFromImage(frontImg, pcbimage.SideFront, params)
	if err != nil {
		return nil, fmt.Errorf("front via detection failed: %w", err)
	}
	fmt.Printf("AlignWithVias: found %d front vias\n", len(frontResult.Vias))

	// Detect vias on back
	fmt.Printf("AlignWithVias: detecting back vias\n")
	backResult, err := via.DetectViasFromImage(backImg, pcbimage.SideBack, params)
	if err != nil {
		return nil, fmt.Errorf("back via detection failed: %w", err)
	}
	fmt.Printf("AlignWithVias: found %d back vias\n", len(backResult.Vias))

	// Match vias across sides
	tolerance := via.SuggestMatchTolerance(dpi)
	matchResult := via.MatchViasAcrossSides(frontResult.Vias, backResult.Vias, tolerance)
	fmt.Printf("AlignWithVias: matched %d vias (tolerance=%.1f px, unmatched=%d)\n",
		matchResult.Matched, tolerance, matchResult.Unmatched)

	if matchResult.Matched < 3 {
		return &ViaAlignmentResult{
			MatchedVias:    matchResult.Matched,
			TotalFrontVias: len(frontResult.Vias),
			TotalBackVias:  len(backResult.Vias),
			FrontVias:      frontResult.Vias,
			BackVias:       backResult.Vias,
		}, fmt.Errorf("not enough matched vias for affine: %d (need >= 3)", matchResult.Matched)
	}

	// Extract point pairs from confirmed via matches
	var frontPts, backPts []geometry.Point2D
	frontByID := make(map[string]*via.Via)
	backByID := make(map[string]*via.Via)
	for i := range frontResult.Vias {
		frontByID[frontResult.Vias[i].ID] = &frontResult.Vias[i]
	}
	for i := range backResult.Vias {
		backByID[backResult.Vias[i].ID] = &backResult.Vias[i]
	}

	for _, cv := range matchResult.ConfirmedVias {
		fv, fok := frontByID[cv.FrontViaID]
		bv, bok := backByID[cv.BackViaID]
		if fok && bok {
			frontPts = append(frontPts, fv.Center)
			backPts = append(backPts, bv.Center)
		}
	}

	if len(frontPts) < 3 {
		return nil, fmt.Errorf("failed to extract enough point pairs: %d", len(frontPts))
	}

	// Compute affine transform: back → front
	transform, inliers, err := ComputeAffineRANSAC(backPts, frontPts, 2000, 3.0)
	if err != nil {
		return nil, fmt.Errorf("affine computation failed: %w", err)
	}

	avgError := CalculateAlignmentError(backPts, frontPts, transform)
	fmt.Printf("AlignWithVias: affine computed, %d inliers, avgError=%.2f px\n",
		len(inliers), avgError)

	return &ViaAlignmentResult{
		Transform:      transform,
		MatchedVias:    matchResult.Matched,
		TotalFrontVias: len(frontResult.Vias),
		TotalBackVias:  len(backResult.Vias),
		AvgError:       avgError,
		FrontVias:      frontResult.Vias,
		BackVias:       backResult.Vias,
	}, nil
}

// WarpAffineGoImage applies an affine transform to a Go image.Image.
// The output image will have the specified width and height.
func WarpAffineGoImage(img image.Image, transform geometry.AffineTransform, width, height int) (image.Image, error) {
	mat, err := imageToMat(img)
	if err != nil {
		return nil, fmt.Errorf("failed to convert image: %w", err)
	}
	defer mat.Close()

	warped := WarpAffine(mat, transform, width, height)
	defer warped.Close()

	return matToImage(warped)
}
