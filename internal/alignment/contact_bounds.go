package alignment

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// BoardRotationResult contains board detection results including rotation.
type BoardRotationResult struct {
	Bounds   geometry.RectInt
	Angle    float64 // Rotation angle in degrees (to make board orthogonal)
	Detected bool
}

// detectBoardBounds detects the PCB board boundaries using background subtraction.
// This approach samples the background color from edge midpoints (avoiding corners
// which may have scanner artifacts) and finds pixels that differ significantly.
func detectBoardBounds(img gocv.Mat) geometry.RectInt {
	imgH := img.Rows()
	imgW := img.Cols()

	// Downsample for faster processing
	scale := math.Min(1.0, 1500.0/float64(max(imgW, imgH)))
	var small gocv.Mat
	if scale < 1.0 {
		small = gocv.NewMat()
		gocv.Resize(img, &small, image.Point{}, scale, scale, gocv.InterpolationArea)
	} else {
		small = img.Clone()
	}
	defer small.Close()

	smallH := small.Rows()
	smallW := small.Cols()

	// Sample background color from edge midpoints (not corners - they may have artifacts)
	sampleSize := 30
	bgColor := sampleBackgroundColor(small, sampleSize)

	// If background is very dark (black borders from rotation), assume whole image is board
	// This handles images that have already been cropped to board bounds
	if bgColor.R < 30 && bgColor.G < 30 && bgColor.B < 30 {
		fmt.Printf("detectBoardBounds: dark background detected (R=%d,G=%d,B=%d), using full image as board\n",
			bgColor.R, bgColor.G, bgColor.B)
		return geometry.RectInt{X: 0, Y: 0, Width: imgW, Height: imgH}
	}

	// Create mask of pixels that differ from background
	// Use per-channel difference threshold (25 is ~10% of 255)
	mask := createBackgroundDiffMask(small, bgColor, 25) // threshold per channel
	defer mask.Close()

	// Morphological operations to clean up
	kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Point{5, 5})
	defer kernel.Close()

	// Close to fill small gaps
	gocv.MorphologyEx(mask, &mask, gocv.MorphClose, kernel)
	// Open to remove small noise
	gocv.MorphologyEx(mask, &mask, gocv.MorphOpen, kernel)

	// Find contours
	contours := gocv.FindContours(mask, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	if contours.Size() == 0 {
		return geometry.RectInt{X: 0, Y: 0, Width: imgW, Height: imgH}
	}

	// Find largest contour that isn't the full image
	var bestContour gocv.PointVector
	bestArea := 0.0
	fullArea := float64(smallW * smallH)

	for i := 0; i < contours.Size(); i++ {
		contour := contours.At(i)
		area := gocv.ContourArea(contour)
		if area < fullArea*0.9 && area > bestArea {
			bestArea = area
			bestContour = contour
		}
	}

	if bestArea == 0 {
		return geometry.RectInt{X: 0, Y: 0, Width: imgW, Height: imgH}
	}

	// Get minimum area rotated rectangle
	rotRect := gocv.MinAreaRect(bestContour)

	// Calculate the 4 corners of the rotated rectangle
	cx := float64(rotRect.Center.X)
	cy := float64(rotRect.Center.Y)
	w := float64(rotRect.Width)
	h := float64(rotRect.Height)
	angle := float64(rotRect.Angle) * math.Pi / 180.0

	// Half dimensions
	hw := w / 2
	hh := h / 2

	// Calculate corners (rotated around center)
	cos := math.Cos(angle)
	sin := math.Sin(angle)

	corners := [][2]float64{
		{cx + (-hw)*cos - (-hh)*sin, cy + (-hw)*sin + (-hh)*cos},
		{cx + (hw)*cos - (-hh)*sin, cy + (hw)*sin + (-hh)*cos},
		{cx + (hw)*cos - (hh)*sin, cy + (hw)*sin + (hh)*cos},
		{cx + (-hw)*cos - (hh)*sin, cy + (-hw)*sin + (hh)*cos},
	}

	// Find axis-aligned bounding box that contains all corners
	minX, minY := float64(smallW), float64(smallH)
	maxX, maxY := 0.0, 0.0
	for _, pt := range corners {
		if pt[0] < minX {
			minX = pt[0]
		}
		if pt[0] > maxX {
			maxX = pt[0]
		}
		if pt[1] < minY {
			minY = pt[1]
		}
		if pt[1] > maxY {
			maxY = pt[1]
		}
	}

	// Add small margin (0.5% of image dimension)
	marginX := float64(smallW) * 0.005
	marginY := float64(smallH) * 0.005
	minX = math.Max(0, minX-marginX)
	minY = math.Max(0, minY-marginY)
	maxX = math.Min(float64(smallW), maxX+marginX)
	maxY = math.Min(float64(smallH), maxY+marginY)

	// Scale back to original coordinates
	bounds := geometry.RectInt{
		X:      int(minX / scale),
		Y:      int(minY / scale),
		Width:  int((maxX - minX) / scale),
		Height: int((maxY - minY) / scale),
	}

	// Sanity check: if detected bounds are less than 25% of image, use full image
	// This catches cases where board detection fails
	if bounds.Width < imgW/4 || bounds.Height < imgH/4 {
		fmt.Printf("detectBoardBounds: bounds too small (%dx%d vs %dx%d), using full image\n",
			bounds.Width, bounds.Height, imgW, imgH)
		return geometry.RectInt{X: 0, Y: 0, Width: imgW, Height: imgH}
	}

	return bounds
}

// sampleBackgroundColor samples the background color from edge midpoints.
// Avoids corners which may have scanner artifacts.
func sampleBackgroundColor(img gocv.Mat, sampleSize int) color.RGBA {
	h := img.Rows()
	w := img.Cols()
	midX := w / 2
	midY := h / 2

	// Sample from 4 edge midpoints
	samples := []image.Rectangle{
		image.Rect(midX-sampleSize/2, 5, midX+sampleSize/2, 5+sampleSize),     // top
		image.Rect(midX-sampleSize/2, h-5-sampleSize, midX+sampleSize/2, h-5), // bottom
		image.Rect(5, midY-sampleSize/2, 5+sampleSize, midY+sampleSize/2),     // left
		image.Rect(w-5-sampleSize, midY-sampleSize/2, w-5, midY+sampleSize/2), // right
	}

	var totalR, totalG, totalB int
	var count int

	for _, rect := range samples {
		// Clamp to image bounds
		if rect.Min.X < 0 {
			rect.Min.X = 0
		}
		if rect.Min.Y < 0 {
			rect.Min.Y = 0
		}
		if rect.Max.X > w {
			rect.Max.X = w
		}
		if rect.Max.Y > h {
			rect.Max.Y = h
		}

		// Sample pixels in the region and average
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			for x := rect.Min.X; x < rect.Max.X; x++ {
				totalB += int(img.GetUCharAt(y, x*3+0))
				totalG += int(img.GetUCharAt(y, x*3+1))
				totalR += int(img.GetUCharAt(y, x*3+2))
				count++
			}
		}
	}

	if count == 0 {
		return color.RGBA{R: 50, G: 50, B: 50, A: 255} // fallback dark gray
	}

	return color.RGBA{
		R: uint8(totalR / count),
		G: uint8(totalG / count),
		B: uint8(totalB / count),
		A: 255,
	}
}

// createBackgroundDiffMask creates a binary mask where pixels differ from background.
func createBackgroundDiffMask(img gocv.Mat, bgColor color.RGBA, threshold int) gocv.Mat {
	h := img.Rows()
	w := img.Cols()

	// Create output mask
	mask := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8UC1)

	// For each pixel, check if it differs significantly from background
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// BGR format
			b := int(img.GetUCharAt(y, x*3+0))
			g := int(img.GetUCharAt(y, x*3+1))
			r := int(img.GetUCharAt(y, x*3+2))

			// Check if any channel differs by more than threshold
			diffR := r - int(bgColor.R)
			if diffR < 0 {
				diffR = -diffR
			}
			diffG := g - int(bgColor.G)
			if diffG < 0 {
				diffG = -diffG
			}
			diffB := b - int(bgColor.B)
			if diffB < 0 {
				diffB = -diffB
			}

			if diffR > threshold || diffG > threshold || diffB > threshold {
				mask.SetUCharAt(y, x, 255)
			}
		}
	}

	return mask
}

// DetectBoardRotation detects the board boundaries and rotation angle.
// Returns the angle needed to rotate the image to make the board orthogonal.
// Uses background subtraction for robust detection across varying board colors.
func DetectBoardRotation(img gocv.Mat) BoardRotationResult {
	imgH := img.Rows()
	imgW := img.Cols()

	result := BoardRotationResult{
		Bounds:   geometry.RectInt{X: 0, Y: 0, Width: imgW, Height: imgH},
		Angle:    0,
		Detected: false,
	}

	// Downsample for faster processing
	scale := math.Min(1.0, 1500.0/float64(max(imgW, imgH)))
	var small gocv.Mat
	if scale < 1.0 {
		small = gocv.NewMat()
		gocv.Resize(img, &small, image.Point{}, scale, scale, gocv.InterpolationArea)
	} else {
		small = img.Clone()
	}
	defer small.Close()

	smallH := small.Rows()
	smallW := small.Cols()

	// Sample background color from edge midpoints (not corners - they may have artifacts)
	bgColor := sampleBackgroundColor(small, 30)

	// If background is very dark (black borders from rotation or already cropped),
	// the image is already the board - return detected=true with full bounds and no rotation needed
	if bgColor.R < 30 && bgColor.G < 30 && bgColor.B < 30 {
		result.Detected = true
		return result
	}

	// Create mask of pixels that differ from background
	mask := createBackgroundDiffMask(small, bgColor, 25)
	defer mask.Close()

	// Morphological operations to clean up
	kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Point{5, 5})
	defer kernel.Close()
	gocv.MorphologyEx(mask, &mask, gocv.MorphClose, kernel)
	gocv.MorphologyEx(mask, &mask, gocv.MorphOpen, kernel)

	// Find contours
	contours := gocv.FindContours(mask, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	if contours.Size() == 0 {
		return result
	}

	// Find largest contour that isn't the full image
	var bestContour gocv.PointVector
	bestArea := 0.0
	fullArea := float64(smallH * smallW)

	for i := 0; i < contours.Size(); i++ {
		contour := contours.At(i)
		area := gocv.ContourArea(contour)
		if area < fullArea*0.9 && area > bestArea {
			bestArea = area
			bestContour = contour
		}
	}

	if bestArea == 0 {
		return result
	}

	// Get minimum area rotated rectangle
	rotRect := gocv.MinAreaRect(bestContour)

	// Extract rotation angle
	// OpenCV's minAreaRect returns:
	// - width: length of the first edge
	// - height: length of the second edge
	// - angle: rotation of the "width" edge from horizontal, in degrees
	//
	// We want to find the angle to rotate the board so its LONG edge is horizontal.
	rawAngle := float64(rotRect.Angle)
	width := float64(rotRect.Width)
	height := float64(rotRect.Height)

	// Calculate the angle of the LONG edge from horizontal
	// If width >= height: long edge is "width", angle is rawAngle
	// If height > width: long edge is "height", which is perpendicular to width
	//                    so its angle from horizontal is (rawAngle + 90) or (rawAngle - 90)
	var angle float64
	if width >= height {
		// Long edge is the "width" edge
		angle = rawAngle
	} else {
		// Long edge is the "height" edge (perpendicular to width)
		angle = rawAngle + 90
	}

	// Normalize angle to [-45, 45] range
	// This represents the smallest rotation needed to make the long edge horizontal
	for angle > 45 {
		angle -= 90
	}
	for angle < -45 {
		angle += 90
	}

	// Calculate the 4 corners of the rotated rectangle
	cx := float64(rotRect.Center.X)
	cy := float64(rotRect.Center.Y)
	hw := width / 2
	hh := height / 2
	angleRad := float64(rotRect.Angle) * math.Pi / 180.0
	cos := math.Cos(angleRad)
	sin := math.Sin(angleRad)

	corners := [][2]float64{
		{cx + (-hw)*cos - (-hh)*sin, cy + (-hw)*sin + (-hh)*cos},
		{cx + (hw)*cos - (-hh)*sin, cy + (hw)*sin + (-hh)*cos},
		{cx + (hw)*cos - (hh)*sin, cy + (hw)*sin + (hh)*cos},
		{cx + (-hw)*cos - (hh)*sin, cy + (-hw)*sin + (hh)*cos},
	}

	// Find axis-aligned bounding box that contains all corners
	minX, minY := float64(smallW), float64(smallH)
	maxX, maxY := 0.0, 0.0
	for _, pt := range corners {
		if pt[0] < minX {
			minX = pt[0]
		}
		if pt[0] > maxX {
			maxX = pt[0]
		}
		if pt[1] < minY {
			minY = pt[1]
		}
		if pt[1] > maxY {
			maxY = pt[1]
		}
	}

	// Add small margin (0.5% of image dimension)
	marginX := float64(smallW) * 0.005
	marginY := float64(smallH) * 0.005
	minX = math.Max(0, minX-marginX)
	minY = math.Max(0, minY-marginY)
	maxX = math.Min(float64(smallW), maxX+marginX)
	maxY = math.Min(float64(smallH), maxY+marginY)

	// Scale bounds back to original coordinates
	result.Bounds = geometry.RectInt{
		X:      int(minX / scale),
		Y:      int(minY / scale),
		Width:  int((maxX - minX) / scale),
		Height: int((maxY - minY) / scale),
	}
	result.Angle = angle
	result.Detected = true

	return result
}

// DetectBoardRotationFromImage detects board rotation from a Go image.Image.
func DetectBoardRotationFromImage(img image.Image) BoardRotationResult {
	mat, err := imageToMat(img)
	if err != nil {
		return BoardRotationResult{Detected: false}
	}
	defer mat.Close()

	return DetectBoardRotation(mat)
}
