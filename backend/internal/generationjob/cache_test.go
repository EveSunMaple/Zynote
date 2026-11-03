package generationjob

import (
	"context"
	"errors"
	"testing"
)

func TestPaidSuccessAdoptionRequiresIdentityAndEpoch(t *testing.T) {
	store, fence := newTestStore(t)
	ctx := context.Background()
	body := []byte(`{"cards":[]}`)
	if err := store.SaveSuccess(ctx, fence, "chunk-2", "source-plan-recipe", body); err != nil {
		t.Fatal(err)
	}
	next, err := store.Acquire(ctx, fence.JobID, fence.Generation, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.AdoptSuccess(ctx, next, "chunk-2", "other-source"); err != nil || found {
		t.Fatalf("wrong identity adopted: %v %v", found, err)
	}
	got, found, err := store.AdoptSuccess(ctx, next, "chunk-2", "source-plan-recipe")
	if err != nil || !found || string(got) != string(body) {
		t.Fatalf("paid success lost: %s %v %v", got, found, err)
	}
	if err := store.Revoke(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AdoptSuccess(ctx, next, "chunk-2", "source-plan-recipe"); !errors.Is(err, ErrFence) {
		t.Fatal("revoked cache adopted", err)
	}
	if _, err := store.Acquire(ctx, next.JobID, next.Generation, 2); !errors.Is(err, ErrFence) {
		t.Fatal("same hash revived tombstone", err)
	}
	if err := store.WithFence(ctx, next, func() error { t.Fatal("revoked publisher ran"); return nil }); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
}
