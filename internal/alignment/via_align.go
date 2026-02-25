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
	FrontVias      []via.Via // All detected front vias (matched ones have BothSidesConfirmed=true)
	BackVias       []via.Via // All detected back vias (matched ones have BothSidesConfirmed=true)
	// Point pairs used for the affine (inliers only, after RANSAC)
	UsedFrontPts []geometry.Point2D
	UsedBackPts  []geometry.Point2D
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

// FineAlignViaTranslation performs fine alignment using only translation.
// After coarse alignment (contacts), the images are rotationally correct but may
// have a small translational offset. This function detects vias on both sides,
// sorts them by Y coordinate, matches the lowest-Y vias (closest to the connector
// edge, which are most accurate after coarse rotation correction), and computes
// a pure translation offset from the matched pairs.
func FineAlignViaTranslation(frontImg, backImg image.Image, dpi float64) (*ViaAlignmentResult, error) {
	if frontImg == nil || backImg == nil {
		return nil, fmt.Errorf("nil image")
	}

	params := via.DefaultParams().WithDPI(dpi)
	// Loosen detection for alignment — we need approximate centers spread
	// across the whole board, not high-confidence vias.
	params.CircularityMin = 0.40
	params.FillRatioMin = 0.65
	params.ContrastMin = 1.1
	params.ValMin = 120

	// Detect vias on front
	fmt.Printf("FineAlignViaTranslation: detecting front vias (DPI=%.0f, minR=%d, maxR=%d)\n",
		dpi, params.MinRadiusPixels, params.MaxRadiusPixels)
	frontResult, err := via.DetectViasFromImage(frontImg, pcbimage.SideFront, params)
	if err != nil {
		return nil, fmt.Errorf("front via detection failed: %w", err)
	}
	fmt.Printf("FineAlignViaTranslation: found %d front vias\n", len(frontResult.Vias))

	// Detect vias on back
	fmt.Printf("FineAlignViaTranslation: detecting back vias\n")
	backResult, err := via.DetectViasFromImage(backImg, pcbimage.SideBack, params)
	if err != nil {
		return nil, fmt.Errorf("back via detection failed: %w", err)
	}
	fmt.Printf("FineAlignViaTranslation: found %d back vias\n", len(backResult.Vias))

	if len(frontResult.Vias) < 1 || len(backResult.Vias) < 1 {
		return &ViaAlignmentResult{
			TotalFrontVias: len(frontResult.Vias),
			TotalBackVias:  len(backResult.Vias),
			FrontVias:      frontResult.Vias,
			BackVias:       backResult.Vias,
		}, fmt.Errorf("not enough vias: front=%d, back=%d (need >= 1 each)",
			len(frontResult.Vias), len(backResult.Vias))
	}

	// Sort both via sets by Y ascending (lowest Y = closest to top/connector)
	sortedFront := make([]via.Via, len(frontResult.Vias))
	copy(sortedFront, frontResult.Vias)
	sort.Slice(sortedFront, func(i, j int) bool { return sortedFront[i].Center.Y < sortedFront[j].Center.Y })

	sortedBack := make([]via.Via, len(backResult.Vias))
	copy(sortedBack, backResult.Vias)
	sort.Slice(sortedBack, func(i, j int) bool { return sortedBack[i].Center.Y < sortedBack[j].Center.Y })

	// Take the lowest-Y vias — use the bottom 30% of Y range, or at least 3
	frontYMin := sortedFront[0].Center.Y
	frontYMax := sortedFront[len(sortedFront)-1].Center.Y
	frontYCutoff := frontYMin + (frontYMax-frontYMin)*0.3

	backYMin := sortedBack[0].Center.Y
	backYMax := sortedBack[len(sortedBack)-1].Center.Y
	backYCutoff := backYMin + (backYMax-backYMin)*0.3

	var candidateFront, candidateBack []via.Via
	for _, v := range sortedFront {
		if v.Center.Y <= frontYCutoff {
			candidateFront = append(candidateFront, v)
		}
	}
	for _, v := range sortedBack {
		if v.Center.Y <= backYCutoff {
			candidateBack = append(candidateBack, v)
		}
	}

	fmt.Printf("FineAlignViaTranslation: candidates near connector: front=%d (Y<%.0f), back=%d (Y<%.0f)\n",
		len(candidateFront), frontYCutoff, len(candidateBack), backYCutoff)

	if len(candidateFront) < 1 || len(candidateBack) < 1 {
		return &ViaAlignmentResult{
			TotalFrontVias: len(frontResult.Vias),
			TotalBackVias:  len(backResult.Vias),
			FrontVias:      frontResult.Vias,
			BackVias:       backResult.Vias,
		}, fmt.Errorf("no candidate vias near connector")
	}

	// Match candidates by mutual nearest neighbor. After coarse align, images
	// are roughly aligned so we use tight tolerance. Mutual matching requires
	// both sides to agree — dramatically reduces false matches.
	tolerance := 0.03 * dpi // 18px at 600 DPI
	if tolerance < 10 {
		tolerance = 10
	}

	type matchPair struct {
		front, back geometry.Point2D
		dist        float64
	}

	// For each front candidate, find nearest back candidate
	frontToBack := make(map[int]int)   // front idx → back idx
	frontToDist := make(map[int]float64)
	for i, fv := range candidateFront {
		bestDist := math.MaxFloat64
		bestIdx := -1
		for j, bv := range candidateBack {
			dx := fv.Center.X - bv.Center.X
			dy := fv.Center.Y - bv.Center.Y
			d := math.Sqrt(dx*dx + dy*dy)
			if d < bestDist {
				bestDist = d
				bestIdx = j
			}
		}
		if bestIdx >= 0 && bestDist <= tolerance {
			frontToBack[i] = bestIdx
			frontToDist[i] = bestDist
		}
	}

	// For each back candidate, find nearest front candidate
	backToFront := make(map[int]int)
	for j, bv := range candidateBack {
		bestDist := math.MaxFloat64
		bestIdx := -1
		for i, fv := range candidateFront {
			dx := fv.Center.X - bv.Center.X
			dy := fv.Center.Y - bv.Center.Y
			d := math.Sqrt(dx*dx + dy*dy)
			if d < bestDist {
				bestDist = d
				bestIdx = i
			}
		}
		if bestIdx >= 0 && bestDist <= tolerance {
			backToFront[j] = bestIdx
		}
	}

	// Keep only mutual matches (both agree)
	var matches []matchPair
	for fi, bi := range frontToBack {
		if backToFront[bi] == fi {
			matches = append(matches, matchPair{
				front: candidateFront[fi].Center,
				back:  candidateBack[bi].Center,
				dist:  frontToDist[fi],
			})
		}
	}

	fmt.Printf("FineAlignViaTranslation: matched %d near-connector via pairs (tolerance=%.0f px)\n",
		len(matches), tolerance)

	if len(matches) < 1 {
		return &ViaAlignmentResult{
			TotalFrontVias: len(frontResult.Vias),
			TotalBackVias:  len(backResult.Vias),
			FrontVias:      frontResult.Vias,
			BackVias:       backResult.Vias,
		}, fmt.Errorf("no matched vias within tolerance %.0f px", tolerance)
	}

	// Outlier rejection: use both median-relative AND absolute cutoff.
	// The absolute cutoff prevents high median (from many false matches)
	// from disabling rejection entirely.
	type rawResid struct {
		idx  int
		dist float64
	}
	rawResids := make([]rawResid, len(matches))
	for i, m := range matches {
		dx := m.front.X - m.back.X
		dy := m.front.Y - m.back.Y
		rawResids[i] = rawResid{idx: i, dist: math.Sqrt(dx*dx + dy*dy)}
	}
	sort.Slice(rawResids, func(i, j int) bool { return rawResids[i].dist < rawResids[j].dist })
	medianDist := rawResids[len(rawResids)/2].dist
	cutoff := medianDist * 2.5
	absCutoff := 0.02 * dpi // 12px at 600 DPI — near-connector vias should be very close
	if cutoff > absCutoff {
		cutoff = absCutoff
	}
	if cutoff < 5 {
		cutoff = 5
	}
	var cleanMatches []matchPair
	for _, r := range rawResids {
		if r.dist <= cutoff {
			cleanMatches = append(cleanMatches, matches[r.idx])
		}
	}
	fmt.Printf("FineAlignViaTranslation: outlier rejection: %d -> %d pairs (median=%.1f, absCut=%.1f, cutoff=%.1f)\n",
		len(matches), len(cleanMatches), medianDist, absCutoff, cutoff)
	matches = cleanMatches

	if len(matches) < 1 {
		return &ViaAlignmentResult{
			TotalFrontVias: len(frontResult.Vias),
			TotalBackVias:  len(backResult.Vias),
			FrontVias:      frontResult.Vias,
			BackVias:       backResult.Vias,
		}, fmt.Errorf("no matches survived outlier rejection")
	}

	// ── Iterative refinement ──
	// The transform maps back coords → front coords:
	//   x' = A*x + B*y + TX
	//   y' = C*x + D*y + TY
	// Start with identity, refine each pass.
	curTransform := geometry.Identity()

	// Pass 1: Y-regression from near-connector matches
	{
		var sBY, sFY, sBY2, sBYFY float64
		n := float64(len(matches))
		for _, m := range matches {
			sBY += m.back.Y
			sFY += m.front.Y
			sBY2 += m.back.Y * m.back.Y
			sBYFY += m.back.Y * m.front.Y
		}
		alpha := (n*sBYFY - sBY*sFY) / (n*sBY2 - sBY*sBY)
		beta := (sFY - alpha*sBY) / n
		// x' = x, y' = alpha*y + beta
		curTransform = geometry.AffineTransform{A: 1, B: 0, TX: 0, C: 0, D: alpha, TY: beta}
		fmt.Printf("Pass 1 — Y regression: y' = %.6f * y + %.1f (%d near-connector pairs)\n",
			alpha, beta, len(matches))
	}

	type residual struct {
		frontX, frontY float64
		residX, residY float64
	}

	// Passes 2+: virtually transform all back vias, re-match, regress residuals,
	// update transform. Each pass adds shear/rotation/scale corrections.
	const maxPasses = 5
	var finalMatches []matchPair
	for pass := 2; pass <= maxPasses; pass++ {
		// Apply current transform to all back via positions
		type transformedVia struct {
			origBack geometry.Point2D
			mapped   geometry.Point2D
		}
		allBackT := make([]transformedVia, len(sortedBack))
		for i, bv := range sortedBack {
			mx := curTransform.A*bv.Center.X + curTransform.B*bv.Center.Y + curTransform.TX
			my := curTransform.C*bv.Center.X + curTransform.D*bv.Center.Y + curTransform.TY
			allBackT[i] = transformedVia{origBack: bv.Center, mapped: geometry.Point2D{X: mx, Y: my}}
		}

		// Match all front vias against transformed back vias using mutual nearest neighbor
		passTol := 0.03 * dpi // 18px at 600 DPI
		if passTol < 10 {
			passTol = 10
		}

		// Front → back nearest neighbor
		fToBIdx := make(map[int]int)
		fToBDist := make(map[int]float64)
		for fi, fv := range sortedFront {
			bestDist := math.MaxFloat64
			bestIdx := -1
			for j, bv := range allBackT {
				dx := fv.Center.X - bv.mapped.X
				dy := fv.Center.Y - bv.mapped.Y
				d := math.Sqrt(dx*dx + dy*dy)
				if d < bestDist {
					bestDist = d
					bestIdx = j
				}
			}
			if bestIdx >= 0 && bestDist <= passTol {
				fToBIdx[fi] = bestIdx
				fToBDist[fi] = bestDist
			}
		}

		// Back → front nearest neighbor
		bToFIdx := make(map[int]int)
		for bi, bv := range allBackT {
			bestDist := math.MaxFloat64
			bestIdx := -1
			for fi, fv := range sortedFront {
				dx := fv.Center.X - bv.mapped.X
				dy := fv.Center.Y - bv.mapped.Y
				d := math.Sqrt(dx*dx + dy*dy)
				if d < bestDist {
					bestDist = d
					bestIdx = fi
				}
			}
			if bestIdx >= 0 && bestDist <= passTol {
				bToFIdx[bi] = bestIdx
			}
		}

		// Keep only mutual matches
		var passMatches []matchPair
		for fi, bi := range fToBIdx {
			if bToFIdx[bi] == fi {
				passMatches = append(passMatches, matchPair{
					front: sortedFront[fi].Center,
					back:  allBackT[bi].origBack,
					dist:  fToBDist[fi],
				})
			}
		}

		// Outlier rejection with absolute cutoff
		if len(passMatches) > 3 {
			type resEntry struct {
				idx  int
				dist float64
			}
			var resids []resEntry
			for i, m := range passMatches {
				mx := curTransform.A*m.back.X + curTransform.B*m.back.Y + curTransform.TX
				my := curTransform.C*m.back.X + curTransform.D*m.back.Y + curTransform.TY
				dx := m.front.X - mx
				dy := m.front.Y - my
				resids = append(resids, resEntry{idx: i, dist: math.Sqrt(dx*dx + dy*dy)})
			}
			sort.Slice(resids, func(i, j int) bool { return resids[i].dist < resids[j].dist })
			med := resids[len(resids)/2].dist
			cut := med * 2.5
			absCut := 0.025 * dpi // 15px at 600 DPI — slightly wider than pass 1
			if cut > absCut {
				cut = absCut
			}
			if cut < 5 {
				cut = 5
			}
			var clean []matchPair
			for _, r := range resids {
				if r.dist <= cut {
					clean = append(clean, passMatches[r.idx])
				}
			}
			fmt.Printf("Pass %d — matched %d, after outlier rejection %d (median=%.1f, absCut=%.1f, cutoff=%.1f)\n",
				pass, len(passMatches), len(clean), med, absCut, cut)
			passMatches = clean
		} else {
			fmt.Printf("Pass %d — matched %d (no outlier rejection)\n", pass, len(passMatches))
		}
		finalMatches = passMatches

		if len(passMatches) < 3 {
			fmt.Printf("Pass %d — not enough matches to refine\n", pass)
			break
		}

		// Compute residuals under current transform
		var residuals []residual
		for _, m := range passMatches {
			mx := curTransform.A*m.back.X + curTransform.B*m.back.Y + curTransform.TX
			my := curTransform.C*m.back.X + curTransform.D*m.back.Y + curTransform.TY
			residuals = append(residuals, residual{
				frontX: m.front.X, frontY: m.front.Y,
				residX: m.front.X - mx, residY: m.front.Y - my,
			})
		}

		// Regress residuals to find corrections:
		// residX vs Y → shear (B term)
		// residX vs X → X-scale correction
		// residY vs Y → Y-scale correction
		// residX mean → X translation
		// residY mean → Y translation
		nf := float64(len(residuals))
		var sRX, sRY, sX, sY, sX2, sY2, sYRX, sXRX, sYRY float64
		for _, r := range residuals {
			sRX += r.residX
			sRY += r.residY
			sX += r.frontX
			sY += r.frontY
			sX2 += r.frontX * r.frontX
			sY2 += r.frontY * r.frontY
			sYRX += r.frontY * r.residX
			sXRX += r.frontX * r.residX
			sYRY += r.frontY * r.residY
		}

		// X residual vs Y (shear)
		shearSlope := (nf*sYRX - sY*sRX) / (nf*sY2 - sY*sY)
		shearIntercept := (sRX - shearSlope*sY) / nf

		// X residual vs X (rotation about Y axis / X-scale)
		rotSlope := (nf*sXRX - sX*sRX) / (nf*sX2 - sX*sX)

		// Y residual vs Y (Y-scale correction)
		yScaleSlope := (nf*sYRY - sY*sRY) / (nf*sY2 - sY*sY)
		yScaleIntercept := (sRY - yScaleSlope*sY) / nf

		meanRX := sRX / nf
		meanRY := sRY / nf

		fmt.Printf("Pass %d — residual regressions:\n", pass)
		fmt.Printf("  X vs Y (shear):    slope=%.6f intercept=%.2f\n", shearSlope, shearIntercept)
		fmt.Printf("  X vs X (rotation): slope=%.6f\n", rotSlope)
		fmt.Printf("  Y vs Y (Y-scale):  slope=%.6f intercept=%.2f\n", yScaleSlope, yScaleIntercept)
		fmt.Printf("  mean residual: (%.2f, %.2f)\n", meanRX, meanRY)

		// Build correction transform from residuals.
		// The residual is: front - transform(back) ≈ correction * transform(back)
		// So: corrected = (I + correction) * current_transform
		// correction: x' = (1+rotSlope)*x + shearSlope*y + shearIntercept
		//             y' = yScaleSlope*y + yScaleIntercept  (+ implicit identity)
		correction := geometry.AffineTransform{
			A:  1 + rotSlope,
			B:  shearSlope,
			TX: shearIntercept,
			C:  0,
			D:  1 + yScaleSlope,
			TY: yScaleIntercept,
		}
		curTransform = correction.Compose(curTransform)

		fmt.Printf("Pass %d — updated transform: A=%.6f B=%.6f TX=%.1f C=%.6f D=%.6f TY=%.1f\n",
			pass, curTransform.A, curTransform.B, curTransform.TX,
			curTransform.C, curTransform.D, curTransform.TY)

		// Print post-correction residuals
		var postResiduals []residual
		var postSumErr float64
		for _, m := range passMatches {
			mx := curTransform.A*m.back.X + curTransform.B*m.back.Y + curTransform.TX
			my := curTransform.C*m.back.X + curTransform.D*m.back.Y + curTransform.TY
			dx := m.front.X - mx
			dy := m.front.Y - my
			postSumErr += math.Sqrt(dx*dx + dy*dy)
			postResiduals = append(postResiduals, residual{
				frontX: m.front.X, frontY: m.front.Y,
				residX: dx, residY: dy,
			})
		}
		postAvgErr := postSumErr / nf
		fmt.Printf("Pass %d — post-correction avg error: %.2f px (%d pairs)\n", pass, postAvgErr, len(passMatches))

		// Check convergence: if avg error < 3px, we're done
		if postAvgErr < 3.0 {
			fmt.Printf("Pass %d — converged (avg error %.2f < 3.0 px)\n", pass, postAvgErr)

			sort.Slice(postResiduals, func(i, j int) bool { return postResiduals[i].frontY < postResiduals[j].frontY })
			fmt.Printf("  Final residuals (sorted by Y):\n")
			for _, r := range postResiduals {
				fmt.Printf("    X=%5.0f Y=%5.0f  residual=(%+6.1f, %+6.1f)\n",
					r.frontX, r.frontY, r.residX, r.residY)
			}
			break
		}
	}

	// Compute final error and collect used points
	var usedFront, usedBack []geometry.Point2D
	var sumErr float64
	for _, m := range finalMatches {
		mx := curTransform.A*m.back.X + curTransform.B*m.back.Y + curTransform.TX
		my := curTransform.C*m.back.X + curTransform.D*m.back.Y + curTransform.TY
		dx := m.front.X - mx
		dy := m.front.Y - my
		sumErr += math.Sqrt(dx*dx + dy*dy)
		usedFront = append(usedFront, m.front)
		usedBack = append(usedBack, m.back)
	}
	avgError := 0.0
	if len(finalMatches) > 0 {
		avgError = sumErr / float64(len(finalMatches))
	}

	fmt.Printf("Final transform: A=%.6f B=%.6f TX=%.1f C=%.6f D=%.6f TY=%.1f  (%d matches, avg err=%.2f px)\n",
		curTransform.A, curTransform.B, curTransform.TX,
		curTransform.C, curTransform.D, curTransform.TY,
		len(finalMatches), avgError)

	// Mark matched vias
	for _, m := range finalMatches {
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
		MatchedVias:    len(finalMatches),
		TotalFrontVias: len(frontResult.Vias),
		TotalBackVias:  len(backResult.Vias),
		AvgError:       avgError,
		FrontVias:      frontResult.Vias,
		BackVias:       backResult.Vias,
		UsedFrontPts:   usedFront,
		UsedBackPts:    usedBack,
	}, nil
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

	// Compute rotation from matched pairs.
	// For each pair, dY = frontY - backY. If there's a rotation, dY varies linearly with X.
	// Regress dY vs X: slope = tan(angle_diff)
	var angleDiff float64
	if len(matched) >= 5 {
		// Linear regression: dY = slope * X + intercept
		var sX, sDY, sX2, sXDY float64
		n := float64(len(matched))
		for _, m := range matched {
			x := (m.frontX + m.backX + xOffset) / 2 // average X position
			dy := m.frontY - m.backY
			sX += x
			sDY += dy
			sX2 += x * x
			sXDY += x * dy
		}
		denom := n*sX2 - sX*sX
		if math.Abs(denom) > 0.001 {
			slope := (n*sXDY - sX*sDY) / denom
			intercept := (sDY - slope*sX) / n

			// slope = tan(angle_diff) → angle = atan(slope)
			angleDiff = math.Atan(slope)
			angleDeg := angleDiff * 180 / math.Pi

			fmt.Printf("  rotation from contact pairs: slope=%.6f → angle=%.3f° (intercept=%.1f)\n",
				slope, angleDeg, intercept)

			// Sanity check: clamp to ±3° (beyond that, something is very wrong)
			if math.Abs(angleDeg) > 3.0 {
				fmt.Printf("  WARNING: computed angle %.2f° too large, clamping to ±3°\n", angleDeg)
				if angleDeg > 0 {
					angleDiff = 3.0 * math.Pi / 180
				} else {
					angleDiff = -3.0 * math.Pi / 180
				}
			}
		} else {
			fmt.Printf("  regression degenerate (contacts all at same X?), using 0° rotation\n")
		}
	} else {
		// Not enough matched pairs — fall back to per-side angles with clamping
		frontAngle := frontResult.ContactAngle
		backAngle := backResult.ContactAngle
		if math.Abs(frontAngle) > 1.5 {
			fmt.Printf("  WARNING front angle %.2f° too large, clamping to 0\n", frontAngle)
			frontAngle = 0
		}
		if math.Abs(backAngle) > 1.5 {
			fmt.Printf("  WARNING back angle %.2f° too large, clamping to 0\n", backAngle)
			backAngle = 0
		}
		angleDiff = (frontAngle - backAngle) * math.Pi / 180
		fmt.Printf("  fallback to per-side angles: front=%.2f°, back=%.2f°, diff=%.2f°\n",
			frontAngle, backAngle, (frontAngle-backAngle))
	}

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
