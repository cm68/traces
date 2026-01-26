package via

// DefaultParams returns default via detection parameters.
// These are tuned for bright metallic vias on typical PCB scans.
func DefaultParams() DetectionParams {
	return DetectionParams{
		// HSV ranges for metallic/bright surfaces
		// Low saturation (metallic), high value (bright)
		HueMin: 0,
		HueMax: 180, // All hues - metallic surfaces vary
		SatMin: 0,
		SatMax: 100, // Low saturation for metallic appearance
		ValMin: 180,
		ValMax: 255, // High brightness

		// Typical via sizes
		MinDiamInches: 0.010, // 10 mil via
		MaxDiamInches: 0.050, // 50 mil via

		// Shape constraint - vias should be fairly circular
		CircularityMin: 0.65,

		// Hough circle detection tuning
		HoughDP:       1.2, // Slightly lower resolution for speed
		HoughMinDist:  20,  // Will be recalculated based on DPI
		HoughParam1:   80,  // Canny high threshold
		HoughParam2:   30,  // Accumulator threshold (lower = more sensitive)
	}
}

// WithDPI returns a copy of params with pixel sizes calculated from DPI.
func (p DetectionParams) WithDPI(dpi float64) DetectionParams {
	p.DPI = dpi
	if dpi > 0 {
		// Calculate pixel sizes from physical dimensions
		p.MinRadiusPixels = int(p.MinDiamInches * dpi / 2)
		p.MaxRadiusPixels = int(p.MaxDiamInches * dpi / 2)

		// Ensure minimum values
		if p.MinRadiusPixels < 3 {
			p.MinRadiusPixels = 3
		}
		if p.MaxRadiusPixels < p.MinRadiusPixels {
			p.MaxRadiusPixels = p.MinRadiusPixels * 2
		}

		// Minimum distance between vias scales with minimum size
		p.HoughMinDist = max(10, p.MinRadiusPixels*2)
	}
	return p
}

// WithHSV returns a copy of params with custom HSV color ranges.
// Useful when user has sampled via colors from the image.
func (p DetectionParams) WithHSV(hMin, hMax, sMin, sMax, vMin, vMax float64) DetectionParams {
	p.HueMin = hMin
	p.HueMax = hMax
	p.SatMin = sMin
	p.SatMax = sMax
	p.ValMin = vMin
	p.ValMax = vMax
	return p
}

// WithSizeRange returns a copy of params with custom via size range in inches.
func (p DetectionParams) WithSizeRange(minDiamInches, maxDiamInches float64) DetectionParams {
	p.MinDiamInches = minDiamInches
	p.MaxDiamInches = maxDiamInches
	// Recalculate pixel sizes if DPI is set
	if p.DPI > 0 {
		return p.WithDPI(p.DPI)
	}
	return p
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
