// Package mainwindow provides the main application window.
package mainwindow

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"pcb-tracer/internal/app"
	"pcb-tracer/internal/board"
	"pcb-tracer/internal/version"
	"pcb-tracer/ui/canvas"
	"pcb-tracer/ui/panels"
	"pcb-tracer/ui/prefs"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
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
	win    *gtk.Window
	state  *app.State
	prefs  *prefs.Prefs
	canvas *canvas.ImageCanvas

	// Status bar
	statusBar *gtk.Label

	// Opacity sliders
	frontOpacitySlider *gtk.Scale
	backOpacitySlider  *gtk.Scale

	// Checkerboard toggle
	checkerboardCheck *gtk.CheckButton

	// View menu items
	viewImportItem     *gtk.RadioMenuItem
	viewComponentsItem *gtk.RadioMenuItem
	viewTracesItem     *gtk.RadioMenuItem
	viewPropertiesItem *gtk.RadioMenuItem
	viewLogosItem      *gtk.RadioMenuItem

	// Side panel
	sidePanel *panels.SidePanel

	// Track last saved size
	lastSavedWidth  int
	lastSavedHeight int
}

// New creates a new main window.
func New(state *app.State, p *prefs.Prefs) *MainWindow {
	win, _ := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)

	mw := &MainWindow{
		win:   win,
		state: state,
		prefs: p,
	}

	mw.setupUI()
	mw.setupMenus()
	mw.syncViewMenuSensitivity()
	mw.setupEventHandlers()
	mw.setupKeyboard()

	win.Connect("destroy", func() {
		mw.saveWindowSize()
		mw.sidePanel.SavePreferences()
		mw.prefs.Save()
		gtk.MainQuit()
	})

	return mw
}

// Window returns the underlying GTK window.
func (mw *MainWindow) Window() *gtk.Window {
	return mw.win
}

// SetTitle sets the window title.
func (mw *MainWindow) SetTitle(title string) {
	mw.win.SetTitle(title)
}

// ShowAll shows the window and all children.
func (mw *MainWindow) ShowAll() {
	mw.win.ShowAll()
}

// setupUI creates the main UI layout.
func (mw *MainWindow) setupUI() {
	mw.canvas = canvas.NewImageCanvas()

	// Status bar
	mw.statusBar, _ = gtk.LabelNew("Ready")
	mw.statusBar.SetHAlign(gtk.ALIGN_START)
	mw.statusBar.SetMarginStart(6)
	mw.statusBar.SetMarginEnd(6)
	mw.statusBar.SetMarginTop(2)
	mw.statusBar.SetMarginBottom(2)

	// Toolbar
	toolbar := mw.createToolbar()

	// Side panel
	mw.sidePanel = panels.NewSidePanel(mw.state, mw.canvas, mw.win, mw.prefs)

	// Wrap side panel in scrolled window
	sidePanelScroll, _ := gtk.ScrolledWindowNew(nil, nil)
	sidePanelScroll.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
	sidePanelScroll.SetSizeRequest(320, -1)
	sidePanelScroll.Add(mw.sidePanel.Widget())

	// Right side: toolbar on top, canvas below
	rightBox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	rightBox.PackStart(toolbar, false, false, 0)
	rightBox.PackStart(mw.canvas.Widget(), true, true, 0)

	// Main paned: side panel | canvas area
	paned, _ := gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)
	paned.Pack1(sidePanelScroll, false, false)
	paned.Pack2(rightBox, true, true)
	paned.SetPosition(320)

	// Overall layout: menu is set separately, then paned + status bar
	vbox, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	vbox.PackStart(paned, true, true, 0)

	sep, _ := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
	vbox.PackStart(sep, false, false, 0)
	vbox.PackStart(mw.statusBar, false, false, 0)

	mw.win.Add(vbox)

	// Restore window size
	mw.restoreWindowSize()

	// Set up zoom callback
	mw.canvas.OnZoomChange(func(zoom float64) {
		fmt.Printf("OnZoomChange: saving zoom=%.3f\n", zoom)
		mw.prefs.SetFloat(prefKeyZoom, zoom)
		mw.prefs.Save()
	})

	// Restore zoom
	mw.restoreZoom()

	// Restore last project
	mw.restoreLastProject()
}

// createToolbar creates the toolbar with zoom and opacity controls.
func (mw *MainWindow) createToolbar() *gtk.Box {
	hbox, _ := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 4)
	hbox.SetMarginStart(4)
	hbox.SetMarginEnd(4)
	hbox.SetMarginTop(2)
	hbox.SetMarginBottom(2)

	// Zoom controls
	zoomLabel, _ := gtk.LabelNew("Zoom:")
	hbox.PackStart(zoomLabel, false, false, 0)

	zoomOutBtn, _ := gtk.ButtonNewWithLabel("-")
	zoomOutBtn.Connect("clicked", func() { mw.onZoomOut() })
	hbox.PackStart(zoomOutBtn, false, false, 0)

	zoomInBtn, _ := gtk.ButtonNewWithLabel("+")
	zoomInBtn.Connect("clicked", func() { mw.onZoomIn() })
	hbox.PackStart(zoomInBtn, false, false, 0)

	fitBtn, _ := gtk.ButtonNewWithLabel("Fit")
	fitBtn.Connect("clicked", func() { mw.onToggleFitToWindow() })
	hbox.PackStart(fitBtn, false, false, 0)

	actualBtn, _ := gtk.ButtonNewWithLabel("1:1")
	actualBtn.Connect("clicked", func() { mw.onActualSize() })
	hbox.PackStart(actualBtn, false, false, 0)

	// Separator
	sep1, _ := gtk.SeparatorNew(gtk.ORIENTATION_VERTICAL)
	hbox.PackStart(sep1, false, false, 4)

	// Front opacity
	frontLabel, _ := gtk.LabelNew("Front:")
	hbox.PackStart(frontLabel, false, false, 0)

	mw.frontOpacitySlider, _ = gtk.ScaleNewWithRange(gtk.ORIENTATION_HORIZONTAL, 0, 100, 1)
	mw.frontOpacitySlider.SetValue(100)
	mw.frontOpacitySlider.SetSizeRequest(80, -1)
	mw.frontOpacitySlider.SetDrawValue(false)
	mw.frontOpacitySlider.Connect("value-changed", func() {
		val := mw.frontOpacitySlider.GetValue()
		if mw.state.FrontImage != nil {
			mw.state.FrontImage.Opacity = val / 100.0
			mw.canvas.Refresh()
		}
	})
	hbox.PackStart(mw.frontOpacitySlider, false, false, 0)

	// Back opacity
	backLabel, _ := gtk.LabelNew("Back:")
	hbox.PackStart(backLabel, false, false, 0)

	mw.backOpacitySlider, _ = gtk.ScaleNewWithRange(gtk.ORIENTATION_HORIZONTAL, 0, 100, 1)
	mw.backOpacitySlider.SetValue(100)
	mw.backOpacitySlider.SetSizeRequest(80, -1)
	mw.backOpacitySlider.SetDrawValue(false)
	mw.backOpacitySlider.Connect("value-changed", func() {
		val := mw.backOpacitySlider.GetValue()
		if mw.state.BackImage != nil {
			mw.state.BackImage.Opacity = val / 100.0
			mw.canvas.Refresh()
		}
	})
	hbox.PackStart(mw.backOpacitySlider, false, false, 0)

	// Separator
	sep2, _ := gtk.SeparatorNew(gtk.ORIENTATION_VERTICAL)
	hbox.PackStart(sep2, false, false, 4)

	// Checkerboard toggle
	mw.checkerboardCheck, _ = gtk.CheckButtonNewWithLabel("Checker")
	mw.checkerboardCheck.Connect("toggled", func() {
		mw.onToggleCheckerboard(mw.checkerboardCheck.GetActive())
	})
	hbox.PackStart(mw.checkerboardCheck, false, false, 0)

	return hbox
}

// setupMenus creates the application menus.
func (mw *MainWindow) setupMenus() {
	menuBar, _ := gtk.MenuBarNew()

	// File menu
	fileMenu := mw.createMenu("File",
		menuEntry{"New Project...", mw.onNewProject},
		menuEntry{"Open Project...", mw.onOpenProject},
		menuEntry{}, // separator
		menuEntry{"Save Project", mw.onSaveProject},
		menuEntry{"Save Project As...", mw.onSaveProjectAs},
		menuEntry{}, // separator
		menuEntry{"Export Netlist...", mw.onExportNetlist},
		menuEntry{}, // separator
		menuEntry{"Quit", func() { mw.win.Close() }},
	)
	menuBar.Append(fileMenu)

	// View menu
	viewMenuItem, _ := gtk.MenuItemNewWithLabel("View")
	viewMenu, _ := gtk.MenuNew()
	viewMenuItem.SetSubmenu(viewMenu)
	menuBar.Append(viewMenuItem)

	// Radio group for panel switching
	mw.viewImportItem, _ = gtk.RadioMenuItemNewWithLabel(nil, "Align")
	viewMenu.Append(mw.viewImportItem)

	mw.viewComponentsItem, _ = gtk.RadioMenuItemNewWithLabelFromWidget(mw.viewImportItem, "Components")
	viewMenu.Append(mw.viewComponentsItem)

	mw.viewTracesItem, _ = gtk.RadioMenuItemNewWithLabelFromWidget(mw.viewImportItem, "Traces")
	viewMenu.Append(mw.viewTracesItem)

	mw.viewPropertiesItem, _ = gtk.RadioMenuItemNewWithLabelFromWidget(mw.viewImportItem, "Properties")
	viewMenu.Append(mw.viewPropertiesItem)

	mw.viewLogosItem, _ = gtk.RadioMenuItemNewWithLabelFromWidget(mw.viewImportItem, "Logos")
	viewMenu.Append(mw.viewLogosItem)

	mw.viewImportItem.SetActive(true)

	mw.viewImportItem.Connect("toggled", func() {
		if mw.viewImportItem.GetActive() {
			mw.sidePanel.ShowPanel(panels.PanelImport)
		}
	})
	mw.viewComponentsItem.Connect("toggled", func() {
		if mw.viewComponentsItem.GetActive() {
			mw.sidePanel.ShowPanel(panels.PanelComponents)
		}
	})
	mw.viewTracesItem.Connect("toggled", func() {
		if mw.viewTracesItem.GetActive() {
			mw.sidePanel.ShowPanel(panels.PanelTraces)
		}
	})
	mw.viewPropertiesItem.Connect("toggled", func() {
		if mw.viewPropertiesItem.GetActive() {
			mw.sidePanel.ShowPanel(panels.PanelProperties)
		}
	})
	mw.viewLogosItem.Connect("toggled", func() {
		if mw.viewLogosItem.GetActive() {
			mw.sidePanel.ShowPanel(panels.PanelLogos)
		}
	})

	// Board menu — built dynamically from registry
	boardMenuItem, _ := gtk.MenuItemNewWithLabel("Board")
	boardMenu, _ := gtk.MenuNew()
	boardMenuItem.SetSubmenu(boardMenu)
	for _, specName := range board.ListSpecs() {
		name := specName
		mi, _ := gtk.MenuItemNewWithLabel(name)
		mi.Connect("activate", func() { mw.onSelectBoard(name) })
		boardMenu.Append(mi)
	}
	menuBar.Append(boardMenuItem)

	// Help menu
	helpMenu := mw.createMenu("Help",
		menuEntry{"About", mw.onAbout},
	)
	menuBar.Append(helpMenu)

	// Insert menu bar at top of main vbox
	if vbox, err := mw.win.GetChild(); err == nil {
		if box, ok := vbox.(*gtk.Box); ok {
			box.PackStart(menuBar, false, false, 0)
			box.ReorderChild(menuBar, 0)
		}
	}
}

type menuEntry struct {
	label  string
	action func()
}

func (mw *MainWindow) createMenu(title string, entries ...menuEntry) *gtk.MenuItem {
	item, _ := gtk.MenuItemNewWithLabel(title)
	menu, _ := gtk.MenuNew()
	item.SetSubmenu(menu)

	for _, e := range entries {
		if e.label == "" {
			sep, _ := gtk.SeparatorMenuItemNew()
			menu.Append(sep)
		} else {
			mi, _ := gtk.MenuItemNewWithLabel(e.label)
			action := e.action
			mi.Connect("activate", func() { action() })
			menu.Append(mi)
		}
	}

	return item
}

// setupEventHandlers registers for application state events.
func (mw *MainWindow) setupEventHandlers() {
	mw.state.On(app.EventProjectLoaded, func(data interface{}) {
		if path, ok := data.(string); ok {
			mw.win.SetTitle("PCB Tracer - " + filepath.Base(path))
			mw.updateStatus("Project loaded: " + path)
		}
		// Restore viewport after images are loaded and canvas is sized
		savedZoom := mw.state.ViewZoom
		savedScrollX := mw.state.ViewScrollX
		savedScrollY := mw.state.ViewScrollY
		fmt.Printf("[viewport] restoring zoom=%.3f scrollX=%.1f scrollY=%.1f\n",
			savedZoom, savedScrollX, savedScrollY)
		glib.IdleAdd(func() {
			if savedZoom >= 0.1 && savedZoom <= 10.0 {
				mw.canvas.SetZoom(savedZoom)
				fmt.Printf("[viewport] zoom set to %.3f\n", savedZoom)
			}
			// Defer scroll restore so the canvas has updated its content size
			glib.IdleAdd(func() {
				if savedScrollX != 0 || savedScrollY != 0 {
					mw.canvas.SetScrollOffset(savedScrollX, savedScrollY)
					fmt.Printf("[viewport] scroll set to (%.1f, %.1f)\n", savedScrollX, savedScrollY)
				}
				// Verify
				actualX, actualY := mw.canvas.ScrollOffset()
				fmt.Printf("[viewport] actual scroll after set: (%.1f, %.1f)\n", actualX, actualY)
			})
		})
	})

	mw.state.On(app.EventImageLoaded, func(data interface{}) {
		mw.sidePanel.SyncLayers()
		mw.canvas.Refresh()
		mw.updateStatus("Image loaded")
	})

	mw.state.On(app.EventModified, func(data interface{}) {
		if modified, ok := data.(bool); ok && modified {
			title, _ := mw.win.GetTitle()
			if len(title) > 0 && title[len(title)-1] != '*' {
				mw.win.SetTitle(title + " *")
			}
		}
	})

	mw.state.On(app.EventAlignmentComplete, func(data interface{}) {
		mw.canvas.Refresh()
		mw.updateStatus("Alignment complete")
	})

	mw.state.On(app.EventNormalizationComplete, func(data interface{}) {
		mw.canvas.Refresh()
		mw.updateStatus("Aligned images saved")
		mw.syncViewMenuSensitivity()
	})

	mw.state.On(app.EventProjectLoaded, func(data interface{}) {
		mw.syncViewMenuSensitivity()
	})
}

// setupKeyboard registers global keyboard shortcuts.
func (mw *MainWindow) setupKeyboard() {
	mw.win.Connect("key-press-event", func(win *gtk.Window, ev *gdk.Event) bool {
		keyEvent := gdk.EventKeyNewFromEvent(ev)
		return mw.sidePanel.OnKeyPressed(keyEvent)
	})
}

// syncViewMenuSensitivity updates View menu items based on panel enable state.
func (mw *MainWindow) syncViewMenuSensitivity() {
	mw.viewComponentsItem.SetSensitive(mw.sidePanel.IsPanelEnabled(panels.PanelComponents))
	mw.viewTracesItem.SetSensitive(mw.sidePanel.IsPanelEnabled(panels.PanelTraces))
	mw.viewPropertiesItem.SetSensitive(mw.sidePanel.IsPanelEnabled(panels.PanelProperties))
	mw.viewLogosItem.SetSensitive(mw.sidePanel.IsPanelEnabled(panels.PanelLogos))
}

// syncLayers syncs the state's images to the canvas.
func (mw *MainWindow) syncLayers() {
	mw.sidePanel.SyncLayers()
}

// updateStatus updates the status bar text.
func (mw *MainWindow) updateStatus(text string) {
	mw.statusBar.SetText(text)
}

// restoreWindowSize restores the window size from preferences.
func (mw *MainWindow) restoreWindowSize() {
	width := mw.prefs.Float(prefKeyWindowWidth)
	height := mw.prefs.Float(prefKeyWindowHeight)

	fmt.Printf("restoreWindowSize: saved width=%.1f height=%.1f\n", width, height)

	if width > 100 && height > 100 {
		mw.win.SetDefaultSize(int(width), int(height))
		fmt.Printf("restoreWindowSize: restored to %.1fx%.1f\n", width, height)
	} else {
		mw.win.SetDefaultSize(1200, 800)
		fmt.Println("restoreWindowSize: using default 1200x800")
	}
}

// saveWindowSize saves the current window size to preferences.
func (mw *MainWindow) saveWindowSize() {
	w, h := mw.win.GetSize()
	fmt.Printf("saveWindowSize: saving %dx%d\n", w, h)
	mw.prefs.SetFloat(prefKeyWindowWidth, float64(w))
	mw.prefs.SetFloat(prefKeyWindowHeight, float64(h))
	mw.prefs.Save()
}

// SavePreferences saves window size and zoom to preferences.
func (mw *MainWindow) SavePreferences() {
	mw.saveWindowSize()
	fmt.Println("SavePreferences: saved window size and zoom")
}

// SavePreferencesIfChanged saves window geometry only if it has changed.
func (mw *MainWindow) SavePreferencesIfChanged() {
	w, h := mw.win.GetSize()
	if w != mw.lastSavedWidth || h != mw.lastSavedHeight {
		if w > 100 && h > 100 {
			fmt.Printf("SavePreferencesIfChanged: %dx%d -> %dx%d\n",
				mw.lastSavedWidth, mw.lastSavedHeight, w, h)
			mw.prefs.SetFloat(prefKeyWindowWidth, float64(w))
			mw.prefs.SetFloat(prefKeyWindowHeight, float64(h))
			mw.prefs.Save()
			mw.lastSavedWidth = w
			mw.lastSavedHeight = h
		}
	}
}

// restoreZoom restores the zoom level from preferences.
func (mw *MainWindow) restoreZoom() {
	zoom := mw.prefs.Float(prefKeyZoom)
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
	projectPath := mw.prefs.String(prefKeyLastProject)

	fmt.Printf("restoreLastProject: projectPath=%q\n", projectPath)

	if projectPath == "" {
		fmt.Println("No saved project to restore")
		return
	}

	if _, err := os.Stat(projectPath); os.IsNotExist(err) {
		fmt.Printf("Saved project file not found: %s\n", projectPath)
		return
	}

	if err := mw.state.LoadProject(projectPath); err != nil {
		fmt.Printf("Failed to load project: %v\n", err)
		return
	}

	mw.syncLayers()
	mw.state.SetModified(false)

	hasFront := mw.state.FrontImage != nil
	hasBack := mw.state.BackImage != nil
	fmt.Printf("After loading project: hasFront=%v hasBack=%v aligned=%v\n", hasFront, hasBack, mw.state.Aligned)
}

// Menu action handlers

func (mw *MainWindow) onNewProject() {
	dlg, _ := gtk.FileChooserDialogNewWith2Buttons(
		"Select Front Image",
		mw.win,
		gtk.FILE_CHOOSER_ACTION_OPEN,
		"Cancel", gtk.RESPONSE_CANCEL,
		"Open", gtk.RESPONSE_ACCEPT,
	)
	mw.addImageFilters(dlg)

	if lastDir := mw.prefs.String(prefKeyLastDir); lastDir != "" {
		dlg.SetCurrentFolder(lastDir)
	}

	response := dlg.Run()
	dlg.Destroy()
	if response != gtk.RESPONSE_ACCEPT {
		return
	}
	frontPath := dlg.GetFilename()

	// Now select back image
	dlg2, _ := gtk.FileChooserDialogNewWith2Buttons(
		"Select Back Image",
		mw.win,
		gtk.FILE_CHOOSER_ACTION_OPEN,
		"Cancel", gtk.RESPONSE_CANCEL,
		"Open", gtk.RESPONSE_ACCEPT,
	)
	mw.addImageFilters(dlg2)
	dlg2.SetCurrentFolder(filepath.Dir(frontPath))

	response2 := dlg2.Run()
	dlg2.Destroy()
	if response2 != gtk.RESPONSE_ACCEPT {
		return
	}
	backPath := dlg2.GetFilename()

	mw.prefs.SetString(prefKeyLastDir, filepath.Dir(frontPath))
	mw.prefs.Save()

	mw.state.ResetForNewProject()

	mw.updateStatus("Loading front image...")
	if err := mw.state.LoadRawFrontImage(frontPath); err != nil {
		mw.showError("Failed to load front image: " + err.Error())
		return
	}

	mw.updateStatus("Loading back image...")
	if err := mw.state.LoadRawBackImage(backPath); err != nil {
		mw.showError("Failed to load back image: " + err.Error())
		return
	}

	mw.win.SetTitle("PCB Tracer - New Project")
	mw.syncLayers()
	mw.updateStatus("New project created")
}

func (mw *MainWindow) onOpenProject() {
	dlg, _ := gtk.FileChooserDialogNewWith2Buttons(
		"Open Project",
		mw.win,
		gtk.FILE_CHOOSER_ACTION_OPEN,
		"Cancel", gtk.RESPONSE_CANCEL,
		"Open", gtk.RESPONSE_ACCEPT,
	)

	filter, _ := gtk.FileFilterNew()
	filter.SetName("PCB Projects (*.pcbproj)")
	filter.AddPattern("*.pcbproj")
	dlg.AddFilter(filter)

	if lastDir := mw.prefs.String(prefKeyLastDir); lastDir != "" {
		dlg.SetCurrentFolder(lastDir)
	}

	response := dlg.Run()
	dlg.Destroy()
	if response != gtk.RESPONSE_ACCEPT {
		return
	}

	path := dlg.GetFilename()
	mw.prefs.SetString(prefKeyLastDir, filepath.Dir(path))

	if err := mw.state.LoadProject(path); err != nil {
		mw.showError("Failed to load project: " + err.Error())
		return
	}

	mw.prefs.SetString(prefKeyLastProject, path)
	mw.prefs.Save()
	mw.syncLayers()
}

func (mw *MainWindow) snapshotViewport() {
	mw.state.ViewZoom = mw.canvas.GetZoom()
	mw.state.ViewScrollX, mw.state.ViewScrollY = mw.canvas.ScrollOffset()
	fmt.Printf("[viewport] snapshot: zoom=%.3f scrollX=%.1f scrollY=%.1f\n",
		mw.state.ViewZoom, mw.state.ViewScrollX, mw.state.ViewScrollY)
}

func (mw *MainWindow) onSaveProject() {
	if mw.state.ProjectPath == "" {
		mw.onSaveProjectAs()
		return
	}
	mw.snapshotViewport()
	if err := mw.state.SaveProject(mw.state.ProjectPath); err != nil {
		mw.showError("Failed to save project: " + err.Error())
		return
	}
	mw.prefs.SetString(prefKeyLastProject, mw.state.ProjectPath)
	mw.prefs.Save()
}

func (mw *MainWindow) onSaveProjectAs() {
	dlg, _ := gtk.FileChooserDialogNewWith2Buttons(
		"Save Project As",
		mw.win,
		gtk.FILE_CHOOSER_ACTION_SAVE,
		"Cancel", gtk.RESPONSE_CANCEL,
		"Save", gtk.RESPONSE_ACCEPT,
	)
	dlg.SetDoOverwriteConfirmation(true)
	dlg.SetCurrentName("project.pcbproj")

	filter, _ := gtk.FileFilterNew()
	filter.SetName("PCB Projects (*.pcbproj)")
	filter.AddPattern("*.pcbproj")
	dlg.AddFilter(filter)

	if lastDir := mw.prefs.String(prefKeyLastDir); lastDir != "" {
		dlg.SetCurrentFolder(lastDir)
	}

	response := dlg.Run()
	dlg.Destroy()
	if response != gtk.RESPONSE_ACCEPT {
		return
	}

	path := dlg.GetFilename()
	if filepath.Ext(path) != ".pcbproj" {
		path += ".pcbproj"
	}
	mw.prefs.SetString(prefKeyLastDir, filepath.Dir(path))

	mw.snapshotViewport()
	if err := mw.state.SaveProject(path); err != nil {
		mw.showError("Failed to save project: " + err.Error())
		return
	}
	mw.prefs.SetString(prefKeyLastProject, path)
	mw.prefs.Save()
}

func (mw *MainWindow) onExportNetlist() {
	mw.updateStatus("Export netlist not yet implemented")
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
	enabled := !mw.canvas.GetFitToWindow()
	mw.canvas.SetFitToWindow(enabled)
}

func (mw *MainWindow) onActualSize() {
	mw.disableFitToWindow()
	mw.canvas.SetZoom(1.0)
}

func (mw *MainWindow) onToggleCheckerboard(enabled bool) {
	if !enabled {
		mw.canvas.SetStepEdgeViz(false, 0, 0)
		return
	}
	dpi := mw.state.DPI
	if dpi == 0 {
		dpi = 400
	}
	mw.canvas.SetStepEdgeViz(true, 0, dpi)
}

func (mw *MainWindow) disableFitToWindow() {
	if mw.canvas.GetFitToWindow() {
		mw.canvas.SetFitToWindow(false)
	}
}

func (mw *MainWindow) onSelectBoard(name string) {
	if spec := board.GetSpec(name); spec != nil {
		mw.state.BoardSpec = spec
		mw.sidePanel.SyncBoardSelection()
		mw.updateStatus("Selected board: " + name)
	}
}

func (mw *MainWindow) onAbout() {
	dlg := gtk.MessageDialogNew(
		mw.win,
		gtk.DIALOG_MODAL,
		gtk.MESSAGE_INFO,
		gtk.BUTTONS_OK,
		fmt.Sprintf("PCB Tracer v%s\n\n"+
			"A cross-platform PCB reverse engineering tool.\n\n"+
			"Supports S-100, ISA, Multibus, and custom boards.\n\n"+
			"Built: %s\nCommit: %s",
			version.Version, version.BuildTime, version.GitCommit),
	)
	dlg.SetTitle("About PCB Tracer")
	dlg.Run()
	dlg.Destroy()
}

// addImageFilters adds common image format filters to a file chooser.
func (mw *MainWindow) addImageFilters(dlg *gtk.FileChooserDialog) {
	filter, _ := gtk.FileFilterNew()
	filter.SetName("Images (*.png, *.jpg, *.tif)")
	filter.AddPattern("*.png")
	filter.AddPattern("*.PNG")
	filter.AddPattern("*.jpg")
	filter.AddPattern("*.jpeg")
	filter.AddPattern("*.JPG")
	filter.AddPattern("*.JPEG")
	filter.AddPattern("*.tif")
	filter.AddPattern("*.tiff")
	filter.AddPattern("*.TIF")
	filter.AddPattern("*.TIFF")
	filter.AddPattern("*.bmp")
	filter.AddPattern("*.BMP")
	dlg.AddFilter(filter)

	allFilter, _ := gtk.FileFilterNew()
	allFilter.SetName("All files (*)")
	allFilter.AddPattern("*")
	dlg.AddFilter(allFilter)
}

// showError shows an error dialog.
func (mw *MainWindow) showError(message string) {
	log.Println("Error:", message)
	dlg := gtk.MessageDialogNew(
		mw.win,
		gtk.DIALOG_MODAL,
		gtk.MESSAGE_ERROR,
		gtk.BUTTONS_OK,
		message,
	)
	dlg.Run()
	dlg.Destroy()
}
