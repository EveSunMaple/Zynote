package api

import "testing"

func TestTextbookOCRBoxes(t *testing.T) {
	lines, err := decodeTextbookOCR([]byte(`[{"text":" 识别文字 ","box":[50,100,900,200]}]`))
	if err != nil || len(lines) != 1 || lines[0].Text != "识别文字" || lines[0].Box != [4]float64{0.05, 0.1, 0.9, 0.2} {
		t.Fatalf("unexpected OCR line: %v %v", lines, err)
	}
	for _, invalid := range []string{`null`, `[{"text":"a","box":[8,9]}]`, `[{"text":"a","box":[90,0,20,100]}]`, `[{"text":"a","box":[0,0,1001,300]}]`} {
		if _, err := decodeTextbookOCR([]byte(invalid)); err == nil {
			t.Fatalf("accepted invalid box: %s", invalid)
		}
	}
}
