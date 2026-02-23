package trace

import (
	"encoding/json"
	"fmt"
	"image"
	"math"
	"os"

	"pcb-tracer/pkg/colorutil"

	"gocv.io/x/gocv"
)

// HSVSample holds a single pixel's color in HSV space.
type HSVSample struct {
	H float64 `json:"h"`
	S float64 `json:"s"`
	V float64 `json:"v"`
}

// HSVStats holds mean and standard deviation for H, S, V.
type HSVStats struct {
	HMean float64 `json:"h_mean"`
	HStd  float64 `json:"h_std"`
	SMean float64 `json:"s_mean"`
	SStd  float64 `json:"s_std"`
	VMean float64 `json:"v_mean"`
	VStd  float64 `json:"v_std"`
}

// TraceTrainingSet holds color samples collected from existing manual traces.
type TraceTrainingSet struct {
	OnTraceHSV  []HSVSample `json:"on_trace_hsv"`
	OffTraceHSV []HSVSample `json:"off_trace_hsv"`
	Layer       TraceLayer  `json:"layer"`
}

// Save writes the training set to a JSON file.
func (ts *TraceTrainingSet) Save(path string) error {
	data, err := json.MarshalIndent(ts, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal training set: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// LoadTraceTrainingSet reads a training set from a JSON file.
func LoadTraceTrainingSet(path string) (*TraceTrainingSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ts TraceTrainingSet
	if err := json.Unmarshal(data, &ts); err != nil {
		return nil, fmt.Errorf("unmarshal training set: %w", err)
	}
	return &ts, nil
}

// CollectSamples walks existing manual traces, sampling on-trace and off-trace pixels.
//
// For each trace on the matching layer:
//   - Walk the polyline, stepping every ~5 pixels along each segment
//   - At each step, sample a perpendicular cross-section of halfWidth pixels → on-trace
//   - Sample at 2.5 * halfWidth perpendicular offset → off-trace
func CollectSamples(img image.Image, traces []ExtendedTrace, layer TraceLayer, halfWidth float64) *TraceTrainingSet {
	ts := &TraceTrainingSet{Layer: layer}
	bounds := img.Bounds()

	for _, tr := range traces {
		if tr.Layer != layer || tr.Source != SourceManual {
			continue
		}
		if len(tr.Points) < 2 {
			continue
		}

		for i := 0; i < len(tr.Points)-1; i++ {
			a := tr.Points[i]
			b := tr.Points[i+1]

			dx := b.X - a.X
			dy := b.Y - a.Y
			segLen := math.Sqrt(dx*dx + dy*dy)
			if segLen < 1 {
				continue
			}

			// Unit direction along segment
			ux, uy := dx/segLen, dy/segLen
			// Perpendicular direction
			px, py := -uy, ux

			// Step every ~5 pixels along segment
			stepSize := 5.0
			for t := 0.0; t < segLen; t += stepSize {
				// Point on centerline
				cx := a.X + ux*t
				cy := a.Y + uy*t

				// Sample on-trace: perpendicular strip of halfWidth each side
				for d := -halfWidth; d <= halfWidth; d += 1.0 {
					sx := int(cx + px*d)
					sy := int(cy + py*d)
					if sx < bounds.Min.X || sx >= bounds.Max.X || sy < bounds.Min.Y || sy >= bounds.Max.Y {
						continue
					}
					r, g, bl, _ := img.At(sx, sy).RGBA()
					h, s, v := colorutil.RGBToHSV(float64(r>>8), float64(g>>8), float64(bl>>8))
					ts.OnTraceHSV = append(ts.OnTraceHSV, HSVSample{H: h, S: s, V: v})
				}

				// Sample off-trace: at 2.5 * halfWidth perpendicular offset (both sides)
				offDist := 2.5 * halfWidth
				for _, sign := range []float64{-1, 1} {
					// Sample a small strip at offset
					for d := -2.0; d <= 2.0; d += 1.0 {
						sx := int(cx + px*(sign*offDist+d))
						sy := int(cy + py*(sign*offDist+d))
						if sx < bounds.Min.X || sx >= bounds.Max.X || sy < bounds.Min.Y || sy >= bounds.Max.Y {
							continue
						}
						r, g, bl, _ := img.At(sx, sy).RGBA()
						h, s, v := colorutil.RGBToHSV(float64(r>>8), float64(g>>8), float64(bl>>8))
						ts.OffTraceHSV = append(ts.OffTraceHSV, HSVSample{H: h, S: s, V: v})
					}
				}
			}
		}
	}

	return ts
}

// TraceClassifier scores pixels as on-trace vs off-trace using learned HSV statistics.
type TraceClassifier struct {
	OnStats  HSVStats `json:"on_stats"`
	OffStats HSVStats `json:"off_stats"`
	Trained  bool     `json:"trained"`
	Layer    TraceLayer `json:"layer"`
}

// Train computes HSV statistics from the training set.
func (c *TraceClassifier) Train(ts *TraceTrainingSet) {
	if len(ts.OnTraceHSV) == 0 || len(ts.OffTraceHSV) == 0 {
		return
	}

	c.Layer = ts.Layer
	c.OnStats = computeHSVStats(ts.OnTraceHSV)
	c.OffStats = computeHSVStats(ts.OffTraceHSV)
	c.Trained = true
}

// ScorePixel returns a score from 0.0 (off-trace) to 1.0 (on-trace) for an HSV pixel.
func (c *TraceClassifier) ScorePixel(h, s, v float64) float64 {
	if !c.Trained {
		return 0.5
	}

	posDist := hsvDistance(h, s, v, c.OnStats)
	negDist := hsvDistance(h, s, v, c.OffStats)

	if posDist == 0 && negDist == 0 {
		return 0.5
	}

	// Inverse distance weighting
	posWeight := 1.0 / (posDist + 0.001)
	negWeight := 1.0 / (negDist + 0.001)

	return posWeight / (posWeight + negWeight)
}

// GenerateMask scores all pixels in the image and thresholds to a binary mask.
func (c *TraceClassifier) GenerateMask(img image.Image, threshold float64) gocv.Mat {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	mask := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8U)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			hv, sv, vv := colorutil.RGBToHSV(float64(r>>8), float64(g>>8), float64(b>>8))
			score := c.ScorePixel(hv, sv, vv)
			if score >= threshold {
				mask.SetUCharAt(y, x, 255)
			}
		}
	}

	return mask
}

// Save writes the classifier to a JSON file.
func (c *TraceClassifier) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal classifier: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// LoadTraceClassifier reads a classifier from a JSON file.
func LoadTraceClassifier(path string) (*TraceClassifier, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c TraceClassifier
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("unmarshal classifier: %w", err)
	}
	return &c, nil
}

// StatsString returns a human-readable summary of the learned statistics.
func (c *TraceClassifier) StatsString() string {
	return fmt.Sprintf("On-trace: H=%.1f±%.1f S=%.1f±%.1f V=%.1f±%.1f | Off-trace: H=%.1f±%.1f S=%.1f±%.1f V=%.1f±%.1f",
		c.OnStats.HMean, c.OnStats.HStd, c.OnStats.SMean, c.OnStats.SStd, c.OnStats.VMean, c.OnStats.VStd,
		c.OffStats.HMean, c.OffStats.HStd, c.OffStats.SMean, c.OffStats.SStd, c.OffStats.VMean, c.OffStats.VStd)
}

// computeHSVStats computes mean and std for a slice of HSV samples.
func computeHSVStats(samples []HSVSample) HSVStats {
	n := float64(len(samples))
	if n == 0 {
		return HSVStats{}
	}

	var stats HSVStats

	// Compute means
	for _, s := range samples {
		stats.HMean += s.H
		stats.SMean += s.S
		stats.VMean += s.V
	}
	stats.HMean /= n
	stats.SMean /= n
	stats.VMean /= n

	// Compute std deviations
	var hVar, sVar, vVar float64
	for _, s := range samples {
		hVar += (s.H - stats.HMean) * (s.H - stats.HMean)
		sVar += (s.S - stats.SMean) * (s.S - stats.SMean)
		vVar += (s.V - stats.VMean) * (s.V - stats.VMean)
	}
	stats.HStd = math.Sqrt(hVar / n)
	stats.SStd = math.Sqrt(sVar / n)
	stats.VStd = math.Sqrt(vVar / n)

	return stats
}

// hsvDistance computes a normalized distance from a pixel to a distribution.
func hsvDistance(h, s, v float64, stats HSVStats) float64 {
	hd := sqDiffNorm(h, stats.HMean, stats.HStd+1)
	sd := sqDiffNorm(s, stats.SMean, stats.SStd+1)
	vd := sqDiffNorm(v, stats.VMean, stats.VStd+1)

	// Weight: saturation and value are more discriminating than hue for traces
	return math.Sqrt(hd*1.0 + sd*1.5 + vd*1.5)
}

// sqDiffNorm computes (a-b)^2 / s^2 with safeguard.
func sqDiffNorm(a, b, s float64) float64 {
	if s < 0.001 {
		s = 0.001
	}
	d := (a - b) / s
	return d * d
}

// GenerateMaskFromMat scores all pixels in a gocv.Mat (BGR) and thresholds to binary mask.
func (c *TraceClassifier) GenerateMaskFromMat(img gocv.Mat, threshold float64) gocv.Mat {
	h, w := img.Rows(), img.Cols()
	mask := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8U)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			vec := img.GetVecbAt(y, x)
			// BGR -> RGB
			rv, gv, bv := float64(vec[2]), float64(vec[1]), float64(vec[0])
			hv, sv, vv := colorutil.RGBToHSV(rv, gv, bv)
			score := c.ScorePixel(hv, sv, vv)
			if score >= threshold {
				mask.SetUCharAt(y, x, 255)
			}
		}
	}

	return mask
}

// DetectTracesWithClassifier generates a binary trace mask using the trained classifier.
// It scores each pixel, thresholds, and cleans up the result.
func DetectTracesWithClassifier(img gocv.Mat, classifier *TraceClassifier, threshold float64) gocv.Mat {
	if img.Empty() || classifier == nil || !classifier.Trained {
		return gocv.NewMat()
	}

	// Generate raw mask from classifier
	rawMask := classifier.GenerateMaskFromMat(img, threshold)
	defer rawMask.Close()

	// Cleanup: close gaps, remove noise
	cleaned := CleanupMask(rawMask, 2)

	return cleaned
}

// CollectSamplesFromImage is a convenience wrapper that accepts *image.RGBA.
func CollectSamplesFromImage(img *image.RGBA, traces []ExtendedTrace, layer TraceLayer, halfWidth float64) *TraceTrainingSet {
	return CollectSamples(img, traces, layer, halfWidth)
}

// TraceClassifierFilename returns the standard filename for a layer's classifier.
func TraceClassifierFilename(layer TraceLayer) string {
	switch layer {
	case LayerFront:
		return "trace_classifier_front.json"
	case LayerBack:
		return "trace_classifier_back.json"
	default:
		return "trace_classifier.json"
	}
}

// TraceTrainingFilename returns the standard filename for a layer's training data.
func TraceTrainingFilename(layer TraceLayer) string {
	switch layer {
	case LayerFront:
		return "trace_training_front.json"
	case LayerBack:
		return "trace_training_back.json"
	default:
		return "trace_training.json"
	}
}
