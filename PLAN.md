# Via & Trace Detection Implementation Plan

## TODO

- [x] Auto-alignment must be sub-pixel accurate
  - Sub-pixel contact centers via image moments (contact_edge.go)
  - Step edge detection for Y-axis registration (corners.go)
  - Tighter RANSAC threshold (3.0 → 1.5 pixels)
  - Step edges integrated into alignment transform (align.go)

## Implementation Status

| Phase | Description | Status |
|-------|-------------|--------|
| **Via Detection Core** | Distance-transform pipeline, contour rescue, training, cross-side matching | ✅ Complete |
| **Trace Vectorization** | Flood-fill auto-trace, polyline drawing, vertex editing | ✅ Complete |
| **Features Layer** | Feature interface, union-find nets, net reconciliation | ✅ Complete |
| **UI Integration** | Import, Components, Traces, Library, Logos, Properties, Net List panels | ✅ Complete |
| **Component Detection** | HSV profiling, grid detection, DIP packages, pin detection, OCR, training | ✅ Complete |
| **Pinout Database** | Parts library, pin names, signal directions, fuzzy matching, aliases | ✅ Complete |
| **Electrical Netlist** | Union-find nets, auto-merge, net splitting, KiCad/SPICE export | ✅ Complete |
| **Edge Connectors** | S-100, ISA, Multibus, ECB, STD Bus with signal definitions | ✅ Complete |
| **Logo Detection** | Manufacturer logo template matching | ✅ Complete |
| **Schematic Generation** | Generate schematic from traced netlist | 🟡 WIP |

## Overview

Implement via and trace detection for PCB board images with a dedicated transparent "Detected Features" layer that combines vias and traces from both board sides. Users can assign colors to group features into buses/nets.

## User Requirements

1. **Transparent layer with opacity slider** - A proper layer (not overlay) for detected features
2. **Combined front+back** - Vias and traces from both sides on same layer
3. **Highly saturated colors** - For visibility against board images
4. **User-assignable colors** - Click to select features, assign colors for bus grouping
5. **Both vias and traces** - Detect both feature types in this implementation
6. **Click to select** - Click individual features to select for bus assignment

---

## Architecture

### New Package: `internal/via/`

```
internal/via/
    types.go      - Via, ViaDetectionParams, ViaDetectionResult
    detector.go   - Detection using Hough circles + contour circularity
    params.go     - Default parameters, DPI-based calculation
```

### Extend Package: `internal/trace/`

The existing trace detector already has K-means copper detection. We'll extend it:
```
internal/trace/
    detector.go   - Existing: AutoDetectCopper, CleanupMask, DetectTraces
    vectorize.go  - NEW: Convert copper mask to trace paths (skeleton + contour)
```

### New Package: `internal/features/`

```
internal/features/
    layer.go      - DetectedFeaturesLayer (renders vias/traces as image)
    feature.go    - Feature interface, Bus grouping, color assignment
    render.go     - Render features to RGBA with colors
```

---

## Key Data Structures

### Via (internal/via/types.go)

```go
type Via struct {
    ID          string            // Unique identifier "via-001"
    Center      geometry.Point2D  // Center in image coordinates
    Radius      float64           // Radius in pixels
    Side        image.Side        // Front or Back
    Circularity float64           // Shape quality (0-1)
    Confidence  float64           // Detection confidence
}
```

### Trace (internal/trace/detector.go - existing, extended)

```go
type Trace struct {
    ID       string              // Unique identifier "trace-001"
    Layer    TraceLayer          // Front or Back
    Points   []geometry.Point2D  // Centerline path vertices
    Width    float64             // Estimated trace width in pixels
    Bounds   geometry.RectInt    // Bounding box for hit testing
}
```

### Feature (internal/features/feature.go)

```go
// Feature is the common interface for vias and traces
type Feature interface {
    FeatureID() string
    FeatureType() string  // "via" or "trace"
    FeatureSide() image.Side
    HitTest(x, y float64) bool
    GetBounds() geometry.RectInt
}

// FeatureRef wraps a feature with bus assignment and color
type FeatureRef struct {
    Feature Feature
    BusID   string      // Empty = unassigned
    Color   color.RGBA  // Effective display color
}
```

### DetectedFeaturesLayer (internal/features/layer.go)

```go
type DetectedFeaturesLayer struct {
    Vias         []via.Via         // All detected vias (front + back)
    Traces       []trace.Trace     // All detected traces (future)
    Buses        map[string]*Bus   // Named buses with colors
    Opacity      float64           // Layer opacity (0-1)
    Visible      bool              // Layer visibility
    DefaultColor color.RGBA        // Unassigned feature color
}

type Bus struct {
    ID       string
    Name     string      // User-friendly name
    Color    color.RGBA  // Highly saturated color
    Features []string    // Feature IDs in this bus
}

// Render produces an RGBA image of all features
func (l *DetectedFeaturesLayer) Render(width, height int) *image.RGBA
```

### ViaDetectionParams (internal/via/params.go)

```go
type ViaDetectionParams struct {
    // HSV for metallic/bright circles
    HueMin, HueMax float64  // 0-180 (all hues for metallic)
    SatMin, SatMax float64  // 0-100 (low sat = metallic)
    ValMin, ValMax float64  // 180-255 (bright)

    // Size constraints
    MinDiamInches float64   // 0.010" typical small via
    MaxDiamInches float64   // 0.050" typical large via
    MinRadiusPixels int     // Calculated from DPI
    MaxRadiusPixels int

    // Shape
    CircularityMin float64  // 0.65 minimum

    // Hough parameters
    HoughDP       float64   // 1.2
    HoughMinDist  int       // Scales with via size
    HoughParam1   float64   // 80 (Canny threshold)
    HoughParam2   float64   // 30 (accumulator threshold)
}
```

---

## Detection Algorithms

### Via Detection Flow (internal/via/detector.go)

1. **Preprocess**
   - Convert to grayscale
   - Gaussian blur (9x9, sigma=2)

2. **Create metallic mask**
   - Convert to HSV
   - InRangeWithScalar for bright, low-saturation pixels
   - Morphological close then open (3x3 ellipse kernel)

3. **Hough circle detection** (primary)
   - Apply mask to grayscale image
   - HoughCirclesWithParams with size constraints from DPI
   - Each detected circle → Via with MethodHoughCircle

4. **Contour circularity detection** (rescue pass)
   - FindContours on metallic mask
   - Filter by area (πr² within min/max)
   - Calculate circularity: 4π·area/perimeter²
   - MinEnclosingCircle for center/radius
   - Skip if near existing Hough detection

5. **Deduplicate**
   - Merge detections within MinRadius distance
   - Keep higher confidence

### Trace Detection Flow (internal/trace/)

Leverages existing `detector.go` + new `vectorize.go`:

1. **Copper mask detection** (existing in detector.go)
   - K-means clustering in LAB color space (AutoDetectCopper)
   - Or manual color selection (DetectByColor)
   - Morphological cleanup (CleanupMask)

2. **Skeletonization** (new in vectorize.go)
   - Thin copper mask to single-pixel centerlines
   - Use Zhang-Suen or morphological thinning

3. **Path extraction**
   - Find contours on skeleton
   - Trace connected pixels to build path vertices
   - Simplify paths (Douglas-Peucker or similar)

4. **Width estimation**
   - For each path point, measure distance to original mask edge
   - Average width along path

5. **Trace creation**
   - Each connected path → Trace with Points, Width, Bounds
   - Filter out noise (minimum length threshold)

---

## UI Integration

### Layers Panel Enhancement

Add "Detected Features" entry to layers list:
- Visibility checkbox
- Opacity slider (0-100%)
- Appears after Front/Back image layers

### New Features Panel (extend Traces Panel)

```
┌─────────────────────────────────────┐
│ Via Detection                       │
├─────────────────────────────────────┤
│ [Detect Front Vias] [Detect Back]   │
│ [Sample Via Color]                  │
│ Vias: 47 front, 52 back             │
├─────────────────────────────────────┤
│ Trace Detection                     │
├─────────────────────────────────────┤
│ [Detect Front Traces] [Detect Back] │
│ [Sample Copper Color]               │
│ Traces: 156 front, 142 back         │
├─────────────────────────────────────┤
│ Bus Assignment                      │
├─────────────────────────────────────┤
│ Selected: 5 features                │
│ [New Bus] [Assign to Bus ▼]         │
│                                     │
│ Buses:                              │
│  ● Address (12 features) [■]        │
│  ● Data (8 features)    [■]         │
│  ● Control (5 features) [■]         │
│  ● Unassigned (230)                 │
└─────────────────────────────────────┘
```

### Feature Selection & Coloring

1. **Click to select** - Click via on canvas to select/deselect
2. **Shift+click** - Add to selection
3. **New Bus** - Create bus with color picker, assign selected features
4. **Assign to Bus** - Dropdown to assign selected to existing bus
5. **Color squares** - Click to edit bus color

### Default Colors (Highly Saturated)

Predefined palette for new buses:
- `#FF0000` Red
- `#00FF00` Green
- `#0000FF` Blue
- `#FFFF00` Yellow
- `#FF00FF` Magenta
- `#00FFFF` Cyan
- `#FF8000` Orange
- `#8000FF` Purple

Unassigned vias: `#FFFFFF` White with black outline

---

## Files to Modify

### New Files

| File | Purpose |
|------|---------|
| `internal/via/types.go` | Via, ViaDetectionResult structs |
| `internal/via/params.go` | ViaDetectionParams, defaults |
| `internal/via/detector.go` | Hough + contour detection |
| `internal/trace/vectorize.go` | Skeleton extraction, path tracing |
| `internal/features/feature.go` | Feature interface, Bus struct |
| `internal/features/layer.go` | DetectedFeaturesLayer |
| `internal/features/render.go` | Render features to RGBA image |

### Modified Files

| File | Changes |
|------|---------|
| `internal/trace/detector.go` | Add Bounds field to Trace, extend API |
| `internal/app/state.go` | Add DetectedFeaturesLayer, color params |
| `ui/panels/sidepanel.go` | Add via/trace detection UI to TracesPanel |
| `ui/canvas/canvas.go` | Feature selection, composite features layer |
| `internal/image/composite.go` | Support DetectedFeaturesLayer in compositing |

---

## Implementation Steps

### Phase 1: Via Detection Core
1. Create `internal/via/types.go` - Via, ViaDetectionResult
2. Create `internal/via/params.go` - ViaDetectionParams with defaults
3. Create `internal/via/detector.go` - Hough + contour detection

### Phase 2: Trace Vectorization
4. Create `internal/trace/vectorize.go` - Skeletonization, path extraction
5. Update `internal/trace/detector.go` - Add Bounds, integrate vectorization

### Phase 3: Features Layer
6. Create `internal/features/feature.go` - Feature interface, Bus struct
7. Create `internal/features/layer.go` - DetectedFeaturesLayer
8. Create `internal/features/render.go` - Render to RGBA with colors
9. Update `internal/app/state.go` - Add DetectedFeaturesLayer field

### Phase 4: UI Integration
10. Update `ui/panels/sidepanel.go` - Via/trace detection controls
11. Update layers panel - Add features layer with opacity slider
12. Update `ui/canvas/canvas.go` - Composite features layer, click selection

### Phase 5: Bus Assignment
13. Add bus creation dialog with color picker
14. Implement click-to-select on canvas
15. Add bus assignment dropdown and management

---

## Verification

1. **Via detection accuracy**
   - Load test PCB image with known vias
   - Run detection on front and back
   - Verify count matches expected
   - Check positions align with visible vias

2. **Trace detection accuracy**
   - Run trace detection on front and back
   - Verify traces follow visible copper paths
   - Check trace widths are reasonable

3. **Layer rendering**
   - Toggle features layer visibility
   - Adjust opacity slider - verify transparency changes
   - Verify features from both sides appear on same layer
   - Check highly saturated colors are visible against board

4. **Feature selection**
   - Click on via - verify it highlights as selected
   - Click on trace - verify selection
   - Shift+click to add to selection
   - Click empty area to deselect

5. **Bus assignment**
   - Select multiple features
   - Create new bus with color picker
   - Verify all selected features change to bus color
   - Reassign to different bus - verify color updates

6. **Build verification**
   - `make build` compiles without errors
   - Application launches and loads test image

---

## Component Detection Layer (Phase 2)

A separate transparent layer for IC/component detection on the component side only.

### Requirements

1. **Package outline detection** - Find rectangular IC shapes (DIP, SOIC, QFP, etc.)
2. **Pin 1 indicator detection** - Notch, dot, chamfer
3. **OCR for part markings** - Custom model for IC fonts (not standard language)
4. **Logo template library** - Match manufacturer logos (Intel, Motorola, TI, etc.)
5. **Separate layer** - Independent from via/trace layer with own opacity

### Architecture

```
internal/component/
    detect.go     - Package outline detection (contour analysis, aspect ratio)
    pin1.go       - Pin 1 indicator detection (notch, dot, chamfer)
    ocr.go        - Custom OCR for IC markings (alphanumeric)
    logo.go       - Logo template matching library
    layer.go      - ComponentsLayer (similar to FeaturesLayer)

assets/logos/    - Template images for manufacturer logos
```

### Detection Algorithm

1. **Find rectangular regions**
   - Edge detection (Canny)
   - Find contours with 4 corners (approxPolyDP)
   - Filter by aspect ratio (DIP ~3:1, SOIC ~2.5:1, PLCC ~1:1)
   - Filter by size (based on DPI and expected package sizes)

2. **Classify package type**
   - DIP: Long rectangle, pins on long edges
   - SOIC/SOP: Similar to DIP but smaller pitch
   - PLCC/QFP: Square-ish, pins on all 4 sides
   - Match against standard package dimension database

3. **Detect pin 1 indicator**
   - Look for notch at one end (semicircular cutout)
   - Look for dot near corner (small bright circle)
   - Look for chamfered corner

4. **Extract marking region**
   - Crop top surface of package
   - Preprocess (contrast, threshold)
   - Segment into text lines

5. **OCR marking text**
   - Custom model trained on IC marking fonts
   - Character set: A-Z, 0-9, common symbols (/, -, space)
   - Recognize part numbers (e.g., "SN74LS244N")
   - Recognize date codes (e.g., "8523")

6. **Match logos**
   - Template matching against logo library
   - Scale-invariant matching (multiple template sizes)
   - Return manufacturer name if matched

### Logo Template Library

Store templates in `assets/logos/`:
- `intel.png`, `intel_old.png` - Intel logos
- `motorola.png`, `motorola_m.png` - Motorola logos
- `ti.png` - Texas Instruments
- `national.png` - National Semiconductor
- `fairchild.png` - Fairchild
- `signetics.png` - Signetics
- `amd.png` - AMD
- `zilog.png` - Zilog
- etc.

### Data Structures

```go
type DetectedComponent struct {
    ID          string           // "ic-001"
    Bounds      geometry.RectInt // Bounding rectangle
    PackageType PackageType      // DIP, SOIC, PLCC, QFP, etc.
    PinCount    int              // Detected or inferred pin count
    Pin1Corner  Corner           // TopLeft, TopRight, BottomLeft, BottomRight
    Marking     ComponentMarking // OCR results
    Confidence  float64
}

type ComponentMarking struct {
    Lines        []string  // Raw OCR text lines
    PartNumber   string    // Extracted part number
    DateCode     string    // Extracted date code
    Manufacturer string    // Matched from logo or text
    LogoMatched  bool      // Whether logo was template-matched
}

type PackageType int
const (
    PackageDIP PackageType = iota
    PackageSOIC
    PackagePLCC
    PackageQFP
    PackageUnknown
)
```

### Implementation Phases

**Phase 1: Package Outline Detection**
- Detect rectangular IC shapes
- Classify by aspect ratio
- Store bounds and estimated package type

**Phase 2: Pin 1 Detection**
- Detect notch/dot/chamfer
- Determine component orientation

**Phase 3: OCR Infrastructure**
- Extract marking region
- Preprocess for OCR
- Initial Tesseract-based recognition

**Phase 4: Custom OCR Model**
- Collect training data from detected ICs
- Train model on IC-specific fonts
- Replace Tesseract with custom model

**Phase 5: Logo Library**
- Create initial logo templates
- Implement template matching
- Build UI for adding new logos

---

## Pinout Database & Labeling Infrastructure

Bus assignment and net labeling depend on knowing what signals are on each pin. This requires a parts database (IC pinouts) and edge connector database (bus pinouts). This is foundational work that can proceed independently of component detection.

### Architecture

```
internal/pinout/
    types.go      - Pin, Package, EdgeConnector types
    database.go   - In-memory database, lookup functions
    loader.go     - Load definitions from JSON/YAML files
    s100.go       - S-100 bus pinout (built-in)

data/pinouts/
    connectors/
        s100.json       - S-100 bus (100 pins)
        isa-8bit.json   - ISA 8-bit (62 pins)
        isa-16bit.json  - ISA 16-bit (98 pins)
        multibus.json   - Multibus I
    chips/
        74xx/
            74ls00.json
            74ls04.json
            74ls244.json
            74ls245.json
            ...
        cpu/
            8080.json
            8085.json
            z80.json
            6502.json
        memory/
            2114.json   - 1Kx4 SRAM
            2716.json   - 2K EPROM
            2732.json   - 4K EPROM
            4116.json   - 16K DRAM
        support/
            8224.json   - 8080 clock generator
            8228.json   - 8080 system controller
            8251.json   - USART
            8255.json   - PPI
```

### Data Structures

```go
// Direction indicates signal direction from the chip's perspective
type Direction int

const (
    DirInput Direction = iota   // Signal flows into the chip
    DirOutput                   // Signal flows out of the chip
    DirBidir                    // Bidirectional (e.g., data bus)
    DirTristate                 // Output with high-Z capability
    DirPower                    // VCC, VDD
    DirGround                   // GND, VSS
    DirNC                       // No connection
    DirClock                    // Clock input
    DirAnalog                   // Analog signal
)

// Pin represents a single pin on a package
type Pin struct {
    Number      int       // Physical pin number (1-based)
    Name        string    // Signal name: "A0", "D7", "/RD", "CLK"
    Direction   Direction
    ActiveLow   bool      // True if signal is active-low (directly from '/' in name)
    Description string    // Optional: "Address bit 0", "Active-low read strobe"
    Alternate   string    // Optional: Alternate function name
}

// Package represents an IC with its pinout
type Package struct {
    PartNumber   string          // "74LS244", "8080A", "Z80"
    Manufacturer string          // "Texas Instruments", "Intel", "Zilog"
    Description  string          // "Octal buffer with tri-state outputs"
    PackageType  string          // "DIP-20", "DIP-40", "PLCC-44"
    PinCount     int
    Pins         map[int]Pin     // Pin number -> Pin definition
    Aliases      []string        // Other part numbers: ["SN74LS244N", "74LS244PC"]
    Family       string          // "74xx", "8080", "Z80"
    Datasheet    string          // URL to datasheet (optional)
}

// EdgeConnector represents a bus connector pinout
type EdgeConnector struct {
    Name        string          // "S-100", "ISA-8", "ISA-16"
    Description string          // "IEEE-696 S-100 Bus"
    PinCount    int             // Total pins (e.g., 100 for S-100)
    Pins        map[int]Pin     // Pin number -> Pin definition

    // For dual-row connectors
    TopRow      string          // "Component side" or pin numbering scheme
    BottomRow   string          // "Solder side"
}

// PinoutDatabase holds all loaded pinout definitions
type PinoutDatabase struct {
    Packages   map[string]*Package       // PartNumber -> Package
    Connectors map[string]*EdgeConnector // Name -> EdgeConnector
    Aliases    map[string]string         // Alias -> canonical PartNumber
}
```

### Lookup API

```go
// LookupPin returns pin info for a package and pin number
func (db *PinoutDatabase) LookupPin(partNumber string, pinNum int) (*Pin, error)

// LookupConnectorPin returns pin info for an edge connector
func (db *PinoutDatabase) LookupConnectorPin(connectorName string, pinNum int) (*Pin, error)

// FindBySignal finds all packages with a pin matching the signal name
func (db *PinoutDatabase) FindBySignal(signalName string) []PinLocation

// GetPinsByDirection returns all pins of a package with given direction
func (db *PinoutDatabase) GetPinsByDirection(partNumber string, dir Direction) []Pin

type PinLocation struct {
    PartNumber string
    PinNumber  int
    Pin        Pin
}
```

### JSON Format

**Edge Connector (S-100 example):**
```json
{
  "name": "S-100",
  "description": "IEEE-696 S-100 Bus",
  "pinCount": 100,
  "pins": {
    "1":  {"name": "+8V",    "direction": "power"},
    "2":  {"name": "+16V",   "direction": "power"},
    "3":  {"name": "XRDY",   "direction": "input",  "description": "External Ready"},
    "4":  {"name": "VI0*",   "direction": "input",  "activeLow": true},
    "5":  {"name": "VI1*",   "direction": "input",  "activeLow": true},
    "6":  {"name": "VI2*",   "direction": "input",  "activeLow": true},
    "7":  {"name": "VI3*",   "direction": "input",  "activeLow": true},
    "8":  {"name": "VI4*",   "direction": "input",  "activeLow": true},
    "9":  {"name": "VI5*",   "direction": "input",  "activeLow": true},
    "10": {"name": "VI6*",   "direction": "input",  "activeLow": true},
    "11": {"name": "VI7*",   "direction": "input",  "activeLow": true},
    "79": {"name": "A0",     "direction": "output", "description": "Address bit 0"},
    "80": {"name": "A1",     "direction": "output", "description": "Address bit 1"},
    "...": "..."
  }
}
```

**IC Package (74LS244 example):**
```json
{
  "partNumber": "74LS244",
  "manufacturer": "Texas Instruments",
  "description": "Octal buffer/line driver with tri-state outputs",
  "packageType": "DIP-20",
  "pinCount": 20,
  "family": "74xx",
  "aliases": ["SN74LS244N", "74LS244PC", "HD74LS244P"],
  "pins": {
    "1":  {"name": "/1G",  "direction": "input",    "activeLow": true, "description": "Output enable for buffers 1-4"},
    "2":  {"name": "1A1",  "direction": "input",    "description": "Buffer 1 input"},
    "3":  {"name": "2Y4",  "direction": "tristate", "description": "Buffer 8 output"},
    "4":  {"name": "1A2",  "direction": "input",    "description": "Buffer 2 input"},
    "5":  {"name": "2Y3",  "direction": "tristate", "description": "Buffer 7 output"},
    "6":  {"name": "1A3",  "direction": "input",    "description": "Buffer 3 input"},
    "7":  {"name": "2Y2",  "direction": "tristate", "description": "Buffer 6 output"},
    "8":  {"name": "1A4",  "direction": "input",    "description": "Buffer 4 input"},
    "9":  {"name": "2Y1",  "direction": "tristate", "description": "Buffer 5 output"},
    "10": {"name": "GND",  "direction": "ground"},
    "11": {"name": "2A1",  "direction": "input",    "description": "Buffer 5 input"},
    "12": {"name": "1Y4",  "direction": "tristate", "description": "Buffer 4 output"},
    "13": {"name": "2A2",  "direction": "input",    "description": "Buffer 6 input"},
    "14": {"name": "1Y3",  "direction": "tristate", "description": "Buffer 3 output"},
    "15": {"name": "2A3",  "direction": "input",    "description": "Buffer 7 input"},
    "16": {"name": "1Y2",  "direction": "tristate", "description": "Buffer 2 output"},
    "17": {"name": "2A4",  "direction": "input",    "description": "Buffer 8 input"},
    "18": {"name": "1Y1",  "direction": "tristate", "description": "Buffer 1 output"},
    "19": {"name": "/2G",  "direction": "input",    "activeLow": true, "description": "Output enable for buffers 5-8"},
    "20": {"name": "VCC",  "direction": "power"}
  }
}
```

### UI: Labeling Tab

A new "Labeling" tab in the side panel (separate from Traces):

```
┌─────────────────────────────────────┐
│ Edge Connector                      │
├─────────────────────────────────────┤
│ Type: [S-100 ▼]                     │
│                                     │
│ Pin mapping verified: 47/50         │
│ [Auto-label from contacts]          │
├─────────────────────────────────────┤
│ Components                          │
├─────────────────────────────────────┤
│ Identified: 12 ICs                  │
│                                     │
│ IC-001: 74LS244 (auto)              │
│ IC-002: 74LS244 (auto)              │
│ IC-003: [Unknown ▼] 8-pin DIP       │
│ IC-004: 8080A (manual)              │
│ ...                                 │
│                                     │
│ [Lookup Part] [Add to Database]     │
├─────────────────────────────────────┤
│ Signal Tracing                      │
├─────────────────────────────────────┤
│ From: [S-100 Pin 79 (A0) ▼]         │
│ [Trace Signal]                      │
│                                     │
│ Connections found:                  │
│  → IC-001 pin 2 (1A1)               │
│  → IC-004 pin 25 (A0)               │
│  → Via V-023 → Back side            │
└─────────────────────────────────────┘
```

### Implementation Phases

**Phase 1: Core Types & Database**
1. Create `internal/pinout/types.go` - Direction, Pin, Package, EdgeConnector
2. Create `internal/pinout/database.go` - PinoutDatabase, lookup methods
3. Create `internal/pinout/loader.go` - JSON loading

**Phase 2: Built-in Definitions**
4. Create `data/pinouts/connectors/s100.json` - Full S-100 pinout
5. Create `internal/pinout/s100.go` - Embedded S-100 definition (fallback)
6. Add a few common 74xx chips as examples

**Phase 3: UI Integration**
7. Add "Labeling" tab to side panel
8. Edge connector selection dropdown
9. Component list with part number assignment

**Phase 4: Signal Tracing**
10. Implement trace-following from known pin
11. Display connections in UI
12. Auto-label features based on traced signals

### Relationship to Other Features

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│ Contact         │     │ Component        │     │ Pinout          │
│ Detection       │────▶│ Detection        │────▶│ Database        │
│ (edge pins)     │     │ (IC packages)    │     │ (pin meanings)  │
└─────────────────┘     └──────────────────┘     └─────────────────┘
         │                       │                        │
         │                       │                        │
         ▼                       ▼                        ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Signal Tracing                              │
│  "S-100 pin 79 (A0) connects through via V-023 to IC-004 pin 25" │
└─────────────────────────────────────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Bus Assignment                              │
│  "These 16 traces are the address bus (A0-A15)"                 │
└─────────────────────────────────────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Netlist Export                              │
│  Generate schematic-ready netlist with named signals            │
└─────────────────────────────────────────────────────────────────┘
```

The pinout database enables:
- **Known starting points**: S-100 pins have defined names/directions
- **Endpoint identification**: When a trace reaches an IC pin, look up what signal it should be
- **Validation**: Check if traced connections make sense (output→input, not output→output)
- **Automatic labeling**: Name buses based on where they connect