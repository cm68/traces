package schematic

import (
	"fmt"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"

	"pcb-tracer/internal/app"
)

// SchematicWindow is a separate window showing the schematic view.
type SchematicWindow struct {
	win    *gtk.Window
	canvas *SchematicCanvas
	doc    *SchematicDoc
	state  *app.State

	showStubs bool
	statusBar *gtk.Label
}

// NewSchematicWindow creates a schematic window from the current app state.
func NewSchematicWindow(state *app.State) (*SchematicWindow, error) {
	win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		return nil, fmt.Errorf("cannot create schematic window: %w", err)
	}
	win.SetTitle("Schematic")
	win.SetDefaultSize(1200, 800)

	sw := &SchematicWindow{
		win:   win,
		state: state,
	}

	// Generate schematic from current netlist data
	sw.doc = GenerateSchematic(state)

	// Restore saved layout if available
	if layout := LoadLayout(state.ProjectPath); layout != nil {
		ApplyLayout(sw.doc, layout)
	}

	sw.canvas = NewSchematicCanvas(sw.doc)

	// Auto-save layout on any position or flip change
	sw.canvas.OnLayoutChanged(func() {
		SaveLayout(sw.doc, sw.state.ProjectPath)
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

	symCount := len(sw.doc.Symbols)
	wireCount := len(sw.doc.Wires)
	sw.statusBar.SetText(fmt.Sprintf("%d symbols, %d wires", symCount, wireCount))

	sw.canvas.OnStatusUpdate(func(msg string) {
		sw.statusBar.SetText(msg)
	})

	win.Add(vbox)

	// Fit to window after showing — defer to idle so GTK has allocated sizes
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

func (sw *SchematicWindow) regenerate() {
	sw.doc = GenerateSchematic(sw.state, sw.showStubs)
	// Restore saved layout for symbols that still exist
	if layout := LoadLayout(sw.state.ProjectPath); layout != nil {
		ApplyLayout(sw.doc, layout)
	}
	sw.canvas.SetDoc(sw.doc)
	sw.statusBar.SetText(fmt.Sprintf("%d symbols, %d wires", len(sw.doc.Symbols), len(sw.doc.Wires)))
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
		sw.showStubs = stubsCheck.GetActive()
		sw.regenerate()
	})
	toolbar.PackStart(stubsCheck, false, false, 0)

	return toolbar
}
