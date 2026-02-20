// Package app provides application lifecycle management, configuration, and events.
package app

import (
	"encoding/json"
	"fmt"
	goimage "image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"pcb-tracer/internal/alignment"
	"pcb-tracer/internal/board"
	"pcb-tracer/internal/component"
	"pcb-tracer/internal/connector"
	"pcb-tracer/internal/features"
	"pcb-tracer/internal/image"
	"pcb-tracer/internal/logo"
	"pcb-tracer/internal/netlist"
	"pcb-tracer/internal/ocr"
	"pcb-tracer/internal/trace"
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

	// Manual alignment offsets (pixels) - persisted to project file
	FrontManualOffset geometry.PointInt
	BackManualOffset  geometry.PointInt

	// Manual rotation (degrees) - persisted to project file
	FrontManualRotation float64
	BackManualRotation  float64

	// Shear factors (1.0 = no change) - persisted to project file
	// TopX/BottomX control horizontal shear, LeftY/RightY control vertical shear
	FrontShearTopX    float64
	FrontShearBottomX float64
	FrontShearLeftY   float64
	FrontShearRightY  float64
	BackShearTopX     float64
	BackShearBottomX  float64
	BackShearLeftY    float64
	BackShearRightY   float64

	// Auto-alignment parameters (from automatic alignment process)
	FrontAutoRotation float64 // Rotation detected during front image alignment
	BackAutoRotation  float64 // Rotation detected during back image alignment
	FrontAutoScaleX   float64 // X scale from auto-alignment (1.0 = no scale)
	FrontAutoScaleY   float64 // Y scale from auto-alignment
	BackAutoScaleX    float64
	BackAutoScaleY    float64

	// Rotation center (board center) - persisted to project file
	FrontRotationCenter geometry.Point2D
	BackRotationCenter  geometry.Point2D

	// Crop bounds (detected during initial load, in original image coords)
	FrontCropBounds geometry.RectInt
	BackCropBounds  geometry.RectInt

	// Per-side sampled color parameters (nil = use defaults)
	FrontColorParams *ColorParams
	BackColorParams  *ColorParams

	// Components
	Components []*component.Component

	// Component detection training set
	ComponentTraining *component.TrainingSet

	// Logo detection training set
	LogoTraining *component.LogoSet

	// Detected features layer (vias and traces from both sides)
	FeaturesLayer *features.DetectedFeaturesLayer

	// Via detection color parameters (nil = use defaults)
	ViaColorParams *ColorParams

	// Via training set for machine learning
	ViaTrainingSet *via.TrainingSet

	// Global OCR training database (shared across all projects)
	GlobalOCRTraining *ocr.GlobalTrainingDB

	// Last used OCR orientation (N/S/E/W) - sticky across dialogs
	LastOCROrientation string

	// Normalized image paths (all transforms baked in)
	FrontNormalizedPath string
	BackNormalizedPath  string

	// Logo library for manufacturer marks
	LogoLibrary *logo.LogoLibrary

	// Board definition for pin mapping
	BoardDefinition *connector.BoardDefinition

	// Viewport state (saved/restored with project)
	ViewZoom    float64
	ViewScrollX float64
	ViewScrollY float64

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
	EventConnectorsCreated
	EventConnectorsChanged
	EventBoardDefinitionLoaded
	EventNetlistCreated
	EventNetlistModified
	EventNormalizationComplete // Fired after Save Aligned normalizes images
)

// EventListener is called when an event occurs.
type EventListener func(data interface{})

// NewState creates a new application state.
func NewState() *State {
	// Load logo library from shared preferences
	logoLib, err := logo.LoadFromPreferences()
	if err != nil {
		fmt.Printf("Warning: could not load logo library: %v\n", err)
		logoLib = logo.NewLogoLibrary()
	}

	// Load global OCR training database
	globalOCR, err := ocr.LoadGlobalTraining()
	if err != nil {
		fmt.Printf("Warning: could not load global OCR training: %v\n", err)
		globalOCR = ocr.NewGlobalTrainingDB()
	}
	if len(globalOCR.Samples) > 0 {
		// Purge any low-quality samples that shouldn't be in the DB
		if removed := globalOCR.PurgeLowScores(0.7); removed > 0 {
			fmt.Printf("Purged %d low-score OCR training samples\n", removed)
			ocr.SaveGlobalTraining(globalOCR)
		}
		fmt.Print(globalOCR.Summary())
	}

	return &State{
		BoardSpec:         board.S100Spec(),
		FeaturesLayer:     features.NewDetectedFeaturesLayer(),
		ViaTrainingSet:    via.NewTrainingSet(),
		GlobalOCRTraining: globalOCR,
		LogoLibrary:       logoLib,
		BoardDefinition:   connector.S100Definition(),
		listeners:         make(map[EventType][]EventListener),
	}
}

// SaveLogoLibrary saves the logo library to shared preferences.
func (s *State) SaveLogoLibrary() error {
	s.mu.RLock()
	lib := s.LogoLibrary
	s.mu.RUnlock()

	if lib == nil {
		return nil
	}
	return lib.SaveToPreferences()
}

// SaveGlobalOCRTraining saves the global OCR training database.
func (s *State) SaveGlobalOCRTraining() error {
	s.mu.RLock()
	db := s.GlobalOCRTraining
	s.mu.RUnlock()

	if db == nil {
		return nil
	}
	return ocr.SaveGlobalTraining(db)
}

// AddOCRTrainingSample adds a sample to the global OCR training database.
func (s *State) AddOCRTrainingSample(groundTruth, detected string, score float64, orientation string, params ocr.OCRParams) {
	s.mu.Lock()
	if s.GlobalOCRTraining == nil {
		s.GlobalOCRTraining = ocr.NewGlobalTrainingDB()
	}
	sample := ocr.CreateSampleFromResult(groundTruth, detected, score, orientation, params)
	s.GlobalOCRTraining.AddSample(sample)
	s.mu.Unlock()

	// Auto-save after adding
	s.SaveGlobalOCRTraining()
}

// GetRecommendedOCRParams returns OCR params based on global training data.
func (s *State) GetRecommendedOCRParams() ocr.OCRParams {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.GlobalOCRTraining != nil && len(s.GlobalOCRTraining.Samples) >= 5 {
		return s.GlobalOCRTraining.GetRecommendedParams()
	}
	return ocr.DefaultOCRParams()
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

	// Restore manual offsets
	s.FrontManualOffset = proj.FrontManualOffset
	s.BackManualOffset = proj.BackManualOffset

	// Restore rotation and shear
	s.FrontManualRotation = proj.FrontManualRotation
	s.BackManualRotation = proj.BackManualRotation
	s.FrontShearTopX = proj.FrontShearTopX
	s.FrontShearBottomX = proj.FrontShearBottomX
	s.FrontShearLeftY = proj.FrontShearLeftY
	s.FrontShearRightY = proj.FrontShearRightY
	s.BackShearTopX = proj.BackShearTopX
	s.BackShearBottomX = proj.BackShearBottomX
	s.BackShearLeftY = proj.BackShearLeftY
	s.BackShearRightY = proj.BackShearRightY

	// Restore auto-alignment parameters
	s.FrontAutoRotation = proj.FrontAutoRotation
	s.BackAutoRotation = proj.BackAutoRotation
	s.FrontAutoScaleX = proj.FrontAutoScaleX
	s.FrontAutoScaleY = proj.FrontAutoScaleY
	s.BackAutoScaleX = proj.BackAutoScaleX
	s.BackAutoScaleY = proj.BackAutoScaleY

	// Restore rotation centers
	s.FrontRotationCenter = proj.FrontRotationCenter
	s.BackRotationCenter = proj.BackRotationCenter

	// Restore crop bounds
	s.FrontCropBounds = proj.FrontCropBounds
	s.BackCropBounds = proj.BackCropBounds
	s.mu.Unlock()

	// Restore normalized image paths and viewport
	s.mu.Lock()
	s.FrontNormalizedPath = proj.FrontNormalizedPath
	s.BackNormalizedPath = proj.BackNormalizedPath
	s.ViewZoom = proj.ViewZoom
	s.ViewScrollX = proj.ViewScrollX
	s.ViewScrollY = proj.ViewScrollY
	s.mu.Unlock()

	// Load images - prefer normalized PNGs if they exist
	projectDir := filepath.Dir(path)

	frontLoaded := false
	if proj.FrontNormalizedPath != "" {
		normPath := filepath.Join(projectDir, proj.FrontNormalizedPath)
		if _, err := os.Stat(normPath); err == nil {
			layer := image.NewLayer()
			layer.Path = ""
			if proj.FrontImagePath != "" {
				layer.Path = filepath.Join(projectDir, proj.FrontImagePath)
			}
			layer.Side = image.SideFront
			layer.Visible = true
			if err := s.LoadNormalizedImage(layer, normPath); err != nil {
				fmt.Printf("Failed to load normalized front image, falling back: %v\n", err)
			} else {
				s.mu.Lock()
				s.FrontImage = layer
				if s.DPI == 0 && proj.DPI > 0 {
					s.DPI = proj.DPI
				}
				s.mu.Unlock()
				s.Emit(EventImageLoaded, layer)
				frontLoaded = true
			}
		}
	}
	if !frontLoaded && proj.FrontImagePath != "" {
		frontPath := filepath.Join(projectDir, proj.FrontImagePath)
		if err := s.LoadFrontImage(frontPath); err != nil {
			return err
		}
	}

	backLoaded := false
	if proj.BackNormalizedPath != "" {
		normPath := filepath.Join(projectDir, proj.BackNormalizedPath)
		if _, err := os.Stat(normPath); err == nil {
			layer := image.NewLayer()
			layer.Path = ""
			if proj.BackImagePath != "" {
				layer.Path = filepath.Join(projectDir, proj.BackImagePath)
			}
			layer.Side = image.SideBack
			layer.Visible = true
			if err := s.LoadNormalizedImage(layer, normPath); err != nil {
				fmt.Printf("Failed to load normalized back image, falling back: %v\n", err)
			} else {
				s.mu.Lock()
				s.BackImage = layer
				s.mu.Unlock()
				s.Emit(EventImageLoaded, layer)
				backLoaded = true
			}
		}
	}
	if !backLoaded && proj.BackImagePath != "" {
		backPath := filepath.Join(projectDir, proj.BackImagePath)
		if err := s.LoadBackImage(backPath); err != nil {
			return err
		}
	}

	// Apply all alignment parameters to loaded layers (skip for normalized images)
	s.mu.Lock()
	if s.FrontImage != nil && !s.FrontImage.IsNormalized {
		// Manual adjustments
		s.FrontImage.ManualOffsetX = s.FrontManualOffset.X
		s.FrontImage.ManualOffsetY = s.FrontManualOffset.Y
		s.FrontImage.ManualRotation = s.FrontManualRotation
		s.FrontImage.ShearTopX = s.FrontShearTopX
		s.FrontImage.ShearBottomX = s.FrontShearBottomX
		s.FrontImage.ShearLeftY = s.FrontShearLeftY
		s.FrontImage.ShearRightY = s.FrontShearRightY
		// Auto-alignment parameters
		s.FrontImage.AutoRotation = s.FrontAutoRotation
		s.FrontImage.AutoScaleX = s.FrontAutoScaleX
		s.FrontImage.AutoScaleY = s.FrontAutoScaleY
		// Rotation center (board center)
		s.FrontImage.RotationCenterX = s.FrontRotationCenter.X
		s.FrontImage.RotationCenterY = s.FrontRotationCenter.Y
		// Ensure default values if not set
		if s.FrontImage.ShearTopX == 0 {
			s.FrontImage.ShearTopX = 1.0
		}
		if s.FrontImage.ShearBottomX == 0 {
			s.FrontImage.ShearBottomX = 1.0
		}
		if s.FrontImage.ShearLeftY == 0 {
			s.FrontImage.ShearLeftY = 1.0
		}
		if s.FrontImage.ShearRightY == 0 {
			s.FrontImage.ShearRightY = 1.0
		}
		if s.FrontImage.AutoScaleX == 0 {
			s.FrontImage.AutoScaleX = 1.0
		}
		if s.FrontImage.AutoScaleY == 0 {
			s.FrontImage.AutoScaleY = 1.0
		}
	}
	if s.BackImage != nil && !s.BackImage.IsNormalized {
		// Manual adjustments
		s.BackImage.ManualOffsetX = s.BackManualOffset.X
		s.BackImage.ManualOffsetY = s.BackManualOffset.Y
		s.BackImage.ManualRotation = s.BackManualRotation
		s.BackImage.ShearTopX = s.BackShearTopX
		s.BackImage.ShearBottomX = s.BackShearBottomX
		s.BackImage.ShearLeftY = s.BackShearLeftY
		s.BackImage.ShearRightY = s.BackShearRightY
		// Auto-alignment parameters
		s.BackImage.AutoRotation = s.BackAutoRotation
		s.BackImage.AutoScaleX = s.BackAutoScaleX
		s.BackImage.AutoScaleY = s.BackAutoScaleY
		// Rotation center (board center)
		s.BackImage.RotationCenterX = s.BackRotationCenter.X
		s.BackImage.RotationCenterY = s.BackRotationCenter.Y
		// Ensure default values if not set
		if s.BackImage.ShearTopX == 0 {
			s.BackImage.ShearTopX = 1.0
		}
		if s.BackImage.ShearBottomX == 0 {
			s.BackImage.ShearBottomX = 1.0
		}
		if s.BackImage.ShearLeftY == 0 {
			s.BackImage.ShearLeftY = 1.0
		}
		if s.BackImage.ShearRightY == 0 {
			s.BackImage.ShearRightY = 1.0
		}
		if s.BackImage.AutoScaleX == 0 {
			s.BackImage.AutoScaleX = 1.0
		}
		if s.BackImage.AutoScaleY == 0 {
			s.BackImage.AutoScaleY = 1.0
		}
	}

	// Saved contacts are in pre-alignment coordinates and are not restored.
	// The user must click "Detect Contacts" to re-detect on aligned images.

	// Restore component training samples
	if len(proj.ComponentTrainingSamples) > 0 {
		s.ComponentTraining = component.NewTrainingSet()
		for _, sample := range proj.ComponentTrainingSamples {
			s.ComponentTraining.Add(sample)
		}
	}

	// Restore logo training samples
	if len(proj.LogoTrainingSamples) > 0 {
		s.LogoTraining = component.NewLogoSet()
		for _, sample := range proj.LogoTrainingSamples {
			s.LogoTraining.Add(sample)
		}
	}

	// Restore user-designated components (inline in project file)
	if len(proj.Components) > 0 {
		s.Components = proj.Components
	}

	// Restore vias from project file
	if len(proj.Vias) > 0 {
		if s.FeaturesLayer == nil {
			s.FeaturesLayer = features.NewDetectedFeaturesLayer()
		}
		s.FeaturesLayer.AddVias(proj.Vias)
		fmt.Printf("[Project] Restored %d vias\n", len(proj.Vias))
	}
	if len(proj.ConfirmedVias) > 0 {
		if s.FeaturesLayer == nil {
			s.FeaturesLayer = features.NewDetectedFeaturesLayer()
		}
		for _, cv := range proj.ConfirmedVias {
			s.FeaturesLayer.AddConfirmedVia(cv)
		}
		fmt.Printf("[Project] Restored %d confirmed vias\n", len(proj.ConfirmedVias))
	}
	if len(proj.Traces) > 0 {
		if s.FeaturesLayer == nil {
			s.FeaturesLayer = features.NewDetectedFeaturesLayer()
		}
		s.FeaturesLayer.AddTraces(proj.Traces)
		fmt.Printf("[Project] Restored %d traces\n", len(proj.Traces))
	}
	if len(proj.Connectors) > 0 {
		if s.FeaturesLayer == nil {
			s.FeaturesLayer = features.NewDetectedFeaturesLayer()
		}
		for _, c := range proj.Connectors {
			s.FeaturesLayer.AddConnector(c)
		}
		fmt.Printf("[Project] Restored %d connectors\n", len(proj.Connectors))
	}

	if len(proj.Nets) > 0 {
		if s.FeaturesLayer == nil {
			s.FeaturesLayer = features.NewDetectedFeaturesLayer()
		}
		for _, n := range proj.Nets {
			s.FeaturesLayer.AddNet(n)
		}
		fmt.Printf("[Project] Restored %d nets\n", len(proj.Nets))
	}

	// Logo library is now loaded from shared preferences, not project file
	// Legacy project files with logos are ignored - logos are shared across all projects
	s.mu.Unlock()

	// Load components from external file (legacy support)
	if proj.ComponentsPath != "" && len(s.Components) == 0 {
		compPath := filepath.Join(projectDir, proj.ComponentsPath)
		if err := s.LoadComponents(compPath); err != nil {
			return err
		}
	}

	// Emit components changed if we have any (even if loaded inline)
	if len(s.Components) > 0 {
		s.Emit(EventComponentsChanged, s.Components)
	}

	s.Emit(EventProjectLoaded, path)
	return nil
}

// SaveProject saves the project to the specified path.
func (s *State) SaveProject(path string) error {
	s.mu.RLock()
	proj := ProjectFile{
		Version:           3,
		BoardType:         s.BoardSpec.Name(),
		Aligned:           s.Aligned,
		AlignmentError:    s.AlignmentError,
		DPI:               s.DPI,
		FrontManualOffset: s.FrontManualOffset,
		BackManualOffset:  s.BackManualOffset,
		// Rotation and shear
		FrontManualRotation: s.FrontManualRotation,
		BackManualRotation:  s.BackManualRotation,
		FrontShearTopX:      s.FrontShearTopX,
		FrontShearBottomX:   s.FrontShearBottomX,
		FrontShearLeftY:     s.FrontShearLeftY,
		FrontShearRightY:    s.FrontShearRightY,
		BackShearTopX:       s.BackShearTopX,
		BackShearBottomX:    s.BackShearBottomX,
		BackShearLeftY:      s.BackShearLeftY,
		BackShearRightY:     s.BackShearRightY,
		// Auto-alignment parameters
		FrontAutoRotation:   s.FrontAutoRotation,
		BackAutoRotation:    s.BackAutoRotation,
		FrontAutoScaleX:     s.FrontAutoScaleX,
		FrontAutoScaleY:     s.FrontAutoScaleY,
		BackAutoScaleX:      s.BackAutoScaleX,
		BackAutoScaleY:      s.BackAutoScaleY,
		// Rotation centers
		FrontRotationCenter: s.FrontRotationCenter,
		BackRotationCenter:  s.BackRotationCenter,
		// Crop bounds
		FrontCropBounds: s.FrontCropBounds,
		BackCropBounds:  s.BackCropBounds,
		// Normalized image paths
		FrontNormalizedPath: s.FrontNormalizedPath,
		BackNormalizedPath:  s.BackNormalizedPath,
		// Viewport
		ViewZoom:    s.ViewZoom,
		ViewScrollX: s.ViewScrollX,
		ViewScrollY: s.ViewScrollY,
	}

	// Serialize contacts from detection results
	if s.FrontDetectionResult != nil {
		for _, c := range s.FrontDetectionResult.Contacts {
			proj.FrontContacts = append(proj.FrontContacts, ContactData{
				Center: c.Center,
				Bounds: c.Bounds,
			})
		}
	}
	if s.BackDetectionResult != nil {
		for _, c := range s.BackDetectionResult.Contacts {
			proj.BackContacts = append(proj.BackContacts, ContactData{
				Center: c.Center,
				Bounds: c.Bounds,
			})
		}
	}

	// Serialize component training samples
	if s.ComponentTraining != nil && len(s.ComponentTraining.Samples) > 0 {
		proj.ComponentTrainingSamples = s.ComponentTraining.Samples
	}

	// Serialize logo training samples
	if s.LogoTraining != nil && len(s.LogoTraining.Samples) > 0 {
		proj.LogoTrainingSamples = s.LogoTraining.Samples
	}

	// Serialize user-designated components
	if len(s.Components) > 0 {
		proj.Components = s.Components
	}

	// Serialize vias from features layer
	if s.FeaturesLayer != nil {
		allVias := s.FeaturesLayer.GetAllVias()
		if len(allVias) > 0 {
			proj.Vias = allVias
		}
		confirmedVias := s.FeaturesLayer.GetConfirmedVias()
		if len(confirmedVias) > 0 {
			proj.ConfirmedVias = confirmedVias
		}
		allTraces := s.FeaturesLayer.GetAllTraces()
		if len(allTraces) > 0 {
			proj.Traces = allTraces
			fmt.Printf("[Project] Saving %d traces\n", len(allTraces))
		}
		allConnectors := s.FeaturesLayer.GetConnectors()
		if len(allConnectors) > 0 {
			proj.Connectors = allConnectors
			fmt.Printf("[Project] Saving %d connectors\n", len(allConnectors))
		}
		allNets := s.FeaturesLayer.GetNets()
		if len(allNets) > 0 {
			proj.Nets = allNets
			fmt.Printf("[Project] Saving %d nets\n", len(allNets))
		}
	}

	// Logo library is saved to shared preferences, not project file

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

// ImportFrontImage imports the front side image with automatic detection and processing.
// This detects board bounds, crops, and fine-tunes rotation. Use for new imports only.
func (s *State) ImportFrontImage(path string) error {
	layer, err := image.Load(path)
	if err != nil {
		return err
	}
	layer.Side = image.SideFront

	// Auto-detect board bounds and apply initial rotation/crop
	var cropBounds geometry.RectInt
	layer.Image, cropBounds = autoRotateAndCrop(layer.Image)
	layer.CropX = cropBounds.X
	layer.CropY = cropBounds.Y
	layer.CropWidth = cropBounds.Width
	layer.CropHeight = cropBounds.Height

	// Fine-tune rotation using contact detection
	layer.Image = fineRotateByContacts(layer.Image, s.BoardSpec, layer.DPI)

	s.mu.Lock()
	s.FrontImage = layer
	s.FrontBoardBounds = nil // bounds are now (0,0) since we cropped
	s.FrontCropBounds = cropBounds
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

// LoadFrontImage loads the front side image using saved crop bounds from project.
// This does NOT auto-detect - it applies previously saved processing parameters.
func (s *State) LoadFrontImage(path string) error {
	layer, err := image.Load(path)
	if err != nil {
		return err
	}
	layer.Side = image.SideFront

	s.mu.Lock()
	cropBounds := s.FrontCropBounds
	autoRotation := s.FrontAutoRotation
	s.mu.Unlock()

	// Apply saved crop bounds (if any)
	if cropBounds.Width > 0 && cropBounds.Height > 0 {
		layer.Image = cropImage(layer.Image, cropBounds)
		layer.CropX = cropBounds.X
		layer.CropY = cropBounds.Y
		layer.CropWidth = cropBounds.Width
		layer.CropHeight = cropBounds.Height
	}

	// Apply saved fine rotation (if any)
	if autoRotation != 0 {
		layer.Image = alignment.RotateGoImage(layer.Image, autoRotation)
	}

	s.mu.Lock()
	s.FrontImage = layer
	s.FrontBoardBounds = nil
	// Set DPI from TIFF metadata if available and not already set
	if layer.DPI > 0 && s.DPI == 0 {
		s.DPI = layer.DPI
	}
	s.mu.Unlock()

	s.Emit(EventImageLoaded, layer)
	return nil
}

// ImportBackImage imports the back side image with automatic detection and processing.
// This detects board bounds, crops, fine-tunes rotation, and flips. Use for new imports only.
func (s *State) ImportBackImage(path string) error {
	layer, err := image.Load(path)
	if err != nil {
		return err
	}
	layer.Side = image.SideBack

	// Auto-detect board bounds and apply initial rotation/crop
	var cropBounds geometry.RectInt
	layer.Image, cropBounds = autoRotateAndCrop(layer.Image)
	layer.CropX = cropBounds.X
	layer.CropY = cropBounds.Y
	layer.CropWidth = cropBounds.Width
	layer.CropHeight = cropBounds.Height

	// Fine-tune rotation using contact detection
	layer.Image = fineRotateByContacts(layer.Image, s.BoardSpec, layer.DPI)

	// Flip horizontally - back is viewed from the other side
	layer.Image = flipHorizontal(layer.Image)

	s.mu.Lock()
	s.BackImage = layer
	s.BackBoardBounds = nil // bounds are now (0,0) since we cropped
	s.BackCropBounds = cropBounds
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

// LoadBackImage loads the back side image using saved crop bounds from project.
// This does NOT auto-detect - it applies previously saved processing parameters.
func (s *State) LoadBackImage(path string) error {
	layer, err := image.Load(path)
	if err != nil {
		return err
	}
	layer.Side = image.SideBack

	s.mu.Lock()
	cropBounds := s.BackCropBounds
	autoRotation := s.BackAutoRotation
	s.mu.Unlock()

	// Apply saved crop bounds (if any)
	if cropBounds.Width > 0 && cropBounds.Height > 0 {
		layer.Image = cropImage(layer.Image, cropBounds)
		layer.CropX = cropBounds.X
		layer.CropY = cropBounds.Y
		layer.CropWidth = cropBounds.Width
		layer.CropHeight = cropBounds.Height
	}

	// Apply saved fine rotation (if any)
	if autoRotation != 0 {
		layer.Image = alignment.RotateGoImage(layer.Image, autoRotation)
	}

	// Flip horizontally - back is viewed from the other side
	layer.Image = flipHorizontal(layer.Image)

	s.mu.Lock()
	s.BackImage = layer
	s.BackBoardBounds = nil
	// Set DPI from TIFF metadata if available and not already set
	if layer.DPI > 0 && s.DPI == 0 {
		s.DPI = layer.DPI
	}
	s.mu.Unlock()

	s.Emit(EventImageLoaded, layer)
	return nil
}

// ResetForNewProject clears all state for a new project.
// This zeros all alignment, crop, and detection settings.
func (s *State) ResetForNewProject() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear images
	s.FrontImage = nil
	s.BackImage = nil
	s.AlignedFront = nil
	s.AlignedBack = nil

	// Clear project path
	s.ProjectPath = ""
	s.Modified = false

	// Clear alignment state
	s.Aligned = false
	s.AlignmentError = 0

	// Zero all manual alignment settings
	s.FrontManualOffset = geometry.PointInt{}
	s.BackManualOffset = geometry.PointInt{}
	s.FrontManualRotation = 0
	s.BackManualRotation = 0
	s.FrontShearTopX = 1.0
	s.FrontShearBottomX = 1.0
	s.FrontShearLeftY = 1.0
	s.FrontShearRightY = 1.0
	s.BackShearTopX = 1.0
	s.BackShearBottomX = 1.0
	s.BackShearLeftY = 1.0
	s.BackShearRightY = 1.0

	// Zero auto alignment settings
	s.FrontAutoRotation = 0
	s.BackAutoRotation = 0
	s.FrontAutoScaleX = 1.0
	s.FrontAutoScaleY = 1.0
	s.BackAutoScaleX = 1.0
	s.BackAutoScaleY = 1.0
	s.FrontRotationCenter = geometry.Point2D{}
	s.BackRotationCenter = geometry.Point2D{}

	// Zero crop bounds
	s.FrontCropBounds = geometry.RectInt{}
	s.BackCropBounds = geometry.RectInt{}

	// Clear detection results
	s.FrontDetectionResult = nil
	s.BackDetectionResult = nil
	s.FrontBoardBounds = nil
	s.BackBoardBounds = nil

	// Clear color params
	s.FrontColorParams = nil
	s.BackColorParams = nil

	// Clear components and features
	s.Components = nil
	s.ComponentTraining = nil
	s.LogoTraining = nil
	s.FeaturesLayer = nil
}

// LoadRawFrontImage loads the front side image without any processing.
// No auto-detection, cropping, or rotation is applied.
func (s *State) LoadRawFrontImage(path string) error {
	layer, err := image.Load(path)
	if err != nil {
		return err
	}
	layer.Side = image.SideFront

	s.mu.Lock()
	s.FrontImage = layer
	s.FrontBoardBounds = nil
	// Set DPI from TIFF metadata if available and not already set
	if layer.DPI > 0 && s.DPI == 0 {
		s.DPI = layer.DPI
	}
	s.mu.Unlock()

	s.Emit(EventImageLoaded, layer)
	return nil
}

// LoadRawBackImage loads the back side image with minimal processing.
// No auto-detection, cropping, or rotation is applied.
// The horizontal flip IS applied since it's a viewing requirement, not alignment.
func (s *State) LoadRawBackImage(path string) error {
	layer, err := image.Load(path)
	if err != nil {
		return err
	}
	layer.Side = image.SideBack

	// Flip horizontally - back is viewed from the other side
	// This is not an alignment setting but a viewing requirement
	layer.Image = flipHorizontal(layer.Image)

	s.mu.Lock()
	s.BackImage = layer
	s.BackBoardBounds = nil
	// Set DPI from TIFF metadata if available and not already set
	if layer.DPI > 0 && s.DPI == 0 {
		s.DPI = layer.DPI
	}
	s.mu.Unlock()

	s.Emit(EventImageLoaded, layer)
	return nil
}

// NormalizeFrontImage bakes all transforms into a flat PNG and saves it.
func (s *State) NormalizeFrontImage(projectDir string) error {
	s.mu.Lock()
	if s.FrontImage == nil {
		s.mu.Unlock()
		return fmt.Errorf("no front image loaded")
	}

	// Copy transform params from state to layer
	s.FrontImage.ManualOffsetX = s.FrontManualOffset.X
	s.FrontImage.ManualOffsetY = s.FrontManualOffset.Y
	s.FrontImage.ManualRotation = s.FrontManualRotation
	s.FrontImage.RotationCenterX = s.FrontRotationCenter.X
	s.FrontImage.RotationCenterY = s.FrontRotationCenter.Y
	s.FrontImage.ShearTopX = s.FrontShearTopX
	s.FrontImage.ShearBottomX = s.FrontShearBottomX
	s.FrontImage.ShearLeftY = s.FrontShearLeftY
	s.FrontImage.ShearRightY = s.FrontShearRightY

	normalized, _ := s.FrontImage.Normalize()
	s.mu.Unlock()

	// Save PNG
	normPath := filepath.Join(projectDir, "front_normalized.png")
	if err := saveNormalizedPNG(normalized, normPath); err != nil {
		return fmt.Errorf("failed to save normalized front image: %w", err)
	}

	s.mu.Lock()
	// Replace layer image
	s.FrontImage.Image = normalized
	s.FrontImage.IsNormalized = true
	s.FrontImage.NormalizedPath = normPath

	// Reset manual transforms (they're baked in now)
	s.FrontManualOffset = geometry.PointInt{}
	s.FrontManualRotation = 0
	s.FrontShearTopX = 1.0
	s.FrontShearBottomX = 1.0
	s.FrontShearLeftY = 1.0
	s.FrontShearRightY = 1.0
	s.FrontImage.ManualOffsetX = 0
	s.FrontImage.ManualOffsetY = 0
	s.FrontImage.ManualRotation = 0
	s.FrontImage.ShearTopX = 1.0
	s.FrontImage.ShearBottomX = 1.0
	s.FrontImage.ShearLeftY = 1.0
	s.FrontImage.ShearRightY = 1.0

	// Store relative path for project file
	s.FrontNormalizedPath = "front_normalized.png"

	// Component bounds are NOT remapped. All component coordinates exist
	// exclusively in normalized image space. Components created before
	// normalization will be invalid and should be re-created.
	s.mu.Unlock()

	s.SetModified(true)
	fmt.Printf("Front image normalized and saved to %s\n", normPath)
	return nil
}

// NormalizeBackImage bakes all transforms into a flat PNG and saves it.
func (s *State) NormalizeBackImage(projectDir string) error {
	s.mu.Lock()
	if s.BackImage == nil {
		s.mu.Unlock()
		return fmt.Errorf("no back image loaded")
	}

	// Copy transform params from state to layer
	s.BackImage.ManualOffsetX = s.BackManualOffset.X
	s.BackImage.ManualOffsetY = s.BackManualOffset.Y
	s.BackImage.ManualRotation = s.BackManualRotation
	s.BackImage.RotationCenterX = s.BackRotationCenter.X
	s.BackImage.RotationCenterY = s.BackRotationCenter.Y
	s.BackImage.ShearTopX = s.BackShearTopX
	s.BackImage.ShearBottomX = s.BackShearBottomX
	s.BackImage.ShearLeftY = s.BackShearLeftY
	s.BackImage.ShearRightY = s.BackShearRightY

	normalized, _ := s.BackImage.Normalize()
	s.mu.Unlock()

	// Save PNG
	normPath := filepath.Join(projectDir, "back_normalized.png")
	if err := saveNormalizedPNG(normalized, normPath); err != nil {
		return fmt.Errorf("failed to save normalized back image: %w", err)
	}

	s.mu.Lock()
	// Replace layer image
	s.BackImage.Image = normalized
	s.BackImage.IsNormalized = true
	s.BackImage.NormalizedPath = normPath

	// Reset manual transforms
	s.BackManualOffset = geometry.PointInt{}
	s.BackManualRotation = 0
	s.BackShearTopX = 1.0
	s.BackShearBottomX = 1.0
	s.BackShearLeftY = 1.0
	s.BackShearRightY = 1.0
	s.BackImage.ManualOffsetX = 0
	s.BackImage.ManualOffsetY = 0
	s.BackImage.ManualRotation = 0
	s.BackImage.ShearTopX = 1.0
	s.BackImage.ShearBottomX = 1.0
	s.BackImage.ShearLeftY = 1.0
	s.BackImage.ShearRightY = 1.0

	s.BackNormalizedPath = "back_normalized.png"

	// Component bounds are NOT remapped (same as front).
	s.mu.Unlock()

	s.SetModified(true)
	fmt.Printf("Back image normalized and saved to %s\n", normPath)
	return nil
}

// saveNormalizedPNG writes an image to a PNG file with best compression.
func saveNormalizedPNG(img goimage.Image, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := &png.Encoder{CompressionLevel: png.BestCompression}
	return encoder.Encode(f, img)
}

// LoadNormalizedImage loads a pre-normalized PNG into a layer.
func (s *State) LoadNormalizedImage(layer *image.Layer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	img, _, err := goimage.Decode(f)
	if err != nil {
		return fmt.Errorf("failed to decode normalized image: %w", err)
	}

	layer.Image = img
	layer.IsNormalized = true
	layer.NormalizedPath = path

	// All transforms are identity for normalized images
	layer.ManualOffsetX = 0
	layer.ManualOffsetY = 0
	layer.ManualRotation = 0
	layer.ShearTopX = 1.0
	layer.ShearBottomX = 1.0
	layer.ShearLeftY = 1.0
	layer.ShearRightY = 1.0

	// Preserve DPI: try state first, then extract from original TIFF if available
	if s.DPI > 0 {
		layer.DPI = s.DPI
	} else if layer.Path != "" {
		ext := strings.ToLower(filepath.Ext(layer.Path))
		if ext == ".tiff" || ext == ".tif" {
			if dpi, err := image.ExtractTIFFDPI(layer.Path); err == nil && dpi > 0 {
				layer.DPI = dpi
				fmt.Printf("Extracted DPI %.0f from original TIFF: %s\n", dpi, layer.Path)
			}
		}
	}

	fmt.Printf("Loaded normalized image: %s (%dx%d, DPI=%.0f)\n", path, img.Bounds().Dx(), img.Bounds().Dy(), layer.DPI)
	return nil
}

// HasNormalizedImages returns true if at least one layer has been normalized.
func (s *State) HasNormalizedImages() bool {
	return s.FrontNormalizedPath != "" || s.BackNormalizedPath != ""
}

// autoRotateAndCrop detects board bounds, rotates based on contacts, and crops.
// Returns the cropped image and the crop bounds that were detected.
func autoRotateAndCrop(img goimage.Image) (goimage.Image, geometry.RectInt) {
	// First, just detect board bounds (no rotation yet)
	rotResult := alignment.DetectBoardRotationFromImage(img)
	if !rotResult.Detected {
		// No cropping - return original image with zero bounds (meaning full image)
		return img, geometry.RectInt{}
	}

	// Crop to board bounds first
	cropped := cropImage(img, rotResult.Bounds)

	return cropped, rotResult.Bounds
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

	// Manual alignment offsets (v2+)
	FrontManualOffset geometry.PointInt `json:"front_offset,omitempty"`
	BackManualOffset  geometry.PointInt `json:"back_offset,omitempty"`

	// Manual rotation in degrees (v2+)
	FrontManualRotation float64 `json:"front_rotation,omitempty"`
	BackManualRotation  float64 `json:"back_rotation,omitempty"`

	// Shear factors (v2+) - 0 means use default 1.0
	// TopX/BottomX control horizontal shear, LeftY/RightY control vertical shear
	FrontShearTopX    float64 `json:"front_shear_top_x,omitempty"`
	FrontShearBottomX float64 `json:"front_shear_bottom_x,omitempty"`
	FrontShearLeftY   float64 `json:"front_shear_left_y,omitempty"`
	FrontShearRightY  float64 `json:"front_shear_right_y,omitempty"`
	BackShearTopX     float64 `json:"back_shear_top_x,omitempty"`
	BackShearBottomX  float64 `json:"back_shear_bottom_x,omitempty"`
	BackShearLeftY    float64 `json:"back_shear_left_y,omitempty"`
	BackShearRightY   float64 `json:"back_shear_right_y,omitempty"`

	// Auto-alignment parameters (v3+) - from automatic alignment process
	FrontAutoRotation float64 `json:"front_auto_rotation,omitempty"`
	BackAutoRotation  float64 `json:"back_auto_rotation,omitempty"`
	FrontAutoScaleX   float64 `json:"front_auto_scale_x,omitempty"`
	FrontAutoScaleY   float64 `json:"front_auto_scale_y,omitempty"`
	BackAutoScaleX    float64 `json:"back_auto_scale_x,omitempty"`
	BackAutoScaleY    float64 `json:"back_auto_scale_y,omitempty"`

	// Rotation center (board center) for manual rotation (v3+)
	FrontRotationCenter geometry.Point2D `json:"front_rotation_center,omitempty"`
	BackRotationCenter  geometry.Point2D `json:"back_rotation_center,omitempty"`

	// Crop bounds (v3+) - detected during initial load
	FrontCropBounds geometry.RectInt `json:"front_crop,omitempty"`
	BackCropBounds  geometry.RectInt `json:"back_crop,omitempty"`

	// Detected contacts (v2+)
	FrontContacts []ContactData `json:"front_contacts,omitempty"`
	BackContacts  []ContactData `json:"back_contacts,omitempty"`

	// External data file paths (legacy - use inline fields when possible)
	ComponentsPath string `json:"components_path,omitempty"`
	TracesPath     string `json:"traces_path,omitempty"`
	NetlistPath    string `json:"netlist_path,omitempty"`

	// Component detection training samples (v4+)
	ComponentTrainingSamples []component.TrainingSample `json:"component_training,omitempty"`

	// Logo detection training samples (v4+)
	LogoTrainingSamples []component.LogoSample `json:"logo_training,omitempty"`

	// User-designated components (v5+)
	Components []*component.Component `json:"components,omitempty"`

	// Normalized image paths (v8+) - all transforms baked in
	FrontNormalizedPath string `json:"front_normalized,omitempty"`
	BackNormalizedPath  string `json:"back_normalized,omitempty"`

	// Detected vias (v9+)
	Vias          []via.Via            `json:"vias,omitempty"`
	ConfirmedVias []*via.ConfirmedVia  `json:"confirmed_vias,omitempty"`

	// Traces (v10+)
	Traces []trace.ExtendedTrace `json:"traces,omitempty"`

	// Connectors (v11+) - persistent board edge connectors
	Connectors []*connector.Connector `json:"connectors,omitempty"`

	// Logo library (v7+) - manufacturer mark templates
	LogoLibrary *logo.LogoLibrary `json:"logo_library,omitempty"`

	// Electrical nets (v13+)
	Nets []*netlist.ElectricalNet `json:"nets,omitempty"`

	// Viewport state (v12+)
	ViewZoom    float64 `json:"view_zoom,omitempty"`
	ViewScrollX float64 `json:"view_scroll_x,omitempty"`
	ViewScrollY float64 `json:"view_scroll_y,omitempty"`
}

// ContactData is a JSON-serializable representation of a detected contact.
type ContactData struct {
	Center geometry.Point2D `json:"center"`
	Bounds geometry.RectInt `json:"bounds"`
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

// CreateConnectorsFromAlignment creates Connector objects from the detected alignment contacts.
// This should be called after alignment is complete.
func (s *State) CreateConnectorsFromAlignment() {
	if s.FrontDetectionResult == nil && s.BackDetectionResult == nil {
		return
	}

	bd := s.BoardDefinition
	if bd == nil {
		bd = connector.S100Definition()
		s.BoardDefinition = bd
	}

	// Clear existing connectors
	s.FeaturesLayer.ClearConnectors()

	// Create front connectors
	if s.FrontDetectionResult != nil {
		for i, contact := range s.FrontDetectionResult.Contacts {
			pin := bd.GetPinByPosition(i, true)
			if pin != nil {
				c := connector.NewConnectorFromContact(i, image.SideFront, &contact, pin.PinNumber)
				c.SignalName = pin.SignalName
				s.FeaturesLayer.AddConnector(c)
			}
		}
	}

	// Create back connectors
	if s.BackDetectionResult != nil {
		for i, contact := range s.BackDetectionResult.Contacts {
			pin := bd.GetPinByPosition(i, false)
			if pin != nil {
				c := connector.NewConnectorFromContact(i, image.SideBack, &contact, pin.PinNumber)
				c.SignalName = pin.SignalName
				s.FeaturesLayer.AddConnector(c)
			}
		}
	}

	s.Emit(EventConnectorsCreated, nil)
}
