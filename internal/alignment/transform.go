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
