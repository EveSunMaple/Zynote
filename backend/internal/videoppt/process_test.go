package videoppt

import (
	"math"
	"testing"
)

func TestStableSlides(t *testing.T) {
	slides := &stableSlides{}
	first := make([]byte, 14400)
	for index := range first {
		first[index] = 40
	}
	second := append([]byte(nil), first...)
	for index := 0; index < 5000; index++ {
		second[index] = 200
	}
	slides.push(first, 0)
	slides.push(first, 1)
	slides.push(second, 2)
	slides.push(second, 3)
	if len(slides.Times) != 2 || slides.Times[0] != 0 || slides.Times[1] != 2 {
		t.Fatalf("stable slide mismatch: %v", slides.Times)
	}
}

func TestValidCrop(t *testing.T) {
	if !validCrop(Region{Left: 0.1, Right: 0.9, Top: 0.1, Bottom: 0.9}) {
		t.Fatal("valid crop rejected")
	}
	for _, region := range []Region{
		{Left: 0.5, Right: 0.4, Top: 0.1, Bottom: 0.9},
		{Left: 0, Right: 1, Top: 0.5, Bottom: 0.55},
		{Left: -0.1, Right: 1, Top: 0, Bottom: 1},
		{Left: math.NaN(), Right: 1, Top: 0, Bottom: 1},
		{Left: 0, Right: math.Inf(1), Top: 0, Bottom: 1},
	} {
		if validCrop(region) {
			t.Fatalf("invalid crop accepted: %+v", region)
		}
	}
}
