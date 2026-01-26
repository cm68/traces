package board

// Intel Multibus I Specifications
// Multibus was developed by Intel in 1974 for industrial and embedded systems.

// MultibusP1Spec returns the Multibus I with P1 connector only.
// The P1 connector has 86 pins.
func MultibusP1Spec() *BaseSpec {
	return &BaseSpec{
		SpecName:     "Multibus I (P1)",
		WidthInches:  12.0,
		HeightInches: 6.75,
		Contacts: &ContactSpec{
			Edge:         EdgeBottom,
			Count:        43, // 43 pins per side (86 total)
			PitchInches:  0.1,
			WidthInches:  0.05,
			MarginInches: 0.9,
		},
		AlignMethods: []AlignmentMethod{
			AlignByContacts,
			AlignByCorners,
		},
	}
}

// MultibusP1P2Spec returns the Multibus I with both P1 and P2 connectors.
// P1 has 86 pins, P2 has 60 pins.
func MultibusP1P2Spec() *BaseSpec {
	return &BaseSpec{
		SpecName:     "Multibus I (P1+P2)",
		WidthInches:  12.0,
		HeightInches: 6.75,
		Contacts: &ContactSpec{
			Edge:         EdgeBottom,
			Count:        73, // 43 + 30 pins per side (146 total)
			PitchInches:  0.1,
			WidthInches:  0.05,
			MarginInches: 0.9,
		},
		AlignMethods: []AlignmentMethod{
			AlignByContacts,
			AlignByCorners,
		},
	}
}
