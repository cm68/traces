package alignment

import (
	"fmt"
	"image"

	"pcb-tracer/internal/board"
	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// ProcessedImage holds the result of processing a raw scan image through the
// import pipeline: board detection → rotation → flip → crop.
type ProcessedImage struct {
	Image           image.Image      // Cropped, rotated, and (if back) flipped image
	BoardBounds     geometry.RectInt  // Board bounds in the final image
	RotationApplied float64          // Total degrees rotated
	GrossRotation   int              // 0/90/180/270 to put connector at top
	SkewAngle       float64          // Fine skew correction angle (degrees)
	Flipped         bool             // Whether horizontally flipped (back side)
	DPI             float64          // Image DPI
	ContactEdge     string           // Detected contact edge ("top"/"bottom"/"left"/"right" or "")
}

// ProcessRawImage runs the unified import pipeline on a single raw scan image:
//  1. Detect board bounds and rotation angle
//  2. Determine connector edge orientation → gross rotation (0/90/180/270)
//  3. Combine skew correction with gross rotation
//  4. Rotate the image
//  5. If back side, flip horizontally
//  6. Crop to detected board bounds
func ProcessRawImage(img image.Image, spec board.Spec, side pcbimage.Side, dpi float64) (*ProcessedImage, error) {
	if img == nil {
		return nil, fmt.Errorf("nil image")
	}

	// Convert to Mat once — all operations happen in Mat space
	mat, err := imageToMat(img)
	if err != nil {
		return nil, fmt.Errorf("failed to convert image: %w", err)
	}
	defer mat.Close()

	// Step 1: Detect board bounds and skew angle
	boardResult := DetectBoardRotation(mat)
	skewAngle := 0.0
	if boardResult.Detected {
		skewAngle = boardResult.Angle
	}

	// Step 2: Find which edge has the connector → gross rotation
	grossRotation := 0
	contactEdge := ""
	if spec != nil {
		var params DetectionParams
		if dpi > 0 {
			params = ParamsFromSpecWithDPI(spec, dpi)
		} else {
			params = ParamsFromSpec(spec)
		}
		edge, contacts, _, _, _, _ := findContactEdge(mat, spec, params, false)
		if len(contacts) >= 2 {
			contactEdge = edge
			switch edge {
			case "bottom":
				grossRotation = 180
			case "left":
				grossRotation = 90
			case "right":
				grossRotation = 270
			}
		} else {
			fmt.Printf("ProcessRawImage: no connector edge detected (%d contacts), skipping gross rotation\n", len(contacts))
		}
	}

	// Step 3: Combined rotation (gross + skew)
	totalAngle := float64(grossRotation) + skewAngle

	// Step 4: Rotate
	current := mat.Clone()
	defer current.Close()

	if totalAngle != 0 {
		rotated := rotateMatByAngle(current, totalAngle)
		current.Close()
		current = rotated
	}

	// Step 5: Flip for back side
	flipped := false
	if side == pcbimage.SideBack {
		flippedMat := FlipHorizontal(current)
		current.Close()
		current = flippedMat
		flipped = true
	}

	// Step 6: Re-detect board bounds on the rotated/flipped image and crop
	newBounds := detectBoardBounds(current)

	// Only crop if the bounds are smaller than the full image (i.e., there's meaningful border to remove)
	imgW := current.Cols()
	imgH := current.Rows()
	if newBounds.Width < imgW*95/100 || newBounds.Height < imgH*95/100 {
		roi := current.Region(image.Rect(newBounds.X, newBounds.Y,
			newBounds.X+newBounds.Width, newBounds.Y+newBounds.Height))
		cropped := roi.Clone()
		roi.Close()
		current.Close()
		current = cropped
		// Update bounds to be relative to the cropped image
		newBounds = geometry.RectInt{X: 0, Y: 0, Width: current.Cols(), Height: current.Rows()}
	}

	// Convert back to Go image
	result, err := matToImage(current)
	if err != nil {
		return nil, fmt.Errorf("failed to convert result: %w", err)
	}

	fmt.Printf("ProcessRawImage: side=%s, grossRot=%d, skew=%.2f°, totalAngle=%.2f°, flipped=%v, size=%dx%d\n",
		side, grossRotation, skewAngle, totalAngle, flipped, current.Cols(), current.Rows())

	return &ProcessedImage{
		Image:           result,
		BoardBounds:     newBounds,
		RotationApplied: totalAngle,
		GrossRotation:   grossRotation,
		SkewAngle:       skewAngle,
		Flipped:         flipped,
		DPI:             dpi,
		ContactEdge:     contactEdge,
	}, nil
}

// FlipHorizontalGoImage flips a Go image.Image horizontally.
func FlipHorizontalGoImage(img image.Image) image.Image {
	mat, err := imageToMat(img)
	if err != nil {
		return img
	}
	defer mat.Close()

	flipped := gocv.NewMat()
	defer flipped.Close()
	gocv.Flip(mat, &flipped, 1)

	result, err := matToImage(flipped)
	if err != nil {
		return img
	}
	return result
}
