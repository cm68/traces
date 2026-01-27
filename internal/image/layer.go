// Package image provides image loading, layer management, and compositing.
package image

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"pcb-tracer/pkg/geometry"

	_ "golang.org/x/image/tiff"
)

// Side indicates which side of the board an image represents.
type Side int

const (
	SideUnknown Side = iota
	SideFront        // Component side
	SideBack         // Solder side
)

func (s Side) String() string {
	switch s {
	case SideFront:
		return "Front (Component)"
	case SideBack:
		return "Back (Solder)"
	default:
		return "Unknown"
	}
}

// Layer represents a single image layer in the project.
type Layer struct {
	Path    string         // Original file path
	Image   image.Image    // Loaded image data
	Side    Side           // Front or back
	DPI     float64        // Detected or user-specified DPI
	Visible bool           // Layer visibility
	Opacity float64        // Layer opacity (0.0 - 1.0)
	Bounds  geometry.Rect  // Board bounds within image (if detected)

	// Manual alignment offset (pixels, applied during rendering)
	ManualOffsetX int
	ManualOffsetY int

	// Manual rotation (degrees, positive = clockwise)
	ManualRotation float64

	// Shear/scale factors (1.0 = no change)
	// Different values at top vs bottom create horizontal shear
	// Different values at left vs right create vertical shear
	ShearTopX    float64 // X scale at top edge
	ShearBottomX float64 // X scale at bottom edge
	ShearLeftY   float64 // Y scale at left edge
	ShearRightY  float64 // Y scale at right edge
}

// NewLayer creates a new Layer with default settings.
func NewLayer() *Layer {
	return &Layer{
		Visible:      true,
		Opacity:      1.0,
		ShearTopX:    1.0,
		ShearBottomX: 1.0,
		ShearLeftY:   1.0,
		ShearRightY:  1.0,
	}
}

// Load loads an image from the specified path and returns a Layer.
func Load(path string) (*Layer, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open image: %w", err)
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}

	layer := NewLayer()
	layer.Path = path
	layer.Image = img

	// Try to extract DPI from TIFF metadata
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".tiff" || ext == ".tif" {
		if dpi, err := extractTIFFDPI(path); err == nil {
			layer.DPI = dpi
		}
	}

	// Guess side from filename
	layer.Side = guessSideFromFilename(path)

	return layer, nil
}

// Width returns the image width in pixels.
func (l *Layer) Width() int {
	if l.Image == nil {
		return 0
	}
	return l.Image.Bounds().Dx()
}

// Height returns the image height in pixels.
func (l *Layer) Height() int {
	if l.Image == nil {
		return 0
	}
	return l.Image.Bounds().Dy()
}

// Size returns the image dimensions.
func (l *Layer) Size() geometry.Size {
	return geometry.Size{
		Width:  float64(l.Width()),
		Height: float64(l.Height()),
	}
}

// WidthInches returns the image width in inches if DPI is known.
func (l *Layer) WidthInches() float64 {
	if l.DPI == 0 {
		return 0
	}
	return float64(l.Width()) / l.DPI
}

// HeightInches returns the image height in inches if DPI is known.
func (l *Layer) HeightInches() float64 {
	if l.DPI == 0 {
		return 0
	}
	return float64(l.Height()) / l.DPI
}

// PixelAt returns the color at the specified pixel coordinates.
func (l *Layer) PixelAt(x, y int) color.Color {
	if l.Image == nil {
		return color.Black
	}
	bounds := l.Image.Bounds()
	if x < bounds.Min.X || x >= bounds.Max.X || y < bounds.Min.Y || y >= bounds.Max.Y {
		return color.Black
	}
	return l.Image.At(x, y)
}

// guessSideFromFilename attempts to determine the board side from the filename.
func guessSideFromFilename(path string) Side {
	base := strings.ToLower(filepath.Base(path))

	frontKeywords := []string{"front", "component", "top", "comp"}
	for _, kw := range frontKeywords {
		if strings.Contains(base, kw) {
			return SideFront
		}
	}

	backKeywords := []string{"back", "solder", "bottom", "bot"}
	for _, kw := range backKeywords {
		if strings.Contains(base, kw) {
			return SideBack
		}
	}

	return SideUnknown
}

// extractTIFFDPI attempts to extract DPI from TIFF metadata.
func extractTIFFDPI(path string) (float64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	// Read TIFF header to determine byte order
	header := make([]byte, 8)
	if _, err := file.Read(header); err != nil {
		return 0, err
	}

	var byteOrder binary.ByteOrder
	if header[0] == 'I' && header[1] == 'I' {
		byteOrder = binary.LittleEndian
	} else if header[0] == 'M' && header[1] == 'M' {
		byteOrder = binary.BigEndian
	} else {
		return 0, fmt.Errorf("not a valid TIFF file")
	}

	// Get offset to first IFD
	ifdOffset := byteOrder.Uint32(header[4:8])

	// Seek to IFD
	if _, err := file.Seek(int64(ifdOffset), 0); err != nil {
		return 0, err
	}

	// Read number of directory entries
	var numEntries uint16
	if err := binary.Read(file, byteOrder, &numEntries); err != nil {
		return 0, err
	}

	var xRes, yRes float64
	var resUnit uint16 = 2 // Default to inches

	// Read directory entries
	for i := uint16(0); i < numEntries; i++ {
		entry := make([]byte, 12)
		if _, err := file.Read(entry); err != nil {
			return 0, err
		}

		tag := byteOrder.Uint16(entry[0:2])
		fieldType := byteOrder.Uint16(entry[2:4])
		valueOffset := byteOrder.Uint32(entry[8:12])

		switch tag {
		case 282: // XResolution
			if fieldType == 5 { // RATIONAL
				xRes = readTIFFRational(file, int64(valueOffset), byteOrder)
			}
		case 283: // YResolution
			if fieldType == 5 { // RATIONAL
				yRes = readTIFFRational(file, int64(valueOffset), byteOrder)
			}
		case 296: // ResolutionUnit
			if fieldType == 3 { // SHORT
				resUnit = uint16(valueOffset)
			}
		}
	}

	if xRes == 0 && yRes == 0 {
		return 0, fmt.Errorf("no resolution tags found")
	}

	// Use X resolution (or Y if X is 0)
	dpi := xRes
	if dpi == 0 {
		dpi = yRes
	}

	// Convert from centimeters to inches if needed
	if resUnit == 3 {
		dpi *= 2.54
	}

	if dpi == 0 {
		return 0, fmt.Errorf("DPI is zero")
	}

	return dpi, nil
}

// readTIFFRational reads a RATIONAL value (two uint32s) from a TIFF file.
func readTIFFRational(file *os.File, offset int64, byteOrder binary.ByteOrder) float64 {
	currentPos, _ := file.Seek(0, 1) // Save current position
	defer file.Seek(currentPos, 0)   // Restore position

	file.Seek(offset, 0)
	var num, denom uint32
	binary.Read(file, byteOrder, &num)
	binary.Read(file, byteOrder, &denom)

	if denom == 0 {
		return 0
	}
	return float64(num) / float64(denom)
}

// SupportedFormats returns the list of supported image formats.
func SupportedFormats() []string {
	return []string{".tiff", ".tif", ".png", ".jpg", ".jpeg"}
}

// IsSupportedFormat checks if the given path has a supported image format.
func IsSupportedFormat(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, format := range SupportedFormats() {
		if ext == format {
			return true
		}
	}
	return false
}

// FileFilter returns a file filter string for use in file dialogs.
func FileFilter() string {
	return "Image Files (*.tiff, *.tif, *.png, *.jpg, *.jpeg)"
}
