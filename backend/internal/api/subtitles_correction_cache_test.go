package api

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/generationjob"
	"github.com/EveSunMaple/Zynote/backend/internal/learning"
)

func TestCorrectionPaidLaterSuccessSurvivesEarlierFailureAndResume(t *testing.T) {
	ctx := context.Background()
	ledger, err := generationjob.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	fence, err := ledger.Acquire(ctx, "fixture-job", 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	run := &correctionRun{requestLedger: ledger, requestFence: fence, fingerprint: "exact-source-plan-recipe", contexts: make([]learning.CorrectionContext, 2), client: ai.New(ai.Profile{BaseURL: "https://fixture.invalid/v1", Model: "fixture"}), prompt: "fixture"}
	batches := []learning.CorrectionBatch{{Start: 0, Rows: []documents.Segment{{Start: 0, End: 1000, Text: "first"}}}, {Start: 1, Rows: []documents.Segment{{Start: 1000, End: 2000, Text: "second"}}}}
	var secondCalls atomic.Int64
	secondSaved := make(chan struct{})
	failure := errors.New("fixture first chunk format failure")
	result := func(batch learning.CorrectionBatch) []learning.CorrectedSegment {
		source := batch.Rows[0]
		return []learning.CorrectedSegment{{Start: source.Start, End: source.End, Text: source.Text, OriginalText: source.Text}}
	}
	paid := func(_ context.Context, batch learning.CorrectionBatch, index int) ([]learning.CorrectedSegment, error) {
		if index == 0 {
			<-secondSaved
			return nil, failure
		}
		secondCalls.Add(1)
		return result(batch), nil
	}
	err = learning.RunCorrectionBatches(ctx, batches, func(ctx context.Context, batch learning.CorrectionBatch, index int) ([]learning.CorrectedSegment, error) {
		rows, err := run.withCachedBatch(ctx, batch, index, paid)
		if index == 1 && err == nil {
			close(secondSaved)
		}
		return rows, err
	}, func([]learning.CorrectedSegment, learning.CorrectionBatch) error {
		t.Fatal("failed first chunk was committed")
		return nil
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	next, err := ledger.Acquire(ctx, fence.JobID, fence.Generation, 8)
	if err != nil {
		t.Fatal(err)
	}
	run.requestFence = next
	commits := []int{}
	err = learning.RunCorrectionBatches(ctx, batches, func(ctx context.Context, batch learning.CorrectionBatch, index int) ([]learning.CorrectedSegment, error) {
		return run.withCachedBatch(ctx, batch, index, func(_ context.Context, batch learning.CorrectionBatch, index int) ([]learning.CorrectedSegment, error) {
			if index == 1 {
				secondCalls.Add(1)
			}
			return result(batch), nil
		})
	}, func(_ []learning.CorrectedSegment, batch learning.CorrectionBatch) error {
		commits = append(commits, batch.Start)
		return nil
	})
	if err != nil || secondCalls.Load() != 1 || len(commits) != 2 || commits[0] != 0 || commits[1] != 1 {
		t.Fatalf("err=%v paid second=%d commits=%v", err, secondCalls.Load(), commits)
	}
	run.contexts[1].Courseware = "changed exact wire context"
	_, err = run.withCachedBatch(ctx, batches[1], 1, func(context.Context, learning.CorrectionBatch, int) ([]learning.CorrectedSegment, error) {
		secondCalls.Add(1)
		return result(batches[1]), nil
	})
	if err != nil || secondCalls.Load() != 2 {
		t.Fatalf("changed wire reused old cache: %d %v", secondCalls.Load(), err)
	}
}
