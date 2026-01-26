package connector

// S100Definition returns the standard S-100 IEEE 696 pin definitions.
// Based on IEEE 696 standard with direction and logic sense information.
func S100Definition() *BoardDefinition {
	bd := NewBoardDefinition("S-100 (IEEE 696)", 100)

	// Component side (front): pins 1-50
	// Pin 1 is at the left edge when viewing the component side
	addS100FrontPins(bd)

	// Solder side (back): pins 51-100
	// Pin 51 is at the right edge when viewing the solder side (mirrors front)
	addS100BackPins(bd)

	return bd
}

func addS100FrontPins(bd *BoardDefinition) {
	// Power
	bd.AddPin(&PinDefinition{PinNumber: 1, SignalName: "+8V", Description: "+8V Power", ConnectorIndex: 0, Side: "front", Direction: DirectionPower})
	bd.AddPin(&PinDefinition{PinNumber: 2, SignalName: "+16V", Description: "+16V Power", ConnectorIndex: 1, Side: "front", Direction: DirectionPower})

	// Control signals
	bd.AddPin(&PinDefinition{PinNumber: 3, SignalName: "XRDY", Description: "Extended Ready", ConnectorIndex: 2, Side: "front", Direction: DirectionInput, LogicSense: LogicActiveHigh})

	// Vectored interrupts
	bd.AddPin(&PinDefinition{PinNumber: 4, SignalName: "VI0*", Description: "Vectored Interrupt 0", ConnectorIndex: 3, Side: "front", Direction: DirectionInput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 5, SignalName: "VI1*", Description: "Vectored Interrupt 1", ConnectorIndex: 4, Side: "front", Direction: DirectionInput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 6, SignalName: "VI2*", Description: "Vectored Interrupt 2", ConnectorIndex: 5, Side: "front", Direction: DirectionInput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 7, SignalName: "VI3*", Description: "Vectored Interrupt 3", ConnectorIndex: 6, Side: "front", Direction: DirectionInput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 8, SignalName: "VI4*", Description: "Vectored Interrupt 4", ConnectorIndex: 7, Side: "front", Direction: DirectionInput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 9, SignalName: "VI5*", Description: "Vectored Interrupt 5", ConnectorIndex: 8, Side: "front", Direction: DirectionInput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 10, SignalName: "VI6*", Description: "Vectored Interrupt 6", ConnectorIndex: 9, Side: "front", Direction: DirectionInput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 11, SignalName: "VI7*", Description: "Vectored Interrupt 7", ConnectorIndex: 10, Side: "front", Direction: DirectionInput, LogicSense: LogicActiveLow})

	// System control
	bd.AddPin(&PinDefinition{PinNumber: 12, SignalName: "NMI*", Description: "Non-Maskable Interrupt", ConnectorIndex: 11, Side: "front", Direction: DirectionInput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 13, SignalName: "PWRFAIL*", Description: "Power Fail", ConnectorIndex: 12, Side: "front", Direction: DirectionInput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 14, SignalName: "DMA3*", Description: "DMA Request 3", ConnectorIndex: 13, Side: "front", Direction: DirectionInput, LogicSense: LogicActiveLow})

	// Extended address
	bd.AddPin(&PinDefinition{PinNumber: 15, SignalName: "A18", Description: "Address bit 18", ConnectorIndex: 14, Side: "front", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 18})
	bd.AddPin(&PinDefinition{PinNumber: 16, SignalName: "A16", Description: "Address bit 16", ConnectorIndex: 15, Side: "front", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 16})
	bd.AddPin(&PinDefinition{PinNumber: 17, SignalName: "A17", Description: "Address bit 17", ConnectorIndex: 16, Side: "front", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 17})

	// Bus control
	bd.AddPin(&PinDefinition{PinNumber: 18, SignalName: "SDSB*", Description: "Status Disable", ConnectorIndex: 17, Side: "front", Direction: DirectionOutput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 19, SignalName: "CDSB*", Description: "Control Disable", ConnectorIndex: 18, Side: "front", Direction: DirectionOutput, LogicSense: LogicActiveLow})

	// Ground
	bd.AddPin(&PinDefinition{PinNumber: 20, SignalName: "GND", Description: "Ground", ConnectorIndex: 19, Side: "front", Direction: DirectionGround})

	// Undefined
	bd.AddPin(&PinDefinition{PinNumber: 21, SignalName: "NDEF", Description: "Not Defined", ConnectorIndex: 20, Side: "front", Direction: DirectionBidirectional})

	// Bus control continued
	bd.AddPin(&PinDefinition{PinNumber: 22, SignalName: "ADSB*", Description: "Address Disable", ConnectorIndex: 21, Side: "front", Direction: DirectionOutput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 23, SignalName: "DODSB*", Description: "Data Out Disable", ConnectorIndex: 22, Side: "front", Direction: DirectionOutput, LogicSense: LogicActiveLow})

	// Clock and status
	bd.AddPin(&PinDefinition{PinNumber: 24, SignalName: "PHI", Description: "System Clock", ConnectorIndex: 23, Side: "front", Direction: DirectionOutput, LogicSense: LogicRisingEdge})
	bd.AddPin(&PinDefinition{PinNumber: 25, SignalName: "pSTVAL*", Description: "Status Valid", ConnectorIndex: 24, Side: "front", Direction: DirectionOutput, LogicSense: LogicActiveLow})

	// Processor status
	bd.AddPin(&PinDefinition{PinNumber: 26, SignalName: "pHLDA", Description: "Hold Acknowledge", ConnectorIndex: 25, Side: "front", Direction: DirectionOutput})

	// Reserved for future use
	bd.AddPin(&PinDefinition{PinNumber: 27, SignalName: "RFU", Description: "Reserved for Future Use", ConnectorIndex: 26, Side: "front", Direction: DirectionBidirectional})
	bd.AddPin(&PinDefinition{PinNumber: 28, SignalName: "RFU", Description: "Reserved for Future Use", ConnectorIndex: 27, Side: "front", Direction: DirectionBidirectional})

	// Address bus
	bd.AddPin(&PinDefinition{PinNumber: 29, SignalName: "A5", Description: "Address bit 5", ConnectorIndex: 28, Side: "front", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 5})
	bd.AddPin(&PinDefinition{PinNumber: 30, SignalName: "A4", Description: "Address bit 4", ConnectorIndex: 29, Side: "front", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 4})
	bd.AddPin(&PinDefinition{PinNumber: 31, SignalName: "A3", Description: "Address bit 3", ConnectorIndex: 30, Side: "front", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 3})
	bd.AddPin(&PinDefinition{PinNumber: 32, SignalName: "A15", Description: "Address bit 15", ConnectorIndex: 31, Side: "front", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 15})
	bd.AddPin(&PinDefinition{PinNumber: 33, SignalName: "A12", Description: "Address bit 12", ConnectorIndex: 32, Side: "front", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 12})
	bd.AddPin(&PinDefinition{PinNumber: 34, SignalName: "A9", Description: "Address bit 9", ConnectorIndex: 33, Side: "front", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 9})

	// Data bus out
	bd.AddPin(&PinDefinition{PinNumber: 35, SignalName: "DO1", Description: "Data Out bit 1", ConnectorIndex: 34, Side: "front", Direction: DirectionOutput, BusGroup: "data", BusBitIndex: 1})
	bd.AddPin(&PinDefinition{PinNumber: 36, SignalName: "DO0", Description: "Data Out bit 0", ConnectorIndex: 35, Side: "front", Direction: DirectionOutput, BusGroup: "data", BusBitIndex: 0})

	// Address bus continued
	bd.AddPin(&PinDefinition{PinNumber: 37, SignalName: "A10", Description: "Address bit 10", ConnectorIndex: 36, Side: "front", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 10})

	// Data bus out continued
	bd.AddPin(&PinDefinition{PinNumber: 38, SignalName: "DO4", Description: "Data Out bit 4", ConnectorIndex: 37, Side: "front", Direction: DirectionOutput, BusGroup: "data", BusBitIndex: 4})
	bd.AddPin(&PinDefinition{PinNumber: 39, SignalName: "DO5", Description: "Data Out bit 5", ConnectorIndex: 38, Side: "front", Direction: DirectionOutput, BusGroup: "data", BusBitIndex: 5})
	bd.AddPin(&PinDefinition{PinNumber: 40, SignalName: "DO6", Description: "Data Out bit 6", ConnectorIndex: 39, Side: "front", Direction: DirectionOutput, BusGroup: "data", BusBitIndex: 6})

	// Data bus in
	bd.AddPin(&PinDefinition{PinNumber: 41, SignalName: "DI2", Description: "Data In bit 2", ConnectorIndex: 40, Side: "front", Direction: DirectionInput, BusGroup: "data", BusBitIndex: 2})
	bd.AddPin(&PinDefinition{PinNumber: 42, SignalName: "DI3", Description: "Data In bit 3", ConnectorIndex: 41, Side: "front", Direction: DirectionInput, BusGroup: "data", BusBitIndex: 3})
	bd.AddPin(&PinDefinition{PinNumber: 43, SignalName: "DI7", Description: "Data In bit 7", ConnectorIndex: 42, Side: "front", Direction: DirectionInput, BusGroup: "data", BusBitIndex: 7})

	// Status signals
	bd.AddPin(&PinDefinition{PinNumber: 44, SignalName: "sM1", Description: "Machine Cycle 1", ConnectorIndex: 43, Side: "front", Direction: DirectionOutput})
	bd.AddPin(&PinDefinition{PinNumber: 45, SignalName: "sOUT", Description: "Output Cycle", ConnectorIndex: 44, Side: "front", Direction: DirectionOutput})
	bd.AddPin(&PinDefinition{PinNumber: 46, SignalName: "sINP", Description: "Input Cycle", ConnectorIndex: 45, Side: "front", Direction: DirectionOutput})
	bd.AddPin(&PinDefinition{PinNumber: 47, SignalName: "sMEMR", Description: "Memory Read", ConnectorIndex: 46, Side: "front", Direction: DirectionOutput})
	bd.AddPin(&PinDefinition{PinNumber: 48, SignalName: "sHLTA", Description: "Halt Acknowledge", ConnectorIndex: 47, Side: "front", Direction: DirectionOutput})

	// Clock and Ground
	bd.AddPin(&PinDefinition{PinNumber: 49, SignalName: "CLOCK", Description: "2 MHz Clock", ConnectorIndex: 48, Side: "front", Direction: DirectionOutput, LogicSense: LogicRisingEdge})
	bd.AddPin(&PinDefinition{PinNumber: 50, SignalName: "GND", Description: "Ground", ConnectorIndex: 49, Side: "front", Direction: DirectionGround})
}

func addS100BackPins(bd *BoardDefinition) {
	// Power
	bd.AddPin(&PinDefinition{PinNumber: 51, SignalName: "+8V", Description: "+8V Power", ConnectorIndex: 0, Side: "back", Direction: DirectionPower})
	bd.AddPin(&PinDefinition{PinNumber: 52, SignalName: "-16V", Description: "-16V Power", ConnectorIndex: 1, Side: "back", Direction: DirectionPower})

	// Ground
	bd.AddPin(&PinDefinition{PinNumber: 53, SignalName: "GND", Description: "Ground", ConnectorIndex: 2, Side: "back", Direction: DirectionGround})

	// System control
	bd.AddPin(&PinDefinition{PinNumber: 54, SignalName: "SLAVE CLR*", Description: "Slave Clear", ConnectorIndex: 3, Side: "back", Direction: DirectionOutput, LogicSense: LogicActiveLow})

	// DMA
	bd.AddPin(&PinDefinition{PinNumber: 55, SignalName: "DMA0*", Description: "DMA Request 0", ConnectorIndex: 4, Side: "back", Direction: DirectionInput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 56, SignalName: "DMA1*", Description: "DMA Request 1", ConnectorIndex: 5, Side: "back", Direction: DirectionInput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 57, SignalName: "DMA2*", Description: "DMA Request 2", ConnectorIndex: 6, Side: "back", Direction: DirectionInput, LogicSense: LogicActiveLow})

	// Bus control
	bd.AddPin(&PinDefinition{PinNumber: 58, SignalName: "sXTRQ*", Description: "16-bit Transfer Request", ConnectorIndex: 7, Side: "back", Direction: DirectionOutput, LogicSense: LogicActiveLow})

	// Extended address
	bd.AddPin(&PinDefinition{PinNumber: 59, SignalName: "A19", Description: "Address bit 19", ConnectorIndex: 8, Side: "back", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 19})
	bd.AddPin(&PinDefinition{PinNumber: 60, SignalName: "SIXTN*", Description: "16-bit Slave", ConnectorIndex: 9, Side: "back", Direction: DirectionInput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 61, SignalName: "A20", Description: "Address bit 20", ConnectorIndex: 10, Side: "back", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 20})
	bd.AddPin(&PinDefinition{PinNumber: 62, SignalName: "A21", Description: "Address bit 21", ConnectorIndex: 11, Side: "back", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 21})
	bd.AddPin(&PinDefinition{PinNumber: 63, SignalName: "A22", Description: "Address bit 22", ConnectorIndex: 12, Side: "back", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 22})
	bd.AddPin(&PinDefinition{PinNumber: 64, SignalName: "A23", Description: "Address bit 23", ConnectorIndex: 13, Side: "back", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 23})

	// Undefined
	bd.AddPin(&PinDefinition{PinNumber: 65, SignalName: "NDEF", Description: "Not Defined", ConnectorIndex: 14, Side: "back", Direction: DirectionBidirectional})
	bd.AddPin(&PinDefinition{PinNumber: 66, SignalName: "NDEF", Description: "Not Defined", ConnectorIndex: 15, Side: "back", Direction: DirectionBidirectional})

	// Memory control
	bd.AddPin(&PinDefinition{PinNumber: 67, SignalName: "PHANTOM*", Description: "Phantom Memory", ConnectorIndex: 16, Side: "back", Direction: DirectionOutput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 68, SignalName: "MWRT", Description: "Memory Write", ConnectorIndex: 17, Side: "back", Direction: DirectionOutput})

	// Reserved for future use
	bd.AddPin(&PinDefinition{PinNumber: 69, SignalName: "RFU", Description: "Reserved for Future Use", ConnectorIndex: 18, Side: "back", Direction: DirectionBidirectional})

	// Ground
	bd.AddPin(&PinDefinition{PinNumber: 70, SignalName: "GND", Description: "Ground", ConnectorIndex: 19, Side: "back", Direction: DirectionGround})

	// Reserved for future use
	bd.AddPin(&PinDefinition{PinNumber: 71, SignalName: "RFU", Description: "Reserved for Future Use", ConnectorIndex: 20, Side: "back", Direction: DirectionBidirectional})

	// System control
	bd.AddPin(&PinDefinition{PinNumber: 72, SignalName: "RDY", Description: "Ready", ConnectorIndex: 21, Side: "back", Direction: DirectionInput, LogicSense: LogicActiveHigh})
	bd.AddPin(&PinDefinition{PinNumber: 73, SignalName: "INT*", Description: "Interrupt", ConnectorIndex: 22, Side: "back", Direction: DirectionInput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 74, SignalName: "HOLD*", Description: "Hold", ConnectorIndex: 23, Side: "back", Direction: DirectionInput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 75, SignalName: "RESET*", Description: "Reset", ConnectorIndex: 24, Side: "back", Direction: DirectionOutput, LogicSense: LogicActiveLow})

	// Processor status
	bd.AddPin(&PinDefinition{PinNumber: 76, SignalName: "pSYNC", Description: "Sync", ConnectorIndex: 25, Side: "back", Direction: DirectionOutput})
	bd.AddPin(&PinDefinition{PinNumber: 77, SignalName: "pWR*", Description: "Write", ConnectorIndex: 26, Side: "back", Direction: DirectionOutput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 78, SignalName: "pDBIN", Description: "Data Bus In", ConnectorIndex: 27, Side: "back", Direction: DirectionOutput})

	// Lower address bus
	bd.AddPin(&PinDefinition{PinNumber: 79, SignalName: "A0", Description: "Address bit 0", ConnectorIndex: 28, Side: "back", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 0})
	bd.AddPin(&PinDefinition{PinNumber: 80, SignalName: "A1", Description: "Address bit 1", ConnectorIndex: 29, Side: "back", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 1})
	bd.AddPin(&PinDefinition{PinNumber: 81, SignalName: "A2", Description: "Address bit 2", ConnectorIndex: 30, Side: "back", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 2})
	bd.AddPin(&PinDefinition{PinNumber: 82, SignalName: "A6", Description: "Address bit 6", ConnectorIndex: 31, Side: "back", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 6})
	bd.AddPin(&PinDefinition{PinNumber: 83, SignalName: "A7", Description: "Address bit 7", ConnectorIndex: 32, Side: "back", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 7})
	bd.AddPin(&PinDefinition{PinNumber: 84, SignalName: "A8", Description: "Address bit 8", ConnectorIndex: 33, Side: "back", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 8})
	bd.AddPin(&PinDefinition{PinNumber: 85, SignalName: "A13", Description: "Address bit 13", ConnectorIndex: 34, Side: "back", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 13})
	bd.AddPin(&PinDefinition{PinNumber: 86, SignalName: "A14", Description: "Address bit 14", ConnectorIndex: 35, Side: "back", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 14})
	bd.AddPin(&PinDefinition{PinNumber: 87, SignalName: "A11", Description: "Address bit 11", ConnectorIndex: 36, Side: "back", Direction: DirectionOutput, BusGroup: "address", BusBitIndex: 11})

	// Data bus out
	bd.AddPin(&PinDefinition{PinNumber: 88, SignalName: "DO2", Description: "Data Out bit 2", ConnectorIndex: 37, Side: "back", Direction: DirectionOutput, BusGroup: "data", BusBitIndex: 2})
	bd.AddPin(&PinDefinition{PinNumber: 89, SignalName: "DO3", Description: "Data Out bit 3", ConnectorIndex: 38, Side: "back", Direction: DirectionOutput, BusGroup: "data", BusBitIndex: 3})
	bd.AddPin(&PinDefinition{PinNumber: 90, SignalName: "DO7", Description: "Data Out bit 7", ConnectorIndex: 39, Side: "back", Direction: DirectionOutput, BusGroup: "data", BusBitIndex: 7})

	// Data bus in
	bd.AddPin(&PinDefinition{PinNumber: 91, SignalName: "DI4", Description: "Data In bit 4", ConnectorIndex: 40, Side: "back", Direction: DirectionInput, BusGroup: "data", BusBitIndex: 4})
	bd.AddPin(&PinDefinition{PinNumber: 92, SignalName: "DI5", Description: "Data In bit 5", ConnectorIndex: 41, Side: "back", Direction: DirectionInput, BusGroup: "data", BusBitIndex: 5})
	bd.AddPin(&PinDefinition{PinNumber: 93, SignalName: "DI6", Description: "Data In bit 6", ConnectorIndex: 42, Side: "back", Direction: DirectionInput, BusGroup: "data", BusBitIndex: 6})
	bd.AddPin(&PinDefinition{PinNumber: 94, SignalName: "DI1", Description: "Data In bit 1", ConnectorIndex: 43, Side: "back", Direction: DirectionInput, BusGroup: "data", BusBitIndex: 1})
	bd.AddPin(&PinDefinition{PinNumber: 95, SignalName: "DI0", Description: "Data In bit 0", ConnectorIndex: 44, Side: "back", Direction: DirectionInput, BusGroup: "data", BusBitIndex: 0})

	// Status signals
	bd.AddPin(&PinDefinition{PinNumber: 96, SignalName: "sINTA", Description: "Interrupt Acknowledge", ConnectorIndex: 45, Side: "back", Direction: DirectionOutput})
	bd.AddPin(&PinDefinition{PinNumber: 97, SignalName: "sWO*", Description: "Write Out", ConnectorIndex: 46, Side: "back", Direction: DirectionOutput, LogicSense: LogicActiveLow})
	bd.AddPin(&PinDefinition{PinNumber: 98, SignalName: "ERROR*", Description: "Error", ConnectorIndex: 47, Side: "back", Direction: DirectionInput, LogicSense: LogicActiveLow})

	// System control
	bd.AddPin(&PinDefinition{PinNumber: 99, SignalName: "POC*", Description: "Power On Clear", ConnectorIndex: 48, Side: "back", Direction: DirectionOutput, LogicSense: LogicActiveLow})

	// Ground
	bd.AddPin(&PinDefinition{PinNumber: 100, SignalName: "GND", Description: "Ground", ConnectorIndex: 49, Side: "back", Direction: DirectionGround})
}
