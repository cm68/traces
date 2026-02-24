// Package panels provides UI panels for the application.
package panels

import (
	"fmt"
	"image/color"

	"pcb-tracer/internal/app"
	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/ui/canvas"
	"pcb-tracer/ui/prefs"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/gtk"
)

// Panel names for ShowPanel method.
const (
	PanelImport     = "import"
	PanelComponents = "components"
	PanelTraces     = "traces"
	PanelProperties = "properties"
	PanelLogos      = "logos"
	PanelLibrary    = "library"
)

// Preference key for persisting the active panel.
const prefKeyActivePanel = "activePanel"

// SidePanel provides the main side panel with switchable views.
type SidePanel struct {
	state  *app.State
	canvas *canvas.ImageCanvas
	stack  *gtk.Stack
	win    *gtk.Window
	prefs  *prefs.Prefs

	// Individual panels
	importPanel     *ImportPanel
	componentsPanel *ComponentsPanel
	tracesPanel     *TracesPanel
	propertySheet   *PropertySheet
	logosPanel      *LogosPanel
	libraryPanel    *LibraryPanel

	// Currently visible panel name
	currentPanel string

	// Panel enable/disable
	disabledPanels map[string]bool

	// Callback when active panel changes (used by MainWindow to sync radio items)
	onPanelChanged func(string)
}

// NewSidePanel creates a new side panel.
func NewSidePanel(state *app.State, cvs *canvas.ImageCanvas, win *gtk.Window, p *prefs.Prefs) *SidePanel {
	sp := &SidePanel{
		state:          state,
		canvas:         cvs,
		win:            win,
		prefs:          p,
		currentPanel:   PanelImport,
		disabledPanels: make(map[string]bool),
	}

	stack, _ := gtk.StackNew()
	stack.SetTransitionType(gtk.STACK_TRANSITION_TYPE_CROSSFADE)
	stack.SetTransitionDuration(150)
	sp.stack = stack

	// Create import panel
	sp.importPanel = NewImportPanel(state, cvs, win, sp)
	stack.AddNamed(sp.importPanel.Widget(), PanelImport)

	// Create components panel
	sp.componentsPanel = NewComponentsPanel(state, cvs, win)
	stack.AddNamed(sp.componentsPanel.Widget(), PanelComponents)

	// Create traces panel
	sp.tracesPanel = NewTracesPanel(state, cvs, win, p)
	stack.AddNamed(sp.tracesPanel.Widget(), PanelTraces)

	// Create property sheet
	sp.propertySheet = NewPropertySheet(state, cvs, win, func() {
		sp.SyncLayers()
	})
	stack.AddNamed(sp.propertySheet.Widget(), PanelProperties)

	// Create logos panel
	sp.logosPanel = NewLogosPanel(state, cvs, win)
	stack.AddNamed(sp.logosPanel.Widget(), PanelLogos)

	// Create library panel
	sp.libraryPanel = NewLibraryPanel(state)
	stack.AddNamed(sp.libraryPanel.Widget(), PanelLibrary)

	stack.SetVisibleChildName(PanelImport)

	// Start with non-alignment panels disabled
	sp.SetPanelEnabled(PanelComponents, false)
	sp.SetPanelEnabled(PanelTraces, false)
	sp.SetPanelEnabled(PanelProperties, false)
	sp.SetPanelEnabled(PanelLogos, false)

	// After project load, enable panels if normalized images exist
	state.On(app.EventProjectLoaded, func(_ interface{}) {
		sp.updatePanelEnableState()
		sp.importPanel.updateAlignmentUI()
		sp.importPanel.syncBoardSelection()
	})

	return sp
}

// Widget returns the side panel widget for embedding in layouts.
func (sp *SidePanel) Widget() gtk.IWidget {
	return sp.stack
}

// SetOnPanelChanged registers a callback for when the active panel changes.
// MainWindow uses this to keep the View menu radio items in sync.
func (sp *SidePanel) SetOnPanelChanged(cb func(string)) {
	sp.onPanelChanged = cb
}

// ShowPanel switches to the specified panel.
func (sp *SidePanel) ShowPanel(name string) {
	if sp.disabledPanels[name] {
		return
	}
	if name == sp.currentPanel {
		return
	}

	// Deselect component when leaving components panel
	if sp.currentPanel == PanelComponents {
		sp.componentsPanel.DeselectComponent()
	}

	sp.currentPanel = name
	sp.stack.SetVisibleChildName(name)

	// Set up appropriate click/key handlers based on active panel
	switch name {
	case PanelComponents:
		sp.canvas.OnHover(nil)
		sp.canvas.OnMiddleClick(sp.componentsPanel.OnMiddleClickFloodFill)
		sp.canvas.OnLeftClick(func(x, y float64) { sp.componentsPanel.OnLeftClick(x, y) })
		sp.canvas.OnRightClick(func(x, y float64) { sp.componentsPanel.onRightClickDeleteComponent(x, y) })
	case PanelTraces:
		sp.canvas.OnMiddleClick(func(x, y float64) { sp.tracesPanel.onMiddleClick(x, y) })
		sp.canvas.OnLeftClick(func(x, y float64) { sp.tracesPanel.onLeftClick(x, y) })
		sp.canvas.OnRightClick(func(x, y float64) { sp.tracesPanel.onRightClickVia(x, y) })
		sp.canvas.OnHover(func(x, y float64) { sp.tracesPanel.onHover(x, y) })
	case PanelLogos:
		sp.canvas.OnHover(nil)
		sp.canvas.OnMiddleClick(func(x, y float64) { sp.logosPanel.OnMiddleClick(x, y) })
		sp.canvas.OnLeftClick(nil)
		sp.canvas.OnRightClick(nil)
	case PanelLibrary:
		sp.canvas.OnHover(nil)
		sp.canvas.OnMiddleClick(nil)
		sp.canvas.OnLeftClick(nil)
		sp.canvas.OnRightClick(nil)
	default:
		sp.canvas.OnHover(nil)
		sp.canvas.OnMiddleClick(nil)
		sp.canvas.OnLeftClick(nil)
		sp.canvas.OnRightClick(nil)
	}

	if sp.onPanelChanged != nil {
		sp.onPanelChanged(name)
	}
}

// CurrentPanel returns the name of the currently visible panel.
func (sp *SidePanel) CurrentPanel() string {
	return sp.currentPanel
}

// SavePreferences saves panel preferences.
func (sp *SidePanel) SavePreferences() {
	if sp.prefs != nil {
		fmt.Printf("[panel] SavePreferences: saving activePanel=%q\n", sp.currentPanel)
		sp.prefs.SetString(prefKeyActivePanel, sp.currentPanel)
	}
}

// SyncLayers updates the canvas with layers from state.
func (sp *SidePanel) SyncLayers() {
	var layers []*pcbimage.Layer
	if sp.state.FrontImage != nil {
		layers = append(layers, sp.state.FrontImage)
	}
	if sp.state.BackImage != nil {
		layers = append(layers, sp.state.BackImage)
	}
	sp.canvas.SetLayers(layers)

	// Set DPI for background grid
	dpi := sp.state.DPI
	if dpi == 0 && sp.state.FrontImage != nil && sp.state.FrontImage.DPI > 0 {
		dpi = sp.state.FrontImage.DPI
	}
	if dpi == 0 && sp.state.BackImage != nil && sp.state.BackImage.DPI > 0 {
		dpi = sp.state.BackImage.DPI
	}
	sp.canvas.SetDPI(dpi)

	// Set board bounds overlays
	if sp.state.FrontBoardBounds != nil {
		bounds := sp.state.FrontBoardBounds
		sp.canvas.SetOverlay("front_board_bounds", &canvas.Overlay{
			Rectangles: []canvas.OverlayRect{{
				X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height,
			}},
			Color: color.RGBA{R: 0, G: 255, B: 0, A: 128},
		})
	}
	if sp.state.BackBoardBounds != nil {
		bounds := sp.state.BackBoardBounds
		sp.canvas.SetOverlay("back_board_bounds", &canvas.Overlay{
			Rectangles: []canvas.OverlayRect{{
				X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height,
			}},
			Color: color.RGBA{R: 0, G: 0, B: 255, A: 128},
		})
	}

	sp.importPanel.ApplyLayerSelection()
}

// SetTracesEnabled enables or disables the traces panel.
func (sp *SidePanel) SetTracesEnabled(enabled bool) {
	// Will be populated when traces panel is ported
}

// updatePanelEnableState enables or disables panels based on normalization state.
func (sp *SidePanel) updatePanelEnableState() {
	enabled := sp.state.HasNormalizedImages()
	sp.SetPanelEnabled(PanelComponents, enabled)
	sp.SetPanelEnabled(PanelTraces, enabled)
	sp.SetPanelEnabled(PanelProperties, enabled)
	sp.SetPanelEnabled(PanelLogos, enabled)
}

// SetPanelEnabled enables or disables switching to a specific panel.
func (sp *SidePanel) SetPanelEnabled(name string, enabled bool) {
	if enabled {
		delete(sp.disabledPanels, name)
	} else {
		sp.disabledPanels[name] = true
	}
}

// IsPanelEnabled returns whether a panel is enabled.
func (sp *SidePanel) IsPanelEnabled(name string) bool {
	return !sp.disabledPanels[name]
}

// SyncBoardSelection updates the import panel's board combo to match state.
func (sp *SidePanel) SyncBoardSelection() {
	sp.importPanel.syncBoardSelection()
}

// OnKeyPressed dispatches key events to the active panel.
func (sp *SidePanel) OnKeyPressed(ev *gdk.EventKey) bool {
	switch sp.currentPanel {
	case PanelTraces:
		return sp.tracesPanel.OnKeyPressed(ev)
	case PanelLogos:
		return sp.logosPanel.OnKeyPressed(ev)
	case PanelComponents:
		return sp.componentsPanel.OnKeyPressed(ev)
	}
	return false
}
