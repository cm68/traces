// Package project provides project file handling and persistence.
package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// File represents a PCB tracer project file (.pcbproj).
type File struct {
	Version     int       `json:"version"`
	Name        string    `json:"name"`
	Created     time.Time `json:"created"`
	Modified    time.Time `json:"modified"`
	BoardType   string    `json:"board_type"`
	Description string    `json:"description,omitempty"`

	// Image paths (relative to project file)
	FrontImagePath string `json:"front_image,omitempty"`
	BackImagePath  string `json:"back_image,omitempty"`

	// Alignment state
	Aligned        bool    `json:"aligned"`
	AlignmentError float64 `json:"alignment_error,omitempty"`
	DPI            float64 `json:"dpi,omitempty"`

	// Data file paths (relative to project file)
	ComponentsPath string `json:"components,omitempty"`
	TracesPath     string `json:"traces,omitempty"`
	NetlistPath    string `json:"netlist,omitempty"`

	// User settings
	Settings ProjectSettings `json:"settings,omitempty"`
}

// ProjectSettings holds user preferences for the project.
type ProjectSettings struct {
	DefaultOCREngine    string `json:"default_ocr_engine,omitempty"`
	ElectronicsOCRMode  bool   `json:"electronics_ocr_mode"`
	TraceColorTolerance int    `json:"trace_color_tolerance,omitempty"`
}

// New creates a new project file with default settings.
func New(name, boardType string) *File {
	now := time.Now()
	return &File{
		Version:   1,
		Name:      name,
		Created:   now,
		Modified:  now,
		BoardType: boardType,
		Settings: ProjectSettings{
			DefaultOCREngine:    "tesseract",
			ElectronicsOCRMode:  true,
			TraceColorTolerance: 40,
		},
	}
}

// Load loads a project from a .pcbproj file.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var proj File
	if err := json.Unmarshal(data, &proj); err != nil {
		return nil, err
	}

	return &proj, nil
}

// Save saves the project to a file.
func (p *File) Save(path string) error {
	p.Modified = time.Now()

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// SetFrontImage sets the front image path (relative to project).
func (p *File) SetFrontImage(projectPath, imagePath string) {
	rel, err := filepath.Rel(filepath.Dir(projectPath), imagePath)
	if err != nil {
		p.FrontImagePath = imagePath
	} else {
		p.FrontImagePath = rel
	}
	p.Modified = time.Now()
}

// SetBackImage sets the back image path (relative to project).
func (p *File) SetBackImage(projectPath, imagePath string) {
	rel, err := filepath.Rel(filepath.Dir(projectPath), imagePath)
	if err != nil {
		p.BackImagePath = imagePath
	} else {
		p.BackImagePath = rel
	}
	p.Modified = time.Now()
}

// GetFrontImagePath returns the absolute path to the front image.
func (p *File) GetFrontImagePath(projectPath string) string {
	if p.FrontImagePath == "" {
		return ""
	}
	if filepath.IsAbs(p.FrontImagePath) {
		return p.FrontImagePath
	}
	return filepath.Join(filepath.Dir(projectPath), p.FrontImagePath)
}

// GetBackImagePath returns the absolute path to the back image.
func (p *File) GetBackImagePath(projectPath string) string {
	if p.BackImagePath == "" {
		return ""
	}
	if filepath.IsAbs(p.BackImagePath) {
		return p.BackImagePath
	}
	return filepath.Join(filepath.Dir(projectPath), p.BackImagePath)
}

// GetComponentsPath returns the absolute path to the components file.
func (p *File) GetComponentsPath(projectPath string) string {
	if p.ComponentsPath == "" {
		// Default: project_name_components.json
		base := projectPath[:len(projectPath)-len(filepath.Ext(projectPath))]
		return base + "_components.json"
	}
	if filepath.IsAbs(p.ComponentsPath) {
		return p.ComponentsPath
	}
	return filepath.Join(filepath.Dir(projectPath), p.ComponentsPath)
}

// GetNetlistPath returns the absolute path to the netlist file.
func (p *File) GetNetlistPath(projectPath string) string {
	if p.NetlistPath == "" {
		// Default: project_name_netlist.json
		base := projectPath[:len(projectPath)-len(filepath.Ext(projectPath))]
		return base + "_netlist.json"
	}
	if filepath.IsAbs(p.NetlistPath) {
		return p.NetlistPath
	}
	return filepath.Join(filepath.Dir(projectPath), p.NetlistPath)
}
