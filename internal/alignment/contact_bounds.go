package alignment

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"

	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
)

// BoardRotationResult contains board detection results including rotation.
type BoardRotationResult struct {
	Bounds   geometry.RectInt
	Angle    float64 // Rotation angle in degrees (angle of board's long edge from horizontal)
	Detected bool
	// MinAreaRect details (in original image coordinates, not downscaled)
	BoardCenterX float64 // Center X of the board in original image coords
	BoardCenterY float64 // Center Y of the board in original image coords
	BoardWidth   float64 // Long edge of the board (pixels)
	BoardHeight  float64 // Short edge of the board (pixels)
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

	// Create mask of pixels that differ from background
	diffThreshold := 25
	if bgColor.R < 30 && bgColor.G < 30 && bgColor.B < 30 {
		diffThreshold = 40
	}
	mask := createBackgroundDiffMask(small, bgColor, diffThreshold)
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

	// Add 5% margin to avoid clipping the board edges
	marginX := float64(smallW) * 0.05
	marginY := float64(smallH) * 0.05
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

	// Sample many small patches along all 4 edges.
	// After rotation the image may have black borders, so some patches will
	// be black. We collect per-patch averages and pick the median-brightness
	// patch as the representative background color.
	type patchColor struct {
		r, g, b    int
		brightness int
	}

	margin := 5
	nPatches := 8 // patches per edge
	var patches []patchColor

	for i := 0; i < nPatches; i++ {
		// Spread patches evenly along each edge
		fx := margin + (w-2*margin-sampleSize)*i/(nPatches-1)
		fy := margin + (h-2*margin-sampleSize)*i/(nPatches-1)
		edgeSamples := []image.Rectangle{
			image.Rect(fx, margin, fx+sampleSize, margin+sampleSize),       // top
			image.Rect(fx, h-margin-sampleSize, fx+sampleSize, h-margin),   // bottom
			image.Rect(margin, fy, margin+sampleSize, fy+sampleSize),       // left
			image.Rect(w-margin-sampleSize, fy, w-margin, fy+sampleSize),   // right
		}
		for _, rect := range edgeSamples {
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
			var pr, pg, pb, cnt int
			for y := rect.Min.Y; y < rect.Max.Y; y++ {
				for x := rect.Min.X; x < rect.Max.X; x++ {
					pb += int(img.GetUCharAt(y, x*3+0))
					pg += int(img.GetUCharAt(y, x*3+1))
					pr += int(img.GetUCharAt(y, x*3+2))
					cnt++
				}
			}
			if cnt > 0 {
				ar := pr / cnt
				ag := pg / cnt
				ab := pb / cnt
				patches = append(patches, patchColor{r: ar, g: ag, b: ab, brightness: ar + ag + ab})
			}
		}
	}

	if len(patches) == 0 {
		return color.RGBA{R: 50, G: 50, B: 50, A: 255}
	}

	// Sort by brightness and pick the median — ignores black border outliers (low)
	// and any bright board pixels that happen to be on the edge (high).
	sort.Slice(patches, func(i, j int) bool {
		return patches[i].brightness < patches[j].brightness
	})
	med := patches[len(patches)/2]
	return color.RGBA{
		R: uint8(med.r),
		G: uint8(med.g),
		B: uint8(med.b),
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
	fmt.Printf("DetectBoardRotation: bgColor=(%d,%d,%d) on %dx%d (scale=%.3f from %dx%d)\n",
		bgColor.R, bgColor.G, bgColor.B, smallW, smallH, scale, imgW, imgH)

	// Create mask of pixels that differ from background
	// (Works even with dark/black backgrounds from rotation borders)
	diffThreshold := 25
	if bgColor.R < 30 && bgColor.G < 30 && bgColor.B < 30 {
		// Very dark background — use a higher threshold to separate board from
		// black borders (the board pixels will be much brighter than black).
		diffThreshold = 40
		fmt.Printf("DetectBoardRotation: dark background, using higher diff threshold=%d\n", diffThreshold)
	}
	mask := createBackgroundDiffMask(small, bgColor, diffThreshold)
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

	fmt.Printf("DetectBoardRotation: minAreaRect center=(%.1f,%.1f) size=%.1fx%.1f rawAngle=%.2f° contourArea=%.0f imgArea=%.0f scale=%.3f\n",
		float64(rotRect.Center.X), float64(rotRect.Center.Y),
		width, height, rawAngle, bestArea, fullArea, scale)

	// Calculate the angle of the LONG edge from horizontal
	// If width >= height: long edge is "width", angle is rawAngle
	// If height > width: long edge is "height", which is perpendicular to width
	//                    so its angle from horizontal is (rawAngle + 90) or (rawAngle - 90)
	var angle float64
	if width >= height {
		// Long edge is the "width" edge
		angle = rawAngle
		fmt.Printf("DetectBoardRotation: width>=height, angle = rawAngle = %.2f°\n", angle)
	} else {
		// Long edge is the "height" edge (perpendicular to width)
		angle = rawAngle + 90
		fmt.Printf("DetectBoardRotation: height>width, angle = rawAngle+90 = %.2f°\n", angle)
	}

	// Normalize angle to [-45, 45] range
	// This represents the smallest rotation needed to make the long edge horizontal
	for angle > 45 {
		angle -= 90
	}
	for angle < -45 {
		angle += 90
	}
	fmt.Printf("DetectBoardRotation: normalized angle = %.2f°\n", angle)

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

	// Add 5% margin to avoid clipping the board edges
	marginX := float64(smallW) * 0.05
	marginY := float64(smallH) * 0.05
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

	// Store MinAreaRect details in original image coordinates
	result.BoardCenterX = cx / scale
	result.BoardCenterY = cy / scale
	longEdge := math.Max(width, height)
	shortEdge := math.Min(width, height)
	result.BoardWidth = longEdge / scale
	result.BoardHeight = shortEdge / scale
	fmt.Printf("DetectBoardRotation: board center=(%.0f,%.0f) size=%.0fx%.0f in original coords\n",
		result.BoardCenterX, result.BoardCenterY, result.BoardWidth, result.BoardHeight)

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

// RotateAndCropToBoard rotates an image to straighten the board and crops to the
// board bounds. It uses the MinAreaRect from the detection result to compute the
// crop mathematically (no re-detection needed after rotation).
func RotateAndCropToBoard(img image.Image, result BoardRotationResult) (image.Image, geometry.RectInt) {
	b := img.Bounds()
	origW := float64(b.Dx())
	origH := float64(b.Dy())

	// Rotate the image to straighten the board.
	// result.Angle is the tilt of the board's long edge from horizontal.
	// In image coords (Y down), GetRotationMatrix2D with this angle
	// rotates the board to be horizontal.
	corrAngle := result.Angle
	rotated := RotateGoImage(img, corrAngle)
	rotB := rotated.Bounds()
	newW := float64(rotB.Dx())
	newH := float64(rotB.Dy())

	// Compute where the board center lands in the rotated image.
	// rotateMatByAngle rotates around the original center and expands the canvas.
	// A point (x,y) in the original maps to:
	//   x' = (x - origW/2)*cos(θ) - (y - origH/2)*sin(θ) + newW/2
	//   y' = (x - origW/2)*sin(θ) + (y - origH/2)*cos(θ) + newH/2
	theta := corrAngle * math.Pi / 180
	cosA := math.Cos(theta)
	sinA := math.Sin(theta)
	dx := result.BoardCenterX - origW/2
	dy := result.BoardCenterY - origH/2
	newCX := dx*cosA - dy*sinA + newW/2
	newCY := dx*sinA + dy*cosA + newH/2

	// After rotation, the board is axis-aligned. Crop to its dimensions + 5% margin.
	marginW := result.BoardWidth * 0.05
	marginH := result.BoardHeight * 0.05
	cropX := int(math.Round(newCX - result.BoardWidth/2 - marginW))
	cropY := int(math.Round(newCY - result.BoardHeight/2 - marginH))
	cropW := int(math.Round(result.BoardWidth + 2*marginW))
	cropH := int(math.Round(result.BoardHeight + 2*marginH))

	// Clamp to image bounds
	if cropX < 0 {
		cropX = 0
	}
	if cropY < 0 {
		cropY = 0
	}
	if cropX+cropW > rotB.Dx() {
		cropW = rotB.Dx() - cropX
	}
	if cropY+cropH > rotB.Dy() {
		cropH = rotB.Dy() - cropY
	}

	bounds := geometry.RectInt{X: cropX, Y: cropY, Width: cropW, Height: cropH}
	fmt.Printf("RotateAndCropToBoard: correction=%.2f° boardCenter=(%.0f,%.0f)->(%.0f,%.0f) crop=(%d,%d) %dx%d on %dx%d\n",
		corrAngle, result.BoardCenterX, result.BoardCenterY, newCX, newCY,
		cropX, cropY, cropW, cropH, rotB.Dx(), rotB.Dy())

	cropped := CropGoImage(rotated, bounds)
	return cropped, bounds
}

// CropGoImage crops a Go image to the specified bounds.
func CropGoImage(img image.Image, bounds geometry.RectInt) image.Image {
	mat, err := imageToMat(img)
	if err != nil {
		return img
	}
	defer mat.Close()

	rect := image.Rect(bounds.X, bounds.Y, bounds.X+bounds.Width, bounds.Y+bounds.Height)
	roi := mat.Region(rect)
	cropped := roi.Clone()
	roi.Close()
	defer cropped.Close()

	result, err := matToImage(cropped)
	if err != nil {
		return img
	}
	return result
}

// CropBlackBorders finds the bounding box of non-black content in an image.
// This is designed for images that have been rotated and have black borders
// from the canvas expansion. Returns the cropped image and bounds.
func CropBlackBorders(img image.Image) (image.Image, geometry.RectInt) {
	b := img.Bounds()
	fullBounds := geometry.RectInt{X: 0, Y: 0, Width: b.Dx(), Height: b.Dy()}

	mat, err := imageToMat(img)
	if err != nil {
		return img, fullBounds
	}
	defer mat.Close()

	// Convert to grayscale and threshold to find non-black pixels
	gray := gocv.NewMat()
	defer gray.Close()
	gocv.CvtColor(mat, &gray, gocv.ColorBGRToGray)

	binary := gocv.NewMat()
	defer binary.Close()
	gocv.Threshold(gray, &binary, 15, 255, gocv.ThresholdBinary)

	// Find contours of non-black regions
	contours := gocv.FindContours(binary, gocv.RetrievalExternal, gocv.ChainApproxSimple)
	defer contours.Close()

	if contours.Size() == 0 {
		return img, fullBounds
	}

	// Find bounding rect that encompasses all contours
	minX, minY := mat.Cols(), mat.Rows()
	maxX, maxY := 0, 0
	for i := 0; i < contours.Size(); i++ {
		rect := gocv.BoundingRect(contours.At(i))
		if rect.Min.X < minX {
			minX = rect.Min.X
		}
		if rect.Min.Y < minY {
			minY = rect.Min.Y
		}
		if rect.Max.X > maxX {
			maxX = rect.Max.X
		}
		if rect.Max.Y > maxY {
			maxY = rect.Max.Y
		}
	}

	// Sanity check: don't crop to something tiny
	cropW := maxX - minX
	cropH := maxY - minY
	if cropW < mat.Cols()/4 || cropH < mat.Rows()/4 {
		fmt.Printf("CropBlackBorders: detected region too small (%dx%d on %dx%d), returning full image\n",
			cropW, cropH, mat.Cols(), mat.Rows())
		return img, fullBounds
	}

	bounds := geometry.RectInt{X: minX, Y: minY, Width: cropW, Height: cropH}
	fmt.Printf("CropBlackBorders: cropping to (%d,%d) %dx%d from %dx%d\n",
		minX, minY, cropW, cropH, mat.Cols(), mat.Rows())

	// Crop the mat
	roi := mat.Region(image.Rect(minX, minY, maxX, maxY))
	cropped := roi.Clone()
	roi.Close()
	defer cropped.Close()

	result, err := matToImage(cropped)
	if err != nil {
		return img, fullBounds
	}
	return result, bounds
}
