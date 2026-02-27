// Command componenttrain extracts component training samples from pcbproj files.
// It reads confirmed components, extracts HSV and size features from the board
// image, and writes them to a training set JSON file.
//
// Usage: componenttrain <pcbproj-file> [output-json]
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"pcb-tracer/internal/component"
	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/pkg/geometry"
)

// ProjectFile mirrors the pcbproj structure for the fields we need.
type ProjectFile struct {
	FrontImagePath string                 `json:"front_image,omitempty"`
	Components     []*component.Component `json:"components,omitempty"`
	DPI            float64                `json:"dpi,omitempty"`
	FrontCrop      *CropBounds            `json:"front_crop,omitempty"`
}

// CropBounds represents a crop region.
type CropBounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <pcbproj-file> [output-json]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nExtracts component training samples from a pcbproj file.\n")
		fmt.Fprintf(os.Stderr, "Default output: lib/component_training.json\n")
		os.Exit(1)
	}

	projectPath := os.Args[1]
	outputPath := "lib/component_training.json"
	if len(os.Args) >= 3 {
		outputPath = os.Args[2]
	}

	// Load project file
	fmt.Printf("Loading project: %s\n", projectPath)
	data, err := os.ReadFile(projectPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading project: %v\n", err)
		os.Exit(1)
	}

	var proj ProjectFile
	if err := json.Unmarshal(data, &proj); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing project: %v\n", err)
		os.Exit(1)
	}

	if proj.FrontImagePath == "" {
		fmt.Fprintf(os.Stderr, "Error: project has no front_image\n")
		os.Exit(1)
	}

	// Filter confirmed components
	var confirmed []*component.Component
	for _, comp := range proj.Components {
		if comp.Confirmed {
			confirmed = append(confirmed, comp)
		}
	}

	if len(confirmed) == 0 {
		fmt.Println("No confirmed components found.")
		os.Exit(0)
	}

	fmt.Printf("Found %d confirmed components\n", len(confirmed))

	// Load front image
	projectDir := filepath.Dir(projectPath)
	imgPath := filepath.Join(projectDir, proj.FrontImagePath)
	fmt.Printf("Loading image: %s\n", imgPath)
	layer, err := pcbimage.Load(imgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading image: %v\n", err)
		os.Exit(1)
	}

	// Use DPI from project file, fall back to image-detected DPI
	dpi := proj.DPI
	if dpi == 0 {
		dpi = layer.DPI
	}
	if dpi == 0 {
		dpi = 600 // reasonable default for scanned boards
	}
	fmt.Printf("Using DPI: %.0f\n", dpi)

	// Extract training samples from each confirmed component
	var samples []component.TrainingSample
	for _, comp := range confirmed {
		bounds := comp.Bounds
		// Apply crop offset if present
		if proj.FrontCrop != nil {
			bounds = geometry.Rect{
				X:      comp.Bounds.X + proj.FrontCrop.X,
				Y:      comp.Bounds.Y + proj.FrontCrop.Y,
				Width:  comp.Bounds.Width,
				Height: comp.Bounds.Height,
			}
		}

		sample := component.ExtractSampleFeatures(layer.Image, bounds, dpi)
		sample.Reference = comp.ID
		samples = append(samples, sample)
		fmt.Printf("  %s: %.1fx%.1f mm (H=%.0f S=%.0f V=%.0f)\n",
			comp.ID, sample.WidthMM, sample.HeightMM,
			sample.MeanHue, sample.MeanSat, sample.MeanVal)
	}

	// Load existing output and merge if it exists
	ts := component.NewTrainingSet()
	if existingData, err := os.ReadFile(outputPath); err == nil {
		if err := json.Unmarshal(existingData, ts); err != nil {
			fmt.Printf("Warning: could not parse existing %s, starting fresh\n", outputPath)
			ts = component.NewTrainingSet()
		} else {
			fmt.Printf("Loaded %d existing samples from %s\n", len(ts.Samples), outputPath)
		}
	}

	// Add new samples (Add deduplicates by bounds)
	for _, s := range samples {
		ts.Add(s)
	}
	ts.Dedup()

	// Write output
	out, err := json.MarshalIndent(ts, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error serializing: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output directory: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(outputPath, out, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing output: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nWrote %d training samples to %s\n", len(ts.Samples), outputPath)
}
