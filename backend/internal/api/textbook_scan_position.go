package api

import (
	"encoding/base64"
	"image"
	"image/jpeg"
	"math"
	"strings"
)

// The raster and its transform must come from the same rendered PDF page.
type scanPlacement struct {
	pixels *image.Gray
	toPDF  [6]float64
}

func newScanPlacement(data string, affine [6]float64) (scanPlacement, bool) {
	encoded, ok := strings.CutPrefix(data, "data:image/jpeg;base64,")
	if !ok {
		return scanPlacement{}, false
	}
	for _, value := range affine {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return scanPlacement{}, false
		}
	}
	if math.Abs(affine[2]*affine[5]-affine[3]*affine[4]) < 0.001 {
		return scanPlacement{}, false
	}
	config, err := jpeg.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 16000000 {
		return scanPlacement{}, false
	}
	bitmap, err := jpeg.Decode(base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)))
	if err != nil {
		return scanPlacement{}, false
	}
	var gray *image.Gray
	switch v := bitmap.(type) {
	case *image.YCbCr:
		gray = &image.Gray{Pix: v.Y, Stride: v.YStride, Rect: v.Rect}
	case *image.Gray:
		gray = v
	default:
		return scanPlacement{}, false
	}
	return scanPlacement{gray, affine}, true
}

// Align a proposed single line to ink on the page. A box between two plausible
// lines is less useful than an explicit positioning warning.
func inkPercentile(histogram *[256]int, target int) int {
	for i, count := range histogram {
		target -= count
		if target <= 0 {
			return i
		}
	}
	return 255
}

func inkDetector(g *image.Gray, x0, x1, y0, y1 int) (func(uint8) bool, bool) {
	var histogram [256]int
	n := (x1 - x0) * (y1 - y0)
	for py := y0; py < y1; py++ {
		for _, lum := range g.Pix[py*g.Stride+x0 : py*g.Stride+x1] {
			histogram[lum]++
		}
	}
	background := inkPercentile(&histogram, max(1, n/2))
	dark, light := inkPercentile(&histogram, max(1, n/100)), inkPercentile(&histogram, max(1, n*99/100))
	brightInk := light-background > background-dark
	contrast := max(light-background, background-dark)
	if contrast < 35 {
		return nil, false
	}
	delta := max(25, contrast/3)
	return func(v uint8) bool {
		if brightInk {
			return int(v) >= background+delta
		}
		return int(v) <= background-delta
	}, true
}

func inkBands(g *image.Gray, x0, x1, y0, y1 int, isInk func(uint8) bool) []image.Rectangle {
	bands := make([]image.Rectangle, 0, 4)
	lastInkY := -10
	for y := y0; y < y1; y++ {
		left, right, count := x1, x0, 0
		for i, v := range g.Pix[y*g.Stride+x0 : y*g.Stride+x1] {
			if isInk(v) {
				left = min(left, x0+i)
				right = x0 + i + 1
				count++
			}
		}
		if count < max(1, (x1-x0)/150) {
			continue
		}
		line := image.Rect(left, y, right, y+1)
		if len(bands) > 0 && y-lastInkY <= 2 {
			bands[len(bands)-1] = bands[len(bands)-1].Union(line)
		} else {
			bands = append(bands, line)
		}
		lastInkY = y
	}
	return bands
}

// Vertical overlap relative to both boxes (IoU) favors a close-fitting
// single line over a horizontal rule or a neighboring line.
func selectInkBand(bands []image.Rectangle, top, bottom, height, width int) (image.Rectangle, bool) {
	best, runner := 0.0, 0.0
	var chosen image.Rectangle
	for _, band := range bands {
		if band.Dy() < max(2, height/6) || band.Dx() < max(2, width/8) {
			continue
		}
		intersection := max(0, min(bottom, band.Max.Y)-max(top, band.Min.Y))
		score := float64(intersection) / float64(max(bottom, band.Max.Y)-min(top, band.Min.Y))
		if score > best {
			runner, best, chosen = best, score, band
		} else {
			runner = math.Max(runner, score)
		}
	}
	if best < 0.25 || runner >= 0.65*best {
		return image.Rectangle{}, false
	}
	return chosen, true
}

// Complete partially clipped glyphs, stopping at the first blank column.
func expandInkBand(g *image.Gray, chosen image.Rectangle, x0, x1 int, isInk func(uint8) bool) image.Rectangle {
	hasInk := func(x int) bool {
		for y := chosen.Min.Y; y < chosen.Max.Y; y++ {
			if isInk(g.Pix[y*g.Stride+x]) {
				return true
			}
		}
		return false
	}
	maxSide := max(2, chosen.Dy())
	if chosen.Min.X == x0 {
		for x := x0 - 1; x >= max(0, x0-maxSide) && hasInk(x); x-- {
			chosen.Min.X = x
		}
	}
	if chosen.Max.X == x1 {
		for x := x1; x < min(g.Rect.Dx(), x1+maxSide) && hasInk(x); x++ {
			chosen.Max.X = x + 1
		}
	}
	return chosen
}

func (placement scanPlacement) inkBox(rect [4]float64) (image.Rectangle, bool) {
	g := placement.pixels
	if g == nil {
		return image.Rectangle{}, false
	}
	w, h := g.Rect.Dx(), g.Rect.Dy()
	x0 := max(0, int(math.Floor(rect[0]*float64(w))))
	x1 := min(w, int(math.Ceil(rect[2]*float64(w))))
	top := int(math.Floor(rect[1] * float64(h)))
	bottom := int(math.Ceil(rect[3] * float64(h)))
	height := bottom - top
	if x1-x0 < 2 || height < 2 {
		return image.Rectangle{}, false
	}
	// Small margins allow the model's box to miss an ascender or baseline without
	// searching the rest of the column for unrelated text.
	pad := min(max(3, height/2), max(4, h/55))
	y0, y1 := max(0, top-pad), min(h, bottom+pad)
	isInk, ok := inkDetector(g, x0, x1, y0, y1)
	if !ok {
		return image.Rectangle{}, false
	}
	chosen, ok := selectInkBand(inkBands(g, x0, x1, y0, y1, isInk), top, bottom, height, x1-x0)
	if !ok {
		return image.Rectangle{}, false
	}
	chosen = expandInkBand(g, chosen, x0, x1, isInk)
	return chosen.Inset(-1).Intersect(g.Rect), true
}

func (placement scanPlacement) locate(rect [4]float64) ([4]float64, bool) {
	box, ok := placement.inkBox(rect)
	if !ok {
		return [4]float64{}, false
	}
	w, h := float64(placement.pixels.Rect.Dx()), float64(placement.pixels.Rect.Dy())
	m := placement.toPDF
	left, bottom, right, top := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, x := range []int{box.Min.X, box.Max.X} {
		for _, y := range []int{box.Min.Y, box.Max.Y} {
			u, v := float64(x)/w, float64(y)/h
			px, py := m[0]+u*m[2]+v*m[4], m[1]+u*m[3]+v*m[5]
			left, bottom, right, top = math.Min(left, px), math.Min(bottom, py), math.Max(right, px), math.Max(top, py)
		}
	}
	return [4]float64{left, bottom, right - left, top - bottom}, true
}
