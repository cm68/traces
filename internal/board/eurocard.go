package board

// Europe Card Bus (ECB) Specifications
// ECB uses the DIN 41612 connector standard with Eurocard form factors.

// ECBSpec returns the ECB (Europe Card Bus) specification.
// Uses DIN 41612 64-pin connector (a+c rows).
func ECBSpec() *BaseSpec {
	return &BaseSpec{
		SpecName:     "ECB (Europe Card Bus)",
		WidthInches:  6.3,   // 160mm Eurocard
		HeightInches: 3.94,  // 100mm Eurocard
		Contacts: &ContactSpec{
			Edge:         EdgeBottom,
			Count:        32, // 32 pins per side (64 total, a+c rows)
			PitchInches:  0.1, // 2.54mm = 0.1"
			WidthInches:  0.05,
			MarginInches: 0.6,
		},
		AlignMethods: []AlignmentMethod{
			AlignByContacts,
			AlignByCorners,
		},
	}
}

// STDBusSpec returns the STD Bus specification.
// STD Bus is a simple 8-bit bus for industrial control.
func STDBusSpec() *BaseSpec {
	return &BaseSpec{
		SpecName:     "STD Bus",
		WidthInches:  6.5,
		HeightInches: 4.5,
		Contacts: &ContactSpec{
			Edge:         EdgeBottom,
			Count:        28, // 28 pins per side (56 total)
			PitchInches:  0.1,
			WidthInches:  0.05,
			MarginInches: 0.7,
		},
		AlignMethods: []AlignmentMethod{
			AlignByContacts,
			AlignByCorners,
		},
	}
}
