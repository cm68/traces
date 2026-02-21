package panels

import (
	"fmt"
	"strconv"
	"strings"

	"pcb-tracer/internal/app"
	"pcb-tracer/internal/component"
	"pcb-tracer/internal/connector"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

// directionNames lists the valid direction strings in order.
var directionNames = []string{"Input", "Output", "Bidirectional", "Power", "Ground"}

// directionFromString converts a direction name to a SignalDirection.
func directionFromString(s string) connector.SignalDirection {
	switch s {
	case "Input":
		return connector.DirectionInput
	case "Output":
		return connector.DirectionOutput
	case "Bidirectional":
		return connector.DirectionBidirectional
	case "Power":
		return connector.DirectionPower
	case "Ground":
		return connector.DirectionGround
	default:
		return connector.DirectionBidirectional
	}
}

// nextDirection cycles to the next direction string.
func nextDirection(current string) string {
	for i, name := range directionNames {
		if name == current {
			return directionNames[(i+1)%len(directionNames)]
		}
	}
	return directionNames[0]
}

// LibraryPanel displays and manages component library part definitions.
type LibraryPanel struct {
	state *app.State
	box   *gtk.Box

	// Part list
	partListBox *gtk.ListBox
	selectedIdx int

	// Detail form
	partNumberEntry *gtk.Entry
	packageEntry    *gtk.Entry
	aliasesEntry    *gtk.Entry
	pinCountEntry   *gtk.Entry

	// Pin table
	pinStore *gtk.ListStore
	pinView  *gtk.TreeView

	// Currently editing part (pointer into library)
	currentPart *component.PartDefinition
}

// NewLibraryPanel creates a new library panel.
func NewLibraryPanel(state *app.State) *LibraryPanel {
	lp := &LibraryPanel{
		state:       state,
		selectedIdx: -1,
	}

	lp.box, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	lp.box.SetMarginStart(4)
	lp.box.SetMarginEnd(4)
	lp.box.SetMarginTop(4)
	lp.box.SetMarginBottom(4)

	// --- Part List ---
	listFrame, _ := gtk.FrameNew("Parts")
	listScroll, _ := gtk.ScrolledWindowNew(nil, nil)
	listScroll.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	listScroll.SetSizeRequest(-1, 120)

	lp.partListBox, _ = gtk.ListBoxNew()
	lp.partListBox.Connect("row-activated", func(lb *gtk.ListBox, row *gtk.ListBoxRow) {
		idx := row.GetIndex()
		lp.selectPart(idx)
	})
	listScroll.Add(lp.partListBox)
	listFrame.Add(listScroll)
	lp.box.PackStart(listFrame, false, false, 0)

	// --- Part Detail ---
	detailFrame, _ := gtk.FrameNew("Part Detail")
	detailBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	detailBox.SetMarginStart(4)
	detailBox.SetMarginEnd(4)
	detailBox.SetMarginTop(4)
	detailBox.SetMarginBottom(4)

	// Part Number row
	pnRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	pnLabel, _ := gtk.LabelNew("Part Number:")
	pnLabel.SetSizeRequest(80, -1)
	pnLabel.SetHAlign(gtk.ALIGN_START)
	lp.partNumberEntry, _ = gtk.EntryNew()
	lp.partNumberEntry.SetPlaceholderText("e.g. 74LS244")
	lp.partNumberEntry.Connect("changed", func() {
		lp.applyPartFields()
	})
	pnRow.PackStart(pnLabel, false, false, 0)
	pnRow.PackStart(lp.partNumberEntry, true, true, 0)
	detailBox.PackStart(pnRow, false, false, 0)

	// Package row
	pkgRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	pkgLabel, _ := gtk.LabelNew("Package:")
	pkgLabel.SetSizeRequest(80, -1)
	pkgLabel.SetHAlign(gtk.ALIGN_START)
	lp.packageEntry, _ = gtk.EntryNew()
	lp.packageEntry.SetPlaceholderText("e.g. DIP-20")
	lp.packageEntry.Connect("changed", func() {
		lp.applyPartFields()
	})
	pkgRow.PackStart(pkgLabel, false, false, 0)
	pkgRow.PackStart(lp.packageEntry, true, true, 0)
	detailBox.PackStart(pkgRow, false, false, 0)

	// Aliases row
	aliasRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	aliasLabel, _ := gtk.LabelNew("Aliases:")
	aliasLabel.SetSizeRequest(80, -1)
	aliasLabel.SetHAlign(gtk.ALIGN_START)
	lp.aliasesEntry, _ = gtk.EntryNew()
	lp.aliasesEntry.SetPlaceholderText("e.g. 74LS373N, SN74373")
	lp.aliasesEntry.Connect("changed", func() {
		lp.applyPartFields()
	})
	aliasRow.PackStart(aliasLabel, false, false, 0)
	aliasRow.PackStart(lp.aliasesEntry, true, true, 0)
	detailBox.PackStart(aliasRow, false, false, 0)

	// Pin Count row
	pcRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	pcLabel, _ := gtk.LabelNew("Pin Count:")
	pcLabel.SetSizeRequest(80, -1)
	pcLabel.SetHAlign(gtk.ALIGN_START)
	lp.pinCountEntry, _ = gtk.EntryNew()
	lp.pinCountEntry.SetPlaceholderText("e.g. 14")
	lp.pinCountEntry.SetWidthChars(6)
	setPinsBtn, _ := gtk.ButtonNewWithLabel("Set Pins")
	setPinsBtn.Connect("clicked", func() { lp.onSetPins() })
	pcRow.PackStart(pcLabel, false, false, 0)
	pcRow.PackStart(lp.pinCountEntry, false, false, 0)
	pcRow.PackStart(setPinsBtn, false, false, 0)
	detailBox.PackStart(pcRow, false, false, 0)

	// Pin Table
	lp.createPinTable()
	pinScroll, _ := gtk.ScrolledWindowNew(nil, nil)
	pinScroll.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	pinScroll.SetSizeRequest(-1, 200)
	pinScroll.Add(lp.pinView)
	detailBox.PackStart(pinScroll, true, true, 0)

	// Action buttons
	btnRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	newBtn, _ := gtk.ButtonNewWithLabel("New Part")
	newBtn.Connect("clicked", func() { lp.onNewPart() })
	deleteBtn, _ := gtk.ButtonNewWithLabel("Delete Part")
	deleteBtn.Connect("clicked", func() { lp.onDeletePart() })
	saveBtn, _ := gtk.ButtonNewWithLabel("Save Library")
	saveBtn.Connect("clicked", func() { lp.onSaveLibrary() })
	btnRow.PackStart(newBtn, false, false, 0)
	btnRow.PackStart(deleteBtn, false, false, 0)
	btnRow.PackStart(saveBtn, false, false, 0)
	detailBox.PackStart(btnRow, false, false, 0)

	detailFrame.Add(detailBox)
	lp.box.PackStart(detailFrame, true, true, 0)

	lp.refreshPartList()
	return lp
}

// Widget returns the panel's root widget.
func (lp *LibraryPanel) Widget() gtk.IWidget {
	return lp.box
}

// createPinTable sets up the TreeView with ListStore for the pin table.
func (lp *LibraryPanel) createPinTable() {
	// Columns: pin number (int shown as string), name (string), direction (string)
	var err error
	lp.pinStore, err = gtk.ListStoreNew(glib.TYPE_STRING, glib.TYPE_STRING, glib.TYPE_STRING)
	if err != nil {
		fmt.Printf("Error creating pin list store: %v\n", err)
		return
	}

	lp.pinView, err = gtk.TreeViewNewWithModel(lp.pinStore)
	if err != nil {
		fmt.Printf("Error creating pin tree view: %v\n", err)
		return
	}
	lp.pinView.SetHeadersVisible(true)

	// Column 0: Pin # (not editable)
	numRenderer, _ := gtk.CellRendererTextNew()
	numCol, _ := gtk.TreeViewColumnNewWithAttribute("#", numRenderer, "text", 0)
	numCol.SetMinWidth(30)
	lp.pinView.AppendColumn(numCol)

	// Column 1: Pin Name (editable)
	nameRenderer, _ := gtk.CellRendererTextNew()
	nameRenderer.SetProperty("editable", true)
	nameRenderer.Connect("edited", func(renderer *gtk.CellRendererText, pathStr string, newText string) {
		lp.onPinNameEdited(pathStr, newText)
	})
	nameCol, _ := gtk.TreeViewColumnNewWithAttribute("Name", nameRenderer, "text", 1)
	nameCol.SetExpand(true)
	lp.pinView.AppendColumn(nameCol)

	// Column 2: Direction (click to cycle through options)
	dirRenderer, _ := gtk.CellRendererTextNew()
	dirRenderer.SetProperty("editable", true)
	dirRenderer.Connect("edited", func(renderer *gtk.CellRendererText, pathStr string, newText string) {
		lp.onPinDirectionEdited(pathStr, newText)
	})
	dirCol, _ := gtk.TreeViewColumnNewWithAttribute("Direction", dirRenderer, "text", 2)
	dirCol.SetMinWidth(90)
	lp.pinView.AppendColumn(dirCol)
}

// onPinNameEdited handles inline editing of a pin name in the pin table.
func (lp *LibraryPanel) onPinNameEdited(pathStr, newText string) {
	if lp.currentPart == nil {
		return
	}

	idx, err := strconv.Atoi(pathStr)
	if err != nil || idx < 0 || idx >= len(lp.currentPart.Pins) {
		return
	}

	lp.currentPart.Pins[idx].Name = newText

	// Update the store
	iter, err := lp.pinStore.GetIterFromString(pathStr)
	if err == nil {
		lp.pinStore.SetValue(iter, 1, newText)
	}
}

// onPinDirectionEdited handles editing of the direction column.
// Typing a valid direction name sets it; otherwise it cycles to the next value.
func (lp *LibraryPanel) onPinDirectionEdited(pathStr, newText string) {
	if lp.currentPart == nil {
		return
	}

	idx, err := strconv.Atoi(pathStr)
	if err != nil || idx < 0 || idx >= len(lp.currentPart.Pins) {
		return
	}

	// Check if the typed text matches a valid direction
	valid := false
	for _, name := range directionNames {
		if name == newText {
			valid = true
			break
		}
	}

	var dirStr string
	if valid {
		dirStr = newText
	} else {
		// Cycle to the next direction
		current := lp.currentPart.Pins[idx].Direction.String()
		dirStr = nextDirection(current)
	}

	lp.currentPart.Pins[idx].Direction = directionFromString(dirStr)

	iter, err := lp.pinStore.GetIterFromString(pathStr)
	if err == nil {
		lp.pinStore.SetValue(iter, 2, dirStr)
	}
}

// refreshPartList rebuilds the part list from the library.
func (lp *LibraryPanel) refreshPartList() {
	// Remove all children
	children := lp.partListBox.GetChildren()
	children.Foreach(func(item interface{}) {
		if w, ok := item.(gtk.IWidget); ok {
			lp.partListBox.Remove(w)
		}
	})

	lib := lp.state.ComponentLibrary
	if lib == nil {
		return
	}

	for _, part := range lib.Parts {
		label, _ := gtk.LabelNew(fmt.Sprintf("%s (%d pins)", part.Key(), part.PinCount))
		label.SetHAlign(gtk.ALIGN_START)
		lp.partListBox.Add(label)
	}

	lp.partListBox.ShowAll()
}

// selectPart selects a part by index and populates the detail form.
func (lp *LibraryPanel) selectPart(idx int) {
	lib := lp.state.ComponentLibrary
	if lib == nil || idx < 0 || idx >= len(lib.Parts) {
		return
	}

	lp.selectedIdx = idx
	part := lib.Parts[idx]
	lp.currentPart = part

	// Block signals while populating (to prevent feedback from "changed" signals)
	lp.partNumberEntry.SetText(part.PartNumber)
	lp.packageEntry.SetText(part.Package)
	lp.aliasesEntry.SetText(strings.Join(part.Aliases, ", "))
	lp.pinCountEntry.SetText(strconv.Itoa(part.PinCount))

	lp.refreshPinTable()
}

// refreshPinTable rebuilds the pin table from the current part.
func (lp *LibraryPanel) refreshPinTable() {
	lp.pinStore.Clear()

	if lp.currentPart == nil {
		return
	}

	for _, pin := range lp.currentPart.Pins {
		iter := lp.pinStore.Append()
		lp.pinStore.Set(iter,
			[]int{0, 1, 2},
			[]interface{}{
				strconv.Itoa(pin.Number),
				pin.Name,
				pin.Direction.String(),
			},
		)
	}
}

// applyPartFields updates the current part from the entry fields.
func (lp *LibraryPanel) applyPartFields() {
	if lp.currentPart == nil {
		return
	}
	pn, _ := lp.partNumberEntry.GetText()
	pkg, _ := lp.packageEntry.GetText()
	aliasText, _ := lp.aliasesEntry.GetText()
	lp.currentPart.PartNumber = pn
	lp.currentPart.Package = pkg

	// Parse comma-separated aliases
	var aliases []string
	for _, a := range strings.Split(aliasText, ",") {
		a = strings.TrimSpace(a)
		if a != "" {
			aliases = append(aliases, a)
		}
	}
	lp.currentPart.Aliases = aliases
}

// onSetPins creates pin entries based on the pin count field.
func (lp *LibraryPanel) onSetPins() {
	if lp.currentPart == nil {
		lp.onNewPart()
	}

	countStr, _ := lp.pinCountEntry.GetText()
	count, err := strconv.Atoi(countStr)
	if err != nil || count < 1 || count > 999 {
		fmt.Println("Invalid pin count")
		return
	}

	lp.currentPart.PinCount = count
	lp.currentPart.Pins = make([]component.PartPin, count)
	for i := 0; i < count; i++ {
		lp.currentPart.Pins[i] = component.PartPin{
			Number:    i + 1,
			Name:      "",
			Direction: connector.DirectionBidirectional,
		}
	}

	lp.refreshPinTable()
}

// onNewPart clears the form for a new part definition.
func (lp *LibraryPanel) onNewPart() {
	part := &component.PartDefinition{}
	lp.currentPart = part
	lp.selectedIdx = -1

	lp.partNumberEntry.SetText("")
	lp.packageEntry.SetText("")
	lp.aliasesEntry.SetText("")
	lp.pinCountEntry.SetText("")
	lp.pinStore.Clear()

	// Add to library immediately (will be saved on explicit save)
	lib := lp.state.ComponentLibrary
	if lib == nil {
		lib = component.NewComponentLibrary()
		lp.state.ComponentLibrary = lib
	}
	lib.Add(part)
	lp.refreshPartList()
}

// onDeletePart removes the selected part from the library.
func (lp *LibraryPanel) onDeletePart() {
	if lp.currentPart == nil {
		return
	}

	lib := lp.state.ComponentLibrary
	if lib == nil {
		return
	}

	lib.Remove(lp.currentPart.PartNumber, lp.currentPart.Package)
	lp.currentPart = nil
	lp.selectedIdx = -1

	lp.partNumberEntry.SetText("")
	lp.packageEntry.SetText("")
	lp.aliasesEntry.SetText("")
	lp.pinCountEntry.SetText("")
	lp.pinStore.Clear()

	lp.refreshPartList()
}

// onSaveLibrary saves the component library to disk.
func (lp *LibraryPanel) onSaveLibrary() {
	// Re-add current part to ensure any key changes are reflected
	if lp.currentPart != nil {
		lib := lp.state.ComponentLibrary
		if lib != nil {
			lib.Add(lp.currentPart)
			lib.Sort()
		}
	}

	if err := lp.state.SaveComponentLibrary(); err != nil {
		fmt.Printf("Error saving component library: %v\n", err)
	} else {
		fmt.Println("Component library saved")
	}
	lp.refreshPartList()
}
