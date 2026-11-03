package subtitlederived

import (
	"context"
	"strings"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/summarytree"
)

func TestSummaryTreeCannotMoveLeafAcrossChunkBoundaryWithRecomputedHashes(t *testing.T) {
	service := New(t.TempDir(), t.TempDir())
	task := Task{ID: "e2e2e2e2-e2e2-42e2-82e2-e2e2e2e2e2e2", Items: []Item{{Source: Source{Hash: strings.Repeat("a", 64)}, Recipe: &ItemRecipe{Hash: strings.Repeat("b", 64), ModeRecipe: ModeRecipe{Version: 2}}}}}
	item := &task.Items[0]
	scope := summarytree.Scope{SourceSHA256: item.Source.Hash, RecipeSHA256: item.Recipe.Hash}
	rows := []contract.ValidatedSummaryPoint{{Cue: 0, Quote: "原文一", Point: "结论一"}, {Cue: 0, Quote: "原文二", Point: "结论二"}}
	audit := AttemptAudit{State: "complete", TotalChunks: 2, validated: []validatedChunkAudit{{Index: 0, PointCount: 2}, {Index: 1, PointCount: 0}}}
	for _, wrong := range []bool{false, true} {
		builder := summarytree.New(treeStorage{service, task, 0}, scope)
		if wrong {
			if err := builder.Append(context.Background(), 0, rows[:1]); err != nil {
				t.Fatal(err)
			}
			if err := builder.Append(context.Background(), 1, rows[1:]); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := builder.Append(context.Background(), 0, rows); err != nil {
				t.Fatal(err)
			}
			if err := builder.Append(context.Background(), 1, nil); err != nil {
				t.Fatal(err)
			}
		}
		result, err := builder.Finish(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		facts := &contract.SummaryEvidence{TreeRootSHA256: result.Root, TreePages: result.PageCount, TreeNodes: result.NodeCount, TreeLeafSetSHA256: result.LeafSetSHA256, TreeDispositionSHA256: result.DispositionSHA256}
		err = service.verifySummaryTree(context.Background(), &task, item, rows, facts, audit)
		if wrong && err == nil {
			t.Fatal("rehashed chunk boundary forgery accepted")
		}
		if !wrong && err != nil {
			t.Fatalf("valid original partition rejected: %v", err)
		}
	}
	item.Recipe.ModeRecipe.Version = 3
	if err := service.verifySummaryTree(context.Background(), &task, item, rows, &contract.SummaryEvidence{}, audit); err == nil {
		t.Fatal("recipe3 silently accepted missing tree")
	}
}
