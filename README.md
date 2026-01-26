# PCB Tracer

A cross-platform PCB reverse engineering tool written in Go with Fyne GUI. Designed for tracing vintage computer boards like S-100, ISA, and Multibus systems.

## Current Status

**Working:**
- Image loading (TIFF, PNG, JPEG) with automatic DPI extraction
- Layer management with visibility and opacity controls
- Mouse wheel zoom with scrollbar panning
- S-100 board specification with contact detection parameters
- Automatic gold edge contact detection using HSV color filtering
- Contact sampling via rubber-band selection to train color detection
- Front/back image alignment using detected contacts
- Ejector mark detection for precision alignment

**Planned:**
- Trace detection
- Component/IC recognition
- OCR for component labels
- Netlist generation
- Schematic export

## Architecture

```
traces/
├── main.go                  # Entry point
├── internal/
│   ├── alignment/           # Contact detection, image alignment (~4000 lines)
│   ├── app/                 # Application state, theme
│   ├── board/               # Board specifications (S-100 implemented, others stubbed)
│   ├── image/               # Image loading, layers, DPI extraction
│   ├── component/           # Component detection (stub)
│   ├── netlist/             # Netlist generation (stub)
│   ├── ocr/                 # Tesseract integration (stub)
│   ├── project/             # Project file management
│   ├── trace/               # Trace detection (stub)
│   └── version/             # Version info
├── ui/
│   ├── canvas/              # Image canvas with zoom, pan, overlays
│   ├── dialogs/             # Board spec editor
│   ├── mainwindow/          # Main window, menus
│   └── panels/              # Side panels (Import, Layers, etc.)
└── pkg/
    └── geometry/            # Point, Rect, Transform types
```

## Dependencies

- **Fyne v2.5+** - Cross-platform GUI
- **GoCV (OpenCV 4.x)** - Image processing, contact detection

## Building

```bash
make build
```

Or directly:
```bash
go build -o build/pcb-tracer .
```

Requires OpenCV 4.x installed. On Linux:
```bash
make install-deps-linux
```

On macOS:
```bash
make install-deps-macos
```

## Usage

```bash
./build/pcb-tracer [project-file]
```

1. **Load Images**: File > Import Front/Back Image
2. **Verify DPI**: Check that DPI is displayed (extracted from TIFF metadata)
3. **Configure Detection**: Click "Edit Spec..." to adjust HSV color ranges
4. **Sample Contact**: Click "Sample Contact" then rubber-band select a gold contact
5. **Detect Contacts**: Click "Detect Contacts" to find gold edge contacts
6. **Align**: Use detected contacts to align front/back images

## S-100 Board Specifications

- Board: 10" x 5.4375"
- 50 contacts per side, 0.125" pitch
- Contact width: 0.0625", height: 0.375"

At 600 DPI:
- Contact width: 37.5 pixels
- Contact height: 225 pixels
- Contact pitch: 75 pixels

## License

GPL-3.0
