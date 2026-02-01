package alignment

import (
	"fmt"
	"image"
	"image/color"

	"pcb-tracer/internal/board"
	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// Options configures the alignment process.
type Options struct {
	FlipBack bool          // Flip back image horizontally before alignment
	Debug    bool          // Enable debug output
	Spec     board.Spec    // Board specification
	DPI      float64       // Image DPI (if known)
}

// DefaultOptions returns default alignment options.
func DefaultOptions() Options {
	return Options{
		FlipBack: true,
		Debug:    false,
		Spec:     board.S100Spec(),
	}
}

// AlignImages aligns front and back PCB images using detected contacts.
// Returns the rotated front image, aligned back image, and alignment result.
func AlignImages(front, back gocv.Mat, opts Options) (gocv.Mat, gocv.Mat, *AlignmentResult, error) {
	if front.Empty() || back.Empty() {
		return gocv.Mat{}, gocv.Mat{}, nil, fmt.Errorf("empty input image")
	}

	// Detect contacts on front image
	frontResult, err := DetectGoldContacts(front, opts.Spec, opts.DPI, opts.Debug)
	if err != nil {
		return gocv.Mat{}, gocv.Mat{}, nil, fmt.Errorf("front contact detection: %w", err)
	}

	if opts.Debug {
		fmt.Printf("Front: %d contacts, edge=%s, rotation=%d\n",
			len(frontResult.Contacts), frontResult.Edge, frontResult.Rotation)
	}

	// Rotate front if needed
	frontRotated := front.Clone()
	if frontResult.Rotation != 0 {
		rotated := RotateImage(front, frontResult.Rotation)
		frontRotated.Close()
		frontRotated = rotated

		// Re-detect contacts after rotation
		frontResult, err = DetectGoldContacts(frontRotated, opts.Spec, opts.DPI, opts.Debug)
		if err != nil {
			frontRotated.Close()
			return gocv.Mat{}, gocv.Mat{}, nil, fmt.Errorf("front contact detection after rotation: %w", err)
		}
	}

	// Flip back if requested
	backProcessed := back.Clone()
	if opts.FlipBack {
		flipped := FlipHorizontal(back)
		backProcessed.Close()
		backProcessed = flipped
	}

	// Detect contacts on back image
	backResult, err := DetectGoldContacts(backProcessed, opts.Spec, opts.DPI, opts.Debug)
	if err != nil {
		frontRotated.Close()
		backProcessed.Close()
		return gocv.Mat{}, gocv.Mat{}, nil, fmt.Errorf("back contact detection: %w", err)
	}

	if opts.Debug {
		fmt.Printf("Back: %d contacts, edge=%s, rotation=%d\n",
			len(backResult.Contacts), backResult.Edge, backResult.Rotation)
	}

	// Rotate back if needed
	if backResult.Rotation != 0 {
		rotated := RotateImage(backProcessed, backResult.Rotation)
		backProcessed.Close()
		backProcessed = rotated

		// Re-detect contacts after rotation
		backResult, err = DetectGoldContacts(backProcessed, opts.Spec, opts.DPI, opts.Debug)
		if err != nil {
			frontRotated.Close()
			backProcessed.Close()
			return gocv.Mat{}, gocv.Mat{}, nil, fmt.Errorf("back contact detection after rotation: %w", err)
		}
	}

	// Check we have enough contacts
	if len(frontResult.Contacts) < 30 || len(backResult.Contacts) < 30 {
		frontRotated.Close()
		backProcessed.Close()
		return gocv.Mat{}, gocv.Mat{}, nil, fmt.Errorf("not enough contacts: front=%d, back=%d",
			len(frontResult.Contacts), len(backResult.Contacts))
	}

	// Calculate average DPI
	avgDPI := (frontResult.DPI + backResult.DPI) / 2

	// Build point correspondences from contacts
	nContacts := min(len(frontResult.Contacts), len(backResult.Contacts))

	frontPts := make([]geometry.Point2D, 0, nContacts+4)
	backPts := make([]geometry.Point2D, 0, nContacts+4)
	for i := 0; i < nContacts; i++ {
		frontPts = append(frontPts, frontResult.Contacts[i].Center)
		backPts = append(backPts, backResult.Contacts[i].Center)
	}

	// Detect step edges for Y-axis registration (high-contrast board edge)
	if avgDPI > 0 {
		frontStepEdges := DetectStepEdges(frontRotated, frontResult.Contacts, avgDPI)
		backStepEdges := DetectStepEdges(backProcessed, backResult.Contacts, avgDPI)

		// Match step edges by side
		for _, frontEdge := range frontStepEdges {
			for _, backEdge := range backStepEdges {
				if frontEdge.Side == backEdge.Side {
					frontPts = append(frontPts, frontEdge.Corner)
					backPts = append(backPts, backEdge.Corner)
					if opts.Debug {
						fmt.Printf("Step edge %s: front=(%.1f,%.1f) back=(%.1f,%.1f)\n",
							frontEdge.Side,
							frontEdge.Corner.X, frontEdge.Corner.Y,
							backEdge.Corner.X, backEdge.Corner.Y)
					}
					break
				}
			}
		}
	}

	// Compute affine transform (back -> front) with tighter threshold for sub-pixel accuracy
	transform, inliers, err := ComputeAffineRANSAC(backPts, frontPts, 3000, 1.5)
	if err != nil {
		frontRotated.Close()
		backProcessed.Close()
		return gocv.Mat{}, gocv.Mat{}, nil, fmt.Errorf("compute transform: %w", err)
	}

	if opts.Debug {
		fmt.Printf("Transform computed with %d inliers\n", len(inliers))
	}

	// Warp back image to align with front
	backAligned := WarpAffine(backProcessed, transform, frontRotated.Cols(), frontRotated.Rows())
	backProcessed.Close()

	// Calculate alignment error
	contactError := CalculateAlignmentError(backPts, frontPts, transform)

	result := &AlignmentResult{
		Transform:     transform,
		DPI:           avgDPI,
		FrontContacts: frontPts,
		BackContacts:  backPts,
		ContactError:  contactError,
		TotalError:    contactError,
	}

	return frontRotated, backAligned, result, nil
}

// CreateOverlay creates a blended overlay of front and aligned back images.
func CreateOverlay(front, backAligned gocv.Mat, opacity float64) gocv.Mat {
	if front.Empty() || backAligned.Empty() {
		return gocv.NewMat()
	}

	dst := gocv.NewMat()
	gocv.AddWeighted(front, opacity, backAligned, 1.0-opacity, 0, &dst)
	return dst
}

// VisualizeContacts draws detected contacts on an image for debugging.
func VisualizeContacts(img gocv.Mat, contacts []Contact, col color.RGBA) gocv.Mat {
	dst := img.Clone()

	for _, c := range contacts {
		rect := image.Rect(c.Bounds.X, c.Bounds.Y,
			c.Bounds.X+c.Bounds.Width, c.Bounds.Y+c.Bounds.Height)
		gocv.Rectangle(&dst, rect, col, 2)
	}

	return dst
}
