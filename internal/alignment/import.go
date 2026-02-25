package alignment

import (
	"fmt"
	"image"

	"pcb-tracer/internal/board"
	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// ProcessedImage holds the result of processing a scan image through the
// import pipeline: board detection → crop → rotation.
// Note: back-side horizontal flip is handled at image ingest time, not here.
type ProcessedImage struct {
	Image           image.Image      // Cropped and rotated image
	BoardBounds     geometry.RectInt  // Board bounds in the final image
	RotationApplied float64          // Total degrees rotated
	GrossRotation   int              // 0/90/180/270 to put connector at top
	SkewAngle       float64          // Fine skew correction angle (degrees)
	DPI             float64          // Image DPI
	ContactEdge     string           // Detected contact edge ("top"/"bottom"/"left"/"right" or "")
}

// ProcessRawImage runs the import pipeline on a scan image:
//  1. Detect board bounds and rotation angle
//  2. Determine connector edge orientation → gross rotation (0/90/180/270)
//  3. Crop to detected board bounds (removes scanner background)
//  4. Rotate the cropped image (gross + skew correction)
//
// Back-side horizontal flip is handled at image ingest time (state.go), not here.
// Cropping happens before rotation so that rotation-added black borders
// don't confuse re-detection of board bounds.
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

	// Step 3: Crop to board bounds (before rotation to avoid black border issues)
	current := mat.Clone()
	defer current.Close()

	origBounds := boardResult.Bounds
	imgW := current.Cols()
	imgH := current.Rows()
	if origBounds.Width < imgW*95/100 || origBounds.Height < imgH*95/100 {
		fmt.Printf("ProcessRawImage: cropping %dx%d → %dx%d\n",
			imgW, imgH, origBounds.Width, origBounds.Height)
		roi := current.Region(image.Rect(origBounds.X, origBounds.Y,
			origBounds.X+origBounds.Width, origBounds.Y+origBounds.Height))
		cropped := roi.Clone()
		roi.Close()
		current.Close()
		current = cropped
	}

	// Step 4: Rotate the cropped image (gross + skew)
	totalAngle := float64(grossRotation) + skewAngle
	if totalAngle != 0 {
		rotated := rotateMatByAngle(current, totalAngle)
		current.Close()
		current = rotated
	}

	finalBounds := geometry.RectInt{X: 0, Y: 0, Width: current.Cols(), Height: current.Rows()}

	// Convert back to Go image
	result, err := matToImage(current)
	if err != nil {
		return nil, fmt.Errorf("failed to convert result: %w", err)
	}

	fmt.Printf("ProcessRawImage: side=%s, grossRot=%d, skew=%.2f°, totalAngle=%.2f°, size=%dx%d\n",
		side, grossRotation, skewAngle, totalAngle, current.Cols(), current.Rows())

	return &ProcessedImage{
		Image:           result,
		BoardBounds:     finalBounds,
		RotationApplied: totalAngle,
		GrossRotation:   grossRotation,
		SkewAngle:       skewAngle,
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
