// Package logo provides logo detection and template matching for IC manufacturer marks.
package logo

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"pcb-tracer/pkg/geometry"
)

// Logo represents a detected or defined logo template.
// Logos are stored as quantized black/white bitmaps for matching.
type Logo struct {
	Name           string           `json:"name"`            // Template name, e.g., "ST", "NS", "TI"
	ManufacturerID string           `json:"manufacturer_id"` // Manufacturer code for OCR output
	Bounds         geometry.RectInt `json:"bounds"`          // Location in source image
	Width          int              `json:"width"`           // Quantized bitmap width
	Height         int              `json:"height"`          // Quantized bitmap height
	QuantizedSize  int              `json:"quantized_size"`  // Target quantization size (max dimension)
	Bits           []byte           `json:"bits"`            // Packed bitmap (1 bit per pixel, row-major)

	// Source tracking
	SourceComponent string `json:"source_component,omitempty"` // Component ID this was extracted from
	Side            string `json:"side,omitempty"`             // "front" or "back"
}

// NewLogo creates a new logo from an image region.
// The region is quantized to black/white at the specified size.
func NewLogo(name string, img image.Image, bounds geometry.RectInt, quantizedSize int) *Logo {
	if quantizedSize < 8 {
		quantizedSize = 16
	}

	// Extract and quantize the region
	bits, w, h := quantizeRegion(img, bounds, quantizedSize)

	return &Logo{
		Name:          name,
		Bounds:        bounds,
		Width:         w,
		Height:        h,
		QuantizedSize: quantizedSize,
		Bits:          bits,
	}
}

// applyLocalContrastEnhancement applies simplified CLAHE-like enhancement to a grayscale image.
// This improves logo detection by normalizing local contrast variations.
func applyLocalContrastEnhancement(gray []uint8, w, h int) {
	if w < 4 || h < 4 {
		return // Too small for meaningful enhancement
	}

	// Use a tile-based approach with 4x4 tiles
	tileW := max(w/4, 2)
	tileH := max(h/4, 2)

	// Create output buffer
	enhanced := make([]uint8, len(gray))
	copy(enhanced, gray)

	// For each tile, compute local histogram equalization
	for ty := 0; ty < h; ty += tileH {
		for tx := 0; tx < w; tx += tileW {
			// Tile bounds
			x0 := tx
			y0 := ty
			x1 := min(tx+tileW, w)
			y1 := min(ty+tileH, h)

			// Build histogram for this tile
			var hist [256]int
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					hist[gray[y*w+x]]++
				}
			}

			// Compute CDF
			var cdf [256]int
			cdf[0] = hist[0]
			for i := 1; i < 256; i++ {
				cdf[i] = cdf[i-1] + hist[i]
			}

			// Find min non-zero CDF value
			minCDF := 0
			for i := 0; i < 256; i++ {
				if cdf[i] > 0 {
					minCDF = cdf[i]
					break
				}
			}

			// Total pixels in tile
			total := cdf[255]
			if total == minCDF {
				continue // No contrast in tile
			}

			// Apply histogram equalization to tile
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					v := gray[y*w+x]
					// Map through CDF
					newVal := (cdf[v] - minCDF) * 255 / (total - minCDF)
					enhanced[y*w+x] = uint8(newVal)
				}
			}
		}
	}

	// Copy enhanced back to gray
	copy(gray, enhanced)
}

// quantizeRegion extracts a region from an image and converts to black/white bitmap.
// Uses area averaging for downsampling, bilinear interpolation for upsampling.
// Returns packed bits, width, height.
func quantizeRegion(img image.Image, bounds geometry.RectInt, targetSize int) ([]byte, int, int) {
	return quantizeRegionWithOptions(img, bounds, targetSize, true)
}

// quantizeRegionFast is like quantizeRegion but skips contrast enhancement for speed.
func quantizeRegionFast(img image.Image, bounds geometry.RectInt, targetSize int) ([]byte, int, int) {
	return quantizeRegionWithOptions(img, bounds, targetSize, false)
}

// quantizeRegionWithOptions extracts a region with optional contrast enhancement.
func quantizeRegionWithOptions(img image.Image, bounds geometry.RectInt, targetSize int, enhanceContrast bool) ([]byte, int, int) {
	// Calculate aspect ratio to preserve proportions
	aspect := float64(bounds.Width) / float64(bounds.Height)

	var w, h int
	if aspect >= 1.0 {
		w = targetSize
		h = int(float64(targetSize) / aspect)
		if h < 1 {
			h = 1
		}
	} else {
		h = targetSize
		w = int(float64(targetSize) * aspect)
		if w < 1 {
			w = 1
		}
	}

	// Create grayscale image
	grayImg := make([]uint8, w*h)

	// Calculate scale factors (source pixels per output pixel)
	scaleX := float64(bounds.Width) / float64(w)
	scaleY := float64(bounds.Height) / float64(h)

	// Determine if we're upsampling or downsampling
	upsampling := scaleX < 1.0 || scaleY < 1.0

	if upsampling {
		// Bilinear interpolation for upsampling
		for y := 0; y < h; y++ {
			// Map output y to source y (fractional)
			srcY := float64(y) * scaleY
			sy0 := int(srcY)
			sy1 := sy0 + 1
			if sy1 >= bounds.Height {
				sy1 = bounds.Height - 1
			}
			fy := srcY - float64(sy0)

			for x := 0; x < w; x++ {
				// Map output x to source x (fractional)
				srcX := float64(x) * scaleX
				sx0 := int(srcX)
				sx1 := sx0 + 1
				if sx1 >= bounds.Width {
					sx1 = bounds.Width - 1
				}
				fx := srcX - float64(sx0)

				// Get four surrounding pixels
				c00 := colorToGray(img.At(bounds.X+sx0, bounds.Y+sy0))
				c10 := colorToGray(img.At(bounds.X+sx1, bounds.Y+sy0))
				c01 := colorToGray(img.At(bounds.X+sx0, bounds.Y+sy1))
				c11 := colorToGray(img.At(bounds.X+sx1, bounds.Y+sy1))

				// Bilinear interpolation
				top := float64(c00)*(1-fx) + float64(c10)*fx
				bottom := float64(c01)*(1-fx) + float64(c11)*fx
				value := top*(1-fy) + bottom*fy

				grayImg[y*w+x] = uint8(value)
			}
		}
	} else {
		// Nearest-neighbor sampling for downsampling
		// Sample center of each target pixel's source region to preserve sharp edges
		for y := 0; y < h; y++ {
			// Map output y to source y (center of the region)
			srcY := int((float64(y) + 0.5) * scaleY)
			if srcY >= bounds.Height {
				srcY = bounds.Height - 1
			}

			for x := 0; x < w; x++ {
				// Map output x to source x (center of the region)
				srcX := int((float64(x) + 0.5) * scaleX)
				if srcX >= bounds.Width {
					srcX = bounds.Width - 1
				}

				c := img.At(bounds.X+srcX, bounds.Y+srcY)
				grayImg[y*w+x] = colorToGray(c)
			}
		}
	}

	// Apply local contrast enhancement before thresholding (optional for speed)
	// This helps normalize lighting variations across the logo
	if enhanceContrast {
		applyLocalContrastEnhancement(grayImg, w, h)
	}

	// Calculate Otsu threshold on the grayscale image
	threshold := calculateOtsuThresholdFromGray(grayImg, w, h)

	// Ensure at least 25% white pixels for adequate contrast
	// If Otsu gives too few white pixels, lower the threshold
	minWhiteRatio := 0.25
	maxWhiteRatio := 0.75
	total := w * h

	// Count white pixels at current threshold
	countWhite := func(thresh uint8) int {
		count := 0
		for i := 0; i < total; i++ {
			if grayImg[i] > thresh {
				count++
			}
		}
		return count
	}

	whiteCount := countWhite(threshold)
	whiteRatio := float64(whiteCount) / float64(total)

	// If too few white pixels, lower threshold to capture more
	if whiteRatio < minWhiteRatio {
		// Find threshold that gives ~25% white
		// Binary search for the right threshold
		low, high := uint8(0), threshold
		for low < high {
			mid := (low + high) / 2
			ratio := float64(countWhite(mid)) / float64(total)
			if ratio < minWhiteRatio {
				high = mid
			} else {
				low = mid + 1
			}
		}
		threshold = low
		if threshold > 0 {
			threshold--
		}
		whiteRatio = float64(countWhite(threshold)) / float64(total)
	}

	// If too many white pixels, raise threshold
	if whiteRatio > maxWhiteRatio {
		low, high := threshold, uint8(255)
		for low < high {
			mid := (low + high + 1) / 2
			ratio := float64(countWhite(mid)) / float64(total)
			if ratio > maxWhiteRatio {
				low = mid
			} else {
				high = mid - 1
			}
		}
		threshold = low
		whiteRatio = float64(countWhite(threshold)) / float64(total)
	}

	// Apply threshold to create binary bitmap
	bits := make([]byte, (w*h+7)/8)
	for i := 0; i < w*h; i++ {
		if grayImg[i] > threshold {
			bits[i/8] |= 1 << (7 - i%8)
		}
	}

	// Despeckle: clear isolated set bits with no set neighbors
	for py := 0; py < h; py++ {
		for px := 0; px < w; px++ {
			if !getBitPacked(bits, px, py, w) {
				continue
			}
			hasNeighbor := false
			for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
				nx, ny := px+d[0], py+d[1]
				if nx >= 0 && nx < w && ny >= 0 && ny < h && getBitPacked(bits, nx, ny, w) {
					hasNeighbor = true
					break
				}
			}
			if !hasNeighbor {
				i := py*w + px
				bits[i/8] &^= 1 << (7 - i%8)
			}
		}
	}

	return bits, w, h
}

// quantizeRegionMultiThreshold extracts a region and creates multiple Logo candidates
// at different threshold offsets from Otsu. Returns logos at offsets [-20, -10, 0, +10, +20].
// This allows matching to find the best threshold for comparison.
func quantizeRegionMultiThreshold(img image.Image, bounds geometry.RectInt, targetSize int) []*Logo {
	// Calculate aspect ratio to preserve proportions
	aspect := float64(bounds.Width) / float64(bounds.Height)

	var w, h int
	if aspect >= 1.0 {
		w = targetSize
		h = int(float64(targetSize) / aspect)
		if h < 1 {
			h = 1
		}
	} else {
		h = targetSize
		w = int(float64(targetSize) * aspect)
		if w < 1 {
			w = 1
		}
	}

	// Create grayscale image
	grayImg := make([]uint8, w*h)

	// Calculate scale factors
	scaleX := float64(bounds.Width) / float64(w)
	scaleY := float64(bounds.Height) / float64(h)

	// Build grayscale image (using nearest-neighbor for simplicity)
	for y := 0; y < h; y++ {
		srcY := int((float64(y) + 0.5) * scaleY)
		if srcY >= bounds.Height {
			srcY = bounds.Height - 1
		}
		for x := 0; x < w; x++ {
			srcX := int((float64(x) + 0.5) * scaleX)
			if srcX >= bounds.Width {
				srcX = bounds.Width - 1
			}
			c := img.At(bounds.X+srcX, bounds.Y+srcY)
			grayImg[y*w+x] = colorToGray(c)
		}
	}

	// Apply local contrast enhancement
	applyLocalContrastEnhancement(grayImg, w, h)

	// Calculate base Otsu threshold
	baseThreshold := int(calculateOtsuThresholdFromGray(grayImg, w, h))

	// Try multiple threshold offsets
	offsets := []int{-20, -10, 0, 10, 20}
	logos := make([]*Logo, 0, len(offsets))

	for _, offset := range offsets {
		threshold := baseThreshold + offset
		if threshold < 0 {
			threshold = 0
		}
		if threshold > 255 {
			threshold = 255
		}

		// Apply threshold to create binary bitmap
		bits := make([]byte, (w*h+7)/8)
		for i := 0; i < w*h; i++ {
			if int(grayImg[i]) > threshold {
				bits[i/8] |= 1 << (7 - i%8)
			}
		}

		logos = append(logos, &Logo{
			Width:  w,
			Height: h,
			Bits:   bits,
		})
	}

	return logos
}

// calculateOtsuThresholdFromGray computes Otsu threshold from a grayscale array.
func calculateOtsuThresholdFromGray(gray []uint8, w, h int) uint8 {
	// Build histogram
	var hist [256]int
	total := w * h

	for i := 0; i < total; i++ {
		hist[gray[i]]++
	}

	if total == 0 {
		return 128
	}

	// Calculate sum of all pixel values
	var sum float64
	for i := 0; i < 256; i++ {
		sum += float64(i) * float64(hist[i])
	}

	var sumB float64
	var wB, wF int
	var maxVar float64
	threshold := uint8(128)

	for t := 0; t < 256; t++ {
		wB += hist[t]
		if wB == 0 {
			continue
		}
		wF = total - wB
		if wF == 0 {
			break
		}

		sumB += float64(t) * float64(hist[t])
		mB := sumB / float64(wB)
		mF := (sum - sumB) / float64(wF)

		// Between-class variance
		variance := float64(wB) * float64(wF) * (mB - mF) * (mB - mF)
		if variance > maxVar {
			maxVar = variance
			threshold = uint8(t)
		}
	}

	return threshold
}

// calculateOtsuThreshold computes optimal threshold for a region using Otsu's method.
func calculateOtsuThreshold(img image.Image, bounds geometry.RectInt) uint8 {
	// Build histogram
	var hist [256]int
	total := 0

	for y := bounds.Y; y < bounds.Y+bounds.Height; y++ {
		for x := bounds.X; x < bounds.X+bounds.Width; x++ {
			c := img.At(x, y)
			gray := colorToGray(c)
			hist[gray]++
			total++
		}
	}

	if total == 0 {
		return 128
	}

	// Calculate sum of all pixel values
	var sum float64
	for i := 0; i < 256; i++ {
		sum += float64(i) * float64(hist[i])
	}

	var sumB float64
	var wB, wF int
	var maxVar float64
	threshold := uint8(128)

	for t := 0; t < 256; t++ {
		wB += hist[t]
		if wB == 0 {
			continue
		}
		wF = total - wB
		if wF == 0 {
			break
		}

		sumB += float64(t) * float64(hist[t])
		mB := sumB / float64(wB)
		mF := (sum - sumB) / float64(wF)

		// Between-class variance
		variance := float64(wB) * float64(wF) * (mB - mF) * (mB - mF)
		if variance > maxVar {
			maxVar = variance
			threshold = uint8(t)
		}
	}

	return threshold
}

// colorToGray converts a color to grayscale value.
func colorToGray(c color.Color) uint8 {
	r, g, b, _ := c.RGBA()
	// Standard luminance formula
	gray := (299*r + 587*g + 114*b) / 1000
	return uint8(gray >> 8)
}

// GetBit returns the bit value at (x, y) in the quantized bitmap.
func (l *Logo) GetBit(x, y int) bool {
	if x < 0 || x >= l.Width || y < 0 || y >= l.Height {
		return false
	}
	bitIdx := y*l.Width + x
	return (l.Bits[bitIdx/8] & (1 << (7 - bitIdx%8))) != 0
}

// SetBit sets the bit value at (x, y) in the quantized bitmap.
func (l *Logo) SetBit(x, y int, value bool) {
	if x < 0 || x >= l.Width || y < 0 || y >= l.Height {
		return
	}
	bitIdx := y*l.Width + x
	if value {
		l.Bits[bitIdx/8] |= 1 << (7 - bitIdx%8)
	} else {
		l.Bits[bitIdx/8] &^= 1 << (7 - bitIdx%8)
	}
}

// ToImage converts the quantized bitmap to an image.Image for display.
func (l *Logo) ToImage() *image.Gray {
	img := image.NewGray(image.Rect(0, 0, l.Width, l.Height))
	for y := 0; y < l.Height; y++ {
		for x := 0; x < l.Width; x++ {
			if l.GetBit(x, y) {
				img.SetGray(x, y, color.Gray{Y: 255})
			} else {
				img.SetGray(x, y, color.Gray{Y: 0})
			}
		}
	}
	return img
}

// ToScaledImage creates a scaled version of the logo for display.
func (l *Logo) ToScaledImage(scale int) *image.Gray {
	if scale < 1 {
		scale = 1
	}
	w, h := l.Width*scale, l.Height*scale
	img := image.NewGray(image.Rect(0, 0, w, h))

	for y := 0; y < h; y++ {
		srcY := y / scale
		for x := 0; x < w; x++ {
			srcX := x / scale
			if l.GetBit(srcX, srcY) {
				img.SetGray(x, y, color.Gray{Y: 255})
			} else {
				img.SetGray(x, y, color.Gray{Y: 0})
			}
		}
	}
	return img
}

// findFirstSet returns the (x, y) position of the first set bit scanning row-major.
func (l *Logo) findFirstSet() (int, int, bool) {
	for y := 0; y < l.Height; y++ {
		for x := 0; x < l.Width; x++ {
			if l.GetBit(x, y) {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

// Match compares this logo against another and returns a similarity score (0-1).
// Aligns on the first set bit in each bitmap so small translations don't kill the score.
// Uses a weighted combination of pixel matching and edge matching for robustness.
func (l *Logo) Match(other *Logo) float64 {
	if l.Width != other.Width || l.Height != other.Height {
		return 0
	}

	// Align on first set bit in each bitmap
	lx, ly, lFound := l.findFirstSet()
	ox, oy, oFound := other.findFirstSet()

	if !lFound && !oFound {
		return 1.0 // Both empty
	}
	if !lFound || !oFound {
		return 0
	}

	dx := ox - lx
	dy := oy - ly

	// Pixel-wise matching over the overlapping region
	matching := 0
	total := 0
	edgeMatching := 0
	edgeTotal := 0

	for y := 0; y < l.Height; y++ {
		oy2 := y + dy
		if oy2 < 0 || oy2 >= l.Height {
			continue
		}
		for x := 0; x < l.Width; x++ {
			ox2 := x + dx
			if ox2 < 0 || ox2 >= l.Width {
				continue
			}

			lBit := l.GetBit(x, y)
			oBit := other.GetBit(ox2, oy2)
			total++
			if lBit == oBit {
				matching++
			}

			// Horizontal edge
			if x+1 < l.Width && ox2+1 < l.Width {
				lEdge := lBit != l.GetBit(x+1, y)
				oEdge := oBit != other.GetBit(ox2+1, oy2)
				if lEdge || oEdge {
					edgeTotal++
					if lEdge == oEdge {
						edgeMatching++
					}
				}
			}

			// Vertical edge
			if y+1 < l.Height && oy2+1 < l.Height {
				lEdge := lBit != l.GetBit(x, y+1)
				oEdge := oBit != other.GetBit(ox2, oy2+1)
				if lEdge || oEdge {
					edgeTotal++
					if lEdge == oEdge {
						edgeMatching++
					}
				}
			}
		}
	}

	if total == 0 {
		return 0
	}

	pixelScore := float64(matching) / float64(total)
	edgeScore := 1.0
	if edgeTotal > 0 {
		edgeScore = float64(edgeMatching) / float64(edgeTotal)
	}

	// Weighted combination: 60% pixel, 40% edge
	return 0.6*pixelScore + 0.4*edgeScore
}

// String returns a debug string representation.
func (l *Logo) String() string {
	return fmt.Sprintf("Logo<%s %dx%d at (%d,%d)>", l.Name, l.Width, l.Height, l.Bounds.X, l.Bounds.Y)
}

// LogoLibrary stores a collection of logo templates.
type LogoLibrary struct {
	Logos []*Logo `json:"logos"`
}

// NewLogoLibrary creates a new empty logo library.
func NewLogoLibrary() *LogoLibrary {
	return &LogoLibrary{
		Logos: make([]*Logo, 0),
	}
}

// Add adds a logo to the library, maintaining sorted order by name.
func (lib *LogoLibrary) Add(logo *Logo) {
	lib.Logos = append(lib.Logos, logo)
	lib.Sort()
}

// Sort sorts the logos by name (case-insensitive).
func (lib *LogoLibrary) Sort() {
	sort.Slice(lib.Logos, func(i, j int) bool {
		return strings.ToLower(lib.Logos[i].Name) < strings.ToLower(lib.Logos[j].Name)
	})
}

// Remove removes a logo by name.
func (lib *LogoLibrary) Remove(name string) {
	for i, logo := range lib.Logos {
		if logo.Name == name {
			lib.Logos = append(lib.Logos[:i], lib.Logos[i+1:]...)
			return
		}
	}
}

// Get returns a logo by name, or nil if not found.
func (lib *LogoLibrary) Get(name string) *Logo {
	for _, logo := range lib.Logos {
		if logo.Name == name {
			return logo
		}
	}
	return nil
}

// GetNames returns all logo names in the library.
func (lib *LogoLibrary) GetNames() []string {
	names := make([]string, len(lib.Logos))
	for i, logo := range lib.Logos {
		names[i] = logo.Name
	}
	return names
}

// FindBestMatch finds the best matching logo template for a given image region.
// Uses multi-threshold matching to find the best match across different threshold levels.
// Returns the logo name and match score, or empty string if no good match found.
func (lib *LogoLibrary) FindBestMatch(img image.Image, bounds geometry.RectInt, minScore float64) (string, float64) {
	if len(lib.Logos) == 0 {
		return "", 0
	}

	// Use the most common template size
	targetSize := 16
	if len(lib.Logos) > 0 {
		targetSize = lib.Logos[0].Width
	}

	// Generate multiple candidate quantizations at different thresholds
	candidates := quantizeRegionMultiThreshold(img, bounds, targetSize)

	var bestName string
	var bestScore float64

	// Try each candidate against each template
	for _, template := range lib.Logos {
		for _, candidate := range candidates {
			score := template.Match(candidate)
			if score > bestScore {
				bestScore = score
				bestName = template.Name
			}
		}
	}

	if bestScore >= minScore {
		return bestName, bestScore
	}
	return "", bestScore
}

// LogoMatch represents a detected logo in an image.
type LogoMatch struct {
	Logo        *Logo            // The matched logo template
	Bounds      geometry.RectInt // Location in the source image
	Score       float64          // Match score (0-1)
	Rotation    int              // Rotation in degrees (0, 90, 180, 270)
	ScaleFactor float64          // Scale factor used for matching
}

// quantizedImage holds a pre-quantized grayscale bitmap for fast pattern matching.
type quantizedImage struct {
	gray   []uint8 // Grayscale pixels (0-255)
	width  int
	height int
}

// newQuantizedImage creates a grayscale quantized version of an image region.
// Uses fast nearest-neighbor sampling. Applies rotation if specified.
func newQuantizedImage(img image.Image, bounds geometry.RectInt, targetWidth, targetHeight int, rotation int) *quantizedImage {
	qi := &quantizedImage{
		gray:   make([]uint8, targetWidth*targetHeight),
		width:  targetWidth,
		height: targetHeight,
	}

	imgBounds := img.Bounds()
	scaleX := float64(bounds.Width) / float64(targetWidth)
	scaleY := float64(bounds.Height) / float64(targetHeight)

	for y := 0; y < targetHeight; y++ {
		for x := 0; x < targetWidth; x++ {
			// Map to source coordinates
			srcX := int(float64(x)*scaleX) + bounds.X
			srcY := int(float64(y)*scaleY) + bounds.Y

			// Apply rotation transform
			var finalX, finalY int
			switch rotation {
			case 90:
				// Rotate 90° CW: (x,y) -> (height-1-y, x)
				finalX = bounds.X + bounds.Height - 1 - (srcY - bounds.Y)
				finalY = bounds.Y + (srcX - bounds.X)
			case 180:
				finalX = bounds.X + bounds.Width - 1 - (srcX - bounds.X)
				finalY = bounds.Y + bounds.Height - 1 - (srcY - bounds.Y)
			case 270:
				// Rotate 270° CW (90° CCW): (x,y) -> (y, width-1-x)
				finalX = bounds.X + (srcY - bounds.Y)
				finalY = bounds.Y + bounds.Width - 1 - (srcX - bounds.X)
			default:
				finalX = srcX
				finalY = srcY
			}

			// Clamp to image bounds
			if finalX < imgBounds.Min.X {
				finalX = imgBounds.Min.X
			}
			if finalX >= imgBounds.Max.X {
				finalX = imgBounds.Max.X - 1
			}
			if finalY < imgBounds.Min.Y {
				finalY = imgBounds.Min.Y
			}
			if finalY >= imgBounds.Max.Y {
				finalY = imgBounds.Max.Y - 1
			}

			qi.gray[y*targetWidth+x] = colorToGray(img.At(finalX, finalY))
		}
	}

	return qi
}

// getWindow extracts a sub-window as a thresholded bitmap for matching.
// Uses Otsu thresholding on the window. Returns packed bits.
func (qi *quantizedImage) getWindow(x, y, w, h int) []byte {
	if x < 0 || y < 0 || x+w > qi.width || y+h > qi.height {
		return nil
	}

	// Extract grayscale window and compute Otsu threshold
	window := make([]uint8, w*h)
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			window[dy*w+dx] = qi.gray[(y+dy)*qi.width+(x+dx)]
		}
	}

	threshold := calculateOtsuThresholdFromGray(window, w, h)

	// Apply threshold to create bitmap
	bits := make([]byte, (w*h+7)/8)
	for i := 0; i < w*h; i++ {
		if window[i] > threshold {
			bits[i/8] |= 1 << (7 - i%8)
		}
	}

	// Despeckle: clear isolated set bits with no set neighbors
	for py := 0; py < h; py++ {
		for px := 0; px < w; px++ {
			if !getBitPacked(bits, px, py, w) {
				continue
			}
			hasNeighbor := false
			for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
				nx, ny := px+d[0], py+d[1]
				if nx >= 0 && nx < w && ny >= 0 && ny < h && getBitPacked(bits, nx, ny, w) {
					hasNeighbor = true
					break
				}
			}
			if !hasNeighbor {
				i := py*w + px
				bits[i/8] &^= 1 << (7 - i%8)
			}
		}
	}

	return bits
}

// findFirstSetBit finds the (x, y) of the first set bit in a packed bitmap (row-major).
func findFirstSetBit(bits []byte, width, height int) (int, int, bool) {
	total := width * height
	for i := 0; i < total; i++ {
		if (bits[i/8] & (1 << (7 - i%8))) != 0 {
			return i % width, i / width, true
		}
	}
	return 0, 0, false
}

// getBitPacked returns the bit value at (x, y) in a packed bitmap.
func getBitPacked(bits []byte, x, y, width int) bool {
	idx := y*width + x
	return (bits[idx/8] & (1 << (7 - idx%8))) != 0
}

// matchBits compares two packed bitmaps by aligning on their first set bits.
// This makes the comparison translation-invariant within the window.
func matchBits(a, b []byte, width, height int) float64 {
	if len(a) != len(b) {
		return 0
	}

	ax, ay, aFound := findFirstSetBit(a, width, height)
	bx, by, bFound := findFirstSetBit(b, width, height)

	if !aFound && !bFound {
		return 1.0
	}
	if !aFound || !bFound {
		return 0
	}

	dx := bx - ax
	dy := by - ay

	// Use Jaccard index (intersection over union) on set bits only.
	// Counting matching zeros inflates scores on dark backgrounds where
	// both template and window are mostly zero.
	intersection := 0
	union := 0

	for y := 0; y < height; y++ {
		by2 := y + dy
		if by2 < 0 || by2 >= height {
			continue
		}
		for x := 0; x < width; x++ {
			bx2 := x + dx
			if bx2 < 0 || bx2 >= width {
				continue
			}
			aBit := getBitPacked(a, x, y, width)
			bBit := getBitPacked(b, bx2, by2, width)
			if aBit || bBit {
				union++
				if aBit && bBit {
					intersection++
				}
			}
		}
	}

	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// DetectLogos searches for logo templates in an image using fast pre-quantized matching.
// Uses a Boyer-Moore inspired approach: quantize entire search area once, then scan.
// rotation specifies the image rotation in degrees (0, 90, 180, 270) based on orientation.
// Returns all matches with score >= minScore, sorted by score descending.
func (lib *LogoLibrary) DetectLogos(img image.Image, searchBounds geometry.RectInt, minScore float64, rotation int) []LogoMatch {
	if len(lib.Logos) == 0 {
		return nil
	}

	imgBounds := img.Bounds()
	if searchBounds.Width == 0 || searchBounds.Height == 0 {
		searchBounds = geometry.RectInt{
			X:      imgBounds.Min.X,
			Y:      imgBounds.Min.Y,
			Width:  imgBounds.Dx(),
			Height: imgBounds.Dy(),
		}
	}

	fmt.Printf("[Logo] DetectLogos: %d templates, search area %dx%d, rotation %d\n",
		len(lib.Logos), searchBounds.Width, searchBounds.Height, rotation)

	// Determine the common quantized size (use first template's size)
	qSize := 64
	if len(lib.Logos) > 0 && lib.Logos[0].QuantizedSize > 0 {
		qSize = lib.Logos[0].QuantizedSize
	}

	// Calculate quantized image dimensions
	// The quantized image should map 1:1 with template coordinates
	aspect := float64(searchBounds.Width) / float64(searchBounds.Height)
	var qWidth, qHeight int
	if aspect >= 1.0 {
		qWidth = int(float64(qSize) * aspect * 2) // 2x oversampling for sub-pixel accuracy
		qHeight = qSize * 2
	} else {
		qWidth = qSize * 2
		qHeight = int(float64(qSize) / aspect * 2)
	}

	// Pre-quantize the entire search area (with rotation applied)
	fmt.Printf("[Logo] Pre-quantizing search area to %dx%d (rotation %d)...\n", qWidth, qHeight, rotation)
	qi := newQuantizedImage(img, searchBounds, qWidth, qHeight, rotation)
	fmt.Printf("[Logo] Pre-quantization complete\n")

	// Scale factor from quantized image back to source image
	scaleToSource := float64(searchBounds.Width) / float64(qWidth)

	// Step size in quantized coordinates
	step := max(1, qSize/8)

	// Channel to collect matches from goroutines
	matchChan := make(chan []LogoMatch, len(lib.Logos))
	var wg sync.WaitGroup

	// Process each template in parallel
	for _, template := range lib.Logos {
		wg.Add(1)
		go func(tmpl *Logo) {
			defer wg.Done()
			var templateMatches []LogoMatch

			// Template dimensions in quantized image coordinates
			tmplW := tmpl.Width
			tmplH := tmpl.Height

			// Valid search range
			maxX := qWidth - tmplW
			maxY := qHeight - tmplH

			if maxX < 0 || maxY < 0 {
				fmt.Printf("[Logo] Template <%s>: too large for search area, skipping\n", tmpl.Name)
				matchChan <- nil
				return
			}

			positions := ((maxX / step) + 1) * ((maxY / step) + 1)
			fmt.Printf("[Logo] Template <%s>: scanning %dx%d, step %d, ~%d positions\n",
				tmpl.Name, tmplW, tmplH, step, positions)

			checked := 0

			// Scan the quantized image with fast bit matching
			for y := 0; y <= maxY; y += step {
				for x := 0; x <= maxX; x += step {
					checked++

					// Extract window and compare using fast XOR + popcount
					windowBits := qi.getWindow(x, y, tmplW, tmplH)
					if windowBits == nil {
						continue
					}

					score := matchBits(tmpl.Bits, windowBits, tmplW, tmplH)

					if score >= minScore {
						// Convert quantized position back to source coordinates
						scaleYToSource := float64(searchBounds.Height) / float64(qHeight)
						srcX := int(float64(x)*scaleToSource) + searchBounds.X
						srcY := int(float64(y)*scaleYToSource) + searchBounds.Y

						// Use template's original pixel dimensions for mask size.
						// The quantized-to-source conversion inflates bounds when
						// the search area is much larger than the template.
						srcW := tmpl.Bounds.Width
						srcH := tmpl.Bounds.Height
						if rotation == 90 || rotation == 270 {
							srcW, srcH = srcH, srcW
						}

						templateMatches = append(templateMatches, LogoMatch{
							Logo: tmpl,
							Bounds: geometry.RectInt{
								X:      srcX,
								Y:      srcY,
								Width:  srcW,
								Height: srcH,
							},
							Score:       score,
							Rotation:    rotation,
							ScaleFactor: scaleToSource * float64(tmplW) / float64(srcW),
						})
					}
				}
			}

			fmt.Printf("[Logo] Template <%s> done: %d checked, %d matches\n",
				tmpl.Name, checked, len(templateMatches))
			matchChan <- templateMatches
		}(template)
	}

	// Wait for all goroutines and close channel
	go func() {
		wg.Wait()
		close(matchChan)
	}()

	// Collect all matches
	var matches []LogoMatch
	for templateMatches := range matchChan {
		matches = append(matches, templateMatches...)
	}
	fmt.Printf("[Logo] All templates done, %d total matches\n", len(matches))

	// Sort by score descending
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Score > matches[j].Score
	})

	// Keep only the single best match per template name.
	// The shift-invariant matching in matchBits produces near-identical
	// scores at many positions; only the highest-scoring hit matters.
	bestByName := make(map[string]LogoMatch)
	for _, m := range matches {
		if existing, ok := bestByName[m.Logo.Name]; !ok || m.Score > existing.Score {
			bestByName[m.Logo.Name] = m
		}
	}

	filtered := make([]LogoMatch, 0, len(bestByName))
	for _, m := range bestByName {
		filtered = append(filtered, m)
	}

	// Sort by score descending
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Score > filtered[j].Score
	})

	return filtered
}

// extractRotatedRegion extracts a region from an image with optional rotation.
func extractRotatedRegion(img image.Image, bounds geometry.RectInt, rotation int) image.Image {
	// Simple extraction without rotation first
	region := image.NewRGBA(image.Rect(0, 0, bounds.Width, bounds.Height))
	imgBounds := img.Bounds()

	for y := 0; y < bounds.Height; y++ {
		srcY := bounds.Y + y
		if srcY < imgBounds.Min.Y || srcY >= imgBounds.Max.Y {
			continue
		}
		for x := 0; x < bounds.Width; x++ {
			srcX := bounds.X + x
			if srcX < imgBounds.Min.X || srcX >= imgBounds.Max.X {
				continue
			}
			region.Set(x, y, img.At(srcX, srcY))
		}
	}

	// Apply rotation
	switch rotation {
	case 90:
		rotated := image.NewRGBA(image.Rect(0, 0, bounds.Height, bounds.Width))
		for y := 0; y < bounds.Height; y++ {
			for x := 0; x < bounds.Width; x++ {
				rotated.Set(bounds.Height-1-y, x, region.At(x, y))
			}
		}
		return rotated
	case 180:
		rotated := image.NewRGBA(image.Rect(0, 0, bounds.Width, bounds.Height))
		for y := 0; y < bounds.Height; y++ {
			for x := 0; x < bounds.Width; x++ {
				rotated.Set(bounds.Width-1-x, bounds.Height-1-y, region.At(x, y))
			}
		}
		return rotated
	case 270:
		rotated := image.NewRGBA(image.Rect(0, 0, bounds.Height, bounds.Width))
		for y := 0; y < bounds.Height; y++ {
			for x := 0; x < bounds.Width; x++ {
				rotated.Set(y, bounds.Width-1-x, region.At(x, y))
			}
		}
		return rotated
	default:
		return region
	}
}

// boundsOverlap checks if two rectangles overlap by more than the given fraction.
func boundsOverlap(a, b geometry.RectInt, threshold float64) bool {
	// Calculate intersection
	left := max(a.X, b.X)
	right := min(a.X+a.Width, b.X+b.Width)
	top := max(a.Y, b.Y)
	bottom := min(a.Y+a.Height, b.Y+b.Height)

	if left >= right || top >= bottom {
		return false // No overlap
	}

	intersectArea := float64((right - left) * (bottom - top))
	areaA := float64(a.Width * a.Height)
	areaB := float64(b.Width * b.Height)
	minArea := min(areaA, areaB)

	return intersectArea/minArea >= threshold
}

// GetPreferencesPath returns the path to the logo library preferences file.
// Creates the config directory if it doesn't exist.
func GetPreferencesPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		// Fallback to home directory
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine config directory: %w", err)
		}
		configDir = filepath.Join(home, ".config")
	}

	appDir := filepath.Join(configDir, "pcb-tracer")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return "", fmt.Errorf("cannot create config directory: %w", err)
	}

	return filepath.Join(appDir, "logos.json"), nil
}

// SaveToPreferences saves the logo library to the preferences file.
func (lib *LogoLibrary) SaveToPreferences() error {
	path, err := GetPreferencesPath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(lib, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot serialize logo library: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("cannot write logo library: %w", err)
	}

	fmt.Printf("Saved %d logos to %s\n", len(lib.Logos), path)
	return nil
}

// LoadFromPreferences loads the logo library from the preferences file.
// Returns an empty library if the file doesn't exist.
func LoadFromPreferences() (*LogoLibrary, error) {
	path, err := GetPreferencesPath()
	if err != nil {
		return NewLogoLibrary(), err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No preferences file yet, return empty library
			return NewLogoLibrary(), nil
		}
		return NewLogoLibrary(), fmt.Errorf("cannot read logo library: %w", err)
	}

	var lib LogoLibrary
	if err := json.Unmarshal(data, &lib); err != nil {
		return NewLogoLibrary(), fmt.Errorf("cannot parse logo library: %w", err)
	}

	// Sort by name for consistent display
	lib.Sort()

	fmt.Printf("Loaded %d logos from %s\n", len(lib.Logos), path)
	return &lib, nil
}
