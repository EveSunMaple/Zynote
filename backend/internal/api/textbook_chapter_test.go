package api

import "testing"

func TestDecodeChapterNoteUsesOnlyKnownBodyRows(t *testing.T) {
	good := []byte(`{"text":"由定义检验极限。","indices":[2,0]}`)
	if _, err := decodeChapterNote(good, 3); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"text":"定义","indices":[3]}`, `{"text":"定义","indices":[null]}`, `{"text":"定义","indices":[]}`, `{"text":"定义","indices":[1,1]}`, `{"text":"  ","indices":[1]}`} {
		if _, err := decodeChapterNote([]byte(raw), 3); err == nil {
			t.Fatalf("unexpected valid result: %s", raw)
		}
	}
}
