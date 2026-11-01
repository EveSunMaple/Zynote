package api

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"math"
	"testing"
)

func sampleScanPlacement() scanPlacement {
	p := image.NewGray(image.Rect(0, 0, 1000, 500))
	for i := range p.Pix {
		p.Pix[i] = 250
	}
	// Two printed lines made of separate strokes, with margins on every side.
	for _, top := range []int{12, 27} {
		for y := top; y < top+8; y++ {
			for x := 12; x < 94; x++ {
				if x%7 < 4 {
					p.Pix[y*p.Stride+x] = 30
				}
			}
		}
	}
	return scanPlacement{p, [6]float64{10, 450, 700, 0, 0, -400}}
}

func TestScannedInkAlignsOneLineAndUsesPDFTransform(t *testing.T) { // NOSONAR: table-driven geometry test; extracting fixtures would obscure the scenario.
	place := sampleScanPlacement()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, place.pixels, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	url := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	for _, tc := range []struct {
		transform [6]float64
		want      [4]float64
	}{
		{[6]float64{10, 450, 700, 0, 0, -400}, [4]float64{19.1, 433.2, 56.7, 8}},
		{[6]float64{-25, 30, 0, 700, 400, 0}, [4]float64{-16.2, 39.1, 8, 56.7}},
	} {
		placement, ok := newScanPlacement(url, tc.transform)
		if !ok {
			t.Fatal("rejected valid raster transform")
		}
		box, ok := placement.locate([4]float64{0.01, 0.020, 0.100, 0.044})
		if !ok {
			t.Fatal("rejected single printed line")
		}
		for i, n := range box {
			if math.Abs(n-tc.want[i]) > 1.6 {
				t.Fatalf("misplaced line %v, want %v", box, tc.want)
			}
		}
	}
	for _, loose := range [][4]float64{{.01, .018, .1, .074}, {.3, .4, .5, .5}} {
		if _, ok := place.locate(loose); ok {
			t.Fatalf("accepted ambiguous or blank selection %v", loose)
		}
	}
	// Reversed scans have light letters on a dark page.
	for i, lum := range place.pixels.Pix {
		place.pixels.Pix[i] = 255 - lum
	}
	if _, ok := place.locate([4]float64{.01, .020, .100, .044}); !ok {
		t.Fatal("light text was not located")
	}
}
