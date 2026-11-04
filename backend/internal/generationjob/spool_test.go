package generationjob

import (
	"context"
	"testing"
)

func TestSpoolReservationSurvivesReopenAndGeneration(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := store.Acquire(ctx, "spool", 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReserveSpool(ctx, fence, "0:original", 7, 10); err != nil {
		t.Fatal(err)
	}
	store.Close() // A crash before publication conservatively holds these bytes.
	store, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	next, err := store.Acquire(ctx, "spool", 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReserveSpool(ctx, fence, "stale", 1, 10); err == nil {
		t.Fatal("stale writer reserved")
	}
	if err := store.ReserveSpool(ctx, next, "0:original", 7, 10); err != nil {
		t.Fatal("deduplication charged twice", err)
	}
	if err := store.ReserveSpool(ctx, next, "new", 4, 10); err == nil {
		t.Fatal("resume replenished spool quota")
	}
	if err := store.ReserveSpool(ctx, next, "new", 3, 10); err != nil {
		t.Fatal(err)
	}
}
