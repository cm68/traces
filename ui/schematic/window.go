package schematic

import (
	"fmt"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"

	"pcb-tracer/internal/app"
)

// SchematicWindow is a separate window showing one sheet of the schematic.
type SchematicWindow struct {
	win      *gtk.Window
	canvas   *SchematicCanvas
	doc      *SchematicDoc
	state    *app.State
	manager  *SheetManager
	sheetNum int

	showStubs bool
	statusBar *gtk.Label
}

// NewSchematicWindow creates a schematic window via SheetManager, opening existing layout.
func NewSchematicWindow(state *app.State) (*SheetManager, error) {
	sm := NewSheetManager(state)
	sm.OpenSheet(1)
	return sm, nil
}

// NewSchematicWindowFresh creates a schematic window with a fresh generation (ignoring saved layout).
func NewSchematicWindowFresh(state *app.State) (*SheetManager, error) {
	sm := NewSheetManagerFresh(state)
	sm.OpenSheet(1)
	return sm, nil
}

// newSchematicWindowForSheet creates a window for a specific sheet.
func newSchematicWindowForSheet(state *app.State, doc *SchematicDoc, sheetNum int, sheetName string, manager *SheetManager) (*SchematicWindow, error) {
	win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		return nil, fmt.Errorf("cannot create schematic window: %w", err)
	}

	title := fmt.Sprintf("Schematic — Sheet %d", sheetNum)
	if sheetName != "" {
		title = fmt.Sprintf("Schematic — Sheet %d: %s", sheetNum, sheetName)
	}
	win.SetTitle(title)
	win.SetDefaultSize(1200, 800)

	sw := &SchematicWindow{
		win:      win,
		state:    state,
		doc:      doc,
		manager:  manager,
		sheetNum: sheetNum,
	}

	sw.canvas = NewSchematicCanvas(doc)
	sw.canvas.sheetNum = sheetNum
	sw.canvas.manager = manager

	// Auto-save layout on any position or flip change
	sw.canvas.OnLayoutChanged(func() {
		SaveLayout(sw.doc, sw.state.ProjectPath)
	})

	// Propagate net renames to app state
	sw.canvas.OnNetRenamed(func(netID, newName string) {
		if sw.state.FeaturesLayer != nil {
			sw.state.FeaturesLayer.RenameNet(netID, newName)
		}
	})

	// Build UI
	vbox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)

	// Toolbar
	toolbar := sw.buildToolbar()
	vbox.PackStart(toolbar, false, false, 0)

	// Canvas (fills remaining space)
	vbox.PackStart(sw.canvas.Widget(), true, true, 0)

	// Status bar
	sw.statusBar, _ = gtk.LabelNew("")
	sw.statusBar.SetHAlign(gtk.ALIGN_START)
	sw.statusBar.SetMarginStart(8)
	sw.statusBar.SetMarginEnd(8)
	sw.statusBar.SetMarginTop(2)
	sw.statusBar.SetMarginBottom(2)
	vbox.PackStart(sw.statusBar, false, false, 0)

	sw.updateStatusText()

	sw.canvas.OnStatusUpdate(func(msg string) {
		sw.statusBar.SetText(msg)
	})

	win.Add(vbox)

	// Fit to window after showing
	win.Connect("show", func() {
		glib.IdleAdd(func() {
			sw.canvas.FitToWindow()
		})
	})

	return sw, nil
}

// Show displays the schematic window.
func (sw *SchematicWindow) Show() {
	sw.win.ShowAll()
	sw.win.Present()
}

// refreshView updates the canvas and status bar after data changes.
func (sw *SchematicWindow) refreshView() {
	sw.canvas.SetDoc(sw.doc)
	sw.updateStatusText()
}

func (sw *SchematicWindow) updateStatusText() {
	if sw.manager != nil {
		sw.statusBar.SetText(sw.manager.StatusForSheet(sw.sheetNum))
	} else {
		sw.statusBar.SetText(fmt.Sprintf("%d symbols, %d wires", len(sw.doc.Symbols), len(sw.doc.Wires)))
	}
}

func (sw *SchematicWindow) buildToolbar() *gtk.Box {
	toolbar, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	toolbar.SetMarginStart(4)
	toolbar.SetMarginEnd(4)
	toolbar.SetMarginTop(2)
	toolbar.SetMarginBottom(2)

	// Zoom In
	zoomInBtn, _ := gtk.ButtonNewWithLabel("Zoom +")
	zoomInBtn.Connect("clicked", func() {
		sw.canvas.SetZoom(sw.canvas.GetZoom() * schZoomStep)
	})
	toolbar.PackStart(zoomInBtn, false, false, 0)

	// Zoom Out
	zoomOutBtn, _ := gtk.ButtonNewWithLabel("Zoom -")
	zoomOutBtn.Connect("clicked", func() {
		sw.canvas.SetZoom(sw.canvas.GetZoom() / schZoomStep)
	})
	toolbar.PackStart(zoomOutBtn, false, false, 0)

	// Fit
	fitBtn, _ := gtk.ButtonNewWithLabel("Fit")
	fitBtn.Connect("clicked", func() {
		sw.canvas.FitToWindow()
	})
	toolbar.PackStart(fitBtn, false, false, 0)

	// Separator
	sep, _ := gtk.SeparatorNew(gtk.ORIENTATION_VERTICAL)
	toolbar.PackStart(sep, false, false, 4)

	// Re-layout
	relayoutBtn, _ := gtk.ButtonNewWithLabel("Re-layout")
	relayoutBtn.Connect("clicked", func() {
		AutoLayout(sw.doc)
		for _, sym := range sw.doc.Symbols {
			def := GetSymbolDef(sym.GateType,
				countPinsByDir(sym, "input"),
				countPinsByDir(sym, "output"),
				countPinsByDir(sym, "enable"),
				countPinsByDir(sym, "clock"))
			ComputePinPositions(sym, def)
		}
		positionPowerPorts(sw.doc)
		generateOffSheetConnectors(sw.doc)
		RouteAllWires(sw.doc)
		sw.canvas.updateContentSize()
		sw.canvas.Refresh()
		SaveLayout(sw.doc, sw.state.ProjectPath)
	})
	toolbar.PackStart(relayoutBtn, false, false, 0)

	// Separator
	sep2, _ := gtk.SeparatorNew(gtk.ORIENTATION_VERTICAL)
	toolbar.PackStart(sep2, false, false, 4)

	// Show stubs checkbox
	stubsCheck, _ := gtk.CheckButtonNewWithLabel("Show stubs")
	stubsCheck.SetActive(false)
	stubsCheck.Connect("toggled", func() {
		if sw.manager != nil {
			sw.manager.SetShowStubs(stubsCheck.GetActive())
		}
	})
	toolbar.PackStart(stubsCheck, false, false, 0)

	// Sheet navigation combo (only if multiple sheets)
	if sw.manager != nil && len(sw.doc.Sheets) > 0 {
		sep3, _ := gtk.SeparatorNew(gtk.ORIENTATION_VERTICAL)
		toolbar.PackStart(sep3, false, false, 4)

		sheetLabel, _ := gtk.LabelNew("Sheets:")
		toolbar.PackStart(sheetLabel, false, false, 4)

		for _, sheet := range sw.doc.Sheets {
			s := sheet // capture
			label := fmt.Sprintf("%d: %s", s.Number, s.Name)
			btn, _ := gtk.ButtonNewWithLabel(label)
			btn.Connect("clicked", func() {
				sw.manager.OpenSheet(s.Number)
			})
			toolbar.PackStart(btn, false, false, 0)
		}
	}

	return toolbar
}
