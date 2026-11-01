package contract

import "testing"

func TestASRLanguageContractPreservesDefaultAndRejectsAmbiguousValues(t *testing.T) {
	for input, want := range map[string]string{"": "zh", "zh": "zh", "en": "en", "auto": "auto"} {
		got, err := NormalizeASRLanguage(input)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", input, got, err)
		}
	}
	for _, input := range []string{"ZH", "EN", "Auto", " zh", "en ", "\t", "fr", "translate"} {
		if _, err := NormalizeASRLanguage(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}
