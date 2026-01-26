// Package mainwindow provides the main application window.
package mainwindow

import (
	"fmt"
	"path/filepath"

	"pcb-tracer/internal/app"
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
	prefKeyLastDir    = "lastDirectory"
	prefKeyFrontImage = "lastFrontImage"
	prefKeyBackImage  = "lastBackImage"
)

// MainWindow is the primary application window.
type MainWindow struct {
	fyne.Window
	app       fyne.App
	state     *app.State
	canvas    *canvas.ImageCanvas
	sidePanel *panels.SidePanel
	statusBar *widget.Label

	// Menu items that need state tracking
	fitToWindowItem *fyne.MenuItem
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

	// Restore last images from preferences
	mw.restoreLastImages()

	// Canvas area with toolbar on top
	canvasArea := container.NewBorder(
		toolbar,             // top
		nil,                 // bottom
		nil,                 // left
		nil,                 // right
		mw.canvas.Container(), // center
	)

	// Create main layout: side panel | canvas area
	split := container.NewHSplit(
		mw.sidePanel.Container(),
		canvasArea,
	)
	split.SetOffset(0.25) // Side panel takes 25% of width

	// Main container with status bar at bottom
	content := container.NewBorder(
		nil,                               // top
		container.NewPadded(mw.statusBar), // bottom
		nil,                               // left
		nil,                               // right
		split,                             // center
	)

	mw.SetContent(content)
}

// createToolbar creates the toolbar with zoom controls.
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

	return container.NewHBox(
		widget.NewLabel("Zoom:"),
		zoomOutBtn,
		zoomInBtn,
		fitBtn,
		actualBtn,
	)
}

// setupMenus creates the application menus.
func (mw *MainWindow) setupMenus() {
	// File menu
	fileMenu := fyne.NewMenu("File",
		fyne.NewMenuItem("New Project", mw.onNewProject),
		fyne.NewMenuItem("Open Project...", mw.onOpenProject),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Import Front Image...", mw.onImportFront),
		fyne.NewMenuItem("Import Back Image...", mw.onImportBack),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Save Project", mw.onSaveProject),
		fyne.NewMenuItem("Save Project As...", mw.onSaveProjectAs),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Export Netlist...", mw.onExportNetlist),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { mw.app.Quit() }),
	)

	// Edit menu
	editMenu := fyne.NewMenu("Edit",
		fyne.NewMenuItem("Undo", mw.onUndo),
		fyne.NewMenuItem("Redo", mw.onRedo),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Preferences...", mw.onPreferences),
	)

	// View menu
	mw.fitToWindowItem = fyne.NewMenuItem("  Fit to Window", mw.onToggleFitToWindow)

	viewMenu := fyne.NewMenu("View",
		fyne.NewMenuItem("Zoom In", mw.onZoomIn),
		fyne.NewMenuItem("Zoom Out", mw.onZoomOut),
		mw.fitToWindowItem,
		fyne.NewMenuItem("Actual Size", mw.onActualSize),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Show Front Layer", mw.onToggleFront),
		fyne.NewMenuItem("Show Back Layer", mw.onToggleBack),
	)

	// Tools menu
	toolsMenu := fyne.NewMenu("Tools",
		fyne.NewMenuItem("Detect Contacts", mw.onDetectContacts),
		fyne.NewMenuItem("Align Images", mw.onAlignImages),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Detect Components", mw.onDetectComponents),
		fyne.NewMenuItem("Run OCR", mw.onRunOCR),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Detect Traces", mw.onDetectTraces),
		fyne.NewMenuItem("Generate Netlist", mw.onGenerateNetlist),
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

	mainMenu := fyne.NewMainMenu(fileMenu, editMenu, viewMenu, toolsMenu, boardMenu, helpMenu)
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
	})
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

// restoreLastImages loads the previously used front and back images.
func (mw *MainWindow) restoreLastImages() {
	frontPath := mw.app.Preferences().String(prefKeyFrontImage)
	backPath := mw.app.Preferences().String(prefKeyBackImage)

	fmt.Printf("restoreLastImages: frontPath=%q backPath=%q\n", frontPath, backPath)

	if frontPath != "" {
		fmt.Println("Loading front image...")
		if err := mw.state.LoadFrontImage(frontPath); err != nil {
			fmt.Printf("Failed to load front image: %v\n", err)
		}
	}

	if backPath != "" {
		fmt.Println("Loading back image...")
		if err := mw.state.LoadBackImage(backPath); err != nil {
			fmt.Printf("Failed to load back image: %v\n", err)
		}
	}

	// Sync layers to canvas
	mw.sidePanel.SyncLayers()
	mw.state.SetModified(false) // Don't mark as modified on restore

	hasFront := mw.state.FrontImage != nil
	hasBack := mw.state.BackImage != nil
	fmt.Printf("After loading: hasFront=%v hasBack=%v\n", hasFront, hasBack)

	// If both images were loaded, run automatic detection and alignment
	if hasFront && hasBack {
		fmt.Println("Calling AutoDetectAndAlign...")
		mw.sidePanel.AutoDetectAndAlign()
	} else {
		fmt.Println("Skipping AutoDetectAndAlign - missing image(s)")
	}
}

// Menu action handlers

func (mw *MainWindow) onNewProject() {
	mw.state.ProjectPath = ""
	mw.state.FrontImage = nil
	mw.state.BackImage = nil
	mw.state.Aligned = false
	mw.state.SetModified(false)
	mw.SetTitle("PCB Tracer - New Project")
	mw.canvas.Refresh()
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
		}
	}, mw.Window)
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".pcbproj"}))
	if loc := mw.getLastDir(); loc != nil {
		fd.SetLocation(loc)
	}
	fd.Show()
}

func (mw *MainWindow) onImportFront() {
	mw.importImage(true)
}

func (mw *MainWindow) onImportBack() {
	mw.importImage(false)
}

func (mw *MainWindow) importImage(front bool) {
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		reader.Close()
		path := reader.URI().Path()
		mw.saveLastDir(path)

		var loadErr error
		if front {
			loadErr = mw.state.LoadFrontImage(path)
		} else {
			loadErr = mw.state.LoadBackImage(path)
		}

		if loadErr != nil {
			dialog.ShowError(loadErr, mw.Window)
			return
		}

		// Save path to preferences and sync layers to canvas
		if front {
			mw.app.Preferences().SetString(prefKeyFrontImage, path)
		} else {
			mw.app.Preferences().SetString(prefKeyBackImage, path)
		}
		mw.sidePanel.SyncLayers()
	}, mw.Window)

	fd.SetFilter(storage.NewExtensionFileFilter([]string{".tiff", ".tif", ".png", ".jpg", ".jpeg"}))
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
	}
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
		}
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

	// Update menu label to show state
	if enabled {
		mw.fitToWindowItem.Label = "✓ Fit to Window"
	} else {
		mw.fitToWindowItem.Label = "  Fit to Window"
	}
}

func (mw *MainWindow) onActualSize() {
	mw.disableFitToWindow()
	mw.canvas.SetZoom(1.0)
}

func (mw *MainWindow) disableFitToWindow() {
	if mw.canvas.GetFitToWindow() {
		mw.canvas.SetFitToWindow(false)
		mw.fitToWindowItem.Label = "  Fit to Window"
	}
}

func (mw *MainWindow) onToggleFront() {
	if mw.state.FrontImage != nil {
		mw.state.FrontImage.Visible = !mw.state.FrontImage.Visible
		mw.canvas.Refresh()
	}
}

func (mw *MainWindow) onToggleBack() {
	if mw.state.BackImage != nil {
		mw.state.BackImage.Visible = !mw.state.BackImage.Visible
		mw.canvas.Refresh()
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
