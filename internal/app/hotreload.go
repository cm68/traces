package app

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// HotReloader watches the running binary for changes and triggers a callback
// when a newer version is detected. This is useful during development to
// automatically prompt for restart after recompilation.
type HotReloader struct {
	execPath    string
	startupTime time.Time
	checkInterval time.Duration
	stopCh      chan struct{}
	onNewBinary func() // Called when newer binary detected
}

// NewHotReloader creates a new hot reloader that watches the current executable.
// Returns nil if the executable path cannot be determined.
func NewHotReloader(checkInterval time.Duration) *HotReloader {
	execPath, err := os.Executable()
	if err != nil {
		return nil
	}

	// Resolve symlinks to get the actual file path
	// This is important because go build may create a new file
	// while the old symlink still points to the old location
	realPath, err := filepath.EvalSymlinks(execPath)
	if err == nil {
		execPath = realPath
	}

	// Get the modification time at startup
	info, err := os.Stat(execPath)
	if err != nil {
		return nil
	}

	return &HotReloader{
		execPath:      execPath,
		startupTime:   info.ModTime(),
		checkInterval: checkInterval,
		stopCh:        make(chan struct{}),
	}
}

// OnNewBinary sets the callback to invoke when a newer binary is detected.
// The callback is called from a background goroutine - use appropriate
// synchronization if updating UI.
func (h *HotReloader) OnNewBinary(callback func()) {
	h.onNewBinary = callback
}

// Start begins watching for binary changes in a background goroutine.
func (h *HotReloader) Start() {
	// Create a fresh stop channel in case we're restarting
	h.stopCh = make(chan struct{})
	go h.watchLoop()
}

// Stop stops the watcher goroutine.
func (h *HotReloader) Stop() {
	close(h.stopCh)
}

// watchLoop periodically checks if the binary has been modified.
func (h *HotReloader) watchLoop() {
	ticker := time.NewTicker(h.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			if h.checkForUpdate() && h.onNewBinary != nil {
				h.onNewBinary()
				// Only trigger once - stop watching after detection
				return
			}
		}
	}
}

// checkForUpdate returns true if the binary has been modified since startup.
func (h *HotReloader) checkForUpdate() bool {
	info, err := os.Stat(h.execPath)
	if err != nil {
		return false
	}
	return info.ModTime().After(h.startupTime)
}

// CurrentModTime returns the current modification time of the watched binary.
func (h *HotReloader) CurrentModTime() (time.Time, error) {
	info, err := os.Stat(h.execPath)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

// ExecPath returns the path to the current executable.
func (h *HotReloader) ExecPath() string {
	return h.execPath
}

// StartupTime returns when the binary was last modified at program start.
func (h *HotReloader) StartupTime() time.Time {
	return h.startupTime
}

// ResetBaseline updates the baseline timestamp to the current binary's mod time.
// Call this when the user declines a restart to avoid repeated notifications.
func (h *HotReloader) ResetBaseline() {
	if info, err := os.Stat(h.execPath); err == nil {
		h.startupTime = info.ModTime()
	}
}

// Restart replaces the current process with a new instance of the binary.
// This function does not return on success.
func (h *HotReloader) Restart() error {
	return RestartProcess(h.execPath)
}

// RestartProcess replaces the current process with a new instance of the
// specified executable, preserving command line arguments and environment.
// This function does not return on success.
func RestartProcess(execPath string) error {
	args := os.Args
	env := os.Environ()

	// syscall.Exec replaces the current process - no fork
	return syscall.Exec(execPath, args, env)
}
