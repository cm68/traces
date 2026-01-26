// Package main provides the entry point for the PCB Tracer application.
package main

import (
	"log"
	"os"
	"time"

	"pcb-tracer/internal/app"
	"pcb-tracer/ui/mainwindow"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"
)

const (
	appID      = "com.pcbtracer.app"
	appTitle   = "PCB Tracer"
	appVersion = "0.1.0"
)

func main() {
	// Initialize logging
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("Starting %s v%s", appTitle, appVersion)

	// Create Fyne application
	fyneApp := fyneapp.NewWithID(appID)
	fyneApp.Settings().SetTheme(&app.PCBTracerTheme{})

	// Create application state
	appState := app.NewState()

	// Create main window
	mainWin := mainwindow.New(fyneApp, appState)
	mainWin.SetTitle(appTitle)
	mainWin.Resize(fyne.NewSize(1400, 900))
	mainWin.CenterOnScreen()

	// Handle command line arguments
	if len(os.Args) > 1 {
		projectPath := os.Args[1]
		if err := appState.LoadProject(projectPath); err != nil {
			log.Printf("Failed to load project %s: %v", projectPath, err)
		}
	}

	// Set up hot reload watcher for development
	setupHotReload(mainWin)

	// Show window and run
	mainWin.ShowAndRun()
}

// setupHotReload configures automatic restart detection when the binary is recompiled.
func setupHotReload(win fyne.Window) {
	reloader := app.NewHotReloader(2 * time.Second)
	if reloader == nil {
		log.Println("Hot reload: unable to determine executable path")
		return
	}

	log.Printf("Hot reload: watching %s (modified %s)",
		reloader.ExecPath(), reloader.StartupTime().Format("15:04:05"))

	reloader.OnNewBinary(func() {
		log.Println("Hot reload: newer binary detected")
		// Show dialog on main thread
		dialog.ShowConfirm(
			"New Version Available",
			"The application binary has been updated.\nRestart now?",
			func(restart bool) {
				if restart {
					log.Println("Hot reload: restarting...")
					if err := reloader.Restart(); err != nil {
						log.Printf("Hot reload: restart failed: %v", err)
						dialog.ShowError(err, win)
					}
				} else {
					// User declined - reset baseline and resume watching
					reloader.ResetBaseline()
					reloader.Start()
				}
			},
			win,
		)
	})

	reloader.Start()
}
