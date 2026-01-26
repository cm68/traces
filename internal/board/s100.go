package board

// S-100 (IEEE 696) Bus Specification
// The S-100 bus was the first industry-standard bus for microcomputers.
//
// Physical Characteristics:
// - Board size: 10" x 5.4375" (5 7/16")
// - 100-pin edge connector (50 pins per side)
// - Contact pitch: 0.125" (1/8")
// - Contacts at top edge of board
// - Two ejector holes near top corners

const (
	// S100 board dimensions in inches
	S100WidthInches  = 10.0
	S100HeightInches = 5.4375 // 5 7/16"

	// S100 contact specifications
	S100ContactCount  = 50     // per side (100 total, 50 front + 50 back)
	S100ContactPitch  = 0.125  // 1/8" center-to-center
	S100ContactWidth  = 0.0625 // 1/16" individual contact width
	S100ContactHeight = 0.3    // max contact length per S-100 spec
	S100ContactMargin = 2.125  // from left edge to first contact center

	// S100 ejector hole specifications
	S100HoleDiameter = 0.105 // ejector hole diameter
	S100HoleInsetX   = 0.25  // inset from board edge (X)
	S100HoleInsetY   = 0.25  // inset from top edge (Y)
)

// S100GoldColor returns the HSV color range for gold edge contacts.
// Gold contacts typically appear in the yellow-orange range.
func S100GoldColor() HSVRange {
	return HSVRange{
		HueMin: 15,  // Orange-yellow start
		HueMax: 35,  // Yellow end
		SatMin: 80,  // Moderate saturation
		SatMax: 255,
		ValMin: 120, // Reasonably bright
		ValMax: 255,
	}
}

// S100ContactDetection returns the detection parameters for S-100 contacts.
func S100ContactDetection() *ContactDetectionParams {
	// Contact physical size: 0.0625" x 0.3" = aspect ratio ~4.8:1
	// At 600 DPI: ~37.5 x 180 pixels = ~6750 pixels area
	return &ContactDetectionParams{
		Color:          S100GoldColor(),
		AspectRatioMin: 3.0,  // Allow some tolerance (~4.8:1 nominal)
		AspectRatioMax: 7.0,
		MinAreaPixels:  2000, // Minimum at 600 DPI
		MaxAreaPixels:  15000, // Maximum at 600 DPI
	}
}

// S100Spec returns the fully specified S-100 board definition.
func S100Spec() *BaseSpec {
	return &BaseSpec{
		SpecName:     "S-100 (IEEE 696)",
		WidthInches:  S100WidthInches,
		HeightInches: S100HeightInches,
		Contacts: &ContactSpec{
			Edge:         EdgeTop,
			Count:        S100ContactCount,
			PitchInches:  S100ContactPitch,
			WidthInches:  S100ContactWidth,
			HeightInches: S100ContactHeight,
			MarginInches: S100ContactMargin,
			Detection:    S100ContactDetection(),
		},
		MountHoles: []HoleSpec{
			{
				Name:       "top_left_ejector",
				XInches:    S100HoleInsetX,
				YInches:    S100HoleInsetY,
				DiamInches: S100HoleDiameter,
			},
			{
				Name:       "top_right_ejector",
				XInches:    S100WidthInches - S100HoleInsetX,
				YInches:    S100HoleInsetY,
				DiamInches: S100HoleDiameter,
			},
		},
		AlignMethods: []AlignmentMethod{
			AlignByContacts,
			AlignByHoles,
			AlignByCorners,
		},
	}
}

// S100ContactPositions returns the expected X positions of contacts in inches
// from the left edge of the board (for the top edge contacts).
func S100ContactPositions() []float64 {
	positions := make([]float64, S100ContactCount)
	for i := 0; i < S100ContactCount; i++ {
		positions[i] = S100ContactMargin + float64(i)*S100ContactPitch
	}
	return positions
}

// S100ContactBounds returns the bounding rectangle for all contacts.
// Returns (left, top, right, bottom) in inches from board origin.
func S100ContactBounds() (left, top, right, bottom float64) {
	left = S100ContactMargin - S100ContactWidth/2
	right = S100ContactMargin + float64(S100ContactCount-1)*S100ContactPitch + S100ContactWidth/2
	top = 0 // contacts are at the very top edge
	bottom = S100ContactHeight
	return
}

// S100PinName returns the standard pin name for a contact.
// Pin numbering: 1-50 on component side (front), 51-100 on solder side (back).
func S100PinName(index int, front bool) string {
	if front {
		return string(rune('A' + (index / 26))) + string(rune('0' + (index % 10)))
	}
	return string(rune('A' + ((index + 50) / 26))) + string(rune('0' + ((index + 50) % 10)))
}

// S100SignalNames returns the standard signal names for S-100 pins.
// Based on IEEE 696 standard.
func S100SignalNames() map[int]string {
	return map[int]string{
		// Component side (1-50)
		1:  "+8V",
		2:  "+16V",
		3:  "XRDY",
		4:  "VI0*",
		5:  "VI1*",
		6:  "VI2*",
		7:  "VI3*",
		8:  "VI4*",
		9:  "VI5*",
		10: "VI6*",
		11: "VI7*",
		12: "NMI*",
		13: "PWRFAIL*",
		14: "DMA3*",
		15: "A18",
		16: "A16",
		17: "A17",
		18: "SDSB*",
		19: "CDSB*",
		20: "GND",
		21: "NDEF",
		22: "ADSB*",
		23: "DODSB*",
		24: "+8V",
		25: "+8V",
		26: "pHLDA",
		27: "pSTVAL*",
		28: "pDBIN",
		29: "pWR*",
		30: "GND",
		31: "GND",
		32: "POC*",
		33: "pWAIT*",
		34: "pINTA",
		35: "pM1",
		36: "pOUT",
		37: "pINP",
		38: "pMEMR",
		39: "pHLTA",
		40: "CLOCK",
		41: "GND",
		42: "A14",
		43: "A15",
		44: "A12",
		45: "A13",
		46: "A10",
		47: "A11",
		48: "A8",
		49: "A9",
		50: "+8V",
		// Solder side (51-100)
		51: "+8V",
		52: "-16V",
		53: "GND",
		54: "SLAVE CLR*",
		55: "DMA0*",
		56: "DMA1*",
		57: "DMA2*",
		58: "sXTRQ*",
		59: "A19",
		60: "SIXTN*",
		61: "A20",
		62: "A21",
		63: "A22",
		64: "A23",
		65: "NDEF",
		66: "NDEF",
		67: "PHANTOM*",
		68: "MWRT*",
		69: "REFRESH*",
		70: "GND",
		71: "NDEF",
		72: "RDY",
		73: "INT*",
		74: "HOLD*",
		75: "RESET*",
		76: "pSYNC",
		77: "pWR*",
		78: "pDBIN",
		79: "A0",
		80: "GND",
		81: "A1",
		82: "A2",
		83: "A3",
		84: "A4",
		85: "A5",
		86: "A6",
		87: "A7",
		88: "DI4",
		89: "DI5",
		90: "DI6",
		91: "DI7/DATA 7",
		92: "DI0/DATA 0",
		93: "DI1/DATA 1",
		94: "DI2/DATA 2",
		95: "DI3/DATA 3",
		96: "DO0",
		97: "DO1",
		98: "DO2",
		99: "DO3",
		100: "+8V",
	}
}
