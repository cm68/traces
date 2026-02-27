package alignment

import (
	"fmt"
	"image"
	"math"
	"sort"

	pcbimage "pcb-tracer/internal/image"
	"pcb-tracer/internal/via"
	"pcb-tracer/pkg/geometry"
)

// ViaAlignmentResult holds the result of via-based alignment between front and back images.
type ViaAlignmentResult struct {
	Transform      geometry.AffineTransform
	MatchedVias    int
	TotalFrontVias int
	TotalBackVias  int
	AvgError       float64
	MaxError       float64
	FrontVias      []via.Via // Detected front vias after dense-cluster filtering (matched ones have BothSidesConfirmed=true)
	BackVias       []via.Via // Detected back vias after dense-cluster filtering (matched ones have BothSidesConfirmed=true)
	// Point pairs used for alignment (inliers only)
	UsedFrontPts []geometry.Point2D
	UsedBackPts  []geometry.Point2D
	// Band index (0-based) for each used point, matching UsedFrontPts order.
	// Band 0 is nearest to connector, band N-1 is farthest.
	UsedBands []int
	BandFracs []float64 // The Y-fraction cutoffs used (e.g. [0.25, 0.50, 0.75, 1.0])
}

// AlignWithVias performs via-based alignment between front and back images.
// It detects vias on both sides, matches them, and computes an affine transform
// that maps back image coordinates to front image coordinates.
//
// Since the two images are independently cropped, via coordinates won't match
// directly. We compensate by computing the centroid offset between both via sets,
// shifting back coordinates to align centroids, matching with tight tolerance,
// then computing the affine from the original (unshifted) coordinates.
//
// Returns an error if not enough vias are matched (need >= 3 for affine, prefer >= 6).
func AlignWithVias(frontImg, backImg image.Image, dpi float64) (*ViaAlignmentResult, error) {
	if frontImg == nil || backImg == nil {
		return nil, fmt.Errorf("nil image")
	}

	params := via.DefaultParams().WithDPI(dpi)

	// Detect vias on front
	fmt.Printf("AlignWithVias: detecting front vias (DPI=%.0f, minR=%d, maxR=%d)\n",
		dpi, params.MinRadiusPixels, params.MaxRadiusPixels)
	frontResult, err := via.DetectViasFromImage(frontImg, pcbimage.SideFront, params)
	if err != nil {
		return nil, fmt.Errorf("front via detection failed: %w", err)
	}
	fmt.Printf("AlignWithVias: found %d front vias\n", len(frontResult.Vias))

	// Detect vias on back
	fmt.Printf("AlignWithVias: detecting back vias\n")
	backResult, err := via.DetectViasFromImage(backImg, pcbimage.SideBack, params)
	if err != nil {
		return nil, fmt.Errorf("back via detection failed: %w", err)
	}
	fmt.Printf("AlignWithVias: found %d back vias\n", len(backResult.Vias))

	if len(frontResult.Vias) < 3 || len(backResult.Vias) < 3 {
		return &ViaAlignmentResult{
			TotalFrontVias: len(frontResult.Vias),
			TotalBackVias:  len(backResult.Vias),
			FrontVias:      frontResult.Vias,
			BackVias:       backResult.Vias,
		}, fmt.Errorf("not enough vias: front=%d, back=%d (need >= 3 each)",
			len(frontResult.Vias), len(backResult.Vias))
	}

	// Compute centroids — the images are independently cropped so the same
	// physical via has different pixel coordinates in each image. Subtracting
	// the centroid offset removes the bulk translation before matching.
	frontCentroid := viaCentroid(frontResult.Vias)
	backCentroid := viaCentroid(backResult.Vias)
	offsetX := frontCentroid.X - backCentroid.X
	offsetY := frontCentroid.Y - backCentroid.Y
	fmt.Printf("AlignWithVias: centroid offset dx=%.1f dy=%.1f\n", offsetX, offsetY)

	// Create shifted copies of back vias for matching (shift to front's coordinate frame)
	shiftedBack := make([]via.Via, len(backResult.Vias))
	copy(shiftedBack, backResult.Vias)
	for i := range shiftedBack {
		shiftedBack[i].Center.X += offsetX
		shiftedBack[i].Center.Y += offsetY
	}

	// Match with generous tolerance — the centroid compensation removes bulk
	// translation, but rotation differences between front/back images (up to a
	// few degrees) cause increasing displacement toward the edges. At 2° over
	// 3000px, displacement is ~105px. Use 0.1" tolerance and let RANSAC
	// filter outliers from the wider match set.
	tolerance := 0.1 * dpi // 60px at 600 DPI
	if tolerance < 30 {
		tolerance = 30
	}
	matchResult := via.MatchViasAcrossSides(frontResult.Vias, shiftedBack, tolerance)
	fmt.Printf("AlignWithVias: matched %d vias (tolerance=%.1f px, unmatched=%d)\n",
		matchResult.Matched, tolerance, matchResult.Unmatched)

	// Propagate match flags from shiftedBack to original backResult.Vias
	for i := range shiftedBack {
		if shiftedBack[i].BothSidesConfirmed {
			backResult.Vias[i].BothSidesConfirmed = true
			backResult.Vias[i].MatchedViaID = shiftedBack[i].MatchedViaID
		}
	}

	if matchResult.Matched < 3 {
		return &ViaAlignmentResult{
			MatchedVias:    matchResult.Matched,
			TotalFrontVias: len(frontResult.Vias),
			TotalBackVias:  len(backResult.Vias),
			FrontVias:      frontResult.Vias,
			BackVias:       backResult.Vias,
		}, fmt.Errorf("not enough matched vias for affine: %d (need >= 3)", matchResult.Matched)
	}

	// Extract point pairs: front coords from frontResult, back coords from
	// the ORIGINAL (unshifted) backResult so the affine captures the full transform.
	frontByID := make(map[string]*via.Via, len(frontResult.Vias))
	for i := range frontResult.Vias {
		frontByID[frontResult.Vias[i].ID] = &frontResult.Vias[i]
	}
	origBackByID := make(map[string]*via.Via, len(backResult.Vias))
	for i := range backResult.Vias {
		origBackByID[backResult.Vias[i].ID] = &backResult.Vias[i]
	}

	var frontPts, backPts []geometry.Point2D
	for _, cv := range matchResult.ConfirmedVias {
		fv, fok := frontByID[cv.FrontViaID]
		bv, bok := origBackByID[cv.BackViaID]
		if fok && bok {
			frontPts = append(frontPts, fv.Center)
			backPts = append(backPts, bv.Center)
		}
	}

	if len(frontPts) < 3 {
		return nil, fmt.Errorf("failed to extract enough point pairs: %d", len(frontPts))
	}

	// Compute affine transform: back → front
	transform, inliers, err := ComputeAffineRANSAC(backPts, frontPts, 2000, 3.0)
	if err != nil {
		return nil, fmt.Errorf("affine computation failed: %w", err)
	}

	// Extract inlier points (the ones actually used for the final affine)
	usedFront := make([]geometry.Point2D, len(inliers))
	usedBack := make([]geometry.Point2D, len(inliers))
	for i, idx := range inliers {
		usedFront[i] = frontPts[idx]
		usedBack[i] = backPts[idx]
	}

	avgError := CalculateAlignmentError(usedBack, usedFront, transform)
	fmt.Printf("AlignWithVias: affine computed from %d pairs, %d inliers, avgError=%.2f px\n",
		len(frontPts), len(inliers), avgError)

	return &ViaAlignmentResult{
		Transform:      transform,
		MatchedVias:    matchResult.Matched,
		TotalFrontVias: len(frontResult.Vias),
		TotalBackVias:  len(backResult.Vias),
		AvgError:       avgError,
		FrontVias:      frontResult.Vias,
		BackVias:       backResult.Vias,
		UsedFrontPts:   usedFront,
		UsedBackPts:    usedBack,
	}, nil
}

// quickHoughMatchCount runs a fast Hough voting pass and returns how many
// 1-to-1 via matches were found across all 4 corners. Used to decide whether
// to retry detection with relaxed parameters.
func quickHoughMatchCount(frontResult, backResult *via.ViaDetectionResult,
	frontImg, backImg image.Image, dpi float64) int {

	frontW := float64(frontImg.Bounds().Dx())
	frontH := float64(frontImg.Bounds().Dy())
	backW := float64(backImg.Bounds().Dx())
	backH := float64(backImg.Bounds().Dy())
	frontCorners := [][2]float64{{0, 0}, {frontW, 0}, {0, frontH}, {frontW, frontH}}
	backCorners := [][2]float64{{0, 0}, {backW, 0}, {0, backH}, {backW, backH}}
	const viasPerCorner = 40
	const binSize = 5.0

	type vd struct {
		idx  int
		dist float64
	}

	total := 0
	for ci := 0; ci < 4; ci++ {
		// Select nearest vias to this corner
		fc := frontCorners[ci]
		var frontCands []vd
		for i, fv := range frontResult.Vias {
			dx := fv.Center.X - fc[0]
			dy := fv.Center.Y - fc[1]
			frontCands = append(frontCands, vd{i, math.Sqrt(dx*dx + dy*dy)})
		}
		sort.Slice(frontCands, func(a, b int) bool { return frontCands[a].dist < frontCands[b].dist })
		if len(frontCands) > viasPerCorner {
			frontCands = frontCands[:viasPerCorner]
		}

		bc := backCorners[ci]
		var backCands []vd
		for j, bv := range backResult.Vias {
			dx := bv.Center.X - bc[0]
			dy := bv.Center.Y - bc[1]
			backCands = append(backCands, vd{j, math.Sqrt(dx*dx + dy*dy)})
		}
		sort.Slice(backCands, func(a, b int) bool { return backCands[a].dist < backCands[b].dist })
		if len(backCands) > viasPerCorner {
			backCands = backCands[:viasPerCorner]
		}

		// All-pairs Hough voting
		type votePair struct{ fi, bi int; dx, dy float64 }
		var allPairs []votePair
		votes := make(map[[2]int]int)
		for _, f := range frontCands {
			fv := frontResult.Vias[f.idx]
			for _, b := range backCands {
				dx := fv.Center.X - backResult.Vias[b.idx].Center.X
				dy := fv.Center.Y - backResult.Vias[b.idx].Center.Y
				allPairs = append(allPairs, votePair{f.idx, b.idx, dx, dy})
				bx := int(math.Floor(dx / binSize))
				by := int(math.Floor(dy / binSize))
				votes[[2]int{bx, by}]++
			}
		}

		// Find peak with 3×3 neighborhood, tie-break by magnitude
		var peakBin [2]int
		peakScore := 0
		peakDist2 := math.MaxFloat64
		for k := range votes {
			score := 0
			for ddx := -1; ddx <= 1; ddx++ {
				for ddy := -1; ddy <= 1; ddy++ {
					score += votes[[2]int{k[0] + ddx, k[1] + ddy}]
				}
			}
			cx := (float64(k[0]) + 0.5) * binSize
			cy := (float64(k[1]) + 0.5) * binSize
			d2 := cx*cx + cy*cy
			if score > peakScore || (score == peakScore && d2 < peakDist2) {
				peakScore = score
				peakBin = k
				peakDist2 = d2
			}
		}

		if peakScore < 3 {
			continue
		}

		// Peak center
		var peakDX, peakDY float64
		var peakN int
		for _, p := range allPairs {
			bx := int(math.Floor(p.dx / binSize))
			by := int(math.Floor(p.dy / binSize))
			if bx >= peakBin[0]-1 && bx <= peakBin[0]+1 &&
				by >= peakBin[1]-1 && by <= peakBin[1]+1 {
				peakDX += p.dx
				peakDY += p.dy
				peakN++
			}
		}
		if peakN > 0 {
			peakDX /= float64(peakN)
			peakDY /= float64(peakN)
		}

		peakMag := math.Sqrt(peakDX*peakDX + peakDY*peakDY)
		if peakMag > 0.5*dpi {
			continue
		}

		// Count 1-to-1 matches near peak
		usedF := make(map[int]bool)
		usedB := make(map[int]bool)
		for _, p := range allPairs {
			d := math.Sqrt((p.dx-peakDX)*(p.dx-peakDX) + (p.dy-peakDY)*(p.dy-peakDY))
			if d <= binSize*3 && !usedF[p.fi] && !usedB[p.bi] {
				usedF[p.fi] = true
				usedB[p.bi] = true
				total++
			}
		}
	}
	return total
}

// FineAlignViaTranslation performs fine alignment using vias and contact upper edges.
// After coarse alignment (contacts), the images are rotationally correct but may
// have residual rotation, scale, and translation errors. This function:
//  1. Detects vias on both sides and matches them via corner-based Hough voting
//  2. Optionally uses matched contact upper edges as additional point pairs
//  3. Fits a general affine transform (back → front) with RANSAC
//
// The contact upper edges provide constraint along the entire connector edge,
// including corners where vias may be sparse (e.g. TR under components).
// Only the upper (inner) edges are used because the lower (board-edge) sides vary.
//
// frontContacts/backContacts may be nil if contact data is unavailable.
// coarseTransform maps original back coordinates to coarse-back coordinates
// (needed to transform back contact positions into the coarse-back frame).
// detectAndFilterVias runs parallel via detection (standard + bright-core) on
// both images, merges results, and filters dense clusters. Returns the filtered
// via lists for each side.
func detectAndFilterVias(frontImg, backImg image.Image, dpi float64, params via.DetectionParams, label string) (
	*via.ViaDetectionResult, *via.ViaDetectionResult, error) {

	fmt.Printf("detectAndFilterVias[%s]: DPI=%.0f, minR=%d, maxR=%d, circ=%.2f, fill=%.2f, contrast=%.1f, val=%v\n",
		label, dpi, params.MinRadiusPixels, params.MaxRadiusPixels,
		params.CircularityMin, params.FillRatioMin, params.ContrastMin, params.ValMin)

	type detectResult struct {
		result *via.ViaDetectionResult
		err    error
	}
	frontStdCh := make(chan detectResult, 1)
	frontBCCh := make(chan detectResult, 1)
	backStdCh := make(chan detectResult, 1)
	backBCCh := make(chan detectResult, 1)

	go func() {
		r, e := via.DetectViasFromImage(frontImg, pcbimage.SideFront, params)
		frontStdCh <- detectResult{r, e}
	}()
	go func() {
		r, e := via.DetectBrightCoreVias(frontImg, pcbimage.SideFront, dpi,
			params.MinRadiusPixels, params.MaxRadiusPixels)
		frontBCCh <- detectResult{r, e}
	}()
	go func() {
		r, e := via.DetectViasFromImage(backImg, pcbimage.SideBack, params)
		backStdCh <- detectResult{r, e}
	}()
	go func() {
		r, e := via.DetectBrightCoreVias(backImg, pcbimage.SideBack, dpi,
			params.MinRadiusPixels, params.MaxRadiusPixels)
		backBCCh <- detectResult{r, e}
	}()

	frontStd := <-frontStdCh
	frontBC := <-frontBCCh
	backStd := <-backStdCh
	backBC := <-backBCCh

	if frontStd.err != nil {
		return nil, nil, fmt.Errorf("front via detection failed: %w", frontStd.err)
	}
	if backStd.err != nil {
		return nil, nil, fmt.Errorf("back via detection failed: %w", backStd.err)
	}

	frontResult := frontStd.result
	backResult := backStd.result
	fmt.Printf("  [%s] standard front=%d back=%d\n", label, len(frontResult.Vias), len(backResult.Vias))

	if frontBC.err == nil {
		frontResult.Vias = mergeVias(frontResult.Vias, frontBC.result.Vias, dpi)
	}
	if backBC.err == nil {
		backResult.Vias = mergeVias(backResult.Vias, backBC.result.Vias, dpi)
	}
	fmt.Printf("  [%s] after bright-core merge front=%d back=%d\n", label, len(frontResult.Vias), len(backResult.Vias))

	frontResult.Vias = filterDenseVias(frontResult.Vias, dpi)
	backResult.Vias = filterDenseVias(backResult.Vias, dpi)
	fmt.Printf("  [%s] after dense filter front=%d back=%d\n", label, len(frontResult.Vias), len(backResult.Vias))

	return frontResult, backResult, nil
}

func FineAlignViaTranslation(frontImg, backImg image.Image, dpi float64,
	frontContacts, backContacts *DetectionResult, coarseTransform geometry.AffineTransform) (*ViaAlignmentResult, error) {
	if frontImg == nil || backImg == nil {
		return nil, fmt.Errorf("nil image")
	}

	// Detection parameter levels: start strict, relax if too few matches.
	// Each level makes it progressively easier to detect vias.
	type paramLevel struct {
		label          string
		circularityMin float64
		fillRatioMin   float64
		contrastMin    float64
		valMin         float64
		satMax         float64
		maxDiamInches  float64
	}
	levels := []paramLevel{
		{"standard", 0.40, 0.65, 1.1, 120, 120, 0.059},
		{"relaxed", 0.30, 0.50, 1.05, 100, 140, 0.059},
		{"aggressive", 0.20, 0.35, 1.02, 80, 160, 0.079},
	}

	const minViaMatches = 6 // need at least this many via matches to skip retry

	var frontResult, backResult *via.ViaDetectionResult
	var viaMatches int

	for li, lv := range levels {
		params := via.DefaultParams().WithDPI(dpi)
		params.CircularityMin = lv.circularityMin
		params.FillRatioMin = lv.fillRatioMin
		params.ContrastMin = lv.contrastMin
		params.ValMin = lv.valMin
		params.SatMax = lv.satMax
		if params.MaxDiamInches > lv.maxDiamInches {
			params = params.WithSizeRange(params.MinDiamInches, lv.maxDiamInches)
		}

		var err error
		frontResult, backResult, err = detectAndFilterVias(frontImg, backImg, dpi, params, lv.label)
		if err != nil {
			return nil, err
		}

		if len(frontResult.Vias) < 1 || len(backResult.Vias) < 1 {
			if li < len(levels)-1 {
				fmt.Printf("  [%s] too few vias (front=%d, back=%d), trying next level\n",
					lv.label, len(frontResult.Vias), len(backResult.Vias))
				continue
			}
			return &ViaAlignmentResult{
				TotalFrontVias: len(frontResult.Vias),
				TotalBackVias:  len(backResult.Vias),
				FrontVias:      frontResult.Vias,
				BackVias:       backResult.Vias,
			}, fmt.Errorf("not enough vias: front=%d, back=%d (need >= 1 each)",
				len(frontResult.Vias), len(backResult.Vias))
		}

		// Quick Hough match count to decide if we need to relax
		viaMatches = quickHoughMatchCount(frontResult, backResult, frontImg, backImg, dpi)
		fmt.Printf("  [%s] quick match count: %d vias\n", lv.label, viaMatches)

		if viaMatches >= minViaMatches {
			break // enough matches, proceed
		}
		if li < len(levels)-1 {
			fmt.Printf("  [%s] only %d via matches (need %d), relaxing detection\n",
				lv.label, viaMatches, minViaMatches)
		}
	}

	// Sort both via sets by Y ascending (lowest Y = closest to top/connector)
	sort.Slice(frontResult.Vias, func(i, j int) bool { return frontResult.Vias[i].Center.Y < frontResult.Vias[j].Center.Y })
	sort.Slice(backResult.Vias, func(i, j int) bool { return backResult.Vias[i].Center.Y < backResult.Vias[j].Center.Y })

	// Corner-based matching: pick the N nearest vias to each image corner
	// and match them. Wide spatial spread gives maximum leverage for rotation
	// estimation. Generous tolerance handles missing vias (under components).
	cornerNames := []string{"TL", "TR", "BL", "BR"}
	frontW := float64(frontImg.Bounds().Dx())
	frontH := float64(frontImg.Bounds().Dy())
	backW := float64(backImg.Bounds().Dx())
	backH := float64(backImg.Bounds().Dy())
	frontCorners := [][2]float64{
		{0, 0},             // TL
		{frontW, 0},        // TR
		{0, frontH},        // BL
		{frontW, frontH},   // BR
	}
	backCorners := [][2]float64{
		{0, 0},           // TL
		{backW, 0},       // TR
		{0, backH},       // BL
		{backW, backH},   // BR
	}
	const viasPerCorner = 40
	bandFracs := []float64{0.25, 0.50, 0.75, 1.0} // 4 corners as "bands"

	type matchPair struct {
		front, back       geometry.Point2D
		frontIdx, backIdx int
		dist              float64
		corner            int
	}

	// Pre-select candidates for each corner ONCE so they stay stable
	// across ICP passes. Front candidates use front image corners,
	// back candidates use back image corners (raw coords, no transform).
	type vd struct {
		idx  int
		dist float64
	}
	var cornerFrontCands, cornerBackCands [4][]vd
	for ci := 0; ci < 4; ci++ {
		fc := frontCorners[ci]
		for i, fv := range frontResult.Vias {
			dx := fv.Center.X - fc[0]
			dy := fv.Center.Y - fc[1]
			cornerFrontCands[ci] = append(cornerFrontCands[ci], vd{i, math.Sqrt(dx*dx + dy*dy)})
		}
		sort.Slice(cornerFrontCands[ci], func(a, b int) bool {
			return cornerFrontCands[ci][a].dist < cornerFrontCands[ci][b].dist
		})
		if len(cornerFrontCands[ci]) > viasPerCorner {
			cornerFrontCands[ci] = cornerFrontCands[ci][:viasPerCorner]
		}

		bc := backCorners[ci]
		for j, bv := range backResult.Vias {
			dx := bv.Center.X - bc[0]
			dy := bv.Center.Y - bc[1]
			cornerBackCands[ci] = append(cornerBackCands[ci], vd{j, math.Sqrt(dx*dx + dy*dy)})
		}
		sort.Slice(cornerBackCands[ci], func(a, b int) bool {
			return cornerBackCands[ci][a].dist < cornerBackCands[ci][b].dist
		})
		if len(cornerBackCands[ci]) > viasPerCorner {
			cornerBackCands[ci] = cornerBackCands[ci][:viasPerCorner]
		}
	}

	// Hough voting with RAW back coordinates (no transform applied).
	// This makes voting completely stable — the peak for each corner
	// reflects the actual front-back offset and doesn't change between
	// iterations. The similarity transform is fitted once from all matches.
	const binSize = 5.0 // px — real pairs cluster within ~3-4px
	var matches []matchPair

	// One-time dump of all via positions for diagnostics
	fmt.Printf("\nAll front vias (%d):\n", len(frontResult.Vias))
	for i, v := range frontResult.Vias {
		fmt.Printf("  F%-3d (%6.1f, %6.1f) r=%.1f\n", i+1, v.Center.X, v.Center.Y, v.Radius)
	}
	fmt.Printf("\nAll back vias (%d):\n", len(backResult.Vias))
	for i, v := range backResult.Vias {
		fmt.Printf("  B%-3d (%6.1f, %6.1f) r=%.1f\n", i+1, v.Center.X, v.Center.Y, v.Radius)
	}

	for ci := range frontCorners {
		frontCands := cornerFrontCands[ci]
		backCands := cornerBackCands[ci]

		// All-pairs (dx, dy) deltas using RAW coordinates
		type votePair struct {
			fi, bi int
			dx, dy float64
		}
		var allPairs []votePair
		votes := make(map[[2]int]int)
		for _, fc := range frontCands {
			fv := frontResult.Vias[fc.idx]
			for _, bc := range backCands {
				dx := fv.Center.X - backResult.Vias[bc.idx].Center.X
				dy := fv.Center.Y - backResult.Vias[bc.idx].Center.Y
				allPairs = append(allPairs, votePair{fc.idx, bc.idx, dx, dy})
				bx := int(math.Floor(dx / binSize))
				by := int(math.Floor(dy / binSize))
				votes[[2]int{bx, by}]++
			}
		}

		// Find peak bin using 3×3 neighborhood sum.
		// Break ties by preferring peaks with smaller magnitude — after
		// coarse alignment, real pairs have moderate offsets while false
		// matches from regular spacing have much larger offsets.
		var peakBin [2]int
		peakScore := 0
		peakDist2 := math.MaxFloat64
		for k := range votes {
			score := 0
			for ddx := -1; ddx <= 1; ddx++ {
				for ddy := -1; ddy <= 1; ddy++ {
					score += votes[[2]int{k[0] + ddx, k[1] + ddy}]
				}
			}
			cx := (float64(k[0]) + 0.5) * binSize
			cy := (float64(k[1]) + 0.5) * binSize
			d2 := cx*cx + cy*cy
			if score > peakScore || (score == peakScore && d2 < peakDist2) {
				peakScore = score
				peakBin = k
				peakDist2 = d2
			}
		}

		// Compute peak center from all pairs in the 3×3 neighborhood
		var peakDX, peakDY float64
		var peakN int
		for _, p := range allPairs {
			bx := int(math.Floor(p.dx / binSize))
			by := int(math.Floor(p.dy / binSize))
			if bx >= peakBin[0]-1 && bx <= peakBin[0]+1 &&
				by >= peakBin[1]-1 && by <= peakBin[1]+1 {
				peakDX += p.dx
				peakDY += p.dy
				peakN++
			}
		}
		if peakN > 0 {
			peakDX /= float64(peakN)
			peakDY /= float64(peakN)
		}

		peakMag := math.Sqrt(peakDX*peakDX + peakDY*peakDY)
		fmt.Printf("  %s: %d×%d=%d pairs, peak at dx=%+.1f dy=%+.1f mag=%.1f (votes=%d)\n",
			cornerNames[ci], len(frontCands), len(backCands),
			len(allPairs), peakDX, peakDY, peakMag, peakScore)

		// Require minimum 3 votes for a credible peak — fewer is noise
		if peakScore < 3 {
			fmt.Printf("  %s: peak too weak (%d votes < 3), skipping corner\n",
				cornerNames[ci], peakScore)
			continue
		}

		// Reject corners whose peak offset is implausibly large.
		// After coarse alignment, the raw offsets include residual rotation
		// and scale effects — typically 5-100px. False matches from regular
		// via spacing produce huge offsets (1000+ px).
		maxPeakDist := 0.5 * dpi // 300px at 600 DPI — generous for raw offsets
		if peakMag > maxPeakDist {
			fmt.Printf("  %s: peak offset too large (%.1f px > %.1f), skipping corner\n",
				cornerNames[ci], peakMag, maxPeakDist)
			continue
		}

		// Collect pairs near peak center, greedy 1-to-1 assignment
		type scoredPair struct {
			votePair
			dist float64
		}
		var nearPeak []scoredPair
		for _, p := range allPairs {
			d := math.Sqrt((p.dx-peakDX)*(p.dx-peakDX) + (p.dy-peakDY)*(p.dy-peakDY))
			if d <= binSize*3 { // within 15px of peak center
				nearPeak = append(nearPeak, scoredPair{p, d})
			}
		}
		sort.Slice(nearPeak, func(a, b int) bool { return nearPeak[a].dist < nearPeak[b].dist })

		usedF := make(map[int]bool)
		usedB := make(map[int]bool)
		for _, sp := range nearPeak {
			if usedF[sp.fi] || usedB[sp.bi] {
				continue
			}
			usedF[sp.fi] = true
			usedB[sp.bi] = true
			matches = append(matches, matchPair{
				front:    frontResult.Vias[sp.fi].Center,
				back:     backResult.Vias[sp.bi].Center,
				frontIdx: sp.fi,
				backIdx:  sp.bi,
				dist:     sp.dist,
				corner:   ci,
			})
		}
	}

	// Sort by corner then by X
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].corner != matches[j].corner {
			return matches[i].corner < matches[j].corner
		}
		return matches[i].front.X < matches[j].front.X
	})

	fmt.Printf("Hough voting: %d via matches from %d corners\n", len(matches), len(frontCorners))

	// Dump via matches with raw deltas
	for i, m := range matches {
		dx := m.front.X - m.back.X
		dy := m.front.Y - m.back.Y
		fmt.Printf("  #%-3d %-2s F%-3d B%-3d  front=(%6.1f,%6.1f) back=(%6.1f,%6.1f) dx=%+7.1f dy=%+7.1f\n",
			i+1, cornerNames[m.corner], m.frontIdx+1, m.backIdx+1,
			m.front.X, m.front.Y, m.back.X, m.back.Y, dx, dy)
	}

	// Add matched contact upper edges as additional point pairs.
	// Contacts span the full board width (TL to TR) and are perfectly
	// aligned on Y — their upper (inner) edges provide strong constraint
	// across the top of the board where vias may be sparse.
	const contactCorner = 4 // sentinel: "CT" for contact
	cornerNames = append(cornerNames, "CT")
	if frontContacts != nil && backContacts != nil &&
		len(frontContacts.Contacts) >= 10 && len(backContacts.Contacts) >= 10 {

		// Match contacts by X coordinate (same approach as CoarseAlignFromContacts)
		type contactByX struct {
			x, upperY float64
			idx       int
		}
		var frontByX []contactByX
		for i, c := range frontContacts.Contacts {
			upperY := float64(c.Bounds.Y + c.Bounds.Height)
			frontByX = append(frontByX, contactByX{c.Center.X, upperY, i})
		}
		var backByX []contactByX
		for i, c := range backContacts.Contacts {
			upperY := float64(c.Bounds.Y + c.Bounds.Height)
			backByX = append(backByX, contactByX{c.Center.X, upperY, i})
		}
		sort.Slice(frontByX, func(i, j int) bool { return frontByX[i].x < frontByX[j].x })
		sort.Slice(backByX, func(i, j int) bool { return backByX[i].x < backByX[j].x })

		// X offset between front and back contact centroids
		var fsx, bsx float64
		for _, c := range frontByX {
			fsx += c.x
		}
		for _, c := range backByX {
			bsx += c.x
		}
		xOffset := fsx/float64(len(frontByX)) - bsx/float64(len(backByX))

		xTol := 0.015 * dpi // ~9px at 600 DPI
		if xTol < 8 {
			xTol = 8
		}
		backUsed := make(map[int]bool)
		contactMatches := 0
		for _, fc := range frontByX {
			bestDist := math.MaxFloat64
			bestIdx := -1
			for j, bc := range backByX {
				if backUsed[j] {
					continue
				}
				dx := fc.x - (bc.x + xOffset)
				if math.Abs(dx) > xTol {
					continue
				}
				if math.Abs(dx) < bestDist {
					bestDist = math.Abs(dx)
					bestIdx = j
				}
			}
			if bestIdx >= 0 {
				backUsed[bestIdx] = true
				bc := backByX[bestIdx]
				// Back upper edge in original coordinates → transform to coarse-back frame
				backPt := coarseTransform.Apply(geometry.Point2D{X: bc.x, Y: bc.upperY})
				frontPt := geometry.Point2D{X: fc.x, Y: fc.upperY}
				matches = append(matches, matchPair{
					front:    frontPt,
					back:     backPt,
					frontIdx: -1, // not a via
					backIdx:  -1,
					dist:     0,
					corner:   contactCorner,
				})
				contactMatches++
			}
		}
		fmt.Printf("Contact upper edges: %d matched pairs (xTol=%.0f)\n", contactMatches, xTol)
	}

	if len(matches) < 3 {
		return &ViaAlignmentResult{
			TotalFrontVias: len(frontResult.Vias),
			TotalBackVias:  len(backResult.Vias),
			FrontVias:      frontResult.Vias,
			BackVias:       backResult.Vias,
		}, fmt.Errorf("not enough matched points: %d (need >= 3)", len(matches))
	}

	// Collect point pairs for affine fit: back → front
	var backPts, frontPts []geometry.Point2D
	for _, m := range matches {
		backPts = append(backPts, m.back)
		frontPts = append(frontPts, m.front)
	}

	// Fit affine with RANSAC for robustness — rejects false matches
	// that survived Hough voting. Threshold 5px: real matches under the
	// correct affine should have < 5px residual.
	curTransform, inliers, err := ComputeAffineRANSAC(backPts, frontPts, 2000, 5.0)
	if err != nil {
		return nil, fmt.Errorf("affine fit failed: %w", err)
	}

	// Rebuild cleanMatches from RANSAC inliers
	inlierSet := make(map[int]bool, len(inliers))
	for _, idx := range inliers {
		inlierSet[idx] = true
	}
	var cleanMatches []matchPair
	for i, m := range matches {
		if inlierSet[i] {
			cleanMatches = append(cleanMatches, m)
		}
	}

	// Decompose affine for diagnostics: rotation, X-scale, Y-scale
	angle := math.Atan2(curTransform.C, curTransform.A)
	sx := math.Sqrt(curTransform.A*curTransform.A + curTransform.C*curTransform.C)
	det := curTransform.A*curTransform.D - curTransform.B*curTransform.C
	sy := det / sx // signed Y-scale

	fmt.Printf("Affine fit: rot=%.4f° sx=%.6f sy=%.6f tx=%.1f ty=%.1f (%d inliers / %d pairs)\n",
		angle*180/math.Pi, sx, sy, curTransform.TX, curTransform.TY,
		len(inliers), len(matches))

	// Post-fit residuals
	var postSum, postMax float64
	var avgError, maxErr float64
	fmt.Printf("Post-fit residuals (by corner, X):\n")
	for i, m := range cleanMatches {
		mx := curTransform.A*m.back.X + curTransform.B*m.back.Y + curTransform.TX
		my := curTransform.C*m.back.X + curTransform.D*m.back.Y + curTransform.TY
		dx := m.front.X - mx
		dy := m.front.Y - my
		e := math.Sqrt(dx*dx + dy*dy)
		postSum += e
		if e > postMax {
			postMax = e
		}
		fmt.Printf("  #%-3d %-2s F%-3d B%-3d  (%6.1f,%6.1f) dx=%+5.1f dy=%+5.1f dist=%4.1f\n",
			i+1, cornerNames[m.corner], m.frontIdx+1, m.backIdx+1,
			m.front.X, m.front.Y, dx, dy, e)
	}
	avgError = postSum / float64(len(cleanMatches))
	maxErr = postMax
	fmt.Printf("Post-fit: avg=%.2f max=%.2f px (%d inliers)\n",
		avgError, maxErr, len(cleanMatches))

	// Rebuild used points from final clean matches
	var usedFront, usedBack []geometry.Point2D
	var usedBands []int
	for _, m := range cleanMatches {
		usedFront = append(usedFront, m.front)
		usedBack = append(usedBack, m.back)
		usedBands = append(usedBands, m.corner)
	}

	// Mark matched vias
	for _, m := range cleanMatches {
		for i := range frontResult.Vias {
			if frontResult.Vias[i].Center.X == m.front.X && frontResult.Vias[i].Center.Y == m.front.Y {
				frontResult.Vias[i].BothSidesConfirmed = true
			}
		}
		for i := range backResult.Vias {
			if backResult.Vias[i].Center.X == m.back.X && backResult.Vias[i].Center.Y == m.back.Y {
				backResult.Vias[i].BothSidesConfirmed = true
			}
		}
	}

	return &ViaAlignmentResult{
		Transform:      curTransform,
		MatchedVias:    len(cleanMatches),
		TotalFrontVias: len(frontResult.Vias),
		TotalBackVias:  len(backResult.Vias),
		AvgError:       avgError,
		MaxError:       maxErr,
		FrontVias:      frontResult.Vias,
		BackVias:       backResult.Vias,
		UsedFrontPts:   usedFront,
		UsedBackPts:    usedBack,
		UsedBands:      usedBands,
		BandFracs:      bandFracs,
	}, nil
}

// filterDenseVias removes vias that are part of dense clusters (e.g. header
// pin holes). Isolated PCB vias typically have 0-2 neighbors within a small
// radius, while header pins on 0.1" pitch have 3+. Removing dense clusters
// prevents systematic mismatch when row ordering flips between front and back.
func filterDenseVias(vias []via.Via, dpi float64) []via.Via {
	radius := 0.15 * dpi // ~90px at 600 DPI — 1.5× typical 0.1" header pitch
	r2 := radius * radius
	const maxNeighbors = 2

	var kept []via.Via
	for i, v := range vias {
		neighbors := 0
		for j, u := range vias {
			if i == j {
				continue
			}
			dx := v.Center.X - u.Center.X
			dy := v.Center.Y - u.Center.Y
			if dx*dx+dy*dy <= r2 {
				neighbors++
				if neighbors > maxNeighbors {
					break
				}
			}
		}
		if neighbors <= maxNeighbors {
			kept = append(kept, v)
		}
	}
	if filtered := len(vias) - len(kept); filtered > 0 {
		fmt.Printf("filterDenseVias: removed %d/%d dense-cluster vias (radius=%.0f px)\n",
			filtered, len(vias), radius)
	}
	return kept
}

// mergeVias combines two via lists, deduplicating by proximity.
// If a via from the supplement list is within minDist of an existing via,
// it's considered a duplicate and skipped. Otherwise it's added.
func mergeVias(existing, supplement []via.Via, dpi float64) []via.Via {
	// Minimum distance to consider two detections the same via
	minDist := 0.015 * dpi // ~9px at 600 DPI
	minDist2 := minDist * minDist
	merged := make([]via.Via, len(existing))
	copy(merged, existing)

	added := 0
	for _, sv := range supplement {
		duplicate := false
		for _, ev := range merged {
			dx := sv.Center.X - ev.Center.X
			dy := sv.Center.Y - ev.Center.Y
			if dx*dx+dy*dy <= minDist2 {
				duplicate = true
				break
			}
		}
		if !duplicate {
			merged = append(merged, sv)
			added++
		}
	}
	if added > 0 {
		fmt.Printf("mergeVias: added %d bright-core vias (%d duplicates skipped)\n",
			added, len(supplement)-added)
	}
	return merged
}

// viaCentroid computes the centroid of a set of vias.
func viaCentroid(vias []via.Via) geometry.Point2D {
	var sx, sy float64
	for _, v := range vias {
		sx += v.Center.X
		sy += v.Center.Y
	}
	n := float64(len(vias))
	return geometry.Point2D{X: sx / n, Y: sy / n}
}

// CoarseAlignFromContacts computes a coarse affine transform that aligns the back
// image to the front using gold edge-card contacts. The transform corrects for
// rotation difference and translation offset between the two independently-cropped images.
//
// Rather than trusting per-side ContactAngle (which can be noisy with few contacts),
// we match contacts between front and back by X coordinate, then compute the rotation
// directly from how the Y-difference varies across X. This measures the actual
// front-to-back rotation difference robustly.
func CoarseAlignFromContacts(frontResult, backResult *DetectionResult) (geometry.AffineTransform, error) {
	if len(frontResult.Contacts) < 10 || len(backResult.Contacts) < 10 {
		return geometry.Identity(), fmt.Errorf("not enough contacts: front=%d, back=%d (need >= 10 each)",
			len(frontResult.Contacts), len(backResult.Contacts))
	}

	frontAvg := contactCentroid(frontResult.Contacts)
	backAvg := contactCentroid(backResult.Contacts)

	fmt.Printf("CoarseAlignFromContacts: front=%d contacts, back=%d contacts\n",
		len(frontResult.Contacts), len(backResult.Contacts))
	fmt.Printf("  frontCentroid=(%.1f, %.1f), backCentroid=(%.1f, %.1f)\n",
		frontAvg.X, frontAvg.Y, backAvg.X, backAvg.Y)
	fmt.Printf("  raw angles: front=%.2f°, back=%.2f°\n",
		frontResult.ContactAngle, backResult.ContactAngle)

	// Match contacts between front and back by X coordinate.
	// After coarse cropping, the contacts should be at similar X positions.
	// Sort both by X, then match nearest within tolerance.
	type contactByX struct {
		x, y float64
		idx  int
	}
	frontByX := make([]contactByX, len(frontResult.Contacts))
	for i, c := range frontResult.Contacts {
		frontByX[i] = contactByX{c.Center.X, c.Center.Y, i}
	}
	backByX := make([]contactByX, len(backResult.Contacts))
	for i, c := range backResult.Contacts {
		backByX[i] = contactByX{c.Center.X, c.Center.Y, i}
	}
	sort.Slice(frontByX, func(i, j int) bool { return frontByX[i].x < frontByX[j].x })
	sort.Slice(backByX, func(i, j int) bool { return backByX[i].x < backByX[j].x })

	// X offset between the two images (from centroid difference)
	xOffset := frontAvg.X - backAvg.X

	// Match: for each front contact, find nearest back contact (after X offset)
	type contactMatch struct {
		frontX, frontY float64
		backX, backY   float64
	}
	xTol := 0.015 * frontResult.DPI // ~9px at 600 DPI — one contact pitch
	if xTol < 8 {
		xTol = 8
	}
	var matched []contactMatch
	backUsed := make(map[int]bool)
	for _, fc := range frontByX {
		bestDist := math.MaxFloat64
		bestIdx := -1
		for j, bc := range backByX {
			if backUsed[j] {
				continue
			}
			dx := fc.x - (bc.x + xOffset)
			if math.Abs(dx) > xTol {
				continue
			}
			if math.Abs(dx) < bestDist {
				bestDist = math.Abs(dx)
				bestIdx = j
			}
		}
		if bestIdx >= 0 {
			matched = append(matched, contactMatch{
				frontX: fc.x, frontY: fc.y,
				backX:  backByX[bestIdx].x, backY: backByX[bestIdx].y,
			})
			backUsed[bestIdx] = true
		}
	}

	fmt.Printf("  matched %d contact pairs by X (tolerance=%.1f px)\n", len(matched), xTol)

	// Compute rotation from per-side ContactAngle values.
	// The contacts are collinear (all on one edge), so dY-vs-X regression
	// can't detect rotation — grid rescue places them at uniform Y.
	// Instead, use the angle of the contact line from each side's detection.
	// The import pipeline processes each image independently, so the images
	// may have significant rotation differences — no clamping here.
	frontAngle := frontResult.ContactAngle
	backAngle := backResult.ContactAngle
	angleDiff := (frontAngle - backAngle) * math.Pi / 180
	fmt.Printf("  rotation from contact angles: front=%.2f° - back=%.2f° = %.3f°\n",
		frontAngle, backAngle, angleDiff*180/math.Pi)

	// Build transform: T(frontAvg) · R(angleDiff) · T(-backAvg)
	toOrigin := geometry.Translation(-backAvg.X, -backAvg.Y)
	rotate := geometry.Rotation(angleDiff)
	toFront := geometry.Translation(frontAvg.X, frontAvg.Y)
	transform := toFront.Compose(rotate.Compose(toOrigin))

	fmt.Printf("  final: rotation=%.3f°, transform: T(%.1f,%.1f)·R(%.4f°)·T(%.1f,%.1f)\n",
		angleDiff*180/math.Pi, frontAvg.X, frontAvg.Y,
		angleDiff*180/math.Pi, -backAvg.X, -backAvg.Y)

	return transform, nil
}

// contactCentroid computes the centroid of a set of contacts.
func contactCentroid(contacts []Contact) geometry.Point2D {
	var sx, sy float64
	for _, c := range contacts {
		sx += c.Center.X
		sy += c.Center.Y
	}
	n := float64(len(contacts))
	return geometry.Point2D{X: sx / n, Y: sy / n}
}

// WarpAffineGoImage applies an affine transform to a Go image.Image.
// The output image will have the specified width and height.
func WarpAffineGoImage(img image.Image, transform geometry.AffineTransform, width, height int) (image.Image, error) {
	mat, err := imageToMat(img)
	if err != nil {
		return nil, fmt.Errorf("failed to convert image: %w", err)
	}
	defer mat.Close()

	warped := WarpAffine(mat, transform, width, height)
	defer warped.Close()

	return matToImage(warped)
}
