package summaryglobal

import "testing"

func TestOrganizationMustDisposeEveryLeafExactlyOnce(t *testing.T) {
	input := Input{Children: []Child{{ID: "first", Text: "evidence"}, {ID: "second", Text: "evidence"}}}
	invalid := []string{
		`{"title":"主题","summary":"概述","dispositions":[{"id":"first","state":"consumed","reason":""}]}`,
		`{"title":"主题","summary":"概述","dispositions":[{"id":"first","state":"consumed","reason":""},{"id":"first","state":"consumed","reason":""}]}`,
		`{"title":"主题","summary":"概述","dispositions":[{"id":"first","state":"consumed","reason":""},{"id":"forged","state":"consumed","reason":""}]}`,
		`{"title":"主题","summary":"概述","dispositions":[{"id":"first","state":"consumed","reason":""},{"id":"second","state":"omitted","reason":""}]}`,
	}
	for _, body := range invalid {
		if _, err := ParseOrganization([]byte(body), input); err == nil {
			t.Fatal("forged/omitted-unmarked leaf accepted")
		}
	}
	valid := `{"title":"主题","summary":"概述","dispositions":[{"id":"first","state":"consumed","reason":""},{"id":"second","state":"omitted","reason":"明确不确定"}]}`
	if _, err := ParseOrganization([]byte(valid), input); err != nil {
		t.Fatal(err)
	}
}
func TestRiskReviewCannotSkipOrForgeSelectedEvidence(t *testing.T) {
	input := ReviewInput{Cards: []RiskCard{{ID: "first"}, {ID: "second"}}}
	for _, body := range []string{`{"summaryVerdict":"supported","decisions":[{"id":"first","verdict":"supported","reason":"原文"}]}`, `{"summaryVerdict":"supported","decisions":[{"id":"first","verdict":"supported","reason":"原文"},{"id":"forged","verdict":"supported","reason":"原文"}]}`} {
		if _, err := ParseReview([]byte(body), input); err == nil {
			t.Fatal("incomplete/forged review accepted")
		}
	}
}
