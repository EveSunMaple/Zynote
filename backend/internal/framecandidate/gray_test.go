package framecandidate

import "testing"

func TestGrayMetricsExactKnownPixels(t *testing.T) {
	metrics, err := MeasureGray([]byte{0, 100, 200, 255}, []byte{255, 100, 100, 0})
	if err != nil || metrics.MeanLight != 138.75 || metrics.MeanChange != 152.5 || metrics.NormalizedChange != 610.0/(4*255) {
		t.Fatalf("wrong metrics %+v %v", metrics, err)
	}
	first, err := MeasureGray([]byte{40, 40}, nil)
	if err != nil || first.MeanLight != 40 || first.MeanChange != 0 {
		t.Fatalf("wrong first frame %+v %v", first, err)
	}
	for _, pair := range [][2][]byte{{nil, nil}, {{1}, {1, 2}}} {
		if _, err := MeasureGray(pair[0], pair[1]); err == nil {
			t.Fatal("malformed frame accepted")
		}
	}
}
