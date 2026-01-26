package board

// ISA (Industry Standard Architecture) Bus Specifications
// Originally from the IBM PC (8-bit) and PC/AT (16-bit).

// ISA8Spec returns the 8-bit ISA board specification.
// The 8-bit ISA bus has a 62-pin edge connector.
func ISA8Spec() *BaseSpec {
	return &BaseSpec{
		SpecName:     "8-bit ISA",
		WidthInches:  13.15,  // Standard full-length card
		HeightInches: 4.2,
		Contacts: &ContactSpec{
			Edge:         EdgeBottom,
			Count:        31, // 31 pins per side (62 total)
			PitchInches:  0.1,
			WidthInches:  0.05,
			MarginInches: 0.8,
		},
		AlignMethods: []AlignmentMethod{
			AlignByContacts,
			AlignByCorners,
		},
	}
}

// ISA16Spec returns the 16-bit ISA board specification.
// The 16-bit ISA bus extends the 8-bit with an additional 36-pin connector.
func ISA16Spec() *BaseSpec {
	return &BaseSpec{
		SpecName:     "16-bit ISA",
		WidthInches:  13.15,
		HeightInches: 4.2,
		Contacts: &ContactSpec{
			Edge:         EdgeBottom,
			Count:        49, // 31 + 18 pins per side (98 total)
			PitchInches:  0.1,
			WidthInches:  0.05,
			MarginInches: 0.8,
		},
		AlignMethods: []AlignmentMethod{
			AlignByContacts,
			AlignByCorners,
		},
	}
}
