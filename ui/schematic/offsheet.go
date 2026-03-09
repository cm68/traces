package schematic

// generateOffSheetConnectors creates off-sheet connector indicators for nets
// that span multiple sheets. For each such net, an indicator is placed on each
// sheet near the relevant pin, showing which other sheet the net continues to.
func generateOffSheetConnectors(doc *SchematicDoc) {
	if doc == nil {
		return
	}
	doc.OffSheetConnectors = nil

	// Build net → set of sheets
	type netSheet struct {
		sheets map[int]bool
		name   string
	}
	netSheets := make(map[string]*netSheet) // netID → sheets

	for _, sym := range doc.Symbols {
		sheet := effectiveSheet(sym.Sheet)
		for _, pin := range sym.Pins {
			if pin.NetID == "" {
				continue
			}
			// Skip power nets
			if doc.PowerNetIDs[pin.NetID] {
				continue
			}
			ns := netSheets[pin.NetID]
			if ns == nil {
				ns = &netSheet{sheets: make(map[int]bool), name: pin.NetName}
				netSheets[pin.NetID] = ns
			}
			ns.sheets[sheet] = true
			if pin.NetName != "" {
				ns.name = pin.NetName
			}
		}
	}

	// For each net spanning multiple sheets, create connectors
	for netID, ns := range netSheets {
		if len(ns.sheets) < 2 {
			continue
		}

		// For each sheet this net appears on, create connectors pointing to other sheets
		for sheet := range ns.sheets {
			// Find a pin on this sheet to position the connector near
			var pinX, pinY float64
			var pinDir string
			found := false
			for _, sym := range doc.Symbols {
				if effectiveSheet(sym.Sheet) != sheet {
					continue
				}
				for _, pin := range sym.Pins {
					if pin.NetID == netID {
						pinX = pin.X
						pinY = pin.Y
						pinDir = pin.Direction
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				continue
			}

			// Create one connector per target sheet
			for targetSheet := range ns.sheets {
				if targetSheet == sheet {
					continue
				}
				dir := "output"
				offX := pinX + 60
				if pinDir == "input" || pinDir == "clock" || pinDir == "enable" {
					dir = "input"
					offX = pinX - 140
					if offX < startX/2 {
						offX = startX / 2
					}
				}

				doc.OffSheetConnectors = append(doc.OffSheetConnectors, &OffSheetConnector{
					NetID:       netID,
					NetName:     ns.name,
					Sheet:       sheet,
					TargetSheet: targetSheet,
					X:           offX,
					Y:           max(pinY-10, startY/2),
					Direction:   dir,
				})
			}
		}
	}
}
