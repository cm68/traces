// Package prefs provides JSON-based application preferences.
package prefs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const prefsFile = "preferences.json"

// Prefs stores application preferences as a key-value map.
type Prefs struct {
	mu     sync.RWMutex
	values map[string]interface{}
	path   string
}

// Load reads preferences from ~/.config/pcb-tracer/preferences.json.
// Returns a Prefs with defaults if the file doesn't exist.
func Load() *Prefs {
	p := &Prefs{
		values: make(map[string]interface{}),
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	dir := filepath.Join(configDir, "pcb-tracer")
	p.path = filepath.Join(dir, prefsFile)

	data, err := os.ReadFile(p.path)
	if err != nil {
		return p
	}
	_ = json.Unmarshal(data, &p.values)
	return p
}

// Save writes preferences to disk.
func (p *Prefs) Save() error {
	p.mu.RLock()
	data, err := json.MarshalIndent(p.values, "", "  ")
	p.mu.RUnlock()
	if err != nil {
		return err
	}

	dir := filepath.Dir(p.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(p.path, data, 0o644)
}

// Float returns a float64 preference, or 0 if not set.
func (p *Prefs) Float(key string) float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if v, ok := p.values[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		}
	}
	return 0
}

// FloatWithFallback returns a float64 preference, or fallback if not set.
func (p *Prefs) FloatWithFallback(key string, fallback float64) float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if v, ok := p.values[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		}
	}
	return fallback
}

// SetFloat stores a float64 preference.
func (p *Prefs) SetFloat(key string, val float64) {
	p.mu.Lock()
	p.values[key] = val
	p.mu.Unlock()
}

// String returns a string preference, or "" if not set.
func (p *Prefs) String(key string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if v, ok := p.values[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// SetString stores a string preference.
func (p *Prefs) SetString(key string, val string) {
	p.mu.Lock()
	p.values[key] = val
	p.mu.Unlock()
}

// Bool returns a bool preference, or fallback if not set.
func (p *Prefs) Bool(key string, fallback bool) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if v, ok := p.values[key]; ok {
		switch b := v.(type) {
		case bool:
			return b
		}
	}
	return fallback
}

// SetBool stores a bool preference.
func (p *Prefs) SetBool(key string, val bool) {
	p.mu.Lock()
	p.values[key] = val
	p.mu.Unlock()
}
