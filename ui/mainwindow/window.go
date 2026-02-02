// Package mainwindow provides the main application window.
package mainwindow

import (
	"fmt"
	"os"
	"path/filepath"

	"pcb-tracer/internal/app"
	"pcb-tracer/internal/image"
	"pcb-tracer/internal/version"
	"pcb-tracer/ui/canvas"
	"pcb-tracer/ui/panels"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

const (
	prefKeyLastDir      = "lastDirectory"
	prefKeyLastProject  = "lastProject"
	prefKeyWindowWidth  = "windowWidth"
	prefKeyWindowHeight = "windowHeight"
	prefKeyZoom         = "zoom"
)

// MainWindow is the primary application window.
type MainWindow struct {
	fyne.Window
	app       fyne.App
	state     *app.State
	canvas    *canvas.ImageCanvas
	sidePanel *panels.SidePanel
	statusBar *widget.Label

	// View menu items for panel switching (some need disable until aligned)
	viewImportItem      *fyne.MenuItem
	viewComponentsItem  *fyne.MenuItem
	viewTracesItem      *fyne.MenuItem
	viewPropertiesItem  *fyne.MenuItem
	viewLogosItem       *fyne.MenuItem

	// Opacity sliders in toolbar
	frontOpacitySlider *widget.Slider
	backOpacitySlider  *widget.Slider

	// Checkerboard alignment toggle
	checkerboardCheck *widget.Check

	// Track last saved size for change detection
	lastSavedWidth  float32
	lastSavedHeight float32
}

// New creates a new main window.
func New(fyneApp fyne.App, state *app.State) *MainWindow {
	win := fyneApp.NewWindow("PCB Tracer")

	mw := &MainWindow{
		Window: win,
		app:    fyneApp,
		state:  state,
	}

	mw.setupUI()
	mw.setupMenus()
	mw.setupEventHandlers()

	// Save window size and panel preferences on close
	win.SetCloseIntercept(func() {
		mw.saveWindowSize()
		mw.sidePanel.SavePreferences()
		fyneApp.Quit()
	})

	return mw
}

// setupUI creates the main UI layout.
func (mw *MainWindow) setupUI() {
	// Create the image canvas
	mw.canvas = canvas.NewImageCanvas()

	// Create the side panel with tabs
	mw.sidePanel = panels.NewSidePanel(mw.state, mw.canvas)
	mw.sidePanel.SetWindow(mw.Window)

	// Create status bar
	mw.statusBar = widget.NewLabel("Ready")

	// Create toolbar with zoom controls
	toolbar := mw.createToolbar()

	// Restore window size from preferences
	mw.restoreWindowSize()

	// Set up zoom change callback to save zoom
	mw.canvas.OnZoomChange(func(zoom float64) {
		fmt.Printf("OnZoomChange: saving zoom=%.3f\n", zoom)
		mw.app.Preferences().SetFloat(prefKeyZoom, zoom)
	})

	// Restore zoom from preferences
	mw.restoreZoom()

	// Restore last project from preferences
	mw.restoreLastProject()

	// Canvas area with toolbar on top
	canvasArea := container.NewBorder(
		toolbar,             // top
		nil,                 // bottom
		nil,                 // left
		nil,                 // right
		mw.canvas.Container(), // center
	)

	// Create main layout: side panel on left, canvas area in center
	// Using Border layout instead of HSplit to avoid Fyne scroll offset bug
	// that causes sidepanel clicks to be dispatched incorrectly when canvas is scrolled
	sidePanelWithScroll := container.NewVScroll(mw.sidePanel.Container())
	sidePanelWithScroll.SetMinSize(fyne.NewSize(320, 0))

	// Main container with status bar at bottom
	content := container.NewBorder(
		nil,                               // top
		container.NewPadded(mw.statusBar), // bottom
		sidePanelWithScroll,               // left - scrollable sidepanel
		nil,                               // right
		canvasArea,                        // center - canvas takes remaining space
	)

	mw.SetContent(content)
}

// createToolbar creates the toolbar with zoom and opacity controls.
func (mw *MainWindow) createToolbar() fyne.CanvasObject {
	zoomOutBtn := widget.NewButton("-", func() {
		mw.onZoomOut()
	})
	zoomInBtn := widget.NewButton("+", func() {
		mw.onZoomIn()
	})
	fitBtn := widget.NewButton("Fit", func() {
		mw.onToggleFitToWindow()
	})
	actualBtn := widget.NewButton("1:1", func() {
		mw.onActualSize()
	})

	// Opacity sliders for front and back layers
	mw.frontOpacitySlider = widget.NewSlider(0, 100)
	mw.frontOpacitySlider.SetValue(100)
	mw.frontOpacitySlider.OnChanged = func(val float64) {
		if mw.state.FrontImage != nil {
			mw.state.FrontImage.Opacity = val / 100.0
			mw.sidePanel.SyncLayers()
		}
	}

	mw.backOpacitySlider = widget.NewSlider(0, 100)
	mw.backOpacitySlider.SetValue(100)
	mw.backOpacitySlider.OnChanged = func(val float64) {
		if mw.state.BackImage != nil {
			mw.state.BackImage.Opacity = val / 100.0
			mw.sidePanel.SyncLayers()
		}
	}

	// Checkerboard alignment visualization checkbox
	mw.checkerboardCheck = widget.NewCheck("Checker", func(checked bool) {
		mw.onToggleCheckerboard(checked)
	})

	return container.NewHBox(
		widget.NewLabel("Zoom:"),
		zoomOutBtn,
		zoomInBtn,
		fitBtn,
		actualBtn,
		widget.NewSeparator(),
		widget.NewLabel("Front:"),
		container.NewGridWrap(fyne.NewSize(80, 30), mw.frontOpacitySlider),
		widget.NewLabel("Back:"),
		container.NewGridWrap(fyne.NewSize(80, 30), mw.backOpacitySlider),
		widget.NewSeparator(),
		mw.checkerboardCheck,
	)
}

// setupMenus creates the application menus.
func (mw *MainWindow) setupMenus() {
	// File menu
	fileMenu := fyne.NewMenu("File",
		fyne.NewMenuItem("New Project...", mw.onNewProject),
		fyne.NewMenuItem("Open Project...", mw.onOpenProject),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Save Project", mw.onSaveProject),
		fyne.NewMenuItem("Save Project As...", mw.onSaveProjectAs),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Export Netlist...", mw.onExportNetlist),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { mw.app.Quit() }),
	)

	// View menu - panel switching
	mw.viewImportItem = fyne.NewMenuItem("Align", func() {
		mw.sidePanel.ShowPanel(panels.PanelImport)
		mw.updateViewMenuChecks()
	})
	mw.viewComponentsItem = fyne.NewMenuItem("Components", func() {
		mw.sidePanel.ShowPanel(panels.PanelComponents)
		mw.updateViewMenuChecks()
	})
	mw.viewTracesItem = fyne.NewMenuItem("Traces", func() {
		mw.sidePanel.ShowPanel(panels.PanelTraces)
		mw.updateViewMenuChecks()
	})
	mw.viewPropertiesItem = fyne.NewMenuItem("Properties", func() {
		mw.sidePanel.ShowPanel(panels.PanelProperties)
		mw.updateViewMenuChecks()
	})
	mw.viewLogosItem = fyne.NewMenuItem("Logos", func() {
		mw.sidePanel.ShowPanel(panels.PanelLogos)
		mw.updateViewMenuChecks()
	})

	// Disable alignment-dependent items initially
	if !mw.state.Aligned {
		mw.viewComponentsItem.Disabled = true
		mw.viewTracesItem.Disabled = true
		mw.sidePanel.SetTracesEnabled(false)
	}

	// Mark current panel
	mw.updateViewMenuChecks()

	viewMenu := fyne.NewMenu("View",
		mw.viewImportItem,
		mw.viewComponentsItem,
		mw.viewTracesItem,
		mw.viewPropertiesItem,
		mw.viewLogosItem,
	)

	// Board menu
	boardMenu := fyne.NewMenu("Board",
		fyne.NewMenuItem("S-100 (IEEE 696)", func() { mw.onSelectBoard("S-100 (IEEE 696)") }),
		fyne.NewMenuItem("8-bit ISA", func() { mw.onSelectBoard("8-bit ISA") }),
		fyne.NewMenuItem("16-bit ISA", func() { mw.onSelectBoard("16-bit ISA") }),
		fyne.NewMenuItem("Multibus I (P1)", func() { mw.onSelectBoard("Multibus I (P1)") }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Custom Board...", mw.onCustomBoard),
	)

	// Help menu
	helpMenu := fyne.NewMenu("Help",
		fyne.NewMenuItem("About", mw.onAbout),
	)

	mainMenu := fyne.NewMainMenu(fileMenu, viewMenu, boardMenu, helpMenu)
	mw.SetMainMenu(mainMenu)
}

// setupEventHandlers registers for application events.
func (mw *MainWindow) setupEventHandlers() {
	mw.state.On(app.EventProjectLoaded, func(data interface{}) {
		if path, ok := data.(string); ok {
			mw.SetTitle("PCB Tracer - " + filepath.Base(path))
			mw.updateStatus("Project loaded: " + path)
		}
	})

	mw.state.On(app.EventImageLoaded, func(data interface{}) {
		// Sync the rotated/processed image to the canvas
		mw.sidePanel.SyncLayers()
		mw.canvas.Refresh()
		mw.updateStatus("Image loaded")
	})

	mw.state.On(app.EventModified, func(data interface{}) {
		if modified, ok := data.(bool); ok && modified {
			title := mw.Title()
			if len(title) > 0 && title[len(title)-1] != '*' {
				mw.SetTitle(title + " *")
			}
		}
	})

	mw.state.On(app.EventAlignmentComplete, func(data interface{}) {
		mw.canvas.Refresh()
		mw.updateStatus("Alignment complete")

		// Enable alignment-dependent menu items
		mw.viewComponentsItem.Disabled = false
		mw.viewTracesItem.Disabled = false
		mw.sidePanel.SetTracesEnabled(true)
	})
}

// updateViewMenuChecks updates the checkmarks on View menu panel items.
func (mw *MainWindow) updateViewMenuChecks() {
	current := mw.sidePanel.CurrentPanel()

	// Use checkmark prefix to indicate current panel
	if current == panels.PanelImport {
		mw.viewImportItem.Label = "✓ Align"
	} else {
		mw.viewImportItem.Label = "  Align"
	}

	if current == panels.PanelComponents {
		mw.viewComponentsItem.Label = "✓ Components"
	} else {
		mw.viewComponentsItem.Label = "  Components"
	}

	if current == panels.PanelTraces {
		mw.viewTracesItem.Label = "✓ Traces"
	} else {
		mw.viewTracesItem.Label = "  Traces"
	}

	if current == panels.PanelProperties {
		mw.viewPropertiesItem.Label = "✓ Properties"
	} else {
		mw.viewPropertiesItem.Label = "  Properties"
	}

	if current == panels.PanelLogos {
		mw.viewLogosItem.Label = "✓ Logos"
	} else {
		mw.viewLogosItem.Label = "  Logos"
	}
}

// updateStatus updates the status bar text.
func (mw *MainWindow) updateStatus(text string) {
	mw.statusBar.SetText(text)
}

// getLastDir returns the last used directory as a ListableURI, or nil.
func (mw *MainWindow) getLastDir() fyne.ListableURI {
	path := mw.app.Preferences().String(prefKeyLastDir)
	if path == "" {
		return nil
	}
	uri := storage.NewFileURI(path)
	listable, err := storage.ListerForURI(uri)
	if err != nil {
		return nil
	}
	return listable
}

// saveLastDir saves the directory of the given file path.
func (mw *MainWindow) saveLastDir(filePath string) {
	dir := filepath.Dir(filePath)
	mw.app.Preferences().SetString(prefKeyLastDir, dir)
}

// restoreWindowSize restores the window size from preferences.
func (mw *MainWindow) restoreWindowSize() {
	width := mw.app.Preferences().Float(prefKeyWindowWidth)
	height := mw.app.Preferences().Float(prefKeyWindowHeight)

	fmt.Printf("restoreWindowSize: saved width=%.1f height=%.1f\n", width, height)

	if width > 100 && height > 100 {
		mw.Resize(fyne.NewSize(float32(width), float32(height)))
		fmt.Printf("restoreWindowSize: restored to %.1fx%.1f\n", width, height)
	} else {
		// Default size
		mw.Resize(fyne.NewSize(1200, 800))
		fmt.Println("restoreWindowSize: using default 1200x800")
	}
}

// saveWindowSize saves the current window size to preferences.
func (mw *MainWindow) saveWindowSize() {
	// Use window size, not canvas size (canvas is content area only)
	size := mw.Window.Canvas().Size()
	fmt.Printf("saveWindowSize: saving %.1fx%.1f\n", size.Width, size.Height)
	mw.app.Preferences().SetFloat(prefKeyWindowWidth, float64(size.Width))
	mw.app.Preferences().SetFloat(prefKeyWindowHeight, float64(size.Height))
}

// SavePreferences saves window size and zoom to preferences.
// Call this before hot reload restart to preserve UI state.
func (mw *MainWindow) SavePreferences() {
	mw.saveWindowSize()
	// Zoom is already saved on every change via OnZoomChange callback
	fmt.Println("SavePreferences: saved window size and zoom")
}

// SavePreferencesIfChanged saves window geometry only if it has changed.
// Called periodically by the hot reload timer.
func (mw *MainWindow) SavePreferencesIfChanged() {
	size := mw.Window.Canvas().Size()
	if size.Width != mw.lastSavedWidth || size.Height != mw.lastSavedHeight {
		if size.Width > 100 && size.Height > 100 {
			fmt.Printf("SavePreferencesIfChanged: %.0fx%.0f -> %.0fx%.0f\n",
				mw.lastSavedWidth, mw.lastSavedHeight, size.Width, size.Height)
			mw.app.Preferences().SetFloat(prefKeyWindowWidth, float64(size.Width))
			mw.app.Preferences().SetFloat(prefKeyWindowHeight, float64(size.Height))
			mw.lastSavedWidth = size.Width
			mw.lastSavedHeight = size.Height
		}
	}
}

// restoreZoom restores the zoom level from preferences.
func (mw *MainWindow) restoreZoom() {
	zoom := mw.app.Preferences().Float(prefKeyZoom)
	fmt.Printf("restoreZoom: saved zoom=%.3f\n", zoom)
	if zoom >= 0.1 && zoom <= 10.0 {
		mw.canvas.SetZoom(zoom)
		fmt.Printf("restoreZoom: restored to %.3f\n", zoom)
	} else {
		fmt.Println("restoreZoom: using default zoom (no valid saved value)")
	}
}

// restoreLastProject loads the previously saved project file.
func (mw *MainWindow) restoreLastProject() {
	projectPath := mw.app.Preferences().String(prefKeyLastProject)

	fmt.Printf("restoreLastProject: projectPath=%q\n", projectPath)

	if projectPath == "" {
		fmt.Println("No saved project to restore")
		return
	}

	// Check if file exists
	if _, err := os.Stat(projectPath); os.IsNotExist(err) {
		fmt.Printf("Saved project file not found: %s\n", projectPath)
		return
	}

	// Load the project (this uses the saved crop bounds/rotation, no auto-detection)
	if err := mw.state.LoadProject(projectPath); err != nil {
		fmt.Printf("Failed to load project: %v\n", err)
		return
	}

	// Sync layers to canvas
	mw.sidePanel.SyncLayers()
	mw.state.SetModified(false) // Don't mark as modified on restore

	hasFront := mw.state.FrontImage != nil
	hasBack := mw.state.BackImage != nil
	fmt.Printf("After loading project: hasFront=%v hasBack=%v aligned=%v\n", hasFront, hasBack, mw.state.Aligned)
}

// Menu action handlers

func (mw *MainWindow) onNewProject() {
	// Show a dialog to import front and back images
	mw.showNewProjectDialog()
}

// showNewProjectDialog shows a dialog for creating a new project with front and back images.
func (mw *MainWindow) showNewProjectDialog() {
	var frontPath, backPath string
	var frontLabel, backLabel *widget.Label

	// Create labels to show selected file names
	frontLabel = widget.NewLabel("(none)")
	backLabel = widget.NewLabel("(none)")

	// Create buttons to browse for files
	frontBtn := widget.NewButton("Browse...", func() {
		fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil || reader == nil {
				return
			}
			reader.Close()
			frontPath = reader.URI().Path()
			mw.saveLastDir(frontPath)
			frontLabel.SetText(filepath.Base(frontPath))
		}, mw.Window)
		fd.SetFilter(storage.NewExtensionFileFilter(image.SupportedFormats()))
		if loc := mw.getLastDir(); loc != nil {
			fd.SetLocation(loc)
		}
		fd.Show()
	})

	backBtn := widget.NewButton("Browse...", func() {
		fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil || reader == nil {
				return
			}
			reader.Close()
			backPath = reader.URI().Path()
			mw.saveLastDir(backPath)
			backLabel.SetText(filepath.Base(backPath))
		}, mw.Window)
		fd.SetFilter(storage.NewExtensionFileFilter(image.SupportedFormats()))
		if loc := mw.getLastDir(); loc != nil {
			fd.SetLocation(loc)
		}
		fd.Show()
	})

	// Create the form layout
	form := container.NewVBox(
		widget.NewLabel("Select the scanned images for the new project:"),
		widget.NewSeparator(),
		container.NewBorder(nil, nil, widget.NewLabel("Front Image:"), frontBtn, frontLabel),
		container.NewBorder(nil, nil, widget.NewLabel("Back Image:"), backBtn, backLabel),
	)

	// Show custom dialog
	dlg := dialog.NewCustomConfirm("New Project", "Create", "Cancel", form, func(confirmed bool) {
		if !confirmed {
			return
		}

		if frontPath == "" || backPath == "" {
			dialog.ShowError(fmt.Errorf("please select both front and back images"), mw.Window)
			return
		}

		// Reset all state for new project - zero all alignment settings
		mw.state.ResetForNewProject()

		// Load raw front image (no auto-detection)
		mw.updateStatus("Loading front image...")
		if err := mw.state.LoadRawFrontImage(frontPath); err != nil {
			dialog.ShowError(fmt.Errorf("failed to load front image: %w", err), mw.Window)
			return
		}

		// Load raw back image (no auto-detection)
		mw.updateStatus("Loading back image...")
		if err := mw.state.LoadRawBackImage(backPath); err != nil {
			dialog.ShowError(fmt.Errorf("failed to load back image: %w", err), mw.Window)
			return
		}

		mw.SetTitle("PCB Tracer - New Project")
		mw.sidePanel.SyncLayers()
		mw.sidePanel.ShowPanel(panels.PanelImport) // Show align panel
		mw.updateStatus("New project created - use Auto Align or adjust settings manually")
	}, mw.Window)

	dlg.Resize(fyne.NewSize(500, 200))
	dlg.Show()
}

func (mw *MainWindow) onOpenProject() {
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		reader.Close()
		path := reader.URI().Path()
		mw.saveLastDir(path)
		if err := mw.state.LoadProject(path); err != nil {
			dialog.ShowError(err, mw.Window)
			return
		}
		// Save as last project for next startup
		mw.app.Preferences().SetString(prefKeyLastProject, path)
		mw.sidePanel.SyncLayers()
	}, mw.Window)
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".pcbproj"}))
	if loc := mw.getLastDir(); loc != nil {
		fd.SetLocation(loc)
	}
	fd.Show()
}

func (mw *MainWindow) onSaveProject() {
	if mw.state.ProjectPath == "" {
		mw.onSaveProjectAs()
		return
	}
	if err := mw.state.SaveProject(mw.state.ProjectPath); err != nil {
		dialog.ShowError(err, mw.Window)
		return
	}
	// Save as last project for next startup
	mw.app.Preferences().SetString(prefKeyLastProject, mw.state.ProjectPath)
}

func (mw *MainWindow) onSaveProjectAs() {
	fd := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil || writer == nil {
			return
		}
		writer.Close()
		path := writer.URI().Path()
		if filepath.Ext(path) != ".pcbproj" {
			path += ".pcbproj"
		}
		mw.saveLastDir(path)
		if err := mw.state.SaveProject(path); err != nil {
			dialog.ShowError(err, mw.Window)
			return
		}
		// Save as last project for next startup
		mw.app.Preferences().SetString(prefKeyLastProject, path)
	}, mw.Window)
	fd.SetFileName("project.pcbproj")
	if loc := mw.getLastDir(); loc != nil {
		fd.SetLocation(loc)
	}
	fd.Show()
}

func (mw *MainWindow) onExportNetlist() {
	mw.updateStatus("Export netlist not yet implemented")
}

func (mw *MainWindow) onUndo() {
	mw.updateStatus("Undo not yet implemented")
}

func (mw *MainWindow) onRedo() {
	mw.updateStatus("Redo not yet implemented")
}

func (mw *MainWindow) onShowAlignPanel() {
	mw.sidePanel.ShowPanel(panels.PanelImport)
	mw.updateViewMenuChecks()
}

func (mw *MainWindow) onPreferences() {
	mw.updateStatus("Preferences dialog not yet implemented")
}

func (mw *MainWindow) onZoomIn() {
	mw.disableFitToWindow()
	mw.canvas.ZoomIn()
}

func (mw *MainWindow) onZoomOut() {
	mw.disableFitToWindow()
	mw.canvas.ZoomOut()
}

func (mw *MainWindow) onToggleFitToWindow() {
	// Toggle state
	enabled := !mw.canvas.GetFitToWindow()
	mw.canvas.SetFitToWindow(enabled)
}

func (mw *MainWindow) onActualSize() {
	mw.disableFitToWindow()
	mw.canvas.SetZoom(1.0)
}

// onToggleCheckerboard enables/disables the checkerboard alignment visualization.
func (mw *MainWindow) onToggleCheckerboard(enabled bool) {
	if !enabled {
		mw.canvas.SetStepEdgeViz(false, 0, 0)
		return
	}

	// Enable with current DPI
	dpi := mw.state.DPI
	if dpi == 0 {
		dpi = 400 // Default fallback
	}

	// Use a placeholder Y value (checkerboard doesn't use it)
	mw.canvas.SetStepEdgeViz(true, 0, dpi)
}

func (mw *MainWindow) disableFitToWindow() {
	if mw.canvas.GetFitToWindow() {
		mw.canvas.SetFitToWindow(false)
	}
}

func (mw *MainWindow) onDetectContacts() {
	mw.updateStatus("Contact detection not yet implemented")
}

func (mw *MainWindow) onAlignImages() {
	mw.updateStatus("Image alignment not yet implemented")
}

func (mw *MainWindow) onDetectComponents() {
	mw.updateStatus("Component detection not yet implemented")
}

func (mw *MainWindow) onRunOCR() {
	mw.updateStatus("OCR not yet implemented")
}

func (mw *MainWindow) onDetectTraces() {
	mw.updateStatus("Trace detection not yet implemented")
}

func (mw *MainWindow) onGenerateNetlist() {
	mw.updateStatus("Netlist generation not yet implemented")
}

func (mw *MainWindow) onSelectBoard(name string) {
	mw.updateStatus("Selected board: " + name)
}

func (mw *MainWindow) onCustomBoard() {
	mw.updateStatus("Custom board editor not yet implemented")
}

func (mw *MainWindow) onAbout() {
	dialog.ShowInformation("About PCB Tracer",
		fmt.Sprintf("PCB Tracer v%s\n\n"+
			"A cross-platform PCB reverse engineering tool.\n\n"+
			"Supports S-100, ISA, Multibus, and custom boards.\n\n"+
			"Built: %s\n"+
			"Commit: %s",
			version.Version, version.BuildTime, version.GitCommit),
		mw.Window)
}
