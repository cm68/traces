// Package app provides application lifecycle management, configuration, and events.
package app

import (
	"encoding/json"
	"fmt"
	goimage "image"
	"math"
	"os"
	"path/filepath"
	"sync"

	"pcb-tracer/internal/alignment"
	"pcb-tracer/internal/board"
	"pcb-tracer/internal/component"
	"pcb-tracer/internal/features"
	"pcb-tracer/internal/image"
	"pcb-tracer/internal/via"
	"pcb-tracer/pkg/geometry"
)

// State holds the application state including current project, images, and settings.
type State struct {
	mu sync.RWMutex

	// Project
	ProjectPath string
	Modified    bool

	// Board specification
	BoardSpec board.Spec

	// Images
	FrontImage *image.Layer
	BackImage  *image.Layer

	// Alignment
	Aligned              bool
	AlignedFront         *image.Layer
	AlignedBack          *image.Layer
	AlignmentError       float64
	DPI                  float64
	FrontDetectionResult *alignment.DetectionResult
	BackDetectionResult  *alignment.DetectionResult
	FrontBoardBounds     *geometry.RectInt
	BackBoardBounds      *geometry.RectInt

	// Per-side sampled color parameters (nil = use defaults)
	FrontColorParams *ColorParams
	BackColorParams  *ColorParams

	// Components
	Components []*component.Component

	// Detected features layer (vias and traces from both sides)
	FeaturesLayer *features.DetectedFeaturesLayer

	// Via detection color parameters (nil = use defaults)
	ViaColorParams *ColorParams

	// Via training set for machine learning
	ViaTrainingSet *via.TrainingSet

	// Event listeners
	listeners map[EventType][]EventListener
}

// ColorParams holds sampled HSV color parameters for contact detection.
type ColorParams struct {
	HueMin, HueMax float64
	SatMin, SatMax float64
	ValMin, ValMax float64
}

// EventType identifies different application events.
type EventType int

const (
	EventProjectLoaded EventType = iota
	EventProjectSaved
	EventImageLoaded
	EventAlignmentComplete
	EventComponentsChanged
	EventModified
	EventViasDetected
	EventTracesDetected
	EventFeaturesChanged
	EventSelectionChanged
	EventBusChanged
	EventConfirmedViasChanged
)

// EventListener is called when an event occurs.
type EventListener func(data interface{})

// NewState creates a new application state.
func NewState() *State {
	return &State{
		BoardSpec:      board.S100Spec(),
		FeaturesLayer:  features.NewDetectedFeaturesLayer(),
		ViaTrainingSet: via.NewTrainingSet(),
		listeners:      make(map[EventType][]EventListener),
	}
}

// On registers an event listener for the specified event type.
func (s *State) On(event EventType, listener EventListener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners[event] = append(s.listeners[event], listener)
}

// Emit triggers all listeners for the specified event type.
func (s *State) Emit(event EventType, data interface{}) {
	s.mu.RLock()
	listeners := s.listeners[event]
	s.mu.RUnlock()

	for _, listener := range listeners {
		listener(data)
	}
}

// SetModified marks the project as modified and emits an event.
func (s *State) SetModified(modified bool) {
	s.mu.Lock()
	s.Modified = modified
	s.mu.Unlock()
	s.Emit(EventModified, modified)
}

// LoadProject loads a project from the specified path.
func (s *State) LoadProject(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var proj ProjectFile
	if err := json.Unmarshal(data, &proj); err != nil {
		return err
	}

	s.mu.Lock()
	s.ProjectPath = path
	s.Modified = false

	// Load board spec
	if proj.BoardType != "" {
		s.BoardSpec = board.GetSpec(proj.BoardType)
	}

	// Store alignment data
	s.Aligned = proj.Aligned
	s.AlignmentError = proj.AlignmentError
	s.DPI = proj.DPI
	s.mu.Unlock()

	// Load images
	projectDir := filepath.Dir(path)
	if proj.FrontImagePath != "" {
		frontPath := filepath.Join(projectDir, proj.FrontImagePath)
		if err := s.LoadFrontImage(frontPath); err != nil {
			return err
		}
	}
	if proj.BackImagePath != "" {
		backPath := filepath.Join(projectDir, proj.BackImagePath)
		if err := s.LoadBackImage(backPath); err != nil {
			return err
		}
	}

	// Load components
	if proj.ComponentsPath != "" {
		compPath := filepath.Join(projectDir, proj.ComponentsPath)
		if err := s.LoadComponents(compPath); err != nil {
			return err
		}
	}

	s.Emit(EventProjectLoaded, path)
	return nil
}

// SaveProject saves the project to the specified path.
func (s *State) SaveProject(path string) error {
	s.mu.RLock()
	proj := ProjectFile{
		Version:        1,
		BoardType:      s.BoardSpec.Name(),
		Aligned:        s.Aligned,
		AlignmentError: s.AlignmentError,
		DPI:            s.DPI,
	}

	projectDir := filepath.Dir(path)

	if s.FrontImage != nil {
		proj.FrontImagePath, _ = filepath.Rel(projectDir, s.FrontImage.Path)
	}
	if s.BackImage != nil {
		proj.BackImagePath, _ = filepath.Rel(projectDir, s.BackImage.Path)
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(proj, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}

	s.mu.Lock()
	s.ProjectPath = path
	s.Modified = false
	s.mu.Unlock()

	s.Emit(EventProjectSaved, path)
	return nil
}

// LoadFrontImage loads the front side image.
func (s *State) LoadFrontImage(path string) error {
	layer, err := image.Load(path)
	if err != nil {
		return err
	}
	layer.Side = image.SideFront

	// Auto-detect board bounds and apply initial rotation/crop
	layer.Image = autoRotateAndCrop(layer.Image)

	// Fine-tune rotation using contact detection
	layer.Image = fineRotateByContacts(layer.Image, s.BoardSpec, layer.DPI)

	s.mu.Lock()
	s.FrontImage = layer
	s.FrontBoardBounds = nil // bounds are now (0,0) since we cropped
	s.FrontDetectionResult = nil // Clear old detection - user must re-detect on rotated image
	s.Aligned = false
	s.AlignedFront = nil
	// Set DPI from TIFF metadata if available and not already set
	if layer.DPI > 0 && s.DPI == 0 {
		s.DPI = layer.DPI
	}
	s.mu.Unlock()

	s.SetModified(true)
	s.Emit(EventImageLoaded, layer)
	return nil
}

// LoadBackImage loads the back side image.
func (s *State) LoadBackImage(path string) error {
	layer, err := image.Load(path)
	if err != nil {
		return err
	}
	layer.Side = image.SideBack

	// Auto-detect board bounds and apply initial rotation/crop
	layer.Image = autoRotateAndCrop(layer.Image)

	// Fine-tune rotation using contact detection
	layer.Image = fineRotateByContacts(layer.Image, s.BoardSpec, layer.DPI)

	// Flip horizontally - back is viewed from the other side
	layer.Image = flipHorizontal(layer.Image)

	s.mu.Lock()
	s.BackImage = layer
	s.BackBoardBounds = nil // bounds are now (0,0) since we cropped
	s.BackDetectionResult = nil // Clear old detection - user must re-detect on rotated image
	s.Aligned = false
	s.AlignedBack = nil
	// Set DPI from TIFF metadata if available and not already set
	if layer.DPI > 0 && s.DPI == 0 {
		s.DPI = layer.DPI
	}
	s.mu.Unlock()

	s.SetModified(true)
	s.Emit(EventImageLoaded, layer)
	return nil
}

// autoRotateAndCrop detects board bounds, rotates based on contacts, and crops.
func autoRotateAndCrop(img goimage.Image) goimage.Image {
	// First, just detect board bounds (no rotation yet)
	rotResult := alignment.DetectBoardRotationFromImage(img)
	if !rotResult.Detected {
		return img
	}

	// Crop to board bounds first
	cropped := cropImage(img, rotResult.Bounds)

	return cropped
}

// fineRotateByContacts detects contacts and applies rotation to align them horizontally.
func fineRotateByContacts(img goimage.Image, spec board.Spec, dpi float64) goimage.Image {
	// Run contact detection to get the slope angle
	result, _ := alignment.DetectContactsFromImage(img, spec, dpi)
	if result == nil || len(result.Contacts) < 10 {
		fmt.Printf("Fine rotation: not enough contacts (%d), skipping\n", 0)
		if result != nil {
			fmt.Printf("Fine rotation: not enough contacts (%d), skipping\n", len(result.Contacts))
		}
		return img // Not enough contacts for reliable angle
	}

	// Get the contact angle (slope of the contact line)
	angle := result.ContactAngle
	fmt.Printf("Fine rotation: detected angle=%.2f° from %d contacts\n", angle, len(result.Contacts))

	// Check if angle is significant enough to warrant correction (>0.05 degrees)
	if math.Abs(angle) < 0.05 {
		fmt.Printf("Fine rotation: angle too small (%.2f°), skipping\n", angle)
		return img
	}

	// Apply rotation to correct the slope (rotate by the detected angle to level it)
	rotated := alignment.RotateGoImage(img, angle)
	fmt.Printf("Fine rotation: applied %.2f° rotation\n", angle)

	return rotated
}

// LoadComponents loads components from a JSON file.
func (s *State) LoadComponents(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var components []*component.Component
	if err := json.Unmarshal(data, &components); err != nil {
		return err
	}

	s.mu.Lock()
	s.Components = components
	s.mu.Unlock()

	s.Emit(EventComponentsChanged, components)
	return nil
}

// SaveComponents saves components to a JSON file.
func (s *State) SaveComponents(path string) error {
	s.mu.RLock()
	data, err := json.MarshalIndent(s.Components, "", "  ")
	s.mu.RUnlock()

	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// SetAlignmentResult stores the alignment results.
func (s *State) SetAlignmentResult(front, back *image.Layer, transform geometry.AffineTransform, dpi, err float64) {
	s.mu.Lock()
	s.AlignedFront = front
	s.AlignedBack = back
	s.DPI = dpi
	s.AlignmentError = err
	s.Aligned = true
	s.mu.Unlock()

	s.SetModified(true)
	s.Emit(EventAlignmentComplete, nil)
}

// ProjectFile represents the JSON structure of a .pcbproj file.
type ProjectFile struct {
	Version        int     `json:"version"`
	BoardType      string  `json:"board_type"`
	FrontImagePath string  `json:"front_image,omitempty"`
	BackImagePath  string  `json:"back_image,omitempty"`
	Aligned        bool    `json:"aligned"`
	AlignmentError float64 `json:"alignment_error,omitempty"`
	DPI            float64 `json:"dpi,omitempty"`
	ComponentsPath string  `json:"components,omitempty"`
	TracesPath     string  `json:"traces,omitempty"`
	NetlistPath    string  `json:"netlist,omitempty"`
}

// transformBoundsAfterRotation transforms board bounds from original image coordinates
// to rotated image coordinates. After rotation, the board should be axis-aligned,
// so we use the tighter dimensions (long edge as width, short edge as height).
func transformBoundsAfterRotation(origBounds geometry.RectInt, origW, origH, newW, newH int, angleDeg float64) geometry.RectInt {
	// Original center of bounds
	origCx := float64(origBounds.X) + float64(origBounds.Width)/2
	origCy := float64(origBounds.Y) + float64(origBounds.Height)/2

	// Transform center point by rotation around original image center
	angleRad := angleDeg * math.Pi / 180.0
	relX := origCx - float64(origW)/2
	relY := origCy - float64(origH)/2
	rotX := relX*math.Cos(angleRad) - relY*math.Sin(angleRad)
	rotY := relX*math.Sin(angleRad) + relY*math.Cos(angleRad)
	newCx := rotX + float64(newW)/2
	newCy := rotY + float64(newH)/2

	// After rotation, board is axis-aligned - use long edge as width
	boardW := max(origBounds.Width, origBounds.Height)
	boardH := min(origBounds.Width, origBounds.Height)

	return geometry.RectInt{
		X:      int(newCx) - boardW/2,
		Y:      int(newCy) - boardH/2,
		Width:  boardW,
		Height: boardH,
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// cropImage crops an image to the specified bounds.
func cropImage(img goimage.Image, bounds geometry.RectInt) goimage.Image {
	// Clamp bounds to image dimensions
	imgBounds := img.Bounds()
	x := bounds.X
	y := bounds.Y
	w := bounds.Width
	h := bounds.Height

	if x < imgBounds.Min.X {
		x = imgBounds.Min.X
	}
	if y < imgBounds.Min.Y {
		y = imgBounds.Min.Y
	}
	if x+w > imgBounds.Max.X {
		w = imgBounds.Max.X - x
	}
	if y+h > imgBounds.Max.Y {
		h = imgBounds.Max.Y - y
	}

	// Create cropped image
	cropped := goimage.NewRGBA(goimage.Rect(0, 0, w, h))
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			cropped.Set(dx, dy, img.At(x+dx, y+dy))
		}
	}

	return cropped
}

// flipHorizontal flips an image horizontally (mirror along Y axis).
func flipHorizontal(img goimage.Image) goimage.Image {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	flipped := goimage.NewRGBA(goimage.Rect(0, 0, w, h))

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			flipped.Set(w-1-x, y, img.At(x+bounds.Min.X, y+bounds.Min.Y))
		}
	}
	return flipped
}
