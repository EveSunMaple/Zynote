package research

import (
	"strings"
	"testing"
)

// 模型在 JSON 里写 LaTeX、直接换行，或给出不合格的记忆建议时，回答本身仍应保住。
func TestResearchAnswerSurvivesSloppyJSON(t *testing.T) {
	raw := `{"note":"误差为 $\xi$ 介于 x 与 x0 之间` + "\n" + `见[S1]","memory_candidates":[` +
		`{"kind":"preference","title":"先给结论","body":"回答先给结论","basis":"question","quote":"误差"},` +
		`{"kind":"nonsense","title":"x","body":"y","basis":"note","quote":"不存在的句子"}]}`
	note, candidates, err := ReadResearchAnswer(raw, "t1", "拉格朗日余项的误差怎么估计", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, `$\xi$`) || !strings.Contains(note, "\n见[S1]") {
		t.Fatalf("note not recovered: %q", note)
	}
	if len(candidates) != 1 || candidates[0].Title != "先给结论" {
		t.Fatalf("invalid suggestions must be dropped, valid kept: %+v", candidates)
	}
	if _, _, err := ReadResearchAnswer(`{"note": `, "t1", "q", "", true); err == nil {
		t.Fatal("truly broken JSON must still be rejected")
	}
}
