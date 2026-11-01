package research

import (
	"context"
	"fmt"
	"testing"
)

func TestSearchKeywordsRewritesChineseQuestion(t *testing.T) {
	calls := 0
	reply := ""
	engine := &Engine{Model: func(context.Context, string, string) (string, error) {
		calls++
		if reply == "" {
			return "", fmt.Errorf("断连")
		}
		return reply, nil
	}}
	reply = "`Taylor remainder Lagrange error estimate`\n说明"
	if got := engine.searchKeywords(context.Background(), "拉格朗日余项怎么估计误差"); got != "Taylor remainder Lagrange error estimate" {
		t.Fatalf("keywords = %q", got)
	}
	reply = "泰勒 余项"
	if got := engine.searchKeywords(context.Background(), "泰勒余项"); got != "" {
		t.Fatalf("non-English keywords must fall back, got %q", got)
	}
	reply = ""
	if got := engine.searchKeywords(context.Background(), "泰勒余项"); got != "" {
		t.Fatalf("model failure must fall back, got %q", got)
	}
	before := calls
	if got := engine.searchKeywords(context.Background(), "Taylor remainder"); got != "" || calls != before {
		t.Fatal("an English question is searched as is without asking the model")
	}
}
