// Command aligntest runs the alignment pipeline on front+back images and prints results.
package main

import (
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"

	"pcb-tracer/internal/alignment"
	"pcb-tracer/internal/app"
	"pcb-tracer/pkg/geometry"

	_ "golang.org/x/image/tiff"
)

func main() {
	profile := flag.String("p", "", "Board profile name (e.g. 'S-100 (IEEE 696)')")
	front := flag.String("f", "", "Path to front image")
	back := flag.String("b", "", "Path to back image")
	doAlign := flag.Bool("align", false, "Run full alignment pipeline")
	flag.Parse()

	if *front == "" || *back == "" {
		fmt.Println("Usage: aligntest -f <front> -b <back> -align [-p <profile>]")
		os.Exit(1)
	}

	// Create app state (loads board spec, etc.)
	state := app.NewState()
	if *profile != "" {
		state.SetBoardSpecByName(*profile)
	}

	// Import front
	fmt.Printf("=== Importing front: %s ===\n", *front)
	if err := state.ImportFrontImage(*front); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to import front: %v\n", err)
		os.Exit(1)
	}

	// Import back
	fmt.Printf("\n=== Importing back: %s ===\n", *back)
	if err := state.ImportBackImage(*back); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to import back: %v\n", err)
		os.Exit(1)
	}

	if !*doAlign {
		fmt.Println("\nImport complete. Use -align to run alignment.")
		return
	}

	dpi := state.DPI
	if dpi == 0 && state.FrontImage.DPI > 0 {
		dpi = state.FrontImage.DPI
	}
	if dpi == 0 {
		dpi = 600
	}

	frontImg := state.FrontImage.Image
	backImg := state.BackImage.Image
	frontBounds := frontImg.Bounds()
	frontW, frontH := frontBounds.Dx(), frontBounds.Dy()

	// Step 1: Coarse alignment from contacts
	fmt.Printf("\n=== Coarse alignment (contacts) ===\n")
	frontContactResult, frontErr := alignment.DetectContactsOnTopEdge(
		frontImg, state.BoardSpec, dpi, nil)
	backContactResult, backErr := alignment.DetectContactsOnTopEdge(
		backImg, state.BoardSpec, dpi, nil)

	if frontErr != nil {
		fmt.Fprintf(os.Stderr, "Front contact detection failed: %v\n", frontErr)
		os.Exit(1)
	}
	if backErr != nil {
		fmt.Fprintf(os.Stderr, "Back contact detection failed: %v\n", backErr)
		os.Exit(1)
	}

	coarseTransform, err := alignment.CoarseAlignFromContacts(frontContactResult, backContactResult)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Coarse alignment failed: %v\n", err)
		os.Exit(1)
	}

	// Step 2: Apply coarse transform
	fmt.Printf("\n=== Applying coarse warp ===\n")
	coarseBack, err := alignment.WarpAffineGoImage(backImg, coarseTransform, frontW, frontH)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Coarse warp failed: %v\n", err)
		os.Exit(1)
	}

	// Step 3: Fine alignment via vias
	fmt.Printf("\n=== Fine alignment (vias) ===\n")
	viaResult, viaErr := alignment.FineAlignViaTranslation(frontImg, coarseBack, dpi,
		frontContactResult, backContactResult, coarseTransform)
	if viaErr != nil {
		fmt.Fprintf(os.Stderr, "Via alignment failed: %v\n", viaErr)
		if viaResult == nil {
			os.Exit(1)
		}
	}

	if viaResult != nil && viaResult.MatchedVias >= 1 {
		finalTransform := viaResult.Transform.Compose(coarseTransform)
		angle := math.Atan2(finalTransform.C, finalTransform.A) * 180 / math.Pi
		scale := math.Sqrt(finalTransform.A*finalTransform.A + finalTransform.C*finalTransform.C)

		fmt.Printf("\n=== Final result ===\n")
		fmt.Printf("Matched vias: %d\n", viaResult.MatchedVias)
		fmt.Printf("Avg error: %.2f px\n", viaResult.AvgError)
		fmt.Printf("Max error: %.2f px\n", viaResult.MaxError)
		fmt.Printf("Rotation: %.4f°\n", angle)
		fmt.Printf("Scale: %.6f\n", scale)
		fmt.Printf("Translation: (%.1f, %.1f)\n", finalTransform.TX, finalTransform.TY)

		// Use the fine transform (not composed) since UsedBackPts are in
		// coarse-warped coordinates, not original image coordinates.
		printResiduals(viaResult, viaResult.Transform)
	}
}

func printResiduals(result *alignment.ViaAlignmentResult, transform geometry.AffineTransform) {
	if len(result.UsedFrontPts) == 0 {
		return
	}
	fmt.Printf("\nPer-point residuals (sorted by Y):\n")
	type entry struct {
		fx, fy, err float64
	}
	var entries []entry
	for i := range result.UsedFrontPts {
		fp := result.UsedFrontPts[i]
		bp := result.UsedBackPts[i]
		mx := transform.A*bp.X + transform.B*bp.Y + transform.TX
		my := transform.C*bp.X + transform.D*bp.Y + transform.TY
		dx := fp.X - mx
		dy := fp.Y - my
		entries = append(entries, entry{fp.X, fp.Y, math.Sqrt(dx*dx + dy*dy)})
	}
	for _, e := range entries {
		fmt.Printf("  X=%5.0f Y=%5.0f  err=%.1f px\n", e.fx, e.fy, e.err)
	}
}

// Ensure image.Image is used (for side-effect imports)
var _ image.Image
