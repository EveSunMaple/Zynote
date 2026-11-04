package generationjob

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunLockCrashHelper(t *testing.T) {
	if os.Getenv("ZYNOTE_FIXTURE_RUN_LOCK_HELPER") != "1" {
		return
	}
	release, err := AcquireRunLock(os.Getenv("ZYNOTE_FIXTURE_RUN_LOCK_ROOT"), "fixture-job")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fmt.Println("locked")
	time.Sleep(30 * time.Second)
}

func TestRunLockIsCrossProcessAndReleasedOnCrash(t *testing.T) {
	root := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestRunLockCrashHelper$")
	child.Env = append(os.Environ(), "ZYNOTE_FIXTURE_RUN_LOCK_HELPER=1", "ZYNOTE_FIXTURE_RUN_LOCK_ROOT="+root)
	pipe, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			child.Process.Kill()
			child.Wait()
		}
	}()
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(pipe).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if strings.TrimSpace(line) != "locked" {
			t.Fatalf("helper: %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper lock timeout")
	}
	if release, err := AcquireRunLock(root, "fixture-job"); err == nil {
		release()
		t.Fatal("cross-process lock did not exclude")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	release, err := AcquireRunLock(root, "fixture-job")
	if err != nil {
		t.Fatal("lock survived process death", err)
	}
	release()
}

func newTestStore(t *testing.T) (*Store, Fence) {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	fence, err := store.Acquire(context.Background(), "fixture-job", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	return store, fence
}

func TestPublicationAndRolloverShareSerializationPoint(t *testing.T) {
	store, fence := newTestStore(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	published := make(chan error, 1)
	resumed := make(chan error, 1)
	go func() {
		published <- store.WithFence(ctx, fence, func() error { close(entered); <-release; return nil })
	}()
	<-entered
	go func() { _, err := store.Acquire(ctx, fence.JobID, fence.Generation, 2); resumed <- err }()
	select {
	case err := <-resumed:
		t.Fatalf("rollover passed active publication: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-published; err != nil {
		t.Fatal(err)
	}
	if err := <-resumed; err != nil {
		t.Fatal(err)
	}
	if err := store.WithFence(ctx, fence, func() error { t.Fatal("old publisher ran"); return nil }); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
}

func TestReservationIsAtomicAndIdempotent(t *testing.T) {
	store, fence := newTestStore(t)
	ctx := context.Background()
	hash := strings.Repeat("a", 64)
	if err := store.Reserve(ctx, fence, "first", hash, 8); err != nil {
		t.Fatal(err)
	}
	if err := store.Reserve(ctx, fence, "first", hash, 8); err != nil {
		t.Fatal(err)
	}
	if err := store.Reserve(ctx, fence, "first", strings.Repeat("b", 64), 8); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"second", "third"} {
		wait.Add(1)
		go func() { defer wait.Done(); results <- store.Reserve(ctx, fence, id, hash, 8) }()
	}
	wait.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrBudget) {
			t.Fatal(err)
		}
	}
	budget, err := store.Budget(ctx, fence.JobID)
	if err != nil || winners != 1 || budget.Reserved != 2 {
		t.Fatalf("winners=%d budget=%+v err=%v", winners, budget, err)
	}
}

func TestResumePreservesUnknownAndFencesDispatch(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := store.Acquire(ctx, "fixture-job", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	if err := store.Reserve(ctx, fence, "remote", hash, 8); err != nil {
		t.Fatal(err)
	}
	if err := store.dispatch(ctx, fence, "remote"); err != nil {
		t.Fatal(err)
	}
	if err := store.Reserve(ctx, fence, "local", hash, 8); err != nil {
		t.Fatal(err)
	}
	store.Close()
	store, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	next, err := store.Acquire(ctx, "fixture-job", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.dispatch(ctx, fence, "local"); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
	budget, err := store.Budget(ctx, fence.JobID)
	if err != nil || budget.UnknownHeld != 1 || budget.Reserved != 0 {
		t.Fatalf("budget=%+v err=%v", budget, err)
	}
	if err := store.Reserve(ctx, next, "fresh", hash, 8); err != nil {
		t.Fatal(err)
	}
	if err := store.Reserve(ctx, next, "too-many", hash, 8); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
	// A trusted full-response settlement of the old request changes only its
	// accounting; it does not restore its dispatch/publication generation.
	if err := store.finish(ctx, fence, "remote", true); err != nil {
		t.Fatal(err)
	}
	if err := store.Check(ctx, fence); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
	budget, err = store.Budget(ctx, fence.JobID)
	if err != nil || budget.Spent != 1 || budget.UnknownHeld != 0 {
		t.Fatalf("budget=%+v err=%v", budget, err)
	}
}

func TestCompetingResumeHasOneCASWinner(t *testing.T) {
	store, fence := newTestStore(t)
	ctx := context.Background()
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() { defer wait.Done(); _, err := store.Acquire(ctx, fence.JobID, 1, 2); results <- err }()
	}
	wait.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrFence) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners=%d", winners)
	}
}
