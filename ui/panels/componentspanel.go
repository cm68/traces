package panels

import (
	"fmt"
	"image"
	"image/color"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"pcb-tracer/internal/app"
	"pcb-tracer/internal/component"
	"pcb-tracer/internal/datecode"
	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/internal/logo"
	"pcb-tracer/internal/ocr"
	"pcb-tracer/pkg/geometry"
	"pcb-tracer/ui/canvas"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"gocv.io/x/gocv"
)

// ComponentsPanel displays and manages detected components.
type ComponentsPanel struct {
	state  *app.State
	canvas *canvas.ImageCanvas
	win    *gtk.Window
	box    *gtk.Box // Top-level container

	listBox       *gtk.ListBox
	listScroll    *gtk.ScrolledWindow
	paned         *gtk.Paned // Draggable split between list and edit form
	sortedIndices []int      // Indices into state.Components, sorted by ID

	// Inline edit form
	editingComp        *component.Component
	editingIndex       int
	editFrame          *gtk.Frame
	idEntry            *gtk.Entry
	partNumberEntry    *gtk.Entry
	packageEntry       *gtk.Entry
	manufacturerEntry  *gtk.Entry
	placeEntry         *gtk.Entry
	dateCodeEntry      *gtk.Entry
	revisionEntry      *gtk.Entry
	speedGradeEntry    *gtk.Entry
	descriptionEntry   *gtk.TextView
	ocrTextEntry       *gtk.TextView
	correctedTextEntry *gtk.TextView
	ocrOrientation     []*gtk.RadioButton // N, S, E, W
	ocrTrainingLabel   *gtk.Label
}

// NewComponentsPanel creates a new components panel.
func NewComponentsPanel(state *app.State, cvs *canvas.ImageCanvas, win *gtk.Window) *ComponentsPanel {
	cp := &ComponentsPanel{
		state:        state,
		canvas:       cvs,
		win:          win,
		editingIndex: -1,
	}

	cp.rebuildSortedIndices()

	// Top-level vertical box
	cp.box, _ = gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	cp.box.SetMarginStart(4)
	cp.box.SetMarginEnd(4)
	cp.box.SetMarginTop(4)
	cp.box.SetMarginBottom(4)

	// Button row at top
	btnRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	ocrSilkscreenBtn, _ := gtk.ButtonNewWithLabel("OCR All Silkscreen")
	ocrSilkscreenBtn.Connect("clicked", func() { cp.onOCRSilkscreen() })
	btnRow.PackStart(ocrSilkscreenBtn, true, true, 0)

	trainBtn, _ := gtk.ButtonNewWithLabel("Train Components")
	trainBtn.Connect("clicked", func() { cp.onTrainComponents() })
	btnRow.PackStart(trainBtn, true, true, 0)

	detectBtn, _ := gtk.ButtonNewWithLabel("Detect Components")
	detectBtn.Connect("clicked", func() { cp.onDetectComponents() })
	btnRow.PackStart(detectBtn, true, true, 0)

	cp.box.PackStart(btnRow, false, false, 0)

	// Create the list
	cp.listBox, _ = gtk.ListBoxNew()
	cp.listBox.SetSelectionMode(gtk.SELECTION_NONE)
	cp.listBox.Connect("row-activated", func(_ *gtk.ListBox, row *gtk.ListBoxRow) {
		if row == nil {
			return
		}
		idx := row.GetIndex()
		if idx >= 0 && idx < len(cp.sortedIndices) {
			compIdx := cp.sortedIndices[idx]
			cp.selectComponentByIndex(compIdx)
		}
	})
	cp.listBox.Connect("button-press-event", func(_ *gtk.ListBox, ev *gdk.Event) bool {
		btn := gdk.EventButtonNewFromEvent(ev)
		if btn.Button() != gdk.BUTTON_SECONDARY {
			return false
		}
		row := cp.listBox.GetRowAtY(int(btn.Y()))
		if row == nil {
			return false
		}
		idx := row.GetIndex()
		if idx >= 0 && idx < len(cp.sortedIndices) {
			compIdx := cp.sortedIndices[idx]
			cp.showComponentListMenu(compIdx)
		}
		return true
	})

	cp.listScroll, _ = gtk.ScrolledWindowNew(nil, nil)
	cp.listScroll.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	cp.listScroll.Add(cp.listBox)

	// Create the edit form
	editScroll := cp.buildEditForm()

	// Vertical paned between list and edit form
	cp.paned, _ = gtk.PanedNew(gtk.ORIENTATION_VERTICAL)
	cp.paned.Pack1(cp.listScroll, true, false)
	cp.paned.Pack2(editScroll, true, false)
	cp.paned.SetPosition(250) // Default split position

	cp.box.PackStart(cp.paned, true, true, 0)

	// Populate the list initially
	cp.refreshList()

	// Subscribe to events
	state.On(app.EventComponentsChanged, func(_ interface{}) {
		glib.IdleAdd(func() {
			cp.rebuildSortedIndices()
			cp.refreshList()
			cp.updateComponentOverlay()
		})
	})
	state.On(app.EventProjectLoaded, func(_ interface{}) {
		glib.IdleAdd(func() {
			cp.rebuildSortedIndices()
			cp.refreshList()
			cp.updateComponentOverlay()
		})
	})

	// Handle key presses on the list for component adjustment
	cp.box.Connect("key-press-event", func(_ *gtk.Box, ev *gdk.Event) bool {
		keyEvent := gdk.EventKeyNewFromEvent(ev)
		return cp.OnKeyPressed(keyEvent)
	})

	return cp
}

// Widget returns the panel widget for embedding.
func (cp *ComponentsPanel) Widget() gtk.IWidget {
	return cp.box
}

// buildEditForm creates the inline edit form and returns a scrolled window containing it.
func (cp *ComponentsPanel) buildEditForm() *gtk.ScrolledWindow {
	cp.idEntry, _ = gtk.EntryNew()
	cp.idEntry.SetPlaceholderText("Component ID")

	cp.partNumberEntry, _ = gtk.EntryNew()
	cp.partNumberEntry.SetPlaceholderText("e.g., 74LS244")

	cp.packageEntry, _ = gtk.EntryNew()
	cp.packageEntry.SetPlaceholderText("e.g., DIP-20")

	cp.manufacturerEntry, _ = gtk.EntryNew()
	cp.manufacturerEntry.SetPlaceholderText("e.g., Texas Instruments")

	cp.placeEntry, _ = gtk.EntryNew()
	cp.placeEntry.SetPlaceholderText("e.g., Malaysia")

	cp.dateCodeEntry, _ = gtk.EntryNew()
	cp.dateCodeEntry.SetPlaceholderText("e.g., 8523")

	cp.revisionEntry, _ = gtk.EntryNew()

	cp.speedGradeEntry, _ = gtk.EntryNew()
	cp.speedGradeEntry.SetPlaceholderText("e.g., -25")

	cp.descriptionEntry, _ = gtk.TextViewNew()
	cp.descriptionEntry.SetWrapMode(gtk.WRAP_WORD_CHAR)
	descScroll, _ := gtk.ScrolledWindowNew(nil, nil)
	descScroll.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	descScroll.SetSizeRequest(-1, 40)
	descScroll.Add(cp.descriptionEntry)

	cp.correctedTextEntry, _ = gtk.TextViewNew()
	cp.correctedTextEntry.SetWrapMode(gtk.WRAP_WORD_CHAR)
	corrScroll, _ := gtk.ScrolledWindowNew(nil, nil)
	corrScroll.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	corrScroll.SetSizeRequest(-1, 40)
	corrScroll.Add(cp.correctedTextEntry)

	cp.ocrTextEntry, _ = gtk.TextViewNew()
	cp.ocrTextEntry.SetWrapMode(gtk.WRAP_WORD_CHAR)
	ocrScroll, _ := gtk.ScrolledWindowNew(nil, nil)
	ocrScroll.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	ocrScroll.SetSizeRequest(-1, 40)
	ocrScroll.Add(cp.ocrTextEntry)

	// Form grid
	grid, _ := gtk.GridNew()
	grid.SetColumnSpacing(4)
	grid.SetRowSpacing(2)

	addRow := func(row int, label string, w gtk.IWidget) {
		lbl, _ := gtk.LabelNew(label)
		lbl.SetHAlign(gtk.ALIGN_END)
		grid.Attach(lbl, 0, row, 1, 1)
		grid.Attach(w, 1, row, 1, 1)
	}
	// Make entries expand
	cp.idEntry.SetHExpand(true)
	cp.partNumberEntry.SetHExpand(true)
	cp.packageEntry.SetHExpand(true)
	cp.manufacturerEntry.SetHExpand(true)
	cp.placeEntry.SetHExpand(true)
	cp.dateCodeEntry.SetHExpand(true)
	cp.revisionEntry.SetHExpand(true)
	cp.speedGradeEntry.SetHExpand(true)

	addRow(0, "ID:", cp.idEntry)
	addRow(1, "Part #:", cp.partNumberEntry)
	addRow(2, "Package:", cp.packageEntry)
	addRow(3, "Mfr:", cp.manufacturerEntry)
	addRow(4, "Place:", cp.placeEntry)
	addRow(5, "Date:", cp.dateCodeEntry)
	addRow(6, "Rev:", cp.revisionEntry)
	addRow(7, "Speed:", cp.speedGradeEntry)

	// OCR orientation radio buttons
	orientBox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	var firstRadio *gtk.RadioButton
	for i, dir := range []string{"N", "S", "E", "W"} {
		var rb *gtk.RadioButton
		if i == 0 {
			rb, _ = gtk.RadioButtonNewWithLabel(nil, dir)
			firstRadio = rb
		} else {
			rb, _ = gtk.RadioButtonNewWithLabelFromWidget(firstRadio, dir)
		}
		cp.ocrOrientation = append(cp.ocrOrientation, rb)
		orientBox.PackStart(rb, false, false, 0)
	}

	// OCR buttons
	ocrBtn, _ := gtk.ButtonNewWithLabel("OCR")
	ocrBtn.Connect("clicked", func() { cp.runOCR() })

	trainBtn, _ := gtk.ButtonNewWithLabel("Train")
	trainBtn.Connect("clicked", func() { cp.runOCRTraining() })

	cp.ocrTrainingLabel, _ = gtk.LabelNew("")
	cp.updateOCRTrainingLabel()

	ocrRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	ocrRow.PackStart(ocrBtn, false, false, 0)
	ocrRow.PackStart(trainBtn, false, false, 0)
	dirLabel, _ := gtk.LabelNew("Dir:")
	ocrRow.PackStart(dirLabel, false, false, 0)
	ocrRow.PackStart(orientBox, false, false, 0)

	// Save/Delete buttons
	saveBtn, _ := gtk.ButtonNewWithLabel("Save")
	saveBtn.Connect("clicked", func() { cp.saveEditingComponent() })
	ctx, _ := saveBtn.GetStyleContext()
	ctx.AddClass("suggested-action")

	deleteBtn, _ := gtk.ButtonNewWithLabel("Delete")
	deleteBtn.Connect("clicked", func() { cp.deleteEditingComponent() })
	ctx2, _ := deleteBtn.GetStyleContext()
	ctx2.AddClass("destructive-action")

	btnRow, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	btnRow.PackStart(deleteBtn, false, false, 0)
	spacer, _ := gtk.LabelNew("")
	spacer.SetHExpand(true)
	btnRow.PackStart(spacer, true, true, 0)
	btnRow.PackStart(saveBtn, false, false, 0)

	// Assemble the form
	formBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	formBox.SetMarginStart(4)
	formBox.SetMarginEnd(4)
	formBox.SetMarginTop(4)
	formBox.SetMarginBottom(4)
	formBox.PackStart(grid, false, false, 0)

	sep1, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
	formBox.PackStart(sep1, false, false, 2)
	descLabel, _ := gtk.LabelNew("Description:")
	descLabel.SetHAlign(gtk.ALIGN_START)
	formBox.PackStart(descLabel, false, false, 0)
	formBox.PackStart(descScroll, false, false, 0)

	sep2, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
	formBox.PackStart(sep2, false, false, 2)
	formBox.PackStart(ocrRow, false, false, 0)
	formBox.PackStart(cp.ocrTrainingLabel, false, false, 0)
	corrLabel, _ := gtk.LabelNew("Corrected:")
	corrLabel.SetHAlign(gtk.ALIGN_START)
	formBox.PackStart(corrLabel, false, false, 0)
	formBox.PackStart(corrScroll, false, false, 0)
	ocrLabel, _ := gtk.LabelNew("OCR Result:")
	ocrLabel.SetHAlign(gtk.ALIGN_START)
	formBox.PackStart(ocrLabel, false, false, 0)
	formBox.PackStart(ocrScroll, false, false, 0)

	sep3, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
	formBox.PackStart(sep3, false, false, 2)
	formBox.PackStart(btnRow, false, false, 0)

	cp.editFrame, _ = gtk.FrameNew("Component")
	cp.editFrame.Add(formBox)

	editScroll, _ := gtk.ScrolledWindowNew(nil, nil)
	editScroll.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	editScroll.Add(cp.editFrame)

	return editScroll
}

// refreshList rebuilds the ListBox rows from sortedIndices.
func (cp *ComponentsPanel) refreshList() {
	// Remove all existing children
	cp.listBox.GetChildren().Foreach(func(item interface{}) {
		if w, ok := item.(*gtk.Widget); ok {
			cp.listBox.Remove(w)
		}
	})

	for _, compIdx := range cp.sortedIndices {
		if compIdx >= len(cp.state.Components) {
			continue
		}
		comp := cp.state.Components[compIdx]

		prefix := ""
		if !comp.Confirmed {
			prefix = "* "
		}
		detail := comp.PartNumber
		if detail == "" {
			detail = comp.Package
		}
		text := fmt.Sprintf("%s%s %s", prefix, comp.ID, detail)

		row, _ := gtk.ListBoxRowNew()
		label, _ := gtk.LabelNew(text)
		label.SetHAlign(gtk.ALIGN_START)
		label.SetMarginStart(4)
		label.SetMarginEnd(4)
		label.SetMarginTop(2)
		label.SetMarginBottom(2)
		row.Add(label)
		cp.listBox.Add(row)
	}

	cp.listBox.ShowAll()
}

// getSelectedOrientation returns the currently selected OCR orientation string.
func (cp *ComponentsPanel) getSelectedOrientation() string {
	dirs := []string{"N", "S", "E", "W"}
	for i, rb := range cp.ocrOrientation {
		if rb.GetActive() {
			return dirs[i]
		}
	}
	return "N"
}

// setSelectedOrientation sets the OCR orientation radio button.
func (cp *ComponentsPanel) setSelectedOrientation(orient string) {
	dirs := []string{"N", "S", "E", "W"}
	for i, dir := range dirs {
		if dir == orient {
			cp.ocrOrientation[i].SetActive(true)
			return
		}
	}
	cp.ocrOrientation[0].SetActive(true)
}

// getTextViewText returns the text content of a TextView.
func getTextViewText(tv *gtk.TextView) string {
	buf, _ := tv.GetBuffer()
	start, end := buf.GetBounds()
	text, _ := buf.GetText(start, end, false)
	return text
}

// setTextViewText sets the text content of a TextView.
func setTextViewText(tv *gtk.TextView, text string) {
	buf, _ := tv.GetBuffer()
	buf.SetText(text)
}

// rebuildSortedIndices rebuilds the sorted indices using natural numeric sorting by component ID.
func (cp *ComponentsPanel) rebuildSortedIndices() {
	n := len(cp.state.Components)
	cp.sortedIndices = make([]int, n)
	for i := range cp.sortedIndices {
		cp.sortedIndices[i] = i
	}
	sort.Slice(cp.sortedIndices, func(i, j int) bool {
		ci := cp.state.Components[cp.sortedIndices[i]]
		cj := cp.state.Components[cp.sortedIndices[j]]
		return naturalLess(ci.ID, cj.ID)
	})
}

// updateOCRTrainingLabel updates the training status label with sample counts.
func (cp *ComponentsPanel) updateOCRTrainingLabel() {
	if cp.ocrTrainingLabel == nil {
		return
	}
	if cp.state.GlobalOCRTraining == nil || len(cp.state.GlobalOCRTraining.Samples) == 0 {
		cp.ocrTrainingLabel.SetText("No training data")
		return
	}
	db := cp.state.GlobalOCRTraining
	orientCounts := make(map[string]int)
	for _, s := range db.Samples {
		orientCounts[s.Orientation]++
	}
	cp.ocrTrainingLabel.SetText(fmt.Sprintf("Trained: %d samples (N:%d S:%d E:%d W:%d)",
		len(db.Samples), orientCounts["N"], orientCounts["S"], orientCounts["E"], orientCounts["W"]))
}

// showEditDialog populates the inline edit form for the given component index.
func (cp *ComponentsPanel) showEditDialog(index int) {
	if index < 0 || index >= len(cp.state.Components) {
		return
	}

	comp := cp.state.Components[index]
	cp.editingComp = comp
	cp.editingIndex = index

	cp.idEntry.SetText(comp.ID)
	cp.partNumberEntry.SetText(comp.PartNumber)
	cp.packageEntry.SetText(comp.Package)
	cp.manufacturerEntry.SetText(comp.Manufacturer)
	cp.placeEntry.SetText(comp.Place)
	cp.dateCodeEntry.SetText(comp.DateCode)
	cp.revisionEntry.SetText(comp.Revision)
	cp.speedGradeEntry.SetText(comp.SpeedGrade)
	setTextViewText(cp.descriptionEntry, comp.Description)
	setTextViewText(cp.ocrTextEntry, comp.OCRText)
	setTextViewText(cp.correctedTextEntry, comp.CorrectedText)

	// Set orientation: sticky direction always takes precedence
	if cp.state.LastOCROrientation != "" {
		cp.setSelectedOrientation(cp.state.LastOCROrientation)
	} else if comp.OCROrientation != "" {
		cp.setSelectedOrientation(comp.OCROrientation)
	} else {
		cp.setSelectedOrientation("N")
	}

	// Update frame title
	subtitle := fmt.Sprintf("%s - %s", comp.Package, comp.PartNumber)
	cp.editFrame.SetLabel(fmt.Sprintf("%s (%s)", comp.ID, subtitle))

	// Highlight and scroll to component
	cp.highlightComponent(index, true)
}

// saveEditingComponent saves changes from the inline form to the editing component.
func (cp *ComponentsPanel) saveEditingComponent() {
	if cp.editingComp == nil {
		return
	}

	idText, _ := cp.idEntry.GetText()
	partText, _ := cp.partNumberEntry.GetText()
	pkgText, _ := cp.packageEntry.GetText()
	mfrText, _ := cp.manufacturerEntry.GetText()
	placeText, _ := cp.placeEntry.GetText()
	dateText, _ := cp.dateCodeEntry.GetText()
	revText, _ := cp.revisionEntry.GetText()
	speedText, _ := cp.speedGradeEntry.GetText()
	descText := getTextViewText(cp.descriptionEntry)
	ocrText := getTextViewText(cp.ocrTextEntry)
	corrText := getTextViewText(cp.correctedTextEntry)
	orientation := cp.getSelectedOrientation()

	cp.editingComp.ID = idText
	cp.editingComp.Confirmed = true

	// Auto-rename NEW* components from grid mapping after ID change
	if renamed := component.PropagateGridNames(cp.state.Components, 100); renamed > 0 {
		fmt.Printf("Auto-renamed %d components from grid mapping\n", renamed)
	}

	// Clean up 74/54-series part numbers: strip manufacturer prefix and package suffix,
	// apply OCR correction, and auto-detect manufacturer.
	// e.g., "SN74LS08N" -> part "74LS08", manufacturer "Texas Instruments"
	if partText != "" {
		corrected, _ := component.CorrectOCRPartNumber(partText)
		if core := component.ExtractLogicPart(corrected); core != "" {
			// Identify manufacturer from prefix
			if mfrText == "" {
				prefixToMfr := map[string]string{
					"SN": "Texas Instruments", "TL": "Texas Instruments", "UC": "Texas Instruments",
					"DM": "National Semiconductor", "LM": "National Semiconductor", "DS": "National Semiconductor",
					"MC": "Motorola", "MJ": "Motorola",
					"UA": "Fairchild", "9N": "Fairchild",
					"AM": "AMD",
					"CD": "RCA", "CA": "RCA",
					"HD": "Hitachi", "HA": "Hitachi",
					"TC": "Toshiba",
					"MB": "Fujitsu",
					"NE": "Signetics",
				}
				upper := strings.ToUpper(corrected)
				// Find where the core part starts in the full string
				coreIdx := strings.Index(upper, core)
				if coreIdx > 0 {
					prefix := upper[:coreIdx]
					for p, mfr := range prefixToMfr {
						if prefix == p {
							mfrText = mfr
							cp.manufacturerEntry.SetText(mfrText)
							fmt.Printf("[Save] Manufacturer from prefix %s: %s\n", p, mfr)
							break
						}
					}
				}
			}
			if core != partText {
				partText = core
				cp.partNumberEntry.SetText(partText)
				fmt.Printf("[Save] Part number cleaned: %s\n", partText)
			}
		}
	}

	cp.editingComp.PartNumber = partText
	// Auto-fill package from parts library if not already set
	if partText != "" && pkgText == "" {
		if libPart := cp.state.ComponentLibrary.FindByPartNumber(partText); libPart != nil {
			pkgText = libPart.Package
			cp.packageEntry.SetText(pkgText)
			fmt.Printf("[Save] Library lookup: %s -> %s (%d pins)\n", partText, libPart.Package, libPart.PinCount)
		}
	}
	cp.editingComp.Package = pkgText
	cp.editingComp.Manufacturer = mfrText
	cp.editingComp.Place = placeText
	cp.editingComp.DateCode = dateText
	cp.editingComp.Revision = revText
	cp.editingComp.SpeedGrade = speedText
	cp.editingComp.Description = descText
	cp.editingComp.OCRText = ocrText
	cp.editingComp.CorrectedText = corrText
	// Always update sticky orientation for next component
	cp.state.LastOCROrientation = orientation
	// Only persist orientation on the component if OCR was performed or it already had one
	if strings.TrimSpace(ocrText) != "" || cp.editingComp.OCROrientation != "" {
		cp.editingComp.OCROrientation = orientation
	}

	// Add corrected text as training sample
	if strings.TrimSpace(corrText) != "" && strings.TrimSpace(ocrText) != "" {
		score := ocr.TextSimilarity(ocrText, corrText)
		var params ocr.OCRParams
		if cp.state.GlobalOCRTraining != nil {
			if p, ok := cp.state.GlobalOCRTraining.GetParamsForOrientation(orientation); ok {
				params = p
			} else {
				params = cp.state.GlobalOCRTraining.GetRecommendedParams()
			}
		} else {
			params = ocr.DefaultOCRParams()
		}
		if score >= 0.7 {
			cp.state.AddOCRTrainingSample(corrText, ocrText, score, orientation, params)
			cp.updateOCRTrainingLabel()
			fmt.Printf("[Save] Added training sample: score=%.1f%% orientation=%s\n", score*100, orientation)
		} else {
			fmt.Printf("[Save] Score too low for training: %.1f%% orientation=%s\n", score*100, orientation)
		}
	}

	cp.rebuildSortedIndices()
	cp.refreshList()
	cp.updateComponentOverlay()

	cp.editFrame.SetLabel(fmt.Sprintf("%s (%s - %s)", cp.editingComp.ID, cp.editingComp.Package, cp.editingComp.PartNumber))

	// Persist to disk
	if cp.state.ProjectPath != "" {
		if err := cp.state.SaveProject(cp.state.ProjectPath); err != nil {
			fmt.Printf("Error saving project: %v\n", err)
		}
	} else {
		cp.state.SetModified(true)
	}

	fmt.Printf("Saved component %s\n", cp.editingComp.ID)
}

// deleteEditingComponent deletes the currently editing component.
func (cp *ComponentsPanel) deleteEditingComponent() {
	if cp.editingComp == nil || cp.editingIndex < 0 {
		return
	}
	cp.deleteComponent(cp.editingIndex)
	cp.editingComp = nil
	cp.editingIndex = -1
	cp.clearEditForm()
}

// clearEditForm clears all form fields and the highlight.
func (cp *ComponentsPanel) clearEditForm() {
	cp.idEntry.SetText("")
	cp.partNumberEntry.SetText("")
	cp.packageEntry.SetText("")
	cp.manufacturerEntry.SetText("")
	cp.placeEntry.SetText("")
	cp.dateCodeEntry.SetText("")
	cp.revisionEntry.SetText("")
	cp.speedGradeEntry.SetText("")
	setTextViewText(cp.descriptionEntry, "")
	setTextViewText(cp.ocrTextEntry, "")
	setTextViewText(cp.correctedTextEntry, "")
	cp.setSelectedOrientation("N")

	cp.editFrame.SetLabel("Component")
	cp.updateComponentOverlay()
}

// getComponentImage returns the source image for the currently editing component.
func (cp *ComponentsPanel) getComponentImage() image.Image {
	if cp.editingComp == nil {
		return nil
	}
	var img image.Image
	switch cp.editingComp.Layer {
	case pcbimage.SideBack:
		if cp.state.BackImage != nil {
			img = cp.state.BackImage.Image
		}
	default:
		if cp.state.FrontImage != nil {
			img = cp.state.FrontImage.Image
		}
	}
	if img == nil {
		img = cp.canvas.GetRenderedOutput()
	}
	return img
}

// runOCR performs OCR on the currently editing component.
func (cp *ComponentsPanel) runOCR() {
	if cp.editingComp == nil {
		fmt.Println("[OCR] No component selected")
		return
	}

	img := cp.getComponentImage()
	if img == nil {
		fmt.Println("[OCR] No image available")
		return
	}

	bounds := cp.editingComp.Bounds
	x, y := int(bounds.X), int(bounds.Y)
	w, h := int(bounds.Width), int(bounds.Height)
	fmt.Printf("[OCR] Component bounds: (%d,%d) %dx%d\n", x, y, w, h)

	imgBounds := img.Bounds()
	if x < imgBounds.Min.X {
		x = imgBounds.Min.X
	}
	if y < imgBounds.Min.Y {
		y = imgBounds.Min.Y
	}
	w = min(w, imgBounds.Max.X-x)
	h = min(h, imgBounds.Max.Y-y)

	if w <= 0 || h <= 0 {
		fmt.Println("[OCR] Invalid bounds")
		return
	}

	cropped := image.NewRGBA(image.Rect(0, 0, w, h))
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			cropped.Set(dx, dy, img.At(x+dx, y+dy))
		}
	}

	orientation := cp.getSelectedOrientation()
	logoRotation := orientationToRotation(orientation)
	rotated := rotateForOCR(cropped, orientation)

	rotBounds := rotated.Bounds()
	masked := image.NewRGBA(rotBounds)
	copy(masked.Pix, rotated.Pix)

	// Detect logos and fill them
	var detectedLogos []logo.LogoMatch
	if cp.state.LogoLibrary != nil && len(cp.state.LogoLibrary.Logos) > 0 {
		mw, mh := rotBounds.Dx(), rotBounds.Dy()
		searchBounds := geometry.RectInt{X: 0, Y: 0, Width: mw, Height: mh}
		detectedLogos = cp.state.LogoLibrary.DetectLogos(masked, searchBounds, 0.75, logoRotation)
		if len(detectedLogos) > 0 {
			fmt.Printf("[OCR] Detected %d logos\n", len(detectedLogos))
			bgColor := calculateBackgroundColor(masked)
			compArea := mw * mh
			for _, m := range detectedLogos {
				logoArea := m.Bounds.Width * m.Bounds.Height
				pct := logoArea * 100 / compArea
				fmt.Printf("[OCR Logo] name=%q score=%.3f rot=%d scale=%.2f bounds=(%d,%d %dx%d) area=%d%% of component\n",
					m.Logo.Name, m.Score, m.Rotation, m.ScaleFactor,
					m.Bounds.X, m.Bounds.Y, m.Bounds.Width, m.Bounds.Height, pct)
				if logoArea > compArea/4 {
					fmt.Printf("[OCR Logo] SKIP: too large (%d%% > 25%%)\n", pct)
					continue
				}
				fmt.Printf("[OCR Logo] MASK: filling (%d,%d)-(%d,%d) with bg=(%d,%d,%d)\n",
					m.Bounds.X, m.Bounds.Y,
					m.Bounds.X+m.Bounds.Width, m.Bounds.Y+m.Bounds.Height,
					bgColor.R, bgColor.G, bgColor.B)
				maskRegion(masked, m.Bounds, bgColor)
			}
		}
	}

	// Show OCR preview
	cp.showOCRPreview(rotated, masked, orientation)

	// OCR runs on the masked image, binarized and despeckled
	ocrGray, mw, mh := rgbaToGray(masked)
	ocrThresh := robustOtsu(ocrGray, mw, mh)

	ocrBW := make([]bool, mw*mh)
	for i := 0; i < mw*mh; i++ {
		ocrBW[i] = ocrGray[i] > ocrThresh
	}
	despeckleBW(ocrBW, mw, mh)

	ocrBytes := make([]byte, mw*mh)
	for i := 0; i < mw*mh; i++ {
		if ocrBW[i] {
			ocrBytes[i] = 255
		}
	}

	grayMat, err := gocv.NewMatFromBytes(mh, mw, gocv.MatTypeCV8UC1, ocrBytes)
	if err != nil {
		fmt.Printf("[OCR] Mat conversion failed: %v\n", err)
		return
	}
	defer grayMat.Close()

	bgr := gocv.NewMat()
	defer bgr.Close()
	gocv.CvtColor(grayMat, &bgr, gocv.ColorGrayToBGR)

	engine, err := ocr.NewEngine()
	if err != nil {
		fmt.Printf("[OCR] Engine creation failed: %v\n", err)
		return
	}
	defer engine.Close()

	var text string
	var params ocr.OCRParams
	paramsSource := "default"

	if cp.state.GlobalOCRTraining != nil && len(cp.state.GlobalOCRTraining.Samples) >= 5 {
		if orientParams, ok := cp.state.GlobalOCRTraining.GetParamsForOrientation(orientation); ok {
			params = orientParams
			paramsSource = fmt.Sprintf("global/%s (%d samples)", orientation, len(cp.state.GlobalOCRTraining.Samples))
		} else {
			params = cp.state.GlobalOCRTraining.GetRecommendedParams()
			paramsSource = fmt.Sprintf("global (%d samples)", len(cp.state.GlobalOCRTraining.Samples))
		}
	} else {
		params = ocr.DefaultOCRParams()
	}

	fmt.Printf("[OCR] Using %s params\n", paramsSource)
	text, err = engine.RecognizeWithParams(bgr, params)
	if err != nil {
		fmt.Printf("[OCR] Failed: %v\n", err)
		return
	}

	text = fixOCRPartNumbers(text)

	// Prepend detected logos
	var detectedManufacturer string
	if len(detectedLogos) > 0 {
		var logoNames []string
		for _, m := range detectedLogos {
			logoNames = append(logoNames, fmt.Sprintf("<%s>", m.Logo.Name))
			if detectedManufacturer == "" && m.Logo.ManufacturerID != "" {
				detectedManufacturer = m.Logo.ManufacturerID
			}
		}
		text = strings.Join(logoNames, " ") + "\n" + text
	}

	// Update form fields
	setTextViewText(cp.ocrTextEntry, text)
	cp.editingComp.OCRText = text

	info := parseComponentInfo(text)
	// Apply OCR correction to part number (e.g., 74LSO4 -> 74LS04)
	if info.PartNumber != "" {
		if corrected, changed := component.CorrectOCRPartNumber(info.PartNumber); changed {
			info.PartNumber = corrected
		}
	}
	partText, _ := cp.partNumberEntry.GetText()
	if info.PartNumber != "" && partText == "" {
		cp.partNumberEntry.SetText(info.PartNumber)
	}
	mfrText, _ := cp.manufacturerEntry.GetText()
	if detectedManufacturer != "" && mfrText == "" {
		cp.manufacturerEntry.SetText(detectedManufacturer)
	} else if info.Manufacturer != "" && mfrText == "" {
		cp.manufacturerEntry.SetText(info.Manufacturer)
	}
	dateText, _ := cp.dateCodeEntry.GetText()
	if dateText == "" {
		if code, decoded := datecode.ExtractDateCode(text, 1990); decoded != nil {
			cp.dateCodeEntry.SetText(code)
			fmt.Printf("[OCR] Decoded date: %s -> %s\n", code, decoded.String())
		} else if info.DateCode != "" {
			cp.dateCodeEntry.SetText(info.DateCode)
		}
	}
	placeText, _ := cp.placeEntry.GetText()
	if info.Place != "" && placeText == "" {
		cp.placeEntry.SetText(info.Place)
	}

	// Auto-fill package from component library or part database
	partNum, _ := cp.partNumberEntry.GetText()
	if partNum == "" {
		partNum = info.PartNumber
	}
	pkgText, _ := cp.packageEntry.GetText()
	if partNum != "" && pkgText == "" {
		if libPart := cp.state.ComponentLibrary.FindByPartNumber(partNum); libPart != nil {
			cp.packageEntry.SetText(libPart.Package)
			cp.editingComp.Package = libPart.Package
			fmt.Printf("[OCR] Library lookup: %s -> %s (%d pins)\n", partNum, libPart.Package, libPart.PinCount)
		}
	}

	cp.state.LastOCROrientation = orientation
	fmt.Printf("[OCR] Complete: %s\n", text)
}

// showOCRPreview displays a window with three processing phases.
func (cp *ComponentsPanel) showOCRPreview(raw, masked *image.RGBA, orientation string) {
	w, h := raw.Bounds().Dx(), raw.Bounds().Dy()
	fmt.Printf("[OCR Preview] %dx%d orientation=%s\n", w, h, orientation)

	// Helper: scale an RGBA image to 2x NRGBA
	scale2x := func(src *image.RGBA) *image.NRGBA {
		sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
		out := image.NewNRGBA(image.Rect(0, 0, sw*2, sh*2))
		srcPix := src.Pix
		srcStride := src.Stride
		dstPix := out.Pix
		dstStride := out.Stride
		for y := 0; y < sh; y++ {
			for x := 0; x < sw; x++ {
				si := y*srcStride + x*4
				r, g, b, a := srcPix[si], srcPix[si+1], srcPix[si+2], srcPix[si+3]
				for _, dy := range [2]int{0, 1} {
					for _, dx := range [2]int{0, 1} {
						di := (y*2+dy)*dstStride + (x*2+dx)*4
						dstPix[di] = r
						dstPix[di+1] = g
						dstPix[di+2] = b
						dstPix[di+3] = a
					}
				}
			}
		}
		return out
	}

	// Helper: Otsu threshold an RGBA image to 2x B&W NRGBA
	otsuBW := func(src *image.RGBA) (*image.NRGBA, uint8) {
		gray, sw, sh := rgbaToGray(src)
		thresh := robustOtsu(gray, sw, sh)
		bw := make([]bool, sw*sh)
		for i := 0; i < sw*sh; i++ {
			bw[i] = gray[i] > thresh
		}
		despeckleBW(bw, sw, sh)
		out := image.NewNRGBA(image.Rect(0, 0, sw*2, sh*2))
		outPix := out.Pix
		outStride := out.Stride
		for y := 0; y < sh; y++ {
			for x := 0; x < sw; x++ {
				var v byte
				if bw[y*sw+x] {
					v = 255
				}
				for _, dy := range [2]int{0, 1} {
					for _, dx := range [2]int{0, 1} {
						di := (y*2+dy)*outStride + (x*2+dx)*4
						outPix[di] = v
						outPix[di+1] = v
						outPix[di+2] = v
						outPix[di+3] = 255
					}
				}
			}
		}
		return out, thresh
	}

	rawScaled := scale2x(raw)
	rawBW, rawThresh := otsuBW(raw)
	fmt.Printf("[OCR Preview] Raw Otsu threshold: %d\n", rawThresh)
	maskedBW, maskedThresh := otsuBW(masked)
	fmt.Printf("[OCR Preview] Masked Otsu threshold: %d\n", maskedThresh)

	// Build GTK preview window
	title := fmt.Sprintf("OCR Preview — %s (%s)", cp.editingComp.ID, orientation)
	previewWin, _ := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	previewWin.SetTitle(title)
	previewWin.SetDefaultSize(w*2+60, h*6+280)
	previewWin.SetTransientFor(cp.win)

	content, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	content.SetMarginStart(8)
	content.SetMarginEnd(8)
	content.SetMarginTop(8)
	content.SetMarginBottom(8)

	addImage := func(label string, img *image.NRGBA) {
		lbl, _ := gtk.LabelNew(label)
		lbl.SetHAlign(gtk.ALIGN_START)
		content.PackStart(lbl, false, false, 0)

		iw, ih := img.Bounds().Dx(), img.Bounds().Dy()
		pixbuf, err := gdk.PixbufNew(gdk.COLORSPACE_RGB, true, 8, iw, ih)
		if err == nil {
			pixels := pixbuf.GetPixels()
			stride := pixbuf.GetRowstride()
			for y := 0; y < ih; y++ {
				for x := 0; x < iw; x++ {
					si := y*img.Stride + x*4
					di := y*stride + x*4
					if di+3 < len(pixels) && si+3 < len(img.Pix) {
						pixels[di] = img.Pix[si]
						pixels[di+1] = img.Pix[si+1]
						pixels[di+2] = img.Pix[si+2]
						pixels[di+3] = img.Pix[si+3]
					}
				}
			}
			gtkImg, _ := gtk.ImageNewFromPixbuf(pixbuf)
			content.PackStart(gtkImg, false, false, 0)
		}
	}

	addImage("Color", rawScaled)
	addImage(fmt.Sprintf("B&W (Otsu %d)", rawThresh), rawBW)
	addImage(fmt.Sprintf("Logo Masked B&W (Otsu %d)", maskedThresh), maskedBW)

	sizeLabel, _ := gtk.LabelNew(fmt.Sprintf("%dx%d", w, h))
	content.PackStart(sizeLabel, false, false, 0)

	scroll, _ := gtk.ScrolledWindowNew(nil, nil)
	scroll.Add(content)
	previewWin.Add(scroll)
	previewWin.ShowAll()

	// Set always-on-top via wmctrl
	go func() {
		time.Sleep(200 * time.Millisecond)
		exec.Command("wmctrl", "-r", title, "-b", "add,above").Run()
	}()
}

// runOCRTraining adds the current component image + corrected text as a training sample.
// Uses current best params to run OCR once, scores the result, and stores the sample.
func (cp *ComponentsPanel) runOCRTraining() {
	if cp.editingComp == nil {
		fmt.Println("[OCR Train] No component selected")
		return
	}

	groundTruth := getTextViewText(cp.correctedTextEntry)
	if strings.TrimSpace(groundTruth) == "" {
		fmt.Println("[OCR Train] No ground truth provided")
		return
	}

	img := cp.getComponentImage()
	if img == nil {
		fmt.Println("[OCR Train] No image available")
		return
	}

	bounds := cp.editingComp.Bounds
	x, y := int(bounds.X), int(bounds.Y)
	w, h := int(bounds.Width), int(bounds.Height)

	imgBounds := img.Bounds()
	if x < imgBounds.Min.X {
		x = imgBounds.Min.X
	}
	if y < imgBounds.Min.Y {
		y = imgBounds.Min.Y
	}
	w = min(w, imgBounds.Max.X-x)
	h = min(h, imgBounds.Max.Y-y)
	if w <= 0 || h <= 0 {
		return
	}

	cropped := image.NewRGBA(image.Rect(0, 0, w, h))
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			cropped.Set(dx, dy, img.At(x+dx, y+dy))
		}
	}

	orientation := cp.getSelectedOrientation()
	logoRotation := orientationToRotation(orientation)
	cp.trainLogoDetection(cropped, w, h, groundTruth, logoRotation)

	comp := cp.editingComp
	compID := comp.ID

	// Get current best params from training DB (or defaults)
	params := cp.state.GetRecommendedOCRParams()
	if cp.state.GlobalOCRTraining != nil {
		if p, ok := cp.state.GlobalOCRTraining.GetParamsForOrientation(orientation); ok {
			params = p
		}
	}

	fmt.Printf("[OCR Train] %s: adding training sample (orientation %s)\n", compID, orientation)

	go func() {
		rotated := rotateForOCR(cropped, orientation)
		rotBounds := rotated.Bounds()
		mat, err := gocv.NewMatFromBytes(rotBounds.Dy(), rotBounds.Dx(), gocv.MatTypeCV8UC4, rotated.Pix)
		if err != nil {
			fmt.Printf("[OCR Train] %s: failed to convert image: %v\n", compID, err)
			return
		}
		defer mat.Close()

		bgr := gocv.NewMat()
		defer bgr.Close()
		gocv.CvtColor(mat, &bgr, gocv.ColorRGBAToBGR)

		engine, err := ocr.NewEngine()
		if err != nil {
			fmt.Printf("[OCR Train] %s: failed to create engine: %v\n", compID, err)
			return
		}
		defer engine.Close()

		// Run OCR once with current best params
		ocrText, _ := engine.RecognizeWithParams(bgr, params)
		score := ocr.TextSimilarity(ocrText, groundTruth)
		fmt.Printf("[OCR Train] %s: score=%.1f%% text=%q\n", compID, score*100, ocrText)

		// Always add — the ground truth is known, that's the whole point
		cp.state.AddOCRTrainingSample(groundTruth, ocrText, score, orientation, params)
		fmt.Printf("[OCR Train] %s: added to training database\n", compID)

		glib.IdleAdd(func() {
			cp.updateOCRTrainingLabel()
			if cp.editingComp == comp {
				setTextViewText(cp.ocrTextEntry, ocrText)
				comp.OCRText = ocrText
			}
		})
	}()

	// Parse ground truth into form fields
	info := parseComponentInfo(groundTruth)
	partText, _ := cp.partNumberEntry.GetText()
	if info.PartNumber != "" && partText == "" {
		cp.partNumberEntry.SetText(info.PartNumber)
		cp.editingComp.PartNumber = info.PartNumber
	}
	mfrText, _ := cp.manufacturerEntry.GetText()
	if info.Manufacturer != "" && mfrText == "" {
		cp.manufacturerEntry.SetText(info.Manufacturer)
		cp.editingComp.Manufacturer = info.Manufacturer
	}
	dateText, _ := cp.dateCodeEntry.GetText()
	if info.DateCode != "" && dateText == "" {
		cp.dateCodeEntry.SetText(info.DateCode)
		cp.editingComp.DateCode = info.DateCode
	}
	placeText, _ := cp.placeEntry.GetText()
	if info.Place != "" && placeText == "" {
		cp.placeEntry.SetText(info.Place)
		cp.editingComp.Place = info.Place
	}

	cp.editingComp.CorrectedText = groundTruth
	cp.state.LastOCROrientation = orientation
	cp.state.SetModified(true)

	fmt.Printf("[OCR Train] Parsed: part=%q mfr=%q date=%q place=%q\n",
		info.PartNumber, info.Manufacturer, info.DateCode, info.Place)
}

// trainLogoDetection compares detected logos to ground truth.
func (cp *ComponentsPanel) trainLogoDetection(cropped *image.RGBA, w, h int, groundTruth string, rotation int) {
	if cp.state.LogoLibrary == nil || len(cp.state.LogoLibrary.Logos) == 0 {
		return
	}
	expectedLogos := extractLogoNames(groundTruth)
	if len(expectedLogos) == 0 {
		return
	}
	fmt.Printf("[Logo Train] Expected: %v\n", expectedLogos)

	searchBounds := geometry.RectInt{X: 0, Y: 0, Width: w, Height: h}
	detectedMatches := cp.state.LogoLibrary.DetectLogos(cropped, searchBounds, 0.70, rotation)
	detectedLogos := make(map[string]logo.LogoMatch)
	for _, m := range detectedMatches {
		detectedLogos[m.Logo.Name] = m
	}

	for _, expected := range expectedLogos {
		if _, found := detectedLogos[expected]; found {
			fmt.Printf("[Logo Train] %s: detected\n", expected)
		} else {
			fmt.Printf("[Logo Train] %s: MISSED\n", expected)
		}
	}
	expectedSet := make(map[string]bool)
	for _, e := range expectedLogos {
		expectedSet[e] = true
	}
	for name, m := range detectedLogos {
		if !expectedSet[name] {
			fmt.Printf("[Logo Train] %s: FALSE POSITIVE (score=%.2f)\n", name, m.Score)
		}
	}
}

// showComponentListMenu shows a right-click context menu for a component in the list.
func (cp *ComponentsPanel) showComponentListMenu(compIdx int) {
	if compIdx < 0 || compIdx >= len(cp.state.Components) {
		return
	}
	menu, _ := gtk.MenuNew()
	item, _ := gtk.MenuItemNewWithLabel("Delete")
	item.Connect("activate", func() {
		cp.deleteComponent(compIdx)
	})
	menu.Append(item)
	menu.ShowAll()
	menu.PopupAtPointer(nil)
}

// deleteComponent removes a component by index.
func (cp *ComponentsPanel) deleteComponent(index int) {
	if index < 0 || index >= len(cp.state.Components) {
		return
	}
	comp := cp.state.Components[index]
	fmt.Printf("Deleting component %s (%s)\n", comp.ID, comp.Package)
	cp.state.Components = append(cp.state.Components[:index], cp.state.Components[index+1:]...)
	cp.state.SetModified(true)
	cp.rebuildSortedIndices()
	cp.refreshList()
	cp.updateComponentOverlay()
}

// highlightComponent scrolls the canvas to show the component at the given index.
func (cp *ComponentsPanel) highlightComponent(index int, scrollToView bool) {
	cp.updateComponentOverlay()
	if scrollToView && index >= 0 && index < len(cp.state.Components) {
		comp := cp.state.Components[index]
		cp.canvas.ScrollToRegion(int(comp.Bounds.X), int(comp.Bounds.Y),
			int(comp.Bounds.Width), int(comp.Bounds.Height))
	}
}

// OnKeyPressed handles keyboard input for component adjustment.
func (cp *ComponentsPanel) OnKeyPressed(ev *gdk.EventKey) bool {
	if cp.editingIndex < 0 || cp.editingIndex >= len(cp.state.Components) {
		return false
	}

	dpi := cp.state.DPI
	if dpi <= 0 {
		dpi = 1200
	}
	step := dpi * 0.00394 // 0.1mm in pixels

	comp := cp.state.Components[cp.editingIndex]
	keyval := ev.KeyVal()

	switch keyval {
	case gdk.KEY_Up, gdk.KEY_KP_Up:
		comp.Bounds.Y -= step
	case gdk.KEY_Down, gdk.KEY_KP_Down:
		comp.Bounds.Y += step
	case gdk.KEY_Left, gdk.KEY_KP_Left:
		comp.Bounds.X -= step
	case gdk.KEY_Right, gdk.KEY_KP_Right:
		comp.Bounds.X += step
	case gdk.KEY_KP_Add:
		comp.Bounds.Height += step
	case gdk.KEY_KP_Subtract:
		if comp.Bounds.Height > step {
			comp.Bounds.Height -= step
		}
	case gdk.KEY_KP_Multiply:
		comp.Bounds.Width += step
	case gdk.KEY_KP_Divide:
		if comp.Bounds.Width > step {
			comp.Bounds.Width -= step
		}
	default:
		return false
	}

	cp.state.SetModified(true)
	cp.updateComponentOverlay()
	cp.highlightComponent(cp.editingIndex, false)

	fmt.Printf("Adjusted component %s: pos=(%.1f,%.1f) size=%.1fx%.1f\n",
		comp.ID, comp.Bounds.X, comp.Bounds.Y, comp.Bounds.Width, comp.Bounds.Height)
	return true
}

// frontLayerOffset returns the manual offset of the front layer, matching
// the offset applied by the overlay renderer for LayerFront overlays.
func (cp *ComponentsPanel) frontLayerOffset() (float64, float64) {
	for _, layer := range cp.canvas.GetLayers() {
		if layer != nil && layer.Side == pcbimage.SideFront && !layer.IsNormalized {
			return float64(layer.ManualOffsetX), float64(layer.ManualOffsetY)
		}
	}
	return 0, 0
}

// onRightClickDeleteComponent handles right-click to delete components on the canvas.
func (cp *ComponentsPanel) onRightClickDeleteComponent(x, y float64) {
	offX, offY := cp.frontLayerOffset()
	for i, comp := range cp.state.Components {
		if x >= comp.Bounds.X+offX && x <= comp.Bounds.X+offX+comp.Bounds.Width &&
			y >= comp.Bounds.Y+offY && y <= comp.Bounds.Y+offY+comp.Bounds.Height {
			cp.deleteComponent(i)
			return
		}
	}
}

// nextNewID returns the next sequential "NEW" ID (NEW1, NEW2, ...) by scanning existing components.
func (cp *ComponentsPanel) nextNewID() string {
	maxN := 0
	for _, c := range cp.state.Components {
		if strings.HasPrefix(c.ID, "NEW") {
			if n, err := strconv.Atoi(c.ID[3:]); err == nil && n > maxN {
				maxN = n
			}
		}
	}
	return fmt.Sprintf("NEW%d", maxN+1)
}

// OnLeftClick handles left-click: select component if inside bounds, resize if near edge.
func (cp *ComponentsPanel) OnLeftClick(x, y float64) {
	const edgeThreshold = 10.0

	// Click coords are in image space (includes layer offset).
	// Component bounds are in raw space. Offset-adjust for hit-testing.
	offX, offY := cp.frontLayerOffset()

	for i, comp := range cp.state.Components {
		left := comp.Bounds.X + offX
		right := comp.Bounds.X + offX + comp.Bounds.Width
		top := comp.Bounds.Y + offY
		bottom := comp.Bounds.Y + offY + comp.Bounds.Height

		// Check if click is inside (with edge threshold margin)
		if y < top-edgeThreshold || y > bottom+edgeThreshold {
			continue
		}
		if x < left-edgeThreshold || x > right+edgeThreshold {
			continue
		}

		distLeft := abs64(x - left)
		distRight := abs64(x - right)
		distTop := abs64(y - top)
		distBottom := abs64(y - bottom)

		minEdgeDist := distLeft
		edge := "left"
		if distRight < minEdgeDist {
			minEdgeDist = distRight
			edge = "right"
		}
		if distTop < minEdgeDist {
			minEdgeDist = distTop
			edge = "top"
		}
		if distBottom < minEdgeDist {
			minEdgeDist = distBottom
			edge = "bottom"
		}

		// Near an edge — resize (store back in raw coords)
		if minEdgeDist <= edgeThreshold {
			rawX := x - offX
			rawY := y - offY
			switch edge {
			case "left":
				delta := rawX - comp.Bounds.X
				cp.state.Components[i].Bounds.X = rawX
				cp.state.Components[i].Bounds.Width -= delta
			case "right":
				cp.state.Components[i].Bounds.Width = rawX - comp.Bounds.X
			case "top":
				delta := rawY - comp.Bounds.Y
				cp.state.Components[i].Bounds.Y = rawY
				cp.state.Components[i].Bounds.Height -= delta
			case "bottom":
				cp.state.Components[i].Bounds.Height = rawY - comp.Bounds.Y
			}
			cp.state.SetModified(true)
			cp.updateComponentOverlay()
			fmt.Printf("Resized component %s %s edge to %.0f\n", comp.ID, edge, map[string]float64{
				"left": x, "right": x, "top": y, "bottom": y,
			}[edge])
			return
		}

		// Inside the component — select it
		cp.selectComponentByIndex(i)
		return
	}

	// Click didn't hit any component — create a new one at click point
	// Convert click to raw coords for storage
	rawX := x - offX
	rawY := y - offY

	dpi := cp.state.DPI
	if dpi <= 0 {
		dpi = 1200
	}

	// Compute average width/height from training data or existing components
	var avgW, avgH float64
	if cp.state.GlobalComponentTraining != nil && len(cp.state.GlobalComponentTraining.Samples) > 0 {
		for _, s := range cp.state.GlobalComponentTraining.Samples {
			avgW += s.WidthMM
			avgH += s.HeightMM
		}
		n := float64(len(cp.state.GlobalComponentTraining.Samples))
		avgW = (avgW / n) * dpi / 25.4
		avgH = (avgH / n) * dpi / 25.4
	} else if len(cp.state.Components) > 0 {
		for _, c := range cp.state.Components {
			avgW += c.Bounds.Width
			avgH += c.Bounds.Height
		}
		n := float64(len(cp.state.Components))
		avgW /= n
		avgH /= n
	} else {
		// Fallback: ~10mm x 5mm
		avgW = dpi * 10.0 / 25.4
		avgH = dpi * 5.0 / 25.4
	}

	newX := rawX - avgW/2
	newY := rawY - avgH/2
	compID := cp.nextNewID()

	newComp := &component.Component{
		ID: compID,
		Bounds: geometry.Rect{
			X:      newX,
			Y:      newY,
			Width:  avgW,
			Height: avgH,
		},
		Confirmed: true,
	}

	cp.state.Components = append(cp.state.Components, newComp)
	cp.state.SetModified(true)
	cp.rebuildSortedIndices()
	cp.refreshList()

	fmt.Printf("Created component %s at (%.0f,%.0f) size %.0fx%.0f\n",
		compID, newX, newY, avgW, avgH)

	// Select the new component for editing
	newCompIdx := len(cp.state.Components) - 1
	for sortedPos, compIdx := range cp.sortedIndices {
		if compIdx == newCompIdx {
			row := cp.listBox.GetRowAtIndex(sortedPos)
			if row != nil {
				cp.listBox.SelectRow(row)
			}
			cp.showEditDialog(newCompIdx)
			break
		}
	}

	cp.updateComponentOverlay()
}

// selectComponentByIndex selects a component in both the edit form and the list.
func (cp *ComponentsPanel) selectComponentByIndex(index int) {
	if index == cp.editingIndex {
		return
	}
	cp.showEditDialog(index)

	// Find the sorted position for this component index and select the list row
	for sortedPos, compIdx := range cp.sortedIndices {
		if compIdx == index {
			row := cp.listBox.GetRowAtIndex(sortedPos)
			if row != nil {
				cp.listBox.SelectRow(row)
				// Scroll the list to make the selected row visible
				alloc := row.GetAllocation()
				adj := cp.listScroll.GetVAdjustment()
				upper := adj.GetUpper()
				pageSize := adj.GetPageSize()
				rowY := float64(alloc.GetY())
				rowH := float64(alloc.GetHeight())
				current := adj.GetValue()
				if rowY < current {
					adj.SetValue(rowY)
				} else if rowY+rowH > current+pageSize && upper > pageSize {
					adj.SetValue(rowY + rowH - pageSize)
				}
			}
			break
		}
	}
}

// DeselectComponent clears the current component selection.
func (cp *ComponentsPanel) DeselectComponent() {
	if cp.editingIndex < 0 {
		return
	}
	cp.editingIndex = -1
	cp.editingComp = nil
	cp.listBox.UnselectAll()
	cp.updateComponentOverlay()
}

// OnMiddleClickFloodFill handles middle-click for flood fill component detection.
func (cp *ComponentsPanel) OnMiddleClickFloodFill(x, y float64) {
	img := cp.canvas.GetRenderedOutput()
	if img == nil {
		fmt.Println("No rendered image available for flood fill")
		return
	}

	clickX, clickY := int(x), int(y)
	const colorTolerance = 25

	fmt.Printf("Middle-click flood fill at canvas (%d, %d)\n", clickX, clickY)

	result, err := component.FloodFillDetect(img, clickX, clickY, colorTolerance)
	if err != nil {
		fmt.Printf("Flood fill failed: %v\n", err)
		return
	}

	const gridStep = 3
	const minScore = 0.25

	gridScores := component.GetGridScores(img, result.Bounds, result.SeedColor, colorTolerance, gridStep, minScore)
	trimmedBounds := gridScores.TrimBounds

	zoom := cp.canvas.GetZoom()

	compID := cp.nextNewID()

	dpi := cp.state.DPI
	if dpi <= 0 {
		dpi = 1200
	}
	effectiveDPI := dpi * zoom
	mmToPixels := effectiveDPI / 25.4

	widthMM := float64(trimmedBounds.Width) / mmToPixels
	heightMM := float64(trimmedBounds.Height) / mmToPixels

	pkgType := "UNKNOWN"
	if component.IsValidDIPWidth(widthMM) || component.IsValidDIPWidth(heightMM) {
		dipLength := heightMM
		if component.IsValidDIPWidth(heightMM) {
			dipLength = widthMM
		}
		pinCount := int(dipLength/2.54) * 2
		if pinCount >= 8 && pinCount <= 40 {
			pkgType = fmt.Sprintf("DIP-%d", pinCount)
		}
	}

	newComp := &component.Component{
		ID:      compID,
		Package: pkgType,
		Bounds: geometry.Rect{
			X:      float64(trimmedBounds.X) / zoom,
			Y:      float64(trimmedBounds.Y) / zoom,
			Width:  float64(trimmedBounds.Width) / zoom,
			Height: float64(trimmedBounds.Height) / zoom,
		},
		Confirmed: true,
	}

	cp.state.Components = append(cp.state.Components, newComp)
	cp.state.SetModified(true)
	cp.rebuildSortedIndices()
	cp.refreshList()

	fmt.Printf("Created component %s (%s) at (%.0f,%.0f) size %.0fx%.0f (%.1fx%.1f mm)\n",
		compID, pkgType, newComp.Bounds.X, newComp.Bounds.Y,
		newComp.Bounds.Width, newComp.Bounds.Height, widthMM, heightMM)

	// Select the new component for editing
	newCompIdx := len(cp.state.Components) - 1
	for sortedPos, compIdx := range cp.sortedIndices {
		if compIdx == newCompIdx {
			row := cp.listBox.GetRowAtIndex(sortedPos)
			if row != nil {
				cp.listBox.SelectRow(row)
			}
			cp.showEditDialog(newCompIdx)
			break
		}
	}

	cp.updateComponentOverlay()
}

// showGridDebugOverlay creates an overlay showing grid scoring points.
func (cp *ComponentsPanel) showGridDebugOverlay(gridScores *component.GridScoreResult) {
	if gridScores == nil || len(gridScores.Points) == 0 {
		return
	}
	zoom := cp.canvas.GetZoom()

	matchOverlay := &canvas.Overlay{
		Color:   color.RGBA{R: 0, G: 255, B: 0, A: 200},
		Circles: make([]canvas.OverlayCircle, 0),
	}
	noMatchOverlay := &canvas.Overlay{
		Color:   color.RGBA{R: 255, G: 0, B: 0, A: 200},
		Circles: make([]canvas.OverlayCircle, 0),
	}
	trimmedOutOverlay := &canvas.Overlay{
		Color:   color.RGBA{R: 128, G: 128, B: 128, A: 150},
		Circles: make([]canvas.OverlayCircle, 0),
	}

	radius := 1.5
	for _, pt := range gridScores.Points {
		imgX := float64(pt.X) / zoom
		imgY := float64(pt.Y) / zoom
		circle := canvas.OverlayCircle{X: imgX, Y: imgY, Radius: radius, Filled: true}

		inTrimmed := pt.X >= gridScores.TrimBounds.X &&
			pt.X < gridScores.TrimBounds.X+gridScores.TrimBounds.Width &&
			pt.Y >= gridScores.TrimBounds.Y &&
			pt.Y < gridScores.TrimBounds.Y+gridScores.TrimBounds.Height

		if !inTrimmed {
			trimmedOutOverlay.Circles = append(trimmedOutOverlay.Circles, circle)
		} else if pt.Matches {
			matchOverlay.Circles = append(matchOverlay.Circles, circle)
		} else {
			noMatchOverlay.Circles = append(noMatchOverlay.Circles, circle)
		}
	}

	cp.canvas.SetOverlay("grid_match", matchOverlay)
	cp.canvas.SetOverlay("grid_nomatch", noMatchOverlay)
	cp.canvas.SetOverlay("grid_trimmed", trimmedOutOverlay)
}

// updateComponentOverlay refreshes the component overlay on the canvas.
func (cp *ComponentsPanel) updateComponentOverlay() {
	if len(cp.state.Components) == 0 {
		cp.canvas.SetOverlay("components", nil)
		cp.canvas.Refresh()
		return
	}
	magenta := color.RGBA{R: 255, G: 0, B: 255, A: 255}
	overlay := &canvas.Overlay{
		Color: color.RGBA{R: 255, G: 255, B: 255, A: 255},
		Layer: canvas.LayerFront,
	}
	for i, comp := range cp.state.Components {
		rect := canvas.OverlayRect{
			X:      int(comp.Bounds.X),
			Y:      int(comp.Bounds.Y),
			Width:  int(comp.Bounds.Width),
			Height: int(comp.Bounds.Height),
			Label:  comp.ID + " " + comp.Package,
			Fill:   canvas.FillNone,
		}
		if i == cp.editingIndex {
			rect.Color = &magenta
		}
		overlay.Rectangles = append(overlay.Rectangles, rect)
	}
	cp.canvas.SetOverlay("components", overlay)
	cp.canvas.Refresh()
}

// onTrainComponents adds existing components to the global training set without detecting.
func (cp *ComponentsPanel) onTrainComponents() {
	if len(cp.state.Components) == 0 {
		fmt.Println("[Train] No components to train from")
		return
	}
	if cp.state.FrontImage == nil || cp.state.FrontImage.Image == nil {
		fmt.Println("[Train] No front image loaded")
		return
	}

	frontImg := cp.state.FrontImage.Image
	dpi := cp.state.DPI
	if dpi <= 0 {
		dpi = 1200
	}

	if cp.state.GlobalComponentTraining == nil {
		cp.state.GlobalComponentTraining = component.NewTrainingSet()
	}

	added := 0
	for _, comp := range cp.state.Components {
		sample := component.ExtractSampleFeatures(frontImg, comp.Bounds, dpi)
		sample.Reference = comp.ID
		cp.state.GlobalComponentTraining.Add(sample)
		added++
	}

	cp.state.SaveGlobalComponentTraining()
	fmt.Printf("Trained %d component samples (global total: %d)\n",
		added, len(cp.state.GlobalComponentTraining.Samples))
}

// onDetectComponents detects components using global training data plus any on this board.
func (cp *ComponentsPanel) onDetectComponents() {
	if cp.state.FrontImage == nil || cp.state.FrontImage.Image == nil {
		fmt.Println("[Detect] No front image loaded")
		return
	}

	frontImg := cp.state.FrontImage.Image
	dpi := cp.state.DPI
	if dpi <= 0 {
		dpi = 1200
	}

	if cp.state.GlobalComponentTraining == nil {
		cp.state.GlobalComponentTraining = component.NewTrainingSet()
	}

	if len(cp.state.GlobalComponentTraining.Samples) == 0 {
		fmt.Println("[Detect] No training data available — use Train Components first")
		return
	}

	fmt.Printf("[Detect] Using %d global training samples\n", len(cp.state.GlobalComponentTraining.Samples))

	// Derive detection parameters from global training set
	params := cp.state.GlobalComponentTraining.DeriveParams()

	// Detect board bounds using variance grid
	mmToPixels := dpi / 25.4
	cellMM := params.CellSizeMM
	if cellMM <= 0 {
		cellMM = 2.0
	}
	cellSizePx := int(cellMM * mmToPixels)
	if cellSizePx < 4 {
		cellSizePx = 4
	}

	// Use a coarser cell for board detection (5x the component cell)
	boardCellPx := cellSizePx * 5
	board := component.DetectBoardBounds(frontImg, boardCellPx)

	if board == nil {
		fmt.Println("[Detect] Could not detect board bounds")
		return
	}

	if false {
		// Draw on-board cells in green, off-board in dim red, bounds in yellow
		green := color.RGBA{R: 0, G: 200, B: 0, A: 100}
		dimRed := color.RGBA{R: 150, G: 0, B: 0, A: 60}
		yellow := color.RGBA{R: 255, G: 255, B: 0, A: 255}

		gridOverlay := &canvas.Overlay{Color: yellow}

		for gy := 0; gy < board.GridRows; gy++ {
			for gx := 0; gx < board.GridCols; gx++ {
				c := &dimRed
				if board.OnBoard[gy*board.GridCols+gx] == 1 {
					c = &green
				}
				gridOverlay.Rectangles = append(gridOverlay.Rectangles, canvas.OverlayRect{
					X:      gx * boardCellPx,
					Y:      gy * boardCellPx,
					Width:  boardCellPx,
					Height: boardCellPx,
					Fill:   canvas.FillSolid,
					Color:  c,
				})
			}
		}

		// Board bounds rectangle in yellow
		gridOverlay.Rectangles = append(gridOverlay.Rectangles, canvas.OverlayRect{
			X:      int(board.Bounds.X),
			Y:      int(board.Bounds.Y),
			Width:  int(board.Bounds.Width),
			Height: int(board.Bounds.Height),
			Fill:   canvas.FillNone,
		})

		cp.canvas.SetOverlay("detect_bounds", gridOverlay)
		cp.canvas.Refresh()
	}
	fmt.Printf("[Detect] Board bounds: (%.0f,%.0f) %.0fx%.0f, threshold=%.1f\n",
		board.Bounds.X, board.Bounds.Y, board.Bounds.Width, board.Bounds.Height, board.Threshold)

	// Store board bounds in state for other operations
	cp.state.FrontBoardBounds = &geometry.RectInt{
		X: int(board.Bounds.X), Y: int(board.Bounds.Y),
		Width: int(board.Bounds.Width), Height: int(board.Bounds.Height),
	}

	// Run detection within board bounds
	boardRect := &board.Bounds
	result, err := component.DetectComponentsWithBounds(frontImg, dpi, params, boardRect)
	if err != nil {
		fmt.Printf("[Detect] Detection failed: %v\n", err)
		return
	}

	if result == nil {
		fmt.Println("[Detect] No result")
		return
	}

	// Visualize grid and connected regions
	if result.Grid != nil {
		cellHit := color.RGBA{R: 200, G: 0, B: 0, A: 80}
		gridOverlay := &canvas.Overlay{}

		// Draw all matched cells in dim red (disabled for debugging)
		if false {
		for gy := 0; gy < result.GridRows; gy++ {
			for gx := 0; gx < result.GridCols; gx++ {
				if result.Grid[gy*result.GridCols+gx] == 1 {
					gridOverlay.Rectangles = append(gridOverlay.Rectangles, canvas.OverlayRect{
						X:      result.ScanX + gx*result.CellSizePx,
						Y:      result.ScanY + gy*result.CellSizePx,
						Width:  result.CellSizePx,
						Height: result.CellSizePx,
						Fill:   canvas.FillSolid,
						Color:  &cellHit,
					})
				}
			}
		}
		}

		// Dump HSV stats for first refined bound
		if len(result.RefinedBounds) > 0 && result.HSVBytes != nil {
			rb := result.RefinedBounds[0]
			var hHist [181]int
			var sHist [256]int
			var vHist [256]int
			count := 0
			for y := rb.Min.Y; y < rb.Max.Y; y += 4 {
				for x := rb.Min.X; x < rb.Max.X; x += 4 {
					off := (y*result.HSVWidth + x) * result.HSVChannels
					if off+2 < len(result.HSVBytes) {
						h := result.HSVBytes[off]
						s := result.HSVBytes[off+1]
						v := result.HSVBytes[off+2]
						if int(h) <= 180 { hHist[h]++ }
						sHist[s]++
						vHist[v]++
						count++
					}
				}
			}
			fmt.Printf("[HSV stats] Region 0: %d samples\n", count)
			fmt.Printf("  H peaks: ")
			for i := 0; i <= 180; i++ {
				if hHist[i] > count/20 {
					fmt.Printf("H=%d(%d%%) ", i, hHist[i]*100/count)
				}
			}
			fmt.Println()
			fmt.Printf("  S range: ")
			s10, s50, s90 := 0, 0, 0
			cum := 0
			for i := 0; i < 256; i++ {
				cum += sHist[i]
				if s10 == 0 && cum >= count/10 { s10 = i }
				if s50 == 0 && cum >= count/2 { s50 = i }
				if s90 == 0 && cum >= count*9/10 { s90 = i }
			}
			fmt.Printf("p10=%d p50=%d p90=%d\n", s10, s50, s90)
			fmt.Printf("  V range: ")
			v10, v50, v90 := 0, 0, 0
			cum = 0
			for i := 0; i < 256; i++ {
				cum += vHist[i]
				if v10 == 0 && cum >= count/10 { v10 = i }
				if v50 == 0 && cum >= count/2 { v50 = i }
				if v90 == 0 && cum >= count*9/10 { v90 = i }
			}
			fmt.Printf("p10=%d p50=%d p90=%d\n", v10, v50, v90)
			// Count how many pass the green check
			greenCount := 0
			for y := rb.Min.Y; y < rb.Max.Y; y += 4 {
				for x := rb.Min.X; x < rb.Max.X; x += 4 {
					off := (y*result.HSVWidth + x) * result.HSVChannels
					if off+2 < len(result.HSVBytes) {
						h := result.HSVBytes[off]
						s := result.HSVBytes[off+1]
						v := result.HSVBytes[off+2]
						if h >= 35 && h <= 85 && s >= 30 && v >= 50 {
							greenCount++
						}
					}
				}
			}
			fmt.Printf("  Green check: %d/%d (%.1f%%)\n", greenCount, count, float64(greenCount)/float64(count)*100)
		}

		// Paint green board pixels inside each refined bound (disabled)
		// Uses the actual detector's HSV check via OpenCV for accuracy.
		if false && len(result.RefinedBounds) > 0 {
			greenMark := color.RGBA{R: 0, G: 255, B: 0, A: 120}
			step := 2
			// Get HSV bytes from the detection result's source image
			hsvBytes, hsvW, hsvCh := result.HSVBytes, result.HSVWidth, result.HSVChannels
			if hsvBytes != nil {
				for _, rb := range result.RefinedBounds {
					for y := rb.Min.Y; y < rb.Max.Y; y += step {
						for x := rb.Min.X; x < rb.Max.X; x += step {
							off := (y*hsvW + x) * hsvCh
							if off+2 < len(hsvBytes) {
								h := hsvBytes[off]
								s := hsvBytes[off+1]
								v := hsvBytes[off+2]
								if h >= 35 && h <= 85 && s >= 30 && v >= 50 {
									gridOverlay.Rectangles = append(gridOverlay.Rectangles, canvas.OverlayRect{
										X: x, Y: y, Width: step, Height: step,
										Fill: canvas.FillSolid, Color: &greenMark,
									})
								}
							}
						}
					}
				}
			}
		}

		// Draw region bounding boxes in yellow (grid coordinates)
		yellow := color.RGBA{R: 255, G: 255, B: 0, A: 200}
		if len(result.RefinedBounds) > 0 {
			// Stage 2+: draw refined pixel bounds
			for _, rb := range result.RefinedBounds {
				gridOverlay.Rectangles = append(gridOverlay.Rectangles, canvas.OverlayRect{
					X:      rb.Min.X,
					Y:      rb.Min.Y,
					Width:  rb.Max.X - rb.Min.X,
					Height: rb.Max.Y - rb.Min.Y,
					Fill:   canvas.FillNone,
					Color:  &yellow,
				})
			}
		} else {
			// Stage 1: draw raw grid-coordinate regions
			for _, r := range result.Regions {
				gridOverlay.Rectangles = append(gridOverlay.Rectangles, canvas.OverlayRect{
					X:      result.ScanX + r.Min.X*result.CellSizePx,
					Y:      result.ScanY + r.Min.Y*result.CellSizePx,
					Width:  (r.Max.X - r.Min.X) * result.CellSizePx,
					Height: (r.Max.Y - r.Min.Y) * result.CellSizePx,
					Fill:   canvas.FillNone,
					Color:  &yellow,
				})
			}
		}

		cp.canvas.SetOverlay("detect_grid", gridOverlay)
		cp.canvas.Refresh()
		fmt.Printf("[Detect] Visualizing %d regions\n", len(result.Regions))
	}

	// Build component bounds from refined bounds or raw grid regions
	var detectedBounds []geometry.Rect
	if len(result.RefinedBounds) > 0 {
		for _, rb := range result.RefinedBounds {
			detectedBounds = append(detectedBounds, geometry.Rect{
				X:      float64(rb.Min.X),
				Y:      float64(rb.Min.Y),
				Width:  float64(rb.Max.X - rb.Min.X),
				Height: float64(rb.Max.Y - rb.Min.Y),
			})
		}
	} else {
		for _, r := range result.Regions {
			detectedBounds = append(detectedBounds, geometry.Rect{
				X:      float64(result.ScanX + r.Min.X*result.CellSizePx),
				Y:      float64(result.ScanY + r.Min.Y*result.CellSizePx),
				Width:  float64((r.Max.X - r.Min.X) * result.CellSizePx),
				Height: float64((r.Max.Y - r.Min.Y) * result.CellSizePx),
			})
		}
	}

	if len(detectedBounds) == 0 {
		fmt.Println("[Detect] No components detected")
		return
	}

	// Filter out detections that overlap existing components
	var newBounds []geometry.Rect
	for _, db := range detectedBounds {
		centerX := db.X + db.Width/2
		centerY := db.Y + db.Height/2

		overlaps := false
		for _, existing := range cp.state.Components {
			if centerX >= existing.Bounds.X && centerX <= existing.Bounds.X+existing.Bounds.Width &&
				centerY >= existing.Bounds.Y && centerY <= existing.Bounds.Y+existing.Bounds.Height {
				overlaps = true
				break
			}
		}
		if !overlaps {
			newBounds = append(newBounds, db)
		}
	}

	if len(newBounds) == 0 {
		fmt.Printf("[Detect] %d candidates all overlap existing components\n", len(detectedBounds))
		return
	}

	// Create components from detected bounds
	for _, db := range newBounds {
		compID := cp.nextNewID()
		cp.state.Components = append(cp.state.Components, &component.Component{
			ID:     compID,
			Bounds: db,
		})
	}

	fmt.Printf("Detected %d new components from %d training samples\n", len(newBounds), len(cp.state.GlobalComponentTraining.Samples))

	cp.rebuildSortedIndices()
	cp.refreshList()
	cp.updateComponentOverlay()
	cp.state.SetModified(true)
}

// onOCRSilkscreen runs OCR on the silkscreen to find component labels.
func (cp *ComponentsPanel) onOCRSilkscreen() {
	if cp.state.FrontImage == nil || cp.state.FrontImage.Image == nil {
		fmt.Println("No front image loaded for OCR")
		return
	}

	fmt.Println("Starting silkscreen OCR...")

	engine, err := ocr.NewEngine()
	if err != nil {
		fmt.Printf("Failed to create OCR engine: %v\n", err)
		return
	}
	defer engine.Close()

	img := cp.state.FrontImage.Image
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	rgba := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			rgba.Set(x, y, img.At(x, y))
		}
	}

	mat, err := gocv.NewMatFromBytes(h, w, gocv.MatTypeCV8UC4, rgba.Pix)
	if err != nil {
		fmt.Printf("Failed to convert image: %v\n", err)
		return
	}
	defer mat.Close()

	bgr := gocv.NewMat()
	defer bgr.Close()
	gocv.CvtColor(mat, &bgr, gocv.ColorRGBAToBGR)

	result, err := engine.DetectSilkscreen(bgr)
	if err != nil {
		fmt.Printf("Silkscreen OCR error: %v\n", err)
		return
	}

	fmt.Printf("\n=== Silkscreen OCR Results ===\n")
	fmt.Printf("Found %d component designators:\n", len(result.Designators))

	counts := result.GetDesignatorCounts()
	for prefix, count := range counts {
		fmt.Printf("  %s: %d\n", prefix, count)
		designators := result.GetDesignatorsByType(prefix)
		for _, d := range designators {
			fmt.Printf("    %s at (%d,%d)\n", d.Text, d.Bounds.X, d.Bounds.Y)
		}
	}

	if result.XAxis != nil {
		fmt.Printf("\nX-Axis detected: %d markers\n", len(result.XAxis.Markers))
		for _, m := range result.XAxis.Markers {
			fmt.Printf("  %s at X=%d\n", m.Text, m.Bounds.X)
		}
	}
	if result.YAxis != nil {
		fmt.Printf("\nY-Axis detected: %d markers\n", len(result.YAxis.Markers))
		for _, m := range result.YAxis.Markers {
			fmt.Printf("  %s at Y=%d\n", m.Text, m.Bounds.Y)
		}
	}

	fmt.Printf("\nTotal text items found: %d\n", len(result.AllText))
	fmt.Printf("==============================\n")

	cp.updateOCROverlay(result)
}

// updateOCROverlay shows detected silkscreen text on the canvas.
func (cp *ComponentsPanel) updateOCROverlay(result *ocr.SilkscreenResult) {
	if result == nil || len(result.AllText) == 0 {
		cp.canvas.SetOverlay("ocr", nil)
		cp.canvas.Refresh()
		return
	}

	overlay := &canvas.Overlay{
		Color: color.RGBA{R: 0, G: 255, B: 255, A: 255},
		Layer: canvas.LayerFront,
	}
	for _, d := range result.Designators {
		rect := canvas.OverlayRect{
			X: d.Bounds.X, Y: d.Bounds.Y,
			Width: d.Bounds.Width, Height: d.Bounds.Height,
			Label: d.Text, Fill: canvas.FillNone,
		}
		overlay.Rectangles = append(overlay.Rectangles, rect)
	}
	cp.canvas.SetOverlay("ocr", overlay)
	cp.canvas.Refresh()
}

// ---- Standalone helper functions for OCR processing ----

// robustOtsu computes an Otsu threshold resistant to small bright/dark artifacts.
func robustOtsu(gray []uint8, w, h int) uint8 {
	cols, rows := 2, 4
	type region struct {
		mean float64
		hist [256]int
		n    int
	}
	regions := make([]region, cols*rows)

	for ry := 0; ry < rows; ry++ {
		y0 := ry * h / rows
		y1 := (ry + 1) * h / rows
		for rx := 0; rx < cols; rx++ {
			x0 := rx * w / cols
			x1 := (rx + 1) * w / cols
			r := &regions[ry*cols+rx]
			var sum int64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					v := gray[y*w+x]
					r.hist[v]++
					sum += int64(v)
					r.n++
				}
			}
			if r.n > 0 {
				r.mean = float64(sum) / float64(r.n)
			}
		}
	}

	sort.Slice(regions, func(i, j int) bool {
		return regions[i].mean < regions[j].mean
	})

	var hist [256]int
	total := 0
	for _, r := range regions[2:6] {
		for i := 0; i < 256; i++ {
			hist[i] += r.hist[i]
		}
		total += r.n
	}

	fmt.Printf("[Otsu] Region means: ")
	for i, r := range regions {
		mark := " "
		if i < 2 || i >= 6 {
			mark = "x"
		}
		fmt.Printf("%.0f%s ", r.mean, mark)
	}
	fmt.Println()

	var sum float64
	for i := 0; i < 256; i++ {
		sum += float64(i) * float64(hist[i])
	}
	var sumB float64
	var wB, wF int
	var maxVar float64
	thresh := uint8(128)
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
		variance := float64(wB) * float64(wF) * (mB - mF) * (mB - mF)
		if variance > maxVar {
			maxVar = variance
			thresh = uint8(t)
		}
	}

	minWhiteRatio := 0.10
	countWhite := func(t uint8) int {
		n := 0
		for i := int(t) + 1; i < 256; i++ {
			n += hist[i]
		}
		return n
	}
	whiteCount := countWhite(thresh)
	whiteRatio := float64(whiteCount) / float64(total)
	fmt.Printf("[Otsu] thresh=%d white=%.1f%%\n", thresh, whiteRatio*100)

	if whiteRatio < minWhiteRatio {
		lo, hi := uint8(0), thresh
		for lo < hi {
			mid := (lo + hi) / 2
			ratio := float64(countWhite(mid)) / float64(total)
			if ratio < minWhiteRatio {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		if lo > 0 {
			lo--
		}
		newRatio := float64(countWhite(lo)) / float64(total)
		fmt.Printf("[Otsu] Adjusted for faded: %d -> %d (white %.1f%% -> %.1f%%)\n",
			thresh, lo, whiteRatio*100, newRatio*100)
		thresh = lo
	}
	return thresh
}

// rgbaToGray converts an RGBA image to a grayscale byte slice.
func rgbaToGray(src *image.RGBA) ([]uint8, int, int) {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	srcPix := src.Pix
	srcStride := src.Stride
	gray := make([]uint8, sw*sh)
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			si := y*srcStride + x*4
			gray[y*sw+x] = uint8((299*uint32(srcPix[si]) + 587*uint32(srcPix[si+1]) + 114*uint32(srcPix[si+2])) / 1000)
		}
	}
	return gray, sw, sh
}

// despeckleBW clears isolated set bits with no 4-connected neighbors.
func despeckleBW(bw []bool, w, h int) {
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !bw[y*w+x] {
				continue
			}
			hasNeighbor := false
			for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
				nx, ny := x+d[0], y+d[1]
				if nx >= 0 && nx < w && ny >= 0 && ny < h && bw[ny*w+nx] {
					hasNeighbor = true
					break
				}
			}
			if !hasNeighbor {
				bw[y*w+x] = false
			}
		}
	}
}

func orientationToRotation(orientation string) int {
	switch orientation {
	case "S":
		return 180
	case "E":
		return 90
	case "W":
		return 270
	default:
		return 0
	}
}

func rotateForOCR(img *image.RGBA, orientation string) *image.RGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	srcPix := img.Pix
	srcStride := img.Stride

	switch orientation {
	case "S":
		out := image.NewRGBA(image.Rect(0, 0, w, h))
		dstPix := out.Pix
		for y := 0; y < h; y++ {
			srcRow := y * srcStride
			dstRow := (h - 1 - y) * out.Stride
			for x := 0; x < w; x++ {
				si := srcRow + x*4
				di := dstRow + (w-1-x)*4
				dstPix[di] = srcPix[si]
				dstPix[di+1] = srcPix[si+1]
				dstPix[di+2] = srcPix[si+2]
				dstPix[di+3] = srcPix[si+3]
			}
		}
		return out
	case "E":
		out := image.NewRGBA(image.Rect(0, 0, h, w))
		dstPix := out.Pix
		dstStride := out.Stride
		for y := 0; y < h; y++ {
			srcRow := y * srcStride
			for x := 0; x < w; x++ {
				si := srcRow + x*4
				di := (w-1-x)*dstStride + y*4
				dstPix[di] = srcPix[si]
				dstPix[di+1] = srcPix[si+1]
				dstPix[di+2] = srcPix[si+2]
				dstPix[di+3] = srcPix[si+3]
			}
		}
		return out
	case "W":
		out := image.NewRGBA(image.Rect(0, 0, h, w))
		dstPix := out.Pix
		dstStride := out.Stride
		for y := 0; y < h; y++ {
			srcRow := y * srcStride
			for x := 0; x < w; x++ {
				si := srcRow + x*4
				di := x*dstStride + (h-1-y)*4
				dstPix[di] = srcPix[si]
				dstPix[di+1] = srcPix[si+1]
				dstPix[di+2] = srcPix[si+2]
				dstPix[di+3] = srcPix[si+3]
			}
		}
		return out
	default:
		return img
	}
}

func calculateBackgroundColor(img *image.RGBA) color.RGBA {
	bounds := img.Bounds()
	var r, g, b, count uint64

	for x := bounds.Min.X; x < bounds.Max.X; x++ {
		c := img.RGBAAt(x, bounds.Min.Y)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++
		c = img.RGBAAt(x, bounds.Max.Y-1)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		c := img.RGBAAt(bounds.Min.X, y)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++
		c = img.RGBAAt(bounds.Max.X-1, y)
		r += uint64(c.R)
		g += uint64(c.G)
		b += uint64(c.B)
		count++
	}

	if count == 0 {
		return color.RGBA{A: 255}
	}
	return color.RGBA{
		R: uint8(r / count),
		G: uint8(g / count),
		B: uint8(b / count),
		A: 255,
	}
}

func maskRegion(img *image.RGBA, bounds geometry.RectInt, c color.RGBA) {
	imgBounds := img.Bounds()
	x0 := max(bounds.X, imgBounds.Min.X)
	y0 := max(bounds.Y, imgBounds.Min.Y)
	x1 := min(bounds.X+bounds.Width, imgBounds.Max.X)
	y1 := min(bounds.Y+bounds.Height, imgBounds.Max.Y)
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func extractLogoNames(text string) []string {
	re := regexp.MustCompile(`<([A-Za-z0-9]+)>`)
	matches := re.FindAllStringSubmatch(text, -1)
	var names []string
	for _, m := range matches {
		if len(m) >= 2 {
			names = append(names, strings.ToUpper(m[1]))
		}
	}
	return names
}

func fixOCRPartNumbers(text string) string {
	families := []string{
		"ALS", "ALS", "AS", "LS", "S", "F",
		"HC", "HCT", "AC", "ACT",
		"LV", "LVC", "LVT", "ABT",
		"BCT", "FCT", "GTL", "GTLP",
	}

	familyFixes := map[string]string{
		"L5":  "LS",
		"AL5": "ALS",
		"A5":  "AS",
		"":    "",
	}
	for _, f := range families {
		familyFixes[f] = f
	}

	prefixFixes := map[string]string{
		"0M": "DM", "OM": "DM", "JM": "DM",
		"5N": "SN",
		"0S": "DS",
		"MC": "MC",
		"SN": "SN", "DM": "DM", "DS": "DS",
		"H0": "HD",
		"HD": "HD", "HA": "HA",
		"TC": "TC", "MB": "MB",
		"UA": "UA", "CA": "CA", "CD": "CD",
	}

	pat := regexp.MustCompile(`(?i)\b([A-Z0-9]{0,3})([7T][A4]|[5S][A4])([A-Z]{0,4})([0-9OSA]{1,4})([A-Z]{0,3})\b`)

	result := pat.ReplaceAllStringFunc(text, func(match string) string {
		sub := pat.FindStringSubmatch(match)
		if sub == nil {
			return match
		}
		prefix := sub[1]
		series := sub[2]
		family := sub[3]
		digits := sub[4]
		suffix := sub[5]

		prefixUpper := strings.ToUpper(prefix)
		if fixed, ok := prefixFixes[prefixUpper]; ok {
			prefix = fixed
		} else {
			prefix = prefixUpper
		}

		seriesUpper := strings.ToUpper(series)
		switch seriesUpper[0] {
		case '7', 'T':
			series = "74"
		case '5', 'S':
			series = "54"
		}

		familyUpper := strings.ToUpper(family)
		if familyUpper != "" {
			if fixed, ok := familyFixes[familyUpper]; ok {
				family = fixed
			} else {
				return match
			}
		}

		fixedDigits := strings.Map(func(r rune) rune {
			switch r {
			case 'S', 's':
				return '5'
			case 'A', 'a':
				return '4'
			case 'O', 'o':
				return '0'
			default:
				return r
			}
		}, digits)

		fixed := prefix + series + family + fixedDigits + suffix
		fmt.Printf("[OCR Fix] %q -> %q (prefix=%s series=%s family=%s digits=%s suffix=%s)\n",
			match, fixed, sub[1], sub[2], sub[3], sub[4], sub[5])
		return fixed
	})
	return result
}

// componentInfo holds parsed component information from OCR text.
type componentInfo struct {
	PartNumber   string
	Manufacturer string
	DateCode     string
	Place        string
}

func parseComponentInfo(text string) componentInfo {
	var info componentInfo
	upperText := strings.ToUpper(text)
	lines := strings.Split(strings.TrimSpace(text), "\n")

	logoPattern := regexp.MustCompile(`<([A-Za-z0-9]+)>`)
	logoMatches := logoPattern.FindAllStringSubmatch(text, -1)

	locationNames := []string{
		"MALAYSIA", "PHILIPPINES", "SINGAPORE", "INDONESIA", "THAILAND",
		"VIETNAM", "IRELAND", "GERMANY", "ENGLAND", "SCOTLAND", "CANADA",
		"BRAZIL", "TAIWAN", "SALVADOR", "MEXICO", "KOREA", "JAPAN",
		"CHINA", "INDIA", "HONGKONG", "CAMBODIA", "PORTUGAL", "FRANCE",
		"ITALY", "SPAIN", "AUSTRIA", "SWEDEN", "ISRAEL",
	}
	isLocationLine := func(s string) bool {
		upper := strings.ToUpper(s)
		lettersOnly := strings.Map(func(r rune) rune {
			if r >= 'A' && r <= 'Z' {
				return r
			}
			return -1
		}, upper)
		for _, loc := range locationNames {
			if strings.Contains(lettersOnly, loc) {
				return true
			}
		}
		return false
	}

	// Try 74/54-series match first — these are high-confidence part numbers
	// Find 74/54 directly, then look backwards for known manufacturer prefix
	logic74Core := regexp.MustCompile(`(?i)(7[4A]|54)([A-Z0-9]{2,8})\b`)
	prefixToMfr := map[string]string{
		"SN": "Texas Instruments", "TL": "Texas Instruments", "UC": "Texas Instruments",
		"DM": "National Semiconductor", "LM": "National Semiconductor", "DS": "National Semiconductor",
		"MC": "Motorola", "MJ": "Motorola",
		"UA": "Fairchild", "9N": "Fairchild",
		"AM": "AMD",
		"CD": "RCA", "CA": "RCA",
		"HD": "Hitachi", "HA": "Hitachi",
		"TC": "Toshiba",
		"MB": "Fujitsu",
		"N": "Signetics", "NE": "Signetics",
	}
	if loc := logic74Core.FindStringSubmatchIndex(upperText); loc != nil {
		rawPart := upperText[loc[2]:loc[5]] // series + body
		// Look backwards from match for a known manufacturer prefix
		before := upperText[:loc[0]]
		prefix := ""
		for p := range prefixToMfr {
			if strings.HasSuffix(before, p) && len(p) > len(prefix) {
				prefix = p
			}
		}
		// Apply OCR correction to get clean part number
		corrected, _ := component.CorrectOCRPartNumber(prefix + rawPart)
		// Extract core logic part (strips manufacturer prefix and package suffix)
		// e.g., "DM74LS02N" -> "74LS02"
		corePart := component.ExtractLogicPart(corrected)
		if corePart == "" {
			corePart = corrected
			if prefix != "" && strings.HasPrefix(corePart, prefix) {
				corePart = corePart[len(prefix):]
			}
		}
		if corePart != "" {
			info.PartNumber = corePart
		}
		if prefix != "" && info.Manufacturer == "" {
			if mfr, ok := prefixToMfr[prefix]; ok {
				info.Manufacturer = mfr
			}
		}
	}

	// If no 74/54-series match, fall back to generic first-line-with-letters
	if info.PartNumber == "" {
		partNumPattern := regexp.MustCompile(`[A-Za-z]`)
		var fallbackPartNum string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			withoutLogos := logoPattern.ReplaceAllString(line, "")
			withoutLogos = strings.TrimSpace(withoutLogos)
			if withoutLogos == "" {
				continue
			}
			withoutLogos = strings.TrimRight(withoutLogos, "-_.,;:!/ ")
			if withoutLogos == "" {
				continue
			}
			if isLocationLine(withoutLogos) {
				continue
			}
			if partNumPattern.MatchString(withoutLogos) && len(withoutLogos) >= 3 {
				info.PartNumber = withoutLogos
				break
			}
			if fallbackPartNum == "" {
				fallbackPartNum = withoutLogos
			}
		}
		if info.PartNumber == "" && fallbackPartNum != "" {
			info.PartNumber = fallbackPartNum
		}
	}

	datePattern := regexp.MustCompile(`\b([0-9]{4})\b`)
	if matches := datePattern.FindStringSubmatch(upperText); len(matches) >= 2 {
		info.DateCode = matches[1]
	}

	logoToManufacturer := map[string]string{
		"TI": "Texas Instruments", "MOTOROLA": "Motorola", "MOT": "Motorola",
		"NATIONAL": "National Semiconductor", "NSC": "National Semiconductor", "NS": "National Semiconductor",
		"FAIRCHILD": "Fairchild", "SIGNETICS": "Signetics", "AMD": "AMD",
		"INTEL": "Intel", "ZILOG": "Zilog", "NEC": "NEC",
		"HITACHI": "Hitachi", "TOSHIBA": "Toshiba", "FUJITSU": "Fujitsu",
		"MITSUBISHI": "Mitsubishi", "SAMSUNG": "Samsung", "PHILIPS": "Philips",
		"SIEMENS": "Siemens", "SGS": "SGS-Thomson", "ST": "STMicroelectronics",
		"MOSTEK": "Mostek", "RCA": "RCA", "CYPRESS": "Cypress",
		"LATTICE": "Lattice", "XILINX": "Xilinx", "ALTERA": "Altera",
		"MICROCHIP": "Microchip", "ATMEL": "Atmel", "MAXIM": "Maxim",
		"ANALOG": "Analog Devices", "LINEAR": "Linear Technology",
		"BURRBROWN": "Burr-Brown", "HARRIS": "Harris", "IDT": "IDT",
		"MICRON": "Micron", "HYUNDAI": "Hyundai", "HYNIX": "Hynix",
		"ELPIDA": "Elpida", "INFINEON": "Infineon", "RENESAS": "Renesas",
		"SHARP": "Sharp", "SANYO": "Sanyo", "SONY": "Sony",
		"PANASONIC": "Panasonic", "ROHM": "Rohm", "MURATA": "Murata",
		"TDK": "TDK", "VISHAY": "Vishay", "ONSEMI": "ON Semiconductor",
		"DIODES": "Diodes Inc", "NEXPERIA": "Nexperia",
	}

	for _, match := range logoMatches {
		if len(match) >= 2 {
			logoName := strings.ToUpper(match[1])
			if mfr, ok := logoToManufacturer[logoName]; ok {
				info.Manufacturer = mfr
				break
			}
		}
	}

	locations := []struct {
		pattern string
		place   string
	}{
		{"PHILIPPINES", "Philippines"}, {"SINGAPORE", "Singapore"},
		{"INDONESIA", "Indonesia"}, {"MALAYSIA", "Malaysia"},
		{"THAILAND", "Thailand"}, {"VIETNAM", "Vietnam"},
		{"HONG KONG", "Hong Kong"}, {"IRELAND", "Ireland"},
		{"GERMANY", "Germany"}, {"ENGLAND", "UK"}, {"SCOTLAND", "UK"},
		{"CANADA", "Canada"}, {"BRAZIL", "Brazil"}, {"TAIWAN", "Taiwan"},
		{"EL SALVADOR", "El Salvador"}, {"MEXICO", "Mexico"},
		{"KOREA", "Korea"}, {"JAPAN", "Japan"}, {"CHINA", "China"},
		{"INDIA", "India"}, {"S'PORE", "Singapore"}, {"SPORE", "Singapore"},
		{"M'SIA", "Malaysia"}, {"MSIA", "Malaysia"},
		{"HONGKONG", "Hong Kong"}, {"H.K.", "Hong Kong"},
		{"R.O.C", "Taiwan"}, {"ROC", "Taiwan"},
		{"P.R.C", "China"}, {"PRC", "China"},
		{"USA", "USA"}, {" UK ", "UK"}, {" HK ", "Hong Kong"},
	}
	for _, loc := range locations {
		if strings.Contains(upperText, loc.pattern) {
			info.Place = loc.place
			break
		}
	}

	if info.Place == "" {
		stripped := strings.Map(func(r rune) rune {
			if r >= 'A' && r <= 'Z' {
				return r
			}
			return -1
		}, upperText)

		fuzzyLocations := []struct {
			canonical string
			place     string
		}{
			{"ELSALVADOR", "El Salvador"}, {"PHILIPPINES", "Philippines"},
			{"SINGAPORE", "Singapore"}, {"INDONESIA", "Indonesia"},
			{"MALAYSIA", "Malaysia"}, {"THAILAND", "Thailand"},
			{"VIETNAM", "Vietnam"}, {"HONGKONG", "Hong Kong"},
			{"IRELAND", "Ireland"}, {"GERMANY", "Germany"},
			{"ENGLAND", "UK"}, {"SCOTLAND", "UK"},
			{"CANADA", "Canada"}, {"BRAZIL", "Brazil"},
			{"TAIWAN", "Taiwan"}, {"MEXICO", "Mexico"},
			{"KOREA", "Korea"}, {"JAPAN", "Japan"},
			{"CHINA", "China"}, {"INDIA", "India"},
		}
		for _, loc := range fuzzyLocations {
			if fuzzyContains(stripped, loc.canonical, len(loc.canonical)/5) {
				fmt.Printf("[OCR Place] Fuzzy matched %q in %q -> %s\n", loc.canonical, stripped, loc.place)
				info.Place = loc.place
				break
			}
		}
	}

	return info
}

func fuzzyContains(haystack, needle string, maxErrors int) bool {
	nLen := len(needle)
	if nLen == 0 {
		return true
	}
	if len(haystack) < nLen {
		return false
	}

	sameGroup := func(a, b byte) bool {
		if a == b {
			return true
		}
		groups := [][2]byte{
			{'0', 'O'}, {'0', 'D'}, {'O', 'D'},
			{'1', 'I'}, {'1', 'L'}, {'I', 'L'},
			{'5', 'S'}, {'4', 'A'},
			{'8', 'B'}, {'6', 'G'},
			{'2', 'Z'},
		}
		for _, g := range groups {
			if (a == g[0] && b == g[1]) || (a == g[1] && b == g[0]) {
				return true
			}
		}
		return false
	}

	for i := 0; i <= len(haystack)-nLen; i++ {
		errors := 0
		for j := 0; j < nLen; j++ {
			if !sameGroup(haystack[i+j], needle[j]) {
				errors++
				if errors > maxErrors {
					break
				}
			}
		}
		if errors <= maxErrors {
			return true
		}
	}
	return false
}

func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
