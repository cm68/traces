package alignment

import (
	"fmt"
	"image"
	"math"
	"runtime"
	"sync"

	"gocv.io/x/gocv"
)

// imageToMat converts a Go image.Image to gocv.Mat (parallelized)
func imageToMat(img image.Image) (gocv.Mat, error) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Create BGR Mat (OpenCV default)
	mat := gocv.NewMatWithSize(height, width, gocv.MatTypeCV8UC3)

	// Parallelize by horizontal stripes
	numWorkers := runtime.NumCPU()
	rowsPerWorker := (height + numWorkers - 1) / numWorkers

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		startY := w * rowsPerWorker
		endY := startY + rowsPerWorker
		if endY > height {
			endY = height
		}
		if startY >= height {
			break
		}

		wg.Add(1)
		go func(yStart, yEnd int) {
			defer wg.Done()
			for y := yStart; y < yEnd; y++ {
				for x := 0; x < width; x++ {
					r, g, b, _ := img.At(x+bounds.Min.X, y+bounds.Min.Y).RGBA()
					// OpenCV uses BGR format
					mat.SetUCharAt(y, x*3+0, uint8(b>>8))
					mat.SetUCharAt(y, x*3+1, uint8(g>>8))
					mat.SetUCharAt(y, x*3+2, uint8(r>>8))
				}
			}
		}(startY, endY)
	}
	wg.Wait()

	return mat, nil
}

// matToImage converts a gocv.Mat to a Go image.Image (parallelized)
func matToImage(mat gocv.Mat) (image.Image, error) {
	h := mat.Rows()
	w := mat.Cols()

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	stride := img.Stride

	// Parallelize by horizontal stripes
	numWorkers := runtime.NumCPU()
	rowsPerWorker := (h + numWorkers - 1) / numWorkers

	var wg sync.WaitGroup
	for worker := 0; worker < numWorkers; worker++ {
		startY := worker * rowsPerWorker
		endY := startY + rowsPerWorker
		if endY > h {
			endY = h
		}
		if startY >= h {
			break
		}

		wg.Add(1)
		go func(yStart, yEnd int) {
			defer wg.Done()
			for y := yStart; y < yEnd; y++ {
				rowOffset := y * stride
				for x := 0; x < w; x++ {
					// OpenCV uses BGR format, write directly to Pix slice
					pixOffset := rowOffset + x*4
					img.Pix[pixOffset+0] = mat.GetUCharAt(y, x*3+2) // R
					img.Pix[pixOffset+1] = mat.GetUCharAt(y, x*3+1) // G
					img.Pix[pixOffset+2] = mat.GetUCharAt(y, x*3+0) // B
					img.Pix[pixOffset+3] = 255                      // A
				}
			}
		}(startY, endY)
	}
	wg.Wait()

	return img, nil
}

// RotateGoImage rotates a Go image.Image by the specified angle in degrees.
// Positive angle rotates counter-clockwise.
func RotateGoImage(img image.Image, angleDegrees float64) image.Image {
	if angleDegrees == 0 {
		return img
	}

	bounds := img.Bounds()
	fmt.Printf("RotateGoImage: input %dx%d, angle=%.2f°\n", bounds.Dx(), bounds.Dy(), angleDegrees)

	// Convert to Mat
	mat, err := imageToMat(img)
	if err != nil {
		fmt.Printf("RotateGoImage: imageToMat failed: %v\n", err)
		return img
	}
	defer mat.Close()

	fmt.Printf("RotateGoImage: converted to Mat %dx%d\n", mat.Cols(), mat.Rows())

	// Rotate
	rotated := rotateMatByAngle(mat, angleDegrees)
	defer rotated.Close()

	fmt.Printf("RotateGoImage: rotated Mat %dx%d\n", rotated.Cols(), rotated.Rows())

	// Convert back to Go image
	result, err := matToImage(rotated)
	if err != nil {
		fmt.Printf("RotateGoImage: matToImage failed: %v\n", err)
		return img
	}

	resultBounds := result.Bounds()
	fmt.Printf("RotateGoImage: output %dx%d\n", resultBounds.Dx(), resultBounds.Dy())

	return result
}

// rotateMatByAngle rotates a Mat by an arbitrary angle.
func rotateMatByAngle(img gocv.Mat, angleDegrees float64) gocv.Mat {
	h := img.Rows()
	w := img.Cols()

	// Calculate rotation matrix
	center := image.Point{X: w / 2, Y: h / 2}
	rotMat := gocv.GetRotationMatrix2D(center, angleDegrees, 1.0)
	defer rotMat.Close()

	// Calculate new image bounds after rotation
	angleRad := angleDegrees * math.Pi / 180
	cos := math.Abs(math.Cos(angleRad))
	sin := math.Abs(math.Sin(angleRad))
	newW := int(float64(h)*sin + float64(w)*cos)
	newH := int(float64(h)*cos + float64(w)*sin)

	// Adjust rotation matrix for the new center
	rotMat.SetDoubleAt(0, 2, rotMat.GetDoubleAt(0, 2)+float64(newW-w)/2)
	rotMat.SetDoubleAt(1, 2, rotMat.GetDoubleAt(1, 2)+float64(newH-h)/2)

	// Apply rotation
	rotated := gocv.NewMat()
	gocv.WarpAffine(img, &rotated, rotMat, image.Point{X: newW, Y: newH})

	return rotated
}

// createGoldMaskWithParams creates a binary mask using specified HSV parameters.
func createGoldMaskWithParams(img gocv.Mat, params DetectionParams) gocv.Mat {
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(img, &hsv, gocv.ColorBGRToHSV)

	mask := gocv.NewMat()
	lower := gocv.NewScalar(params.HueMin, params.SatMin, params.ValMin, 0)
	upper := gocv.NewScalar(params.HueMax, params.SatMax, params.ValMax, 0)
	gocv.InRangeWithScalar(hsv, lower, upper, &mask)

	// Morphological close to fill gaps
	kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Point{3, 3})
	defer kernel.Close()
	gocv.MorphologyEx(mask, &mask, gocv.MorphClose, kernel)

	return mask
}

// sampleContactColors samples the average HSV color from the center of found contacts.
func sampleContactColors(img gocv.Mat, contacts []Contact, offsetX, offsetY int) (avgH, avgS, avgV float64) {
	if len(contacts) == 0 {
		return 0, 0, 0
	}

	// Convert to HSV
	hsv := gocv.NewMat()
	defer hsv.Close()
	gocv.CvtColor(img, &hsv, gocv.ColorBGRToHSV)

	var totalH, totalS, totalV float64
	var sampleCount int

	imgH := img.Rows()
	imgW := img.Cols()

	for _, c := range contacts {
		// Sample from center of contact
		cx := c.Bounds.X + c.Bounds.Width/2
		cy := c.Bounds.Y + c.Bounds.Height/2

		// Sample a small region around center
		sampleSize := min(c.Bounds.Width, c.Bounds.Height) / 3
		if sampleSize < 3 {
			sampleSize = 3
		}

		for dy := -sampleSize / 2; dy <= sampleSize/2; dy++ {
			for dx := -sampleSize / 2; dx <= sampleSize/2; dx++ {
				x := cx + dx
				y := cy + dy
				if x >= 0 && x < imgW && y >= 0 && y < imgH {
					h := float64(hsv.GetUCharAt(y, x*3+0))
					s := float64(hsv.GetUCharAt(y, x*3+1))
					v := float64(hsv.GetUCharAt(y, x*3+2))
					totalH += h
					totalS += s
					totalV += v
					sampleCount++
				}
			}
		}
	}

	if sampleCount == 0 {
		return 0, 0, 0
	}

	return totalH / float64(sampleCount), totalS / float64(sampleCount), totalV / float64(sampleCount)
}
