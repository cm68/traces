// Command viatest runs via detection on a PCB image and outputs results.
package main

import (
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"

	"pcb-tracer/internal/via"
	pcbimage "pcb-tracer/internal/image"

	_ "golang.org/x/image/tiff"
)

func main() {
	imagePath := flag.String("image", "", "Path to PCB image (TIFF, PNG, or JPEG)")
	dpi := flag.Float64("dpi", 600, "Image DPI")
	side := flag.String("side", "front", "Board side: front or back")
	flag.Parse()

	if *imagePath == "" {
		fmt.Println("Usage: viatest -image <path> [-dpi 600] [-side front|back]")
		os.Exit(1)
	}

	// Load image
	f, err := os.Open(*imagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open image: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	img, format, err := image.Decode(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to decode image: %v\n", err)
		os.Exit(1)
	}

	bounds := img.Bounds()
	fmt.Printf("Loaded %s image: %dx%d pixels\n", format, bounds.Dx(), bounds.Dy())
	fmt.Printf("DPI: %.0f\n", *dpi)

	// Determine side
	var boardSide pcbimage.Side
	if *side == "back" {
		boardSide = pcbimage.SideBack
	} else {
		boardSide = pcbimage.SideFront
	}
	fmt.Printf("Side: %s\n", boardSide)

	// Set up detection parameters
	params := via.DefaultParams().WithDPI(*dpi)
	fmt.Printf("\nDetection parameters:\n")
	fmt.Printf("  HSV: H(%.0f-%.0f) S(%.0f-%.0f) V(%.0f-%.0f)\n",
		params.HueMin, params.HueMax, params.SatMin, params.SatMax, params.ValMin, params.ValMax)
	fmt.Printf("  Size: %.3f\" - %.3f\" (radius %d-%d px)\n",
		params.MinDiamInches, params.MaxDiamInches, params.MinRadiusPixels, params.MaxRadiusPixels)
	fmt.Printf("  Circularity min: %.2f\n", params.CircularityMin)
	fmt.Printf("  Fill ratio min: %.2f\n", params.FillRatioMin)
	fmt.Printf("  Contrast min: %.1f\n", params.ContrastMin)
	fmt.Printf("  Hough cross-validate: %v (dp=%.1f minDist=%d param1=%.0f param2=%.0f)\n",
		params.RequireHoughConfirm, params.HoughDP, params.HoughMinDist, params.HoughParam1, params.HoughParam2)

	// Run detection
	fmt.Printf("\nDetecting vias...\n")
	result, err := via.DetectViasFromImage(img, boardSide, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Detection failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nDetected %d vias:\n", len(result.Vias))
	fmt.Printf("%-12s %10s %10s %8s %8s %12s %10s\n",
		"ID", "X", "Y", "Radius", "Circ", "Confidence", "Method")
	fmt.Println(string(make([]byte, 80)))

	for _, v := range result.Vias {
		fmt.Printf("%-12s %10.1f %10.1f %8.1f %8.2f %12.2f %10s\n",
			v.ID, v.Center.X, v.Center.Y, v.Radius, v.Circularity, v.Confidence, v.Method)
	}

	fmt.Printf("\nTotal: %d vias detected\n", len(result.Vias))
}
