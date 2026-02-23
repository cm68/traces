package via

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"
)

// TrainingSample represents a labeled via sample for training.
type TrainingSample struct {
	ID        string           `json:"id"`
	Center    geometry.Point2D `json:"center"`
	Radius    float64          `json:"radius"`
	Side      image.Side       `json:"side"`
	IsVia     bool             `json:"is_via"`     // True = positive sample, False = negative
	Source    string           `json:"source"`     // "manual", "confirmed", "rejected"
	Timestamp time.Time        `json:"timestamp"`
}

// TrainingSet holds a collection of labeled via samples.
type TrainingSet struct {
	mu       sync.RWMutex
	Samples  []TrainingSample `json:"samples"`
	FilePath string           `json:"-"` // Path for persistence

	// Statistics
	nextID int
}

// NewTrainingSet creates a new empty training set.
func NewTrainingSet() *TrainingSet {
	return &TrainingSet{
		Samples: make([]TrainingSample, 0),
		nextID:  1,
	}
}

// getViaTrainingLibPath returns the path to via_training.json in the lib/ directory
// next to the executable, or empty string if it can't be determined.
func getViaTrainingLibPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "..", "lib", "via_training.json")
}

// GetTrainingPath returns the path to the global via training file.
// Prefers lib/via_training.json next to the executable; falls back to
// ~/.config/pcb-tracer/via_training.json.
func GetTrainingPath() (string, error) {
	if libPath := getViaTrainingLibPath(); libPath != "" {
		if _, err := os.Stat(libPath); err == nil {
			return libPath, nil
		}
		if dir := filepath.Dir(libPath); dir != "" {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				return libPath, nil
			}
		}
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine config directory: %w", err)
		}
		configDir = filepath.Join(home, ".config")
	}

	appDir := filepath.Join(configDir, "pcb-tracer")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return "", fmt.Errorf("cannot create config directory: %w", err)
	}

	return filepath.Join(appDir, "via_training.json"), nil
}

// LoadGlobalTraining loads the global via training set from the preferred location.
// Returns an empty training set if no file exists.
func LoadGlobalTraining() (*TrainingSet, error) {
	// Try lib/ path first
	if libPath := getViaTrainingLibPath(); libPath != "" {
		if ts, err := LoadTrainingSet(libPath); err == nil && len(ts.Samples) > 0 {
			fmt.Printf("Loaded %d via training samples from %s\n", len(ts.Samples), libPath)
			return ts, nil
		}
	}

	path, err := GetTrainingPath()
	if err != nil {
		return NewTrainingSet(), err
	}

	ts, err := LoadTrainingSet(path)
	if err != nil {
		return NewTrainingSet(), err
	}

	if len(ts.Samples) > 0 {
		fmt.Printf("Loaded %d via training samples from %s\n", len(ts.Samples), path)
	}
	return ts, nil
}

// LoadTrainingSet loads a training set from a JSON file.
func LoadTrainingSet(path string) (*TrainingSet, error) {
	ts := NewTrainingSet()
	ts.FilePath = path

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, return empty set
			return ts, nil
		}
		return nil, fmt.Errorf("failed to read training set: %w", err)
	}

	if err := json.Unmarshal(data, ts); err != nil {
		return nil, fmt.Errorf("failed to parse training set: %w", err)
	}

	// Update nextID based on existing samples
	for _, s := range ts.Samples {
		var id int
		if _, err := fmt.Sscanf(s.ID, "ts-%d", &id); err == nil {
			if id >= ts.nextID {
				ts.nextID = id + 1
			}
		}
	}

	return ts, nil
}

// Save persists the training set to disk.
func (ts *TrainingSet) Save() error {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	if ts.FilePath == "" {
		return fmt.Errorf("no file path set")
	}

	// Ensure directory exists
	dir := filepath.Dir(ts.FilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	data, err := json.MarshalIndent(ts, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize training set: %w", err)
	}

	if err := os.WriteFile(ts.FilePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write training set: %w", err)
	}

	return nil
}

// SetFilePath sets the file path for persistence.
func (ts *TrainingSet) SetFilePath(path string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.FilePath = path
}

// AddPositive adds a positive (is via) training sample.
func (ts *TrainingSet) AddPositive(center geometry.Point2D, radius float64, side image.Side, source string) *TrainingSample {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	sample := TrainingSample{
		ID:        fmt.Sprintf("ts-%04d", ts.nextID),
		Center:    center,
		Radius:    radius,
		Side:      side,
		IsVia:     true,
		Source:    source,
		Timestamp: time.Now(),
	}
	ts.nextID++
	ts.Samples = append(ts.Samples, sample)

	return &sample
}

// AddNegative adds a negative (not via) training sample.
func (ts *TrainingSet) AddNegative(center geometry.Point2D, radius float64, side image.Side, source string) *TrainingSample {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	sample := TrainingSample{
		ID:        fmt.Sprintf("ts-%04d", ts.nextID),
		Center:    center,
		Radius:    radius,
		Side:      side,
		IsVia:     false,
		Source:    source,
		Timestamp: time.Now(),
	}
	ts.nextID++
	ts.Samples = append(ts.Samples, sample)

	return &sample
}

// Remove removes a sample by ID.
func (ts *TrainingSet) Remove(id string) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	for i, s := range ts.Samples {
		if s.ID == id {
			ts.Samples = append(ts.Samples[:i], ts.Samples[i+1:]...)
			return true
		}
	}
	return false
}

// FindNear finds samples near a given point within tolerance.
func (ts *TrainingSet) FindNear(center geometry.Point2D, tolerance float64, side image.Side) []TrainingSample {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	var matches []TrainingSample
	for _, s := range ts.Samples {
		if s.Side != side {
			continue
		}
		dx := s.Center.X - center.X
		dy := s.Center.Y - center.Y
		dist := dx*dx + dy*dy
		if dist <= tolerance*tolerance {
			matches = append(matches, s)
		}
	}
	return matches
}

// PositiveCount returns the number of positive samples.
func (ts *TrainingSet) PositiveCount() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	count := 0
	for _, s := range ts.Samples {
		if s.IsVia {
			count++
		}
	}
	return count
}

// NegativeCount returns the number of negative samples.
func (ts *TrainingSet) NegativeCount() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	count := 0
	for _, s := range ts.Samples {
		if !s.IsVia {
			count++
		}
	}
	return count
}

// Count returns the total number of samples.
func (ts *TrainingSet) Count() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return len(ts.Samples)
}

// Clear removes all samples.
func (ts *TrainingSet) Clear() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.Samples = ts.Samples[:0]
}

// GetPositiveSamples returns all positive samples.
func (ts *TrainingSet) GetPositiveSamples() []TrainingSample {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	var samples []TrainingSample
	for _, s := range ts.Samples {
		if s.IsVia {
			samples = append(samples, s)
		}
	}
	return samples
}

// GetNegativeSamples returns all negative samples.
func (ts *TrainingSet) GetNegativeSamples() []TrainingSample {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	var samples []TrainingSample
	for _, s := range ts.Samples {
		if !s.IsVia {
			samples = append(samples, s)
		}
	}
	return samples
}
