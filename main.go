// Package main provides the entry point for the PCB Tracer application.
package main

import (
	"log"
	"os"
	"time"

	"pcb-tracer/internal/app"
	"pcb-tracer/ui/mainwindow"
	"pcb-tracer/ui/prefs"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

const (
	appTitle   = "PCB Tracer"
	appVersion = "0.1.0"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("Starting %s v%s", appTitle, appVersion)

	gtk.Init(nil)

	appState := app.NewState()
	appPrefs := prefs.Load()

	win := mainwindow.New(appState, appPrefs)
	win.SetTitle(appTitle)

	// Handle command line arguments
	if len(os.Args) > 1 {
		projectPath := os.Args[1]
		if err := appState.LoadProject(projectPath); err != nil {
			log.Printf("Failed to load project %s: %v", projectPath, err)
		}
	}

	setupHotReload(win)

	win.ShowAll()
	gtk.Main()
}

// setupHotReload configures automatic restart detection when the binary is recompiled.
func setupHotReload(win *mainwindow.MainWindow) {
	reloader := app.NewHotReloader(2 * time.Second)
	if reloader == nil {
		log.Println("Hot reload: unable to determine executable path")
		return
	}

	log.Printf("Hot reload: watching %s (modified %s)",
		reloader.ExecPath(), reloader.StartupTime().Format("15:04:05"))

	reloader.OnTick(func() {
		win.SavePreferencesIfChanged()
	})

	reloader.OnNewBinary(func() {
		log.Println("Hot reload: newer binary detected")
		glib.IdleAdd(func() {
			dlg := gtk.MessageDialogNew(
				win.Window(),
				gtk.DIALOG_MODAL,
				gtk.MESSAGE_QUESTION,
				gtk.BUTTONS_YES_NO,
				"The application binary has been updated.\nRestart now?",
			)
			dlg.SetTitle("New Version Available")
			response := dlg.Run()
			dlg.Destroy()

			if response == gtk.RESPONSE_YES {
				log.Println("Hot reload: saving preferences before restart...")
				win.SavePreferences()
				log.Println("Hot reload: restarting...")
				if err := reloader.Restart(); err != nil {
					log.Printf("Hot reload: restart failed: %v", err)
				}
			} else {
				reloader.ResetBaseline()
				reloader.Start()
			}
		})
	})

	reloader.Start()
}
