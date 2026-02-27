package alignment

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand"

	"pcb-tracer/pkg/geometry"

	"gocv.io/x/gocv"
	"gonum.org/v1/gonum/mat"
)

// RANSAC method constant for EstimateAffine2D
const methodRANSAC = 8

// AlignmentResult holds the result of image alignment.
type AlignmentResult struct {
	Transform     geometry.AffineTransform
	DPI           float64
	FrontContacts []geometry.Point2D
	BackContacts  []geometry.Point2D
	ContactError  float64
	TotalError    float64
}

// ComputeAffineTransform computes an affine transform from point correspondences using RANSAC.
// Uses the pure Go implementation for better compatibility across gocv versions.
func ComputeAffineTransform(srcPoints, dstPoints []geometry.Point2D) (geometry.AffineTransform, []int, error) {
	if len(srcPoints) != len(dstPoints) {
		return geometry.AffineTransform{}, nil, fmt.Errorf("point count mismatch: %d vs %d", len(srcPoints), len(dstPoints))
	}
	if len(srcPoints) < 3 {
		return geometry.AffineTransform{}, nil, fmt.Errorf("need at least 3 points, got %d", len(srcPoints))
	}

	// Use pure Go RANSAC implementation for cross-version compatibility
	return ComputeAffineRANSAC(srcPoints, dstPoints, 2000, 3.0)
}

// ComputeAffineRANSAC computes affine transform using pure Go RANSAC implementation.
// This is a fallback if GoCV is not available.
func ComputeAffineRANSAC(srcPoints, dstPoints []geometry.Point2D, iterations int, threshold float64) (geometry.AffineTransform, []int, error) {
	if len(srcPoints) != len(dstPoints) || len(srcPoints) < 3 {
		return geometry.AffineTransform{}, nil, fmt.Errorf("invalid point sets")
	}

	n := len(srcPoints)
	bestInliers := []int{}
	var bestTransform geometry.AffineTransform

	for iter := 0; iter < iterations; iter++ {
		// Randomly sample 3 points
		indices := rand.Perm(n)[:3]

		// Compute transform from sample
		sample := make([]geometry.Point2D, 3)
		target := make([]geometry.Point2D, 3)
		for i, idx := range indices {
			sample[i] = srcPoints[idx]
			target[i] = dstPoints[idx]
		}

		transform, err := computeAffineFromPoints(sample, target)
		if err != nil {
			continue
		}

		// Count inliers
		var inliers []int
		for i := range srcPoints {
			transformed := transform.Apply(srcPoints[i])
			dist := transformed.Distance(dstPoints[i])
			if dist < threshold {
				inliers = append(inliers, i)
			}
		}

		if len(inliers) > len(bestInliers) {
			bestInliers = inliers
			bestTransform = transform
		}
	}

	if len(bestInliers) < 3 {
		return geometry.AffineTransform{}, nil, fmt.Errorf("RANSAC failed to find enough inliers")
	}

	// Recompute transform using all inliers
	inlierSrc := make([]geometry.Point2D, len(bestInliers))
	inlierDst := make([]geometry.Point2D, len(bestInliers))
	for i, idx := range bestInliers {
		inlierSrc[i] = srcPoints[idx]
		inlierDst[i] = dstPoints[idx]
	}

	finalTransform, err := computeAffineLeastSquares(inlierSrc, inlierDst)
	if err != nil {
		return bestTransform, bestInliers, nil
	}

	return finalTransform, bestInliers, nil
}

// ComputeRigidRANSAC computes a rigid transform (rotation + translation, no scale)
// using RANSAC. This is used as a fallback when the affine is poorly constrained
// (e.g., most points are collinear).
func ComputeRigidRANSAC(srcPoints, dstPoints []geometry.Point2D, iterations int, threshold float64) (geometry.AffineTransform, []int, error) {
	if len(srcPoints) != len(dstPoints) || len(srcPoints) < 2 {
		return geometry.AffineTransform{}, nil, fmt.Errorf("invalid point sets")
	}

	n := len(srcPoints)
	bestInliers := []int{}

	for iter := 0; iter < iterations; iter++ {
		// Randomly sample 2 points (minimum for rigid)
		indices := rand.Perm(n)[:2]
		i0, i1 := indices[0], indices[1]

		// Compute rigid from 2 pairs
		transform, err := computeRigidFrom2(srcPoints[i0], srcPoints[i1], dstPoints[i0], dstPoints[i1])
		if err != nil {
			continue
		}

		// Count inliers
		var inliers []int
		for i := range srcPoints {
			transformed := transform.Apply(srcPoints[i])
			dist := transformed.Distance(dstPoints[i])
			if dist < threshold {
				inliers = append(inliers, i)
			}
		}

		if len(inliers) > len(bestInliers) {
			bestInliers = inliers
		}
	}

	if len(bestInliers) < 2 {
		return geometry.AffineTransform{}, nil, fmt.Errorf("rigid RANSAC failed to find enough inliers")
	}

	// Recompute rigid using all inliers (least squares)
	inlierSrc := make([]geometry.Point2D, len(bestInliers))
	inlierDst := make([]geometry.Point2D, len(bestInliers))
	for i, idx := range bestInliers {
		inlierSrc[i] = srcPoints[idx]
		inlierDst[i] = dstPoints[idx]
	}

	finalTransform := computeRigidLeastSquares(inlierSrc, inlierDst)
	return finalTransform, bestInliers, nil
}

// computeRigidFrom2 computes a rigid transform (rotation + translation) from 2 point pairs.
func computeRigidFrom2(s0, s1, d0, d1 geometry.Point2D) (geometry.AffineTransform, error) {
	// Vector in source
	sx, sy := s1.X-s0.X, s1.Y-s0.Y
	// Vector in destination
	dx, dy := d1.X-d0.X, d1.Y-d0.Y

	srcLen := math.Sqrt(sx*sx + sy*sy)
	dstLen := math.Sqrt(dx*dx + dy*dy)
	if srcLen < 0.001 || dstLen < 0.001 {
		return geometry.AffineTransform{}, fmt.Errorf("degenerate points")
	}

	// Rotation angle = angle(dst) - angle(src)
	srcAngle := math.Atan2(sy, sx)
	dstAngle := math.Atan2(dy, dx)
	theta := dstAngle - srcAngle

	cosT := math.Cos(theta)
	sinT := math.Sin(theta)

	// Translation: d0 = R * s0 + t  =>  t = d0 - R * s0
	tx := d0.X - (cosT*s0.X - sinT*s0.Y)
	ty := d0.Y - (sinT*s0.X + cosT*s0.Y)

	return geometry.AffineTransform{
		A: cosT, B: -sinT, TX: tx,
		C: sinT, D: cosT, TY: ty,
	}, nil
}

// computeRigidLeastSquares computes the best rigid transform (rotation + translation)
// from N point pairs using SVD-based method.
func computeRigidLeastSquares(src, dst []geometry.Point2D) geometry.AffineTransform {
	n := float64(len(src))

	// Compute centroids
	var srcCx, srcCy, dstCx, dstCy float64
	for i := range src {
		srcCx += src[i].X
		srcCy += src[i].Y
		dstCx += dst[i].X
		dstCy += dst[i].Y
	}
	srcCx /= n
	srcCy /= n
	dstCx /= n
	dstCy /= n

	// Compute rotation using the cross/dot product method
	var dotSum, crossSum float64
	for i := range src {
		sx, sy := src[i].X-srcCx, src[i].Y-srcCy
		dx, dy := dst[i].X-dstCx, dst[i].Y-dstCy
		dotSum += sx*dx + sy*dy
		crossSum += sx*dy - sy*dx
	}

	theta := math.Atan2(crossSum, dotSum)
	cosT := math.Cos(theta)
	sinT := math.Sin(theta)

	// Translation
	tx := dstCx - (cosT*srcCx - sinT*srcCy)
	ty := dstCy - (sinT*srcCx + cosT*srcCy)

	return geometry.AffineTransform{
		A: cosT, B: -sinT, TX: tx,
		C: sinT, D: cosT, TY: ty,
	}
}

// computeAffineFromPoints computes an affine transform from exactly 3 point pairs.
func computeAffineFromPoints(src, dst []geometry.Point2D) (geometry.AffineTransform, error) {
	if len(src) != 3 || len(dst) != 3 {
		return geometry.AffineTransform{}, fmt.Errorf("need exactly 3 points")
	}

	// Build matrix equation: [x', y'] = [a, b, tx; c, d, ty] * [x, y, 1]
	// For 3 points: A * params = B
	A := mat.NewDense(6, 6, nil)
	B := mat.NewVecDense(6, nil)

	for i := 0; i < 3; i++ {
		x, y := src[i].X, src[i].Y
		xp, yp := dst[i].X, dst[i].Y

		// x' = a*x + b*y + tx
		A.Set(i*2, 0, x)
		A.Set(i*2, 1, y)
		A.Set(i*2, 2, 1)
		B.SetVec(i*2, xp)

		// y' = c*x + d*y + ty
		A.Set(i*2+1, 3, x)
		A.Set(i*2+1, 4, y)
		A.Set(i*2+1, 5, 1)
		B.SetVec(i*2+1, yp)
	}

	// Solve A * params = B
	var params mat.VecDense
	err := params.SolveVec(A, B)
	if err != nil {
		return geometry.AffineTransform{}, err
	}

	return geometry.AffineTransform{
		A:  params.AtVec(0),
		B:  params.AtVec(1),
		TX: params.AtVec(2),
		C:  params.AtVec(3),
		D:  params.AtVec(4),
		TY: params.AtVec(5),
	}, nil
}

// computeAffineLeastSquares computes an affine transform using least squares.
func computeAffineLeastSquares(src, dst []geometry.Point2D) (geometry.AffineTransform, error) {
	n := len(src)
	if n < 3 {
		return geometry.AffineTransform{}, fmt.Errorf("need at least 3 points")
	}

	// Build overdetermined system
	A := mat.NewDense(n*2, 6, nil)
	B := mat.NewVecDense(n*2, nil)

	for i := 0; i < n; i++ {
		x, y := src[i].X, src[i].Y
		xp, yp := dst[i].X, dst[i].Y

		A.Set(i*2, 0, x)
		A.Set(i*2, 1, y)
		A.Set(i*2, 2, 1)
		B.SetVec(i*2, xp)

		A.Set(i*2+1, 3, x)
		A.Set(i*2+1, 4, y)
		A.Set(i*2+1, 5, 1)
		B.SetVec(i*2+1, yp)
	}

	// Solve using QR decomposition
	var qr mat.QR
	qr.Factorize(A)

	var params mat.VecDense
	err := qr.SolveVecTo(&params, false, B)
	if err != nil {
		return geometry.AffineTransform{}, err
	}

	return geometry.AffineTransform{
		A:  params.AtVec(0),
		B:  params.AtVec(1),
		TX: params.AtVec(2),
		C:  params.AtVec(3),
		D:  params.AtVec(4),
		TY: params.AtVec(5),
	}, nil
}

// WarpAffine applies an affine transform to an image.
func WarpAffine(src gocv.Mat, transform geometry.AffineTransform, width, height int) gocv.Mat {
	// Create transform matrix for GoCV
	transformMat := gocv.NewMatWithSize(2, 3, gocv.MatTypeCV64F)
	transformMat.SetDoubleAt(0, 0, transform.A)
	transformMat.SetDoubleAt(0, 1, transform.B)
	transformMat.SetDoubleAt(0, 2, transform.TX)
	transformMat.SetDoubleAt(1, 0, transform.C)
	transformMat.SetDoubleAt(1, 1, transform.D)
	transformMat.SetDoubleAt(1, 2, transform.TY)
	defer transformMat.Close()

	dst := gocv.NewMat()
	gocv.WarpAffineWithParams(src, &dst, transformMat, image.Point{width, height},
		gocv.InterpolationLinear, gocv.BorderConstant, color.RGBA{R: 0, G: 0, B: 0, A: 0})

	return dst
}

// CalculateAlignmentError calculates the mean alignment error after transformation.
func CalculateAlignmentError(srcPoints, dstPoints []geometry.Point2D, transform geometry.AffineTransform) float64 {
	if len(srcPoints) != len(dstPoints) || len(srcPoints) == 0 {
		return math.Inf(1)
	}

	var totalError float64
	for i := range srcPoints {
		transformed := transform.Apply(srcPoints[i])
		error := transformed.Distance(dstPoints[i])
		totalError += error
	}

	return totalError / float64(len(srcPoints))
}

// RotateImage rotates an image by 90, 180, or 270 degrees.
func RotateImage(img gocv.Mat, degrees int) gocv.Mat {
	dst := gocv.NewMat()

	switch degrees {
	case 90:
		gocv.Rotate(img, &dst, gocv.Rotate90Clockwise)
	case 180:
		gocv.Rotate(img, &dst, gocv.Rotate180Clockwise)
	case 270:
		gocv.Rotate(img, &dst, gocv.Rotate90CounterClockwise)
	default:
		dst = img.Clone()
	}

	return dst
}

// FlipHorizontal flips an image horizontally.
func FlipHorizontal(img gocv.Mat) gocv.Mat {
	dst := gocv.NewMat()
	gocv.Flip(img, &dst, 1)
	return dst
}
